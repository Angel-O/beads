#!/usr/bin/env python3
"""Bounded POSIX subprocess execution for upgrade preservation."""

import os
import signal
import subprocess
import threading
import time
from upgrade_baseline_quiescence import _chain_cleanup_failures as _chain_failures
_FAILURE_KINDS = frozenset(("limit", "timeout", "status", "cleanup"))
_READ_BYTES = 64 * 1024
_CLEANUP_SECONDS = 1.0

class ProcessFailure(RuntimeError):
    def __init__(self, kind):
        if kind not in _FAILURE_KINDS:
            raise ValueError("unknown process failure kind")
        self.kind = kind
        super().__init__("process runtime %s failure" % kind)

class ProcessBudget:
    def __init__(self, stdout_limit, stderr_limit):
        limits = (stdout_limit, stderr_limit)
        if any(type(limit) is not int or limit < 0 for limit in limits):
            raise ValueError("process limits must be non-negative integers")
        self._limits = limits
        self._used = [0, 0]
        self._lock = threading.Lock()
    def _charge(self, stream, amount):
        with self._lock:
            if self._used[stream] + amount > self._limits[stream]:
                raise ProcessFailure("limit")
            self._used[stream] += amount

def _observe_status(process):
    options = os.WEXITED | os.WNOHANG | os.WNOWAIT
    observed = os.waitid(os.P_PID, process.pid, options)
    if observed is None or observed.si_pid == 0:
        return None
    return observed.si_status if observed.si_code == os.CLD_EXITED else -observed.si_status

def _reader_primary(primary_errors, reader_errors):
    for error in primary_errors:
        if error is not None:
            return error, True
    for error in reader_errors:
        if error is not None and not isinstance(error, Exception):
            return error, True
    if any(error is not None for error in reader_errors):
        return ProcessFailure("cleanup"), False
    return None, False

class _ProcessState:
    def __init__(self, deadline):
        self.deadline = deadline
        self.process, self.threads = None, []
        self.primary, self.primary_exact = None, False
        self.status, self.ownership = None, True
        self.pre_signal_running = self.signal_attempted = False
        self.signal_delivered = self.reap_started = self.reap_done = False
        self.stop_done, self.join_pending = False, None
        self.liveness_done, self.live_reported = False, False
        self.close_pending = [0, 1]
        self.cleanup_deadline, self.cleanup_complete = None, False
        self.supervision_failures, self.kill_failures = [], []
        self.wait_failures, self.join_failures, self.close_failures = [], [], []
    def prefer(self, error, exact):
        if error is not None and (self.primary is None or exact and not self.primary_exact):
            self.primary, self.primary_exact = error, exact
    def _remaining(self):
        try:
            return max(0.0, self.cleanup_deadline - time.monotonic())
        except BaseException as error:
            self.wait_failures.append(error)
            return 0.0
    def _open_cleanup_deadline(self):
        if self.cleanup_deadline is not None:
            return True
        try:
            self.cleanup_deadline = time.monotonic() + _CLEANUP_SECONDS
        except BaseException as error:
            self.wait_failures.append(error)
            return False
        return True
    def _signal_group(self):
        if (self.signal_attempted or self.signal_delivered or
                self.reap_started or not self.ownership):
            return True
        try:
            observed = _observe_status(self.process)
        except BaseException as error:
            self.kill_failures.append(error)
            if isinstance(error, ChildProcessError):
                self.ownership = False
                self.signal_attempted = True
                return True
            return False
        if observed is not None:
            self.status = observed
        else:
            self.pre_signal_running = True
        try:
            if time.monotonic() >= self.deadline:
                self.prefer(ProcessFailure("timeout"), True)
        except BaseException as error:
            self.wait_failures.append(error)
        try:
            os.killpg(self.process.pid, signal.SIGKILL)
        except ProcessLookupError:
            self.signal_attempted = True
        except BaseException as error:
            self.kill_failures.append(error)
            return False
        else:
            self.signal_delivered = True
            self.signal_attempted = True
        return True
    def _reap(self):
        if self.reap_done:
            return
        self.reap_started = True
        while True:
            remaining = self._remaining()
            try:
                self.process.wait(timeout=remaining)
            except BaseException as error:
                self.wait_failures.append(error)
                if self._remaining() <= 0:
                    return
                try:
                    time.sleep(min(self._remaining(), 0.01))
                except BaseException as delay_error:
                    self.wait_failures.append(delay_error)
            else:
                self.reap_done = True
                return
    def _join_readers(self):
        if self.join_pending is None:
            self.join_pending = list(range(len(self.threads)))
        pending = []
        for index in tuple(self.join_pending):
            try:
                thread = self.threads[index]
                thread.join(timeout=self._remaining())
            except BaseException as error:
                self.join_failures.append(error)
                pending.append(index)
        live = False
        for index, thread in enumerate(self.threads):
            try:
                if thread.is_alive():
                    live = True
                    if index not in pending:
                        pending.append(index)
            except BaseException as error:
                self.join_failures.append(error)
                if index not in pending:
                    pending.append(index)
        self.join_pending = pending
        if live and not self.live_reported:
            self.join_failures.append(ProcessFailure("cleanup"))
            self.live_reported = True
        if not pending:
            self.liveness_done = True
    def _close_pipes(self):
        pipes = (self.process.stdout, self.process.stderr)
        pending = []
        for index in tuple(self.close_pending):
            pipe = None
            try:
                pipe = pipes[index]
                pipe.close()
            except BaseException as error:
                self.close_failures.append(error)
            try:
                closed = pipe is not None and pipe.closed
            except BaseException as error:
                self.close_failures.append(error)
                closed = False
            if not closed:
                pending.append(index)
        self.close_pending = pending
    def cleanup(self, stop, refresh, terminal=False):
        if self.cleanup_complete or not self._open_cleanup_deadline():
            return
        if not self.stop_done:
            try:
                stop.set()
            except BaseException as error:
                self.join_failures.append(error)
            else:
                self.stop_done = True
        refresh()
        if self.reap_started:
            self._reap()
        must_signal = self.primary is not None or self.status not in (None, 0)
        if must_signal and not self.reap_started:
            if self._signal_group():
                self._reap()
        self._join_readers()
        refresh()
        self._close_pipes()
        if not self.reap_started:
            failures = self.cleanup_failures()
            must_signal = self.primary is not None or self.status not in (None, 0)
            if not must_signal and not failures or self._signal_group():
                self._reap()
        if terminal and not self.reap_started:
            self._reap()
        self.cleanup_complete = (self.stop_done and self.reap_done and
            self.liveness_done and not self.join_pending and not self.close_pending)
    def cleanup_failures(self):
        return self.kill_failures + self.wait_failures + self.join_failures + self.close_failures

def run_process(executable, arguments, cwd, environment, deadline, budget, consumer=None):
    state = _ProcessState(deadline)
    captured = bytearray()
    primary_errors, reader_errors = [None, None], [None, None]
    reader_done = [False, False]
    changed = threading.Event()
    stop = threading.Event()
    def read_stream(index, pipe):
        try:
            try:
                descriptor = pipe.fileno()
            except (AttributeError, OSError, ValueError):
                descriptor = None
            if descriptor is not None:
                os.set_blocking(descriptor, False)
            while not stop.is_set():
                try:
                    chunk = (
                        os.read(descriptor, _READ_BYTES)
                        if descriptor is not None
                        else pipe.read(_READ_BYTES)
                    )
                except BlockingIOError:
                    stop.wait(0.01)
                    continue
                if not chunk:
                    return
                try:
                    budget._charge(index, len(chunk))
                    if index == 0:
                        if consumer is None:
                            captured.extend(chunk)
                        else:
                            consumer(chunk)
                except BaseException as error:
                    primary_errors[index] = error
                    return
        except BaseException as error:
            reader_errors[index] = error
        finally:
            reader_done[index] = True
            changed.set()
    def refresh_reader_primary():
        candidate, exact = _reader_primary(primary_errors, reader_errors)
        state.prefer(candidate, exact)

    starting = True
    drain_deadline = None
    try:
        if time.monotonic() >= deadline:
            state.prefer(ProcessFailure("timeout"), True)
        else:
            state.process = subprocess.Popen(
                (executable, *arguments),
                cwd=cwd,
                env=environment,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                bufsize=0,
                close_fds=True,
                start_new_session=True,
            )
            for index, pipe in enumerate((state.process.stdout, state.process.stderr)):
                thread = threading.Thread(
                    target=read_stream,
                    args=(index, pipe),
                    daemon=True,
                )
                try:
                    state.threads.append(thread)
                    thread.start()
                except BaseException as error:
                    if (isinstance(error, Exception) and state.threads and
                            state.threads[-1] is thread and thread.ident is None):
                        state.threads.pop()
                    raise
            starting = False
            while state.primary is None:
                now = time.monotonic()
                if now >= deadline:
                    state.prefer(ProcessFailure("timeout"), True)
                    break
                if state.status is None:
                    try:
                        observed = _observe_status(state.process)
                    except BaseException as error:
                        if isinstance(error, ChildProcessError):
                            state.ownership = False
                        raise
                    if observed is not None:
                        state.status = observed
                    now = time.monotonic()
                    if now >= deadline:
                        state.prefer(ProcessFailure("timeout"), True)
                        break
                    if observed is not None:
                        drain_deadline = now + _CLEANUP_SECONDS
                refresh_reader_primary()
                if state.primary is not None:
                    break
                now = time.monotonic()
                if now >= deadline:
                    state.prefer(ProcessFailure("timeout"), True)
                    break
                if state.status is not None:
                    if now >= drain_deadline:
                        if state.status == 0:
                            state.prefer(ProcessFailure("cleanup"), False)
                        break
                    if all(reader_done):
                        break
                    remaining = min(deadline, drain_deadline) - now
                else:
                    remaining = deadline - now
                changed.wait(min(remaining, 0.01))
                changed.clear()
    except BaseException as error:
        state.supervision_failures.append(error)
        exact = starting or not isinstance(error, Exception)
        state.prefer(error if exact else ProcessFailure("cleanup"), exact)
    finally:
        if state.process is not None:
            try:
                for _attempt in range(8):
                    try:
                        state.cleanup(stop, refresh_reader_primary, _attempt == 7)
                    except BaseException as error:
                        state.wait_failures.append(error)
            except BaseException as error:
                state.wait_failures.append(error)
            finally:
                try:
                    state.cleanup(stop, refresh_reader_primary, True)
                except BaseException as error:
                    state.wait_failures.append(error)
    refresh_reader_primary()
    cleanup_failures = state.cleanup_failures()
    interruption = next(
        (error for error in cleanup_failures if not isinstance(error, Exception)),
        None,
    )
    state.prefer(interruption, interruption is not None)
    returncode = state.process.returncode if state.process is not None else None
    status_failure = None
    if state.status not in (None, 0):
        status_failure = ProcessFailure("status")
    elif state.status is None and returncode not in (None, 0):
        attributed = state.pre_signal_running and state.signal_delivered
        if returncode != -signal.SIGKILL or not attributed:
            status_failure = ProcessFailure("status")
    if state.primary is None and cleanup_failures:
        state.prefer(ProcessFailure("cleanup"), False)
    if state.primary is None:
        state.prefer(status_failure, False)
    if state.primary is None:
        return None if consumer is not None else bytes(captured)
    failures = []
    for pair in zip(primary_errors, reader_errors):
        failures.extend(error for error in pair if error is not None and error is not state.primary)
    failures.extend(state.supervision_failures)
    failures.extend(cleanup_failures)
    if status_failure is not None and status_failure is not state.primary:
        failures.append(status_failure)
    _chain_failures(state.primary, failures)
    raise state.primary
