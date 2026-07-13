#!/usr/bin/env python3
import contextlib
import io
import os
import signal
import subprocess
import sys
import tempfile
import threading
import time
import types
import unittest
from unittest import mock

import upgrade_baseline_process as subject


EXPECTED_TEST_COUNT = 42
STAGE_TESTS = {
    "contract": (
        "test_capture_mode_and_posix_spawn_contract",
        "test_streaming_mode_returns_none_and_delivers_exact_bytes",
        "test_nonzero_status_is_structured",
    ),
    "budget": (
        "test_aggregate_limits_reap_without_over_delivery",
        "test_shared_budget_is_thread_safe_across_concurrent_calls",
    ),
    "failure": (
        "test_timeout_is_structured_and_cleanup_is_bounded",
        "test_process_and_reader_start_failures_remain_exact",
        "test_consumer_failure_and_interrupt_remain_exact",
        "test_reader_failures_become_ordered_cleanup_failure",
        "test_cleanup_failures_chain_in_order_and_deduplicate",
        "test_caller_interrupt_during_wait_remains_exact",
    ),
    "real": ("test_real_descendant_held_pipe_is_terminated_promptly",),
    "hardening": (
        "test_unreachable_group_kill_does_not_block_pipe_cleanup",
        "test_delayed_failure_outranks_nonzero_status",
        "test_session_leader_is_observed_before_group_signal_and_reap",
        "test_falsey_consumer_failure_remains_exact",
        "test_failure_composition_matches_quiescence_order",
        "test_started_then_raising_reader_is_joined",
        "test_read_and_close_base_interrupts_remain_exact",
        "test_cleanup_wait_retry_and_natural_sigkill_are_distinct",
        "test_expired_and_crossing_deadlines_are_timeouts",
        "test_timeout_remains_primary_over_cleanup_interrupt",
        "test_late_consumer_failure_remains_exact_after_join",
        "test_late_consumer_outranks_observation_failure",
        "test_finished_then_raising_reader_is_joined",
        "test_reap_interruption_never_signals_after_ownership_loss",
        "test_natural_sigkill_is_not_suppressed_after_failed_group_signal",
        "test_process_budget_rejects_unbounded_limits",
        "test_waitid_none_crossing_deadline_remains_timeout",
        "test_status_known_drain_crossing_deadline_is_timeout",
        "test_waitid_ownership_loss_never_signals_group",
        "test_cleanup_phase_interrupts_still_release_resources",
        "test_successful_group_signal_preserves_natural_sigkill",
        "test_stale_reader_snapshot_cannot_reap_before_signal",
        "test_cleanup_guard_survives_two_interrupts",
        "test_pre_effect_join_and_close_are_retried",
        "test_cleanup_deadline_interruption_retains_grace",
        "test_interrupted_waitid_retries_while_owned",
        "test_transient_group_signal_failure_is_retried_before_reap",
        "test_reap_transition_interruption_is_resumed",
        "test_pending_resources_survive_transition_interrupts",
        "test_reader_completion_rechecks_absolute_deadline",
    ),
}

class _DelegatingModule:
    def __init__(self, delegate, **overrides):
        self._delegate = delegate
        self.__dict__.update(overrides)

    def __getattr__(self, name):
        return getattr(self._delegate, name)


class _TrackedPipe(io.BytesIO):
    def __init__(
        self,
        data=b"",
        read_error=None,
        close_error=None,
        close_before_error=False,
    ):
        super().__init__(data)
        self.read_errors = [] if read_error is None else [read_error]
        self.close_error = close_error
        self.close_before_error = close_before_error
        self.close_calls = 0

    def read(self, size=-1):
        if self.read_errors:
            raise self.read_errors.pop(0)
        return super().read(size)

    def close(self):
        self.close_calls += 1
        error, self.close_error = self.close_error, None
        if error is not None and self.close_before_error:
            self.close_before_error = False
            raise error
        super().close()
        if error is not None:
            raise error


class _FalseyFailure(RuntimeError):
    def __bool__(self):
        return False


class _FakeProcess:
    _next_pid = 40_000

    def __init__(
        self,
        *,
        stdout=b"",
        stderr=b"",
        status=0,
        auto_exit=True,
        hang=False,
        stdout_error=None,
        stderr_error=None,
        close_error=None,
        wait_errors=(),
        poll_errors=(),
        wait_error_sets_status=False,
        wait_error_reaps=False,
        close_before_error=False,
        require_positive_wait=False,
    ):
        type(self)._next_pid += 1
        self.pid = type(self)._next_pid
        self.stdout = _TrackedPipe(
            stdout, stdout_error, close_error, close_before_error
        )
        self.stderr = _TrackedPipe(
            stderr, stderr_error, close_error, close_before_error
        )
        self.status = status
        self.auto_exit = auto_exit
        self.hang = hang
        self.wait_errors = list(wait_errors)
        self.poll_errors = list(poll_errors)
        self.wait_error_sets_status = wait_error_sets_status
        self.wait_error_reaps = wait_error_reaps
        self.require_positive_wait = require_positive_wait
        self.returncode = None
        self.wait_calls = []
        self.poll_calls = 0
        self.kill_calls = 0
        self.popen_call = None
        self.reaped = False
        self.events = []

    def poll(self):
        self.events.append("poll")
        self.poll_calls += 1
        if self.poll_errors:
            raise self.poll_errors.pop(0)
        if self.returncode is None and self.auto_exit and not self.hang:
            self.returncode = self.status
        if self.returncode is not None:
            self.reaped = True
        return self.returncode

    def wait(self, timeout=None):
        self.events.append("wait")
        self.wait_calls.append(timeout)
        if self.require_positive_wait and (timeout is None or timeout <= 0):
            raise subprocess.TimeoutExpired((b"fake",), timeout)
        if self.wait_errors:
            error = self.wait_errors.pop(0)
            if self.wait_error_reaps:
                self.returncode = self.status
                self.reaped = True
            elif self.wait_error_sets_status:
                self.returncode = self.status
            raise error
        if self.returncode is None and self.hang:
            raise subprocess.TimeoutExpired((b"fake",), timeout)
        if self.returncode is None:
            self.returncode = self.status
        self.reaped = True
        return self.returncode

    def observe(self):
        self.events.append("observe")
        if self.returncode is not None:
            status = self.returncode
        elif self.auto_exit and not self.hang:
            status = self.status
        else:
            return None
        code = os.CLD_EXITED if status >= 0 else os.CLD_KILLED
        return types.SimpleNamespace(
            si_pid=self.pid,
            si_uid=0,
            si_signo=signal.SIGCHLD,
            si_status=status if status >= 0 else -status,
            si_code=code,
        )

    def kill(self):
        self.kill_calls += 1
        self.returncode = -signal.SIGKILL


class _ManualThread:
    def __init__(
        self,
        target,
        args,
        kwargs,
        start_error=None,
        join_error=None,
        alive_error=None,
    ):
        self.target = target
        self.args = args
        self.kwargs = kwargs
        self.start_error = start_error
        self.join_error = join_error
        self.alive_error = alive_error
        self.started = False
        self.ident = None
        self.join_calls = []
        self.unhandled = None

    def start(self):
        if self.start_error is not None:
            raise self.start_error
        self.started = True
        self.ident = id(self)
        try:
            self.target(*self.args, **self.kwargs)
        except BaseException as error:
            self.unhandled = error

    def join(self, timeout=None):
        self.join_calls.append(timeout)
        if self.ident is None:
            raise RuntimeError("cannot join thread before it is started")
        error, self.join_error = self.join_error, None
        if error is not None:
            raise error

    def is_alive(self):
        error, self.alive_error = self.alive_error, None
        if error is not None:
            raise error
        return False


class _ThreadFactory:
    def __init__(
        self,
        *,
        fail_start_index=None,
        start_error=None,
        join_error=None,
        alive_error=None,
    ):
        self.fail_start_index = fail_start_index
        self.start_error = start_error
        self.join_error = join_error
        self.alive_error = alive_error
        self.threads = []

    def __call__(self, *args, **kwargs):
        target = kwargs.pop("target", None)
        target_args = kwargs.pop("args", ())
        target_kwargs = kwargs.pop("kwargs", {})
        if target is None and args:
            target, args = args[0], args[1:]
        del args, kwargs
        index = len(self.threads)
        thread = _ManualThread(
            target,
            target_args,
            target_kwargs,
            self.start_error if index == self.fail_start_index else None,
            self.join_error,
            self.alive_error if index == 0 else None,
        )
        self.threads.append(thread)
        return thread


class _RecordingThread:
    def __init__(
        self,
        thread,
        start_error=None,
        *,
        finish_before_error=False,
        join_callback=None,
        join_error=None,
    ):
        self._thread = thread
        self.start_error = start_error
        self.finish_before_error = finish_before_error
        self.join_callback = join_callback
        self.join_errors = [] if join_error is None else [join_error]
        self.started = False
        self.join_calls = []
        self.join_elapsed = []
        self.delegate_join_calls = 0
        self.unhandled = None

    def start(self):
        self._thread.start()
        if self.start_error is not None:
            if self.finish_before_error:
                self._thread.join(timeout=1)
            else:
                self.started = True
            raise self.start_error
        self.started = True

    def join(self, timeout=None):
        self.join_calls.append(timeout)
        if self.join_errors:
            raise self.join_errors.pop(0)
        if self.join_callback is not None:
            callback, self.join_callback = self.join_callback, None
            callback()
        started = time.monotonic()
        try:
            self.delegate_join_calls += 1
            return self._thread.join(timeout)
        finally:
            self.join_elapsed.append(time.monotonic() - started)

    def is_alive(self):
        return self._thread.is_alive()

    def __getattr__(self, name):
        return getattr(self._thread, name)


class _RecordingThreadFactory:
    def __init__(
        self,
        *,
        fail_start_index=None,
        start_error=None,
        finish_before_error=False,
        join_callback=None,
        join_error=None,
    ):
        self.fail_start_index = fail_start_index
        self.start_error = start_error
        self.finish_before_error = finish_before_error
        self.join_callback = join_callback
        self.join_error = join_error
        self.threads = []

    def __call__(self, *args, **kwargs):
        index = len(self.threads)
        thread = _RecordingThread(
            threading.Thread(*args, **kwargs),
            self.start_error if index == self.fail_start_index else None,
            finish_before_error=self.finish_before_error,
            join_callback=self.join_callback,
            join_error=self.join_error if index == 0 else None,
        )
        self.threads.append(thread)
        return thread


class _RuntimeSeams:
    def __init__(
        self,
        testcase,
        processes=(),
        *,
        popen_error=None,
        factory=None,
        kill_errors=(),
    ):
        self.testcase = testcase
        self.processes = list(processes)
        self.popen_error = popen_error
        self.factory = _ThreadFactory() if factory is None else factory
        self.kill_errors = list(kill_errors)
        self.popen_calls = []
        self.killpg_calls = []
        self.waitid_calls = []
        self.signal_reservations = []
        self.stack = contextlib.ExitStack()

    def __enter__(self):
        self.stack.__enter__()

        def popen(*args, **kwargs):
            self.popen_calls.append((args, kwargs))
            if self.popen_error is not None:
                raise self.popen_error
            self.testcase.assertTrue(self.processes, "unexpected extra Popen call")
            process = self.processes.pop(0)
            process.popen_call = (args, kwargs)
            return process

        def killpg(group, requested_signal):
            self.killpg_calls.append((group, requested_signal))
            process = self.processes_by_pid.get(group)
            if process is not None:
                self.signal_reservations.append(not process.reaped)
                process.events.append("signal")
            if self.kill_errors:
                raise self.kill_errors.pop(0)
            if process is not None:
                process.returncode = -requested_signal

        def waitid(id_type, process_id, options):
            self.waitid_calls.append((id_type, process_id, options))
            self.testcase.assertEqual(id_type, os.P_PID)
            self.testcase.assertIn(process_id, self.processes_by_pid)
            return self.processes_by_pid[process_id].observe()

        self.processes_by_pid = {
            process.pid: process for process in self.processes
        }
        subprocess_proxy = _DelegatingModule(subject.subprocess, Popen=popen)
        threading_proxy = _DelegatingModule(subject.threading, Thread=self.factory)
        os_proxy = _DelegatingModule(
            subject.os,
            getpgid=lambda process_id: process_id,
            killpg=killpg,
            waitid=waitid,
        )
        self.stack.enter_context(mock.patch.object(subject, "subprocess", subprocess_proxy))
        self.stack.enter_context(mock.patch.object(subject, "threading", threading_proxy))
        self.stack.enter_context(mock.patch.object(subject, "os", os_proxy))
        return self

    def __exit__(self, exc_type, exc_value, traceback):
        return self.stack.__exit__(exc_type, exc_value, traceback)


class ProcessRuntimeTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = os.fsencode(self.temporary.name)
        self.environment = {"LC_ALL": "C", "PROCESS_RUNTIME_TEST": "1"}

    def test_capture_mode_and_posix_spawn_contract(self):
        process = _FakeProcess(stdout=b"captured", stderr=b"diagnostic")
        with _RuntimeSeams(self, (process,)) as seams:
            output = self._run(subject.ProcessBudget(64, 64))

        self.assertEqual(output, b"captured")
        self.assertEqual(len(seams.popen_calls), 1)
        args, kwargs = seams.popen_calls[0]
        self.assertEqual(tuple(args[0]), (b"/usr/bin/tool", b"--probe"))
        self.assertEqual(kwargs["cwd"], self.root)
        self.assertEqual(kwargs["env"], self.environment)
        self.assertIs(kwargs["stdin"], subprocess.DEVNULL)
        self.assertIs(kwargs["stdout"], subprocess.PIPE)
        self.assertIs(kwargs["stderr"], subprocess.PIPE)
        self.assertEqual(kwargs["bufsize"], 0)
        self.assertTrue(kwargs["close_fds"])
        self.assertTrue(kwargs["start_new_session"])
        self.assertFalse(kwargs.get("shell", False))
        self._assert_fake_clean(process, seams.factory, killed=False)

    def test_streaming_mode_returns_none_and_delivers_exact_bytes(self):
        process = _FakeProcess(stdout=b"streamed bytes")
        chunks = []
        with _RuntimeSeams(self, (process,)) as seams:
            result = self._run(subject.ProcessBudget(64, 64), consumer=chunks.append)

        self.assertIsNone(result)
        self.assertEqual(b"".join(chunks), b"streamed bytes")
        self._assert_fake_clean(process, seams.factory, killed=False)

    def test_aggregate_limits_reap_without_over_delivery(self):
        for stream_name in ("stdout", "stderr"):
            with self.subTest(stream=stream_name):
                first = _FakeProcess(**{stream_name: b"abc"})
                second = _FakeProcess(
                    **{stream_name: b"def"}, auto_exit=False
                )
                budget = subject.ProcessBudget(4, 4)
                delivered = []
                with _RuntimeSeams(self, (first, second)) as seams:
                    self._run(
                        budget,
                        consumer=delivered.append if stream_name == "stdout" else None,
                    )
                    with self.assertRaises(subject.ProcessFailure) as raised:
                        self._run(
                            budget,
                            consumer=(
                                delivered.append if stream_name == "stdout" else None
                            ),
                        )
                self.assertEqual(raised.exception.kind, "limit")
                if stream_name == "stdout":
                    self.assertEqual(b"".join(delivered), b"abc")
                self._assert_fake_clean(first, seams.factory, killed=False)
                self._assert_fake_clean(second, seams.factory, killed=True)

    def test_shared_budget_is_thread_safe_across_concurrent_calls(self):
        budget = subject.ProcessBudget(9, 64)
        barrier = threading.Barrier(2)
        delivered = [[], []]
        outcomes = []

        def invoke(index):
            barrier.wait(timeout=2)
            try:
                subject.run_process(
                    os.fsencode(sys.executable),
                    (b"-B", b"-c", b"import os; os.write(1, b'123456')"),
                    self.root,
                    os.environ.copy(),
                    time.monotonic() + 5,
                    budget,
                    consumer=delivered[index].append,
                )
            except BaseException as error:
                outcomes.append(error)
            else:
                outcomes.append(None)

        workers = [threading.Thread(target=invoke, args=(index,)) for index in range(2)]
        for worker in workers:
            worker.start()
        for worker in workers:
            worker.join(timeout=5)
            self.assertFalse(worker.is_alive(), "concurrent budget worker leaked")

        self.assertEqual(len(outcomes), 2)
        failures = [error for error in outcomes if error is not None]
        self.assertEqual(len(failures), 1, outcomes)
        self.assertTrue(
            all(
                isinstance(error, subject.ProcessFailure) and error.kind == "limit"
                for error in failures
            ),
            outcomes,
        )
        self.assertEqual(sum(len(b"".join(value)) for value in delivered), 6)

    def test_nonzero_status_is_structured(self):
        process = _FakeProcess(stderr=b"untrusted child detail\n", status=9)
        with _RuntimeSeams(self, (process,)) as seams:
            with self.assertRaises(subject.ProcessFailure) as raised:
                self._run(subject.ProcessBudget(64, 64))

        self.assertEqual(raised.exception.kind, "status")
        self.assertNotIn("untrusted child detail", str(raised.exception))
        self.assertEqual(
            seams.killpg_calls,
            [(process.pid, signal.SIGKILL)],
        )
        self._assert_fake_clean(process, seams.factory, killed=True)

    def test_timeout_is_structured_and_cleanup_is_bounded(self):
        process = _FakeProcess(auto_exit=False, hang=True)
        with _RuntimeSeams(self, (process,)) as seams:
            with self.assertRaises(subject.ProcessFailure) as raised:
                self._run(
                    subject.ProcessBudget(64, 64),
                    deadline=time.monotonic() + 0.02,
                )

        self.assertEqual(raised.exception.kind, "timeout")
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_process_and_reader_start_failures_remain_exact(self):
        process_start = OSError("injected Popen failure")
        with _RuntimeSeams(self, popen_error=process_start):
            with self.assertRaises(BaseException) as raised:
                self._run(subject.ProcessBudget(64, 64))
        self.assertIs(raised.exception, process_start)

        reader_start = RuntimeError("injected reader start failure")
        process = _FakeProcess(auto_exit=False)
        factory = _ThreadFactory(fail_start_index=1, start_error=reader_start)
        with _RuntimeSeams(self, (process,), factory=factory) as seams:
            with self.assertRaises(BaseException) as raised:
                self._run(subject.ProcessBudget(64, 64))
        self.assertIs(raised.exception, reader_start)
        self.assertEqual(self._cause_chain(reader_start), [reader_start])
        self._assert_fake_clean(process, factory, killed=True, bounded=True)
        self.assertEqual(sum(thread.started for thread in factory.threads), 1)

        append_failure = RuntimeError("injected pre-effect registration failure")

        class FailingAppend(list):
            def append(self, value):
                if len(self) == 1:
                    raise append_failure
                super().append(value)

        class RegistrationState(subject._ProcessState):
            def __init__(self, deadline):
                super().__init__(deadline)
                self.threads = FailingAppend()

        process = _FakeProcess(auto_exit=False)
        factory = _ThreadFactory()
        with mock.patch.object(subject, "_ProcessState", RegistrationState):
            with _RuntimeSeams(self, (process,), factory=factory) as seams:
                with self.assertRaises(BaseException) as raised:
                    self._run(subject.ProcessBudget(64, 64))
        self.assertIs(raised.exception, append_failure)
        self._assert_fake_clean(process, factory, killed=True, bounded=True)

    def test_consumer_failure_and_interrupt_remain_exact(self):
        failures = (
            RuntimeError("injected consumer failure"),
            KeyboardInterrupt("injected consumer interruption"),
        )
        for failure in failures:
            with self.subTest(failure=type(failure).__name__):
                process = _FakeProcess(stdout=b"data", auto_exit=False)

                def consume(_chunk):
                    raise failure

                with _RuntimeSeams(self, (process,)) as seams:
                    with self.assertRaises(BaseException) as raised:
                        self._run(
                            subject.ProcessBudget(64, 64), consumer=consume
                        )
                self.assertIs(raised.exception, failure)
                self.assertEqual(self._cause_chain(failure).count(failure), 1)
                self._assert_fake_clean(
                    process, seams.factory, killed=True, bounded=True
                )

    def test_reader_failures_become_ordered_cleanup_failure(self):
        stdout_error = OSError("injected stdout reader failure")
        stderr_error = OSError("injected stderr reader failure")
        process = _FakeProcess(
            auto_exit=False,
            stdout_error=stdout_error,
            stderr_error=stderr_error,
        )
        with _RuntimeSeams(self, (process,)) as seams:
            with self.assertRaises(subject.ProcessFailure) as raised:
                self._run(subject.ProcessBudget(64, 64))

        self.assertEqual(raised.exception.kind, "cleanup")
        chain = self._cause_chain(raised.exception)
        self.assertLess(chain.index(stdout_error), chain.index(stderr_error), chain)
        self.assertEqual(chain.count(stdout_error), 1)
        self.assertEqual(chain.count(stderr_error), 1)
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_cleanup_failures_chain_in_order_and_deduplicate(self):
        primary = RuntimeError("injected consumer primary")
        stderr_error = OSError("injected stderr reader failure")
        kill_error = OSError("injected process-group kill failure")
        wait_error = OSError("injected bounded wait failure")
        join_error = OSError("injected bounded join failure")
        close_error = OSError("injected pipe close failure")
        process = _FakeProcess(
            stdout=b"trigger",
            stderr_error=stderr_error,
            status=7,
            auto_exit=False,
            close_error=close_error,
            wait_errors=(wait_error,),
            wait_error_sets_status=True,
        )
        factory = _ThreadFactory(join_error=join_error)

        def consume(_chunk):
            raise primary

        with _RuntimeSeams(
            self,
            (process,),
            factory=factory,
            kill_errors=(kill_error,),
        ) as seams:
            with self.assertRaises(BaseException) as raised:
                self._run(subject.ProcessBudget(64, 64), consumer=consume)

        self.assertIs(raised.exception, primary)
        chain = self._cause_chain(primary)
        ordered = (
            stderr_error,
            kill_error,
            wait_error,
            join_error,
            close_error,
        )
        positions = [chain.index(error) for error in ordered]
        self.assertEqual(positions, sorted(positions), chain)
        for error in (primary, *ordered):
            self.assertEqual(chain.count(error), 1, chain)
        self.assertFalse(
            any(
                isinstance(error, subject.ProcessFailure)
                and error.kind == "cleanup"
                for error in chain
            ),
            chain,
        )
        status = [
            error
            for error in chain
            if isinstance(error, subject.ProcessFailure) and error.kind == "status"
        ]
        self.assertEqual(len(status), 1, chain)
        self.assertGreater(chain.index(status[0]), positions[-1], chain)
        self._assert_fake_clean(process, factory, killed=False, bounded=True)
        self.assertGreaterEqual(len(seams.killpg_calls), 1)

    def test_caller_interrupt_during_wait_remains_exact(self):
        interruption = KeyboardInterrupt("injected caller interruption")
        process = _FakeProcess(auto_exit=False)
        with _RuntimeSeams(self, (process,)) as seams:
            with mock.patch.object(
                subject,
                "_observe_status",
                side_effect=(interruption, None),
            ):
                with self.assertRaises(BaseException) as raised:
                    self._run(subject.ProcessBudget(64, 64))

        self.assertIs(raised.exception, interruption)
        self.assertEqual(self._cause_chain(interruption).count(interruption), 1)
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_real_descendant_held_pipe_is_terminated_promptly(self):
        metadata = os.path.join(self.root, b"descendant.meta")
        heartbeat = os.path.join(self.root, b"descendant.heartbeat")
        code = b"""
import os, sys, time
metadata, heartbeat = os.fsencode(sys.argv[1]), os.fsencode(sys.argv[2])
owner = os.getppid()
descendant = os.fork()
if descendant == 0:
    deadline = time.monotonic() + 15
    with open(heartbeat, 'ab', buffering=0) as output:
        while time.monotonic() < deadline:
            try:
                os.kill(owner, 0)
            except ProcessLookupError:
                break
            output.write(b'x')
            os.fsync(output.fileno())
            time.sleep(0.02)
    os._exit(0)
while not os.path.exists(heartbeat):
    time.sleep(0.005)
with open(metadata, 'wb', buffering=0) as output:
    output.write(('%d %d' % (os.getpgrp(), descendant)).encode('ascii'))
    os.fsync(output.fileno())
os._exit(0)
"""
        environment = os.environ.copy()
        environment["PYTHONDONTWRITEBYTECODE"] = "1"
        started = time.monotonic()
        group = descendant = None
        try:
            with self.assertRaises(subject.ProcessFailure) as raised:
                subject.run_process(
                    os.fsencode(sys.executable),
                    (b"-B", b"-c", code, metadata, heartbeat),
                    self.root,
                    environment,
                    started + 5,
                    subject.ProcessBudget(1024, 1024),
                )
            elapsed = time.monotonic() - started
            self.assertEqual(raised.exception.kind, "cleanup")
            self.assertLess(elapsed, 3.0, "descendant-held pipe exceeded cleanup grace")
            with open(metadata, "rb") as source:
                group, descendant = map(int, source.read().split())
            self.assertNotEqual(group, os.getpgrp())
            before = os.path.getsize(heartbeat)
            time.sleep(0.15)
            self.assertEqual(os.path.getsize(heartbeat), before)
            self._assert_process_terminated(descendant)
        finally:
            if group is None:
                with contextlib.suppress(FileNotFoundError, ValueError, OSError):
                    with open(metadata, "rb") as source:
                        group, descendant = map(int, source.read().split())
            self._terminate_fixture(group, descendant)
            if group is not None:
                self._assert_process_terminated(descendant)

    def test_unreachable_group_kill_does_not_block_pipe_cleanup(self):
        metadata = os.path.join(self.root, b"unreachable.meta")
        heartbeat = os.path.join(self.root, b"unreachable.heartbeat")
        code = b"""
import os, sys, time
metadata, heartbeat = os.fsencode(sys.argv[1]), os.fsencode(sys.argv[2])
descendant = os.fork()
if descendant == 0:
    deadline = time.monotonic() + 2.4
    with open(heartbeat, 'ab', buffering=0) as output:
        while time.monotonic() < deadline:
            output.write(b'x')
            os.fsync(output.fileno())
            time.sleep(0.02)
    os._exit(0)
while not os.path.exists(heartbeat):
    time.sleep(0.005)
with open(metadata, 'wb', buffering=0) as output:
    output.write(('%d %d' % (os.getpgrp(), descendant)).encode('ascii'))
    os.fsync(output.fileno())
os._exit(0)
"""
        kill_error = PermissionError("injected unreachable process group")
        kill_calls = []
        processes = []
        factory = _RecordingThreadFactory()

        def unreachable_killpg(group, requested_signal):
            kill_calls.append((group, requested_signal))
            raise kill_error

        def recording_popen(*args, **kwargs):
            process = subprocess.Popen(*args, **kwargs)
            processes.append(process)
            return process

        group = None
        descendant = None
        started = time.monotonic()
        try:
            with contextlib.ExitStack() as stack:
                stack.enter_context(
                    mock.patch.object(
                        subject,
                        "subprocess",
                        _DelegatingModule(subject.subprocess, Popen=recording_popen),
                    )
                )
                stack.enter_context(
                    mock.patch.object(
                        subject,
                        "threading",
                        _DelegatingModule(subject.threading, Thread=factory),
                    )
                )
                stack.enter_context(
                    mock.patch.object(
                        subject,
                        "os",
                        _DelegatingModule(subject.os, killpg=unreachable_killpg),
                    )
                )
                with self.assertRaises(subject.ProcessFailure) as raised:
                    subject.run_process(
                        os.fsencode(sys.executable),
                        (b"-B", b"-c", code, metadata, heartbeat),
                        self.root,
                        os.environ.copy(),
                        time.monotonic() + 5,
                        subject.ProcessBudget(1024, 1024),
                    )
            elapsed = time.monotonic() - started
            with open(metadata, "rb") as source:
                group, descendant = map(int, source.read().split())

            self.assertEqual(raised.exception.kind, "cleanup")
            self.assertEqual(self._cause_chain(raised.exception).count(kill_error), 1)
            self.assertLess(
                elapsed,
                1.5,
                "pipe close waited for the writer past the shared cleanup grace",
            )
            self.assertGreaterEqual(len(kill_calls), 2)
            self.assertTrue(
                all(call == (group, signal.SIGKILL) for call in kill_calls)
            )
            self.assertTrue(factory.threads)
            self.assertTrue(all(thread.join_calls for thread in factory.threads))
            self.assertEqual(len(processes), 1)
            self.assertIsNotNone(processes[0].returncode, "leader was not reaped")
            self.assertLessEqual(
                sum(
                    elapsed
                    for thread in factory.threads
                    for elapsed in thread.join_elapsed
                ),
                1.2,
                "reader joins did not share one cleanup deadline",
            )
            self.assertFalse(
                any(thread.is_alive() for thread in factory.threads),
                "reader survived unreachable-group cleanup",
            )
            before = os.path.getsize(heartbeat)
            time.sleep(0.1)
            self.assertGreater(
                os.path.getsize(heartbeat),
                before,
                "writer fallback fired before bounded cleanup returned",
            )
        finally:
            if group is None:
                with contextlib.suppress(FileNotFoundError, ValueError, OSError):
                    with open(metadata, "rb") as source:
                        group, descendant = map(int, source.read().split())
            self._terminate_fixture(group, descendant)
            if descendant is not None:
                self._assert_process_terminated(descendant)
            for process in processes:
                with contextlib.suppress(ChildProcessError, subprocess.TimeoutExpired):
                    process.wait(timeout=1)

    def test_delayed_failure_outranks_nonzero_status(self):
        code = b"import os, sys; os.write(1, b'x'); sys.exit(7)"
        for failure_kind in ("consumer", "limit"):
            with self.subTest(failure=failure_kind):
                failure = (
                    RuntimeError("injected delayed consumer failure")
                    if failure_kind == "consumer"
                    else subject.ProcessFailure("limit")
                )
                budget = subject.ProcessBudget(64, 64)
                delivered = []

                def delayed_consumer(_chunk):
                    time.sleep(0.2)
                    raise failure

                if failure_kind == "limit":
                    def delayed_charge(_stream, _amount):
                        time.sleep(0.2)
                        raise failure

                    budget._charge = delayed_charge
                    consumer = delivered.append
                else:
                    consumer = delayed_consumer

                with self.assertRaises(BaseException) as raised:
                    subject.run_process(
                        os.fsencode(sys.executable),
                        (b"-B", b"-c", code),
                        self.root,
                        os.environ.copy(),
                        time.monotonic() + 5,
                        budget,
                        consumer=consumer,
                    )

                self.assertIs(raised.exception, failure)
                chain = self._cause_chain(failure)
                status = [
                    error
                    for error in chain
                    if isinstance(error, subject.ProcessFailure)
                    and error.kind == "status"
                ]
                self.assertEqual(len(status), 1, chain)
                self.assertIs(chain[-1], status[0])
                self.assertEqual(delivered, [])

    def test_session_leader_is_observed_before_group_signal_and_reap(self):
        required = ("P_PID", "WEXITED", "WNOHANG", "WNOWAIT")
        if not all(hasattr(os, name) for name in required):
            self.skipTest("POSIX waitid WNOWAIT is unavailable")
        process = _FakeProcess(status=7)
        with _RuntimeSeams(self, (process,)) as seams:
            with self.assertRaises(subject.ProcessFailure) as raised:
                self._run(subject.ProcessBudget(64, 64))

        self.assertEqual(raised.exception.kind, "status")
        self.assertTrue(seams.waitid_calls, "leader was reaped without WNOWAIT")
        for id_type, process_id, options in seams.waitid_calls:
            self.assertEqual((id_type, process_id), (os.P_PID, process.pid))
            self.assertEqual(options & os.WEXITED, os.WEXITED)
            self.assertEqual(options & os.WNOHANG, os.WNOHANG)
            self.assertEqual(options & os.WNOWAIT, os.WNOWAIT)
        self.assertEqual(seams.signal_reservations, [True])
        signal_index = process.events.index("signal")
        reap_index = process.events.index("wait")
        self.assertIn("observe", process.events[:signal_index])
        self.assertNotIn("poll", process.events[:signal_index])
        self.assertLess(signal_index, reap_index, process.events)
        self.assertTrue(process.reaped)
        self.assertTrue(process.wait_calls)
        self.assertTrue(
            all(value is not None and 0 <= value <= 1.1 for value in process.wait_calls)
        )

    def test_falsey_consumer_failure_remains_exact(self):
        failure = _FalseyFailure("injected falsey consumer failure")
        process = _FakeProcess(stdout=b"trigger")

        def consume(_chunk):
            raise failure

        with _RuntimeSeams(self, (process,)) as seams:
            with self.assertRaises(BaseException) as raised:
                self._run(subject.ProcessBudget(64, 64), consumer=consume)

        self.assertIs(raised.exception, failure)
        self.assertEqual(self._cause_chain(failure).count(failure), 1)
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_failure_composition_matches_quiescence_order(self):
        with self.subTest(shape="pre-caused"):
            primary = RuntimeError("injected primary")
            original = OSError("injected original primary cause")
            cleanup = OSError("injected cleanup")
            cleanup_underlying = OSError("injected cleanup underlying cause")
            primary.__cause__ = original
            cleanup.__cause__ = cleanup_underlying
            cleanup.__context__ = primary

            subject._chain_failures(primary, (cleanup, cleanup))

            chain = self._cause_chain(primary)
            self.assertEqual(
                chain,
                [primary, cleanup, cleanup_underlying, original],
            )
            self.assertEqual(chain.count(cleanup), 1)
            self.assertFalse(
                self._exception_graph_reaches(cleanup.__context__, {id(primary)})
            )
            self.assertTrue(cleanup.__suppress_context__)

        with self.subTest(shape="self-caused"):
            primary = RuntimeError("injected self-caused primary")
            cleanup = OSError("injected cleanup for self-caused primary")
            primary.__cause__ = primary
            cleanup.__context__ = primary

            subject._chain_failures(primary, (cleanup, cleanup))

            self.assertEqual(self._cause_chain(primary), [primary, cleanup])
            self.assertFalse(
                self._exception_graph_reaches(cleanup.__context__, {id(primary)})
            )

    def test_started_then_raising_reader_is_joined(self):
        start_error = KeyboardInterrupt("injected post-start interruption")
        process = _FakeProcess(auto_exit=False)
        factory = _RecordingThreadFactory(
            fail_start_index=0,
            start_error=start_error,
        )

        with _RuntimeSeams(self, (process,), factory=factory) as seams:
            with self.assertRaises(BaseException) as raised:
                self._run(subject.ProcessBudget(64, 64))

        self.assertIs(raised.exception, start_error)
        self.assertEqual(len(factory.threads), 1)
        reader = factory.threads[0]
        runtime_joined = bool(reader.join_calls)
        runtime_join_calls = tuple(reader.join_calls)
        reader._thread.join(timeout=1)
        self.assertFalse(reader.is_alive(), "post-start reader leaked")
        self.assertTrue(runtime_joined, "started reader was not tracked and joined")
        self.assertTrue(
            all(value is not None and 0 <= value <= 1.1 for value in runtime_join_calls)
        )
        self.assertEqual(seams.killpg_calls, [(process.pid, signal.SIGKILL)])
        self.assertTrue(process.stdout.closed)
        self.assertTrue(process.stderr.closed)

    def test_read_and_close_base_interrupts_remain_exact(self):
        for origin in ("read", "close"):
            for failure_type in (KeyboardInterrupt, SystemExit):
                with self.subTest(origin=origin, failure=failure_type.__name__):
                    failure = failure_type("injected %s interruption" % origin)
                    options = (
                        {"stdout_error": failure, "auto_exit": False}
                        if origin == "read"
                        else {"close_error": failure}
                    )
                    process = _FakeProcess(**options)
                    with _RuntimeSeams(self, (process,)) as seams:
                        with self.assertRaises(BaseException) as raised:
                            self._run(subject.ProcessBudget(64, 64))

                    self.assertIs(raised.exception, failure)
                    self.assertEqual(self._cause_chain(failure).count(failure), 1)
                    self._assert_fake_clean(
                        process,
                        seams.factory,
                        killed=True,
                        bounded=True,
                    )

    def test_cleanup_wait_retry_and_natural_sigkill_are_distinct(self):
        with self.subTest(case="retry reap"):
            primary = RuntimeError("injected consumer primary")
            wait_error = OSError("injected first bounded wait failure")
            process = _FakeProcess(
                stdout=b"trigger",
                auto_exit=False,
                wait_errors=(wait_error,),
            )

            def consume(_chunk):
                raise primary

            with _RuntimeSeams(self, (process,)) as seams:
                with self.assertRaises(BaseException) as raised:
                    self._run(subject.ProcessBudget(64, 64), consumer=consume)

            self.assertIs(raised.exception, primary)
            chain = self._cause_chain(primary)
            self.assertEqual(chain.count(wait_error), 1)
            self.assertFalse(
                any(
                    isinstance(error, subject.ProcessFailure)
                    and error.kind == "status"
                    for error in chain
                ),
                chain,
            )
            self._assert_fake_clean(
                process,
                seams.factory,
                killed=True,
                bounded=True,
            )
            bounded_waits = [value for value in process.wait_calls if value is not None]
            self.assertGreaterEqual(len(bounded_waits), 2, bounded_waits)
            self.assertTrue(all(0 <= value <= 1.1 for value in bounded_waits))
            self.assertLessEqual(bounded_waits[-1], bounded_waits[0])

        with self.subTest(case="natural sigkill"):
            primary = RuntimeError("injected consumer before natural SIGKILL")
            process = _FakeProcess(
                stdout=b"trigger",
                status=-signal.SIGKILL,
            )

            def consume(_chunk):
                raise primary

            with _RuntimeSeams(self, (process,)) as seams:
                with self.assertRaises(BaseException) as raised:
                    self._run(subject.ProcessBudget(64, 64), consumer=consume)

            self.assertIs(raised.exception, primary)
            chain = self._cause_chain(primary)
            status = [
                error
                for error in chain
                if isinstance(error, subject.ProcessFailure)
                and error.kind == "status"
            ]
            self.assertEqual(len(status), 1, chain)
            self.assertIs(chain[-1], status[0])
            self.assertEqual(seams.killpg_calls, [(process.pid, signal.SIGKILL)])
            self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_expired_and_crossing_deadlines_are_timeouts(self):
        expired = _FakeProcess(stdout=b"must not run")
        with _RuntimeSeams(self, (expired,)) as seams:
            with self.assertRaises(subject.ProcessFailure) as raised:
                self._run(
                    subject.ProcessBudget(64, 64),
                    deadline=time.monotonic() - 0.01,
                )
        self.assertEqual(raised.exception.kind, "timeout")
        self.assertEqual(seams.popen_calls, [], "expired work was spawned")

        crossing = _FakeProcess(stdout=b"too late")
        clock = [10.0]

        def cross_deadline(_process):
            clock[0] = 11.0
            return 0

        with _RuntimeSeams(self, (crossing,)) as seams:
            with contextlib.ExitStack() as stack:
                stack.enter_context(
                    mock.patch.object(subject.time, "monotonic", lambda: clock[0])
                )
                stack.enter_context(
                    mock.patch.object(subject, "_observe_status", cross_deadline)
                )
                with self.assertRaises(subject.ProcessFailure) as raised:
                    self._run(
                        subject.ProcessBudget(64, 64),
                        deadline=10.5,
                    )
        self.assertEqual(raised.exception.kind, "timeout")
        self._assert_fake_clean(crossing, seams.factory, killed=True, bounded=True)

    def test_timeout_remains_primary_over_cleanup_interrupt(self):
        interruption = KeyboardInterrupt("injected cleanup interruption")
        process = _FakeProcess(
            auto_exit=False,
            hang=True,
            close_error=interruption,
        )
        with _RuntimeSeams(self, (process,)) as seams:
            with self.assertRaises(BaseException) as raised:
                self._run(
                    subject.ProcessBudget(64, 64),
                    deadline=time.monotonic() + 0.02,
                )

        self.assertIsInstance(raised.exception, subject.ProcessFailure)
        self.assertEqual(raised.exception.kind, "timeout")
        self.assertIn(interruption, self._cause_chain(raised.exception))
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_late_consumer_failure_remains_exact_after_join(self):
        for status in (0, 7):
            with self.subTest(status=status):
                failure = RuntimeError("injected late consumer failure")
                release = threading.Event()
                factory = _RecordingThreadFactory(join_callback=release.set)
                process = _FakeProcess(stdout=b"trigger", status=status)

                def consume(_chunk):
                    self.assertTrue(release.wait(timeout=2))
                    raise failure

                with _RuntimeSeams(
                    self,
                    (process,),
                    factory=factory,
                ) as seams:
                    with mock.patch.object(subject, "_CLEANUP_SECONDS", 0.05):
                        with self.assertRaises(BaseException) as raised:
                            self._run(
                                subject.ProcessBudget(64, 64),
                                consumer=consume,
                            )

                self.assertIs(raised.exception, failure)
                chain = self._cause_chain(failure)
                if status:
                    self.assertEqual(
                        sum(
                            isinstance(error, subject.ProcessFailure)
                            and error.kind == "status"
                            for error in chain
                        ),
                        1,
                        chain,
                    )
                else:
                    self.assertFalse(
                        any(
                            isinstance(error, subject.ProcessFailure)
                            and error.kind == "status"
                            for error in chain
                        ),
                        chain,
                    )
                self._assert_fake_clean(
                    process,
                    seams.factory,
                    killed=True,
                    bounded=True,
                )

    def test_late_consumer_outranks_observation_failure(self):
        consumer_failure = RuntimeError("injected late consumer failure")
        observation_failure = OSError("injected waitid failure")
        release = threading.Event()
        factory = _RecordingThreadFactory(join_callback=release.set)
        process = _FakeProcess(stdout=b"trigger", auto_exit=False)

        def consume(_chunk):
            self.assertTrue(release.wait(timeout=2))
            raise consumer_failure

        with _RuntimeSeams(self, (process,), factory=factory) as seams:
            with mock.patch.object(
                subject,
                "_observe_status",
                side_effect=(observation_failure, None),
            ):
                with self.assertRaises(BaseException) as raised:
                    self._run(
                        subject.ProcessBudget(64, 64),
                        consumer=consume,
                    )

        self.assertIs(raised.exception, consumer_failure)
        self.assertEqual(
            self._cause_chain(consumer_failure).count(observation_failure),
            1,
        )
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_finished_then_raising_reader_is_joined(self):
        start_error = KeyboardInterrupt("injected post-finish start interruption")
        process = _FakeProcess(auto_exit=False)
        factory = _RecordingThreadFactory(
            fail_start_index=0,
            start_error=start_error,
            finish_before_error=True,
        )

        with _RuntimeSeams(self, (process,), factory=factory) as seams:
            with self.assertRaises(BaseException) as raised:
                self._run(subject.ProcessBudget(64, 64))

        self.assertIs(raised.exception, start_error)
        self.assertEqual(len(factory.threads), 1)
        self.assertIsNotNone(factory.threads[0].ident)
        self.assertFalse(factory.threads[0].is_alive())
        self.assertTrue(
            factory.threads[0].join_calls,
            "finished post-start reader was never registered for cleanup",
        )
        self.assertEqual(seams.killpg_calls, [(process.pid, signal.SIGKILL)])

    def test_reap_interruption_never_signals_after_ownership_loss(self):
        interruption = KeyboardInterrupt("injected post-reap interruption")
        process = _FakeProcess(
            wait_errors=(interruption,),
            wait_error_reaps=True,
        )

        with _RuntimeSeams(self, (process,)) as seams:
            with self.assertRaises(BaseException) as raised:
                self._run(subject.ProcessBudget(64, 64))

        self.assertIs(raised.exception, interruption)
        self.assertTrue(process.reaped)
        self.assertEqual(seams.killpg_calls, [])
        self.assertEqual(seams.signal_reservations, [])
        self.assertGreaterEqual(len(process.wait_calls), 2)
        self.assertTrue(process.stdout.closed)
        self.assertTrue(process.stderr.closed)

    def test_natural_sigkill_is_not_suppressed_after_failed_group_signal(self):
        primary = RuntimeError("injected consumer failure before natural SIGKILL")
        kill_failure = ProcessLookupError("injected vanished process group")
        process = _FakeProcess(
            stdout=b"trigger",
            status=-signal.SIGKILL,
            auto_exit=False,
        )

        def consume(_chunk):
            raise primary

        with _RuntimeSeams(
            self,
            (process,),
            kill_errors=(kill_failure,),
        ) as seams:
            with self.assertRaises(BaseException) as raised:
                self._run(
                    subject.ProcessBudget(64, 64),
                    consumer=consume,
                )

        self.assertIs(raised.exception, primary)
        chain = self._cause_chain(primary)
        status = [
            error
            for error in chain
            if isinstance(error, subject.ProcessFailure)
            and error.kind == "status"
        ]
        self.assertEqual(len(status), 1, chain)
        self.assertIs(chain[-1], status[0])
        self.assertEqual(seams.killpg_calls, [(process.pid, signal.SIGKILL)])
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_process_budget_rejects_unbounded_limits(self):
        invalid = (-1, True, 1.0, float("nan"), float("inf"), "64", None)
        for index in (0, 1):
            for limit in invalid:
                with self.subTest(stream=index, limit=limit):
                    limits = [64, 64]
                    limits[index] = limit
                    with self.assertRaisesRegex(
                        ValueError,
                        "^process limits must be non-negative integers$",
                    ):
                        subject.ProcessBudget(*limits)

    def test_waitid_none_crossing_deadline_remains_timeout(self):
        consumer_failure = RuntimeError("consumer became visible after deadline")
        released = threading.Event()
        failed = threading.Event()
        factory = _RecordingThreadFactory()
        process = _FakeProcess(stdout=b"trigger", auto_exit=False)
        clock = [10.0]

        def consume(_chunk):
            self.assertTrue(released.wait(timeout=2))
            failed.set()
            raise consumer_failure

        def cross_deadline(_process):
            clock[0] = 11.0
            released.set()
            self.assertTrue(failed.wait(timeout=2))
            return None

        time_proxy = _DelegatingModule(subject.time, monotonic=lambda: clock[0])
        with _RuntimeSeams(self, (process,), factory=factory) as seams:
            with contextlib.ExitStack() as stack:
                stack.enter_context(mock.patch.object(subject, "time", time_proxy))
                stack.enter_context(
                    mock.patch.object(subject, "_observe_status", cross_deadline)
                )
                with self.assertRaises(BaseException) as raised:
                    self._run(
                        subject.ProcessBudget(64, 64),
                        consumer=consume,
                        deadline=10.5,
                    )

        self.assertIsInstance(raised.exception, subject.ProcessFailure)
        self.assertEqual(raised.exception.kind, "timeout")
        self.assertIn(consumer_failure, self._cause_chain(raised.exception))
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_status_known_drain_crossing_deadline_is_timeout(self):
        ticks = iter((10.0, 10.1, 10.2, 11.0))

        def monotonic():
            return next(ticks, 11.0)

        def slow_consumer(_chunk):
            time.sleep(0.05)

        process = _FakeProcess(stdout=b"trigger")
        factory = _RecordingThreadFactory()
        time_proxy = _DelegatingModule(subject.time, monotonic=monotonic)
        with _RuntimeSeams(self, (process,), factory=factory) as seams:
            with mock.patch.object(subject, "time", time_proxy):
                with self.assertRaises(subject.ProcessFailure) as raised:
                    self._run(
                        subject.ProcessBudget(64, 64),
                        consumer=slow_consumer,
                        deadline=10.5,
                    )

        self.assertEqual(raised.exception.kind, "timeout")
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_waitid_ownership_loss_never_signals_group(self):
        ownership_error = ChildProcessError("injected ECHILD ownership loss")
        process = _FakeProcess(auto_exit=False)
        process.returncode = 0
        process.reaped = True

        with _RuntimeSeams(self, (process,)) as seams:
            with mock.patch.object(
                subject,
                "_observe_status",
                side_effect=ownership_error,
            ):
                with self.assertRaises(subject.ProcessFailure) as raised:
                    self._run(subject.ProcessBudget(64, 64))

        self.assertEqual(raised.exception.kind, "cleanup")
        self.assertEqual(
            self._cause_chain(raised.exception).count(ownership_error),
            1,
        )
        self.assertEqual(seams.killpg_calls, [], "unreserved PGID was signaled")
        self.assertTrue(process.wait_calls)
        self.assertTrue(process.stdout.closed)
        self.assertTrue(process.stderr.closed)

    def test_cleanup_phase_interrupts_still_release_resources(self):
        with self.subTest(phase="transition"):
            primary = RuntimeError("injected consumer primary")
            interruption = KeyboardInterrupt("injected cleanup transition interrupt")
            calls = 0

            def interrupted_monotonic():
                nonlocal calls
                calls += 1
                if calls == 4:
                    raise interruption
                return time.monotonic()

            process = _FakeProcess(stdout=b"trigger", auto_exit=False)

            def consume(_chunk):
                raise primary

            time_proxy = _DelegatingModule(
                subject.time,
                monotonic=interrupted_monotonic,
            )
            with _RuntimeSeams(self, (process,)) as seams:
                with mock.patch.object(subject, "time", time_proxy):
                    with self.assertRaises(BaseException) as raised:
                        self._run(
                            subject.ProcessBudget(64, 64),
                            consumer=consume,
                        )

            self.assertIs(raised.exception, primary)
            self.assertIn(interruption, self._cause_chain(primary))
            self._assert_fake_clean(
                process,
                seams.factory,
                killed=True,
                bounded=True,
            )

        with self.subTest(phase="reader liveness"):
            primary = RuntimeError("injected consumer primary")
            interruption = SystemExit("injected is_alive interruption")
            process = _FakeProcess(stdout=b"trigger", auto_exit=False)
            factory = _ThreadFactory(alive_error=interruption)

            def consume(_chunk):
                raise primary

            with _RuntimeSeams(self, (process,), factory=factory) as seams:
                with self.assertRaises(BaseException) as raised:
                    self._run(
                        subject.ProcessBudget(64, 64),
                        consumer=consume,
                    )

            self.assertIs(raised.exception, primary)
            self.assertIn(interruption, self._cause_chain(primary))
            self._assert_fake_clean(
                process,
                seams.factory,
                killed=True,
                bounded=True,
            )

    def test_successful_group_signal_preserves_natural_sigkill(self):
        primary = RuntimeError("consumer before racing natural SIGKILL")
        process = _FakeProcess(
            stdout=b"trigger",
            status=-signal.SIGKILL,
            auto_exit=False,
        )

        def consume(_chunk):
            raise primary

        with _RuntimeSeams(self, (process,)) as seams:
            with mock.patch.object(
                subject,
                "_observe_status",
                side_effect=(None, -signal.SIGKILL),
            ):
                with self.assertRaises(BaseException) as raised:
                    self._run(
                        subject.ProcessBudget(64, 64),
                        consumer=consume,
                    )

        self.assertIs(raised.exception, primary)
        chain = self._cause_chain(primary)
        status = [
            error
            for error in chain
            if isinstance(error, subject.ProcessFailure)
            and error.kind == "status"
        ]
        self.assertEqual(len(status), 1, chain)
        self.assertIs(chain[-1], status[0])
        self.assertEqual(seams.killpg_calls, [(process.pid, signal.SIGKILL)])
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_stale_reader_snapshot_cannot_reap_before_signal(self):
        primary = RuntimeError("consumer failure after stale reader snapshot")
        process = _FakeProcess(stdout=b"trigger")
        original = subject._reader_primary
        calls = 0

        def stale_once(primary_errors, reader_errors):
            nonlocal calls
            calls += 1
            if calls == 1:
                return None, False
            return original(primary_errors, reader_errors)

        def consume(_chunk):
            raise primary

        with _RuntimeSeams(self, (process,)) as seams:
            with mock.patch.object(subject, "_reader_primary", stale_once):
                with self.assertRaises(BaseException) as raised:
                    self._run(
                        subject.ProcessBudget(64, 64),
                        consumer=consume,
                    )

        self.assertIs(raised.exception, primary)
        self.assertGreaterEqual(calls, 2)
        self.assertEqual(seams.killpg_calls, [(process.pid, signal.SIGKILL)])
        self.assertEqual(seams.signal_reservations, [True])
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_cleanup_guard_survives_two_interrupts(self):
        primary = RuntimeError("consumer before cleanup guard")
        interruptions = [
            KeyboardInterrupt("first cleanup guard interruption"),
            SystemExit("second cleanup guard interruption"),
        ]

        class InterruptingState(subject._ProcessState):
            def __init__(self, deadline):
                self.guard_interruptions = list(interruptions)
                super().__init__(deadline)

            def __getattribute__(self, name):
                if name == "cleanup_complete":
                    pending = object.__getattribute__(
                        self,
                        "guard_interruptions",
                    )
                    if pending:
                        raise pending.pop(0)
                return super().__getattribute__(name)

        process = _FakeProcess(stdout=b"trigger", auto_exit=False)

        def consume(_chunk):
            raise primary

        with _RuntimeSeams(self, (process,)) as seams:
            with mock.patch.object(subject, "_ProcessState", InterruptingState):
                with self.assertRaises(BaseException) as raised:
                    self._run(
                        subject.ProcessBudget(64, 64),
                        consumer=consume,
                    )

        self.assertIs(raised.exception, primary)
        chain = self._cause_chain(primary)
        for interruption in interruptions:
            self.assertEqual(chain.count(interruption), 1, chain)
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_pre_effect_join_and_close_are_retried(self):
        with self.subTest(resource="join"):
            interruption = KeyboardInterrupt("pre-join interruption")
            reader_error = OSError("force cleanup while stdout consumer waits")
            release = threading.Event()
            process = _FakeProcess(
                stdout=b"trigger",
                stderr_error=reader_error,
                auto_exit=False,
            )
            factory = _RecordingThreadFactory(
                join_callback=release.set,
                join_error=interruption,
            )

            def consume(_chunk):
                self.assertTrue(release.wait(timeout=2))

            runtime_alive = None
            try:
                with _RuntimeSeams(self, (process,), factory=factory) as seams:
                    with self.assertRaises(BaseException) as raised:
                        self._run(
                            subject.ProcessBudget(64, 64),
                            consumer=consume,
                        )
                runtime_alive = factory.threads[0].is_alive()
                self.assertIs(raised.exception, interruption)
                self.assertGreaterEqual(factory.threads[0].delegate_join_calls, 1)
                self.assertFalse(runtime_alive, "reader was never actually joined")
                self._assert_fake_clean(
                    process,
                    seams.factory,
                    killed=True,
                    bounded=True,
                )
            finally:
                release.set()
                for thread in factory.threads:
                    thread._thread.join(timeout=1)

        with self.subTest(resource="close"):
            interruption = SystemExit("pre-close interruption")
            process = _FakeProcess(
                close_error=interruption,
                close_before_error=True,
            )
            with _RuntimeSeams(self, (process,)) as seams:
                with self.assertRaises(BaseException) as raised:
                    self._run(subject.ProcessBudget(64, 64))

            self.assertIs(raised.exception, interruption)
            self.assertTrue(process.stdout.closed)
            self.assertTrue(process.stderr.closed)
            self.assertGreaterEqual(process.stdout.close_calls, 2)
            self.assertGreaterEqual(process.stderr.close_calls, 2)
            self._assert_fake_clean(
                process,
                seams.factory,
                killed=True,
                bounded=True,
            )

    def test_cleanup_deadline_interruption_retains_grace(self):
        primary = RuntimeError("consumer before cleanup deadline")
        interruption = KeyboardInterrupt("cleanup deadline interruption")
        calls = 0

        def interrupted_monotonic():
            nonlocal calls
            calls += 1
            if calls == 4:
                raise interruption
            return time.monotonic()

        process = _FakeProcess(
            stdout=b"trigger",
            auto_exit=False,
            require_positive_wait=True,
        )

        def consume(_chunk):
            raise primary

        time_proxy = _DelegatingModule(subject.time, monotonic=interrupted_monotonic)
        with _RuntimeSeams(self, (process,)) as seams:
            with mock.patch.object(subject, "time", time_proxy):
                with self.assertRaises(BaseException) as raised:
                    self._run(
                        subject.ProcessBudget(64, 64),
                        consumer=consume,
                    )

        self.assertIs(raised.exception, primary)
        self.assertIn(interruption, self._cause_chain(primary))
        self.assertTrue(process.reaped)
        self.assertTrue(any(value and value > 0 for value in process.wait_calls))
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_interrupted_waitid_retries_while_owned(self):
        primary = RuntimeError("consumer before pre-signal EINTR")
        interruption = InterruptedError("injected pre-signal EINTR")
        process = _FakeProcess(stdout=b"trigger", auto_exit=False)

        def consume(_chunk):
            raise primary

        with _RuntimeSeams(self, (process,)) as seams:
            with mock.patch.object(
                subject,
                "_observe_status",
                side_effect=(None, interruption, None),
            ):
                with self.assertRaises(BaseException) as raised:
                    self._run(
                        subject.ProcessBudget(64, 64),
                        consumer=consume,
                    )

        self.assertIs(raised.exception, primary)
        self.assertIn(interruption, self._cause_chain(primary))
        self.assertEqual(seams.killpg_calls, [(process.pid, signal.SIGKILL)])
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_transient_group_signal_failure_is_retried_before_reap(self):
        primary = RuntimeError("consumer before pre-effect group-signal failure")
        interruption = KeyboardInterrupt("pre-effect killpg interruption")
        process = _FakeProcess(stdout=b"trigger", auto_exit=False, hang=True)

        def consume(_chunk):
            raise primary

        with mock.patch.object(subject, "_CLEANUP_SECONDS", 0.02):
            with _RuntimeSeams(
                self,
                (process,),
                kill_errors=(interruption,),
            ) as seams:
                with self.assertRaises(BaseException) as raised:
                    self._run(subject.ProcessBudget(64, 64), consumer=consume)

        self.assertIs(raised.exception, primary)
        self.assertIn(interruption, self._cause_chain(primary))
        self.assertEqual(len(seams.killpg_calls), 2)
        self.assertTrue(process.reaped)
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_reap_transition_interruption_is_resumed(self):
        primary = RuntimeError("consumer before reap transition")
        interruption = SystemExit("between reap state and wait")
        process = _FakeProcess(stdout=b"trigger", auto_exit=False)

        class InterruptingReapState(subject._ProcessState):
            injected = False

            def __setattr__(self, name, value):
                super().__setattr__(name, value)
                if name == "reap_started" and value and not type(self).injected:
                    type(self).injected = True
                    raise interruption

        def consume(_chunk):
            raise primary

        with mock.patch.object(subject, "_ProcessState", InterruptingReapState):
            with _RuntimeSeams(self, (process,)) as seams:
                with self.assertRaises(BaseException) as raised:
                    self._run(subject.ProcessBudget(64, 64), consumer=consume)

        self.assertTrue(InterruptingReapState.injected)
        self.assertIs(raised.exception, primary)
        self.assertIn(interruption, self._cause_chain(primary))
        self.assertTrue(process.reaped)
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_pending_resources_survive_transition_interrupts(self):
        for attribute in ("join_pending", "close_pending"):
            with self.subTest(transition=attribute):
                interruption = KeyboardInterrupt("pending-resource transition")
                process = _FakeProcess()
                factory = _ThreadFactory()

                class InterruptingPopList(list):
                    injected = False

                    def pop(self, index=-1):
                        value = super().pop(index)
                        if not type(self).injected:
                            type(self).injected = True
                            raise interruption
                        return value

                class PendingState(subject._ProcessState):
                    def __setattr__(self, name, value):
                        if name == attribute and type(value) is list:
                            value = InterruptingPopList(value)
                        super().__setattr__(name, value)

                raised = None
                with mock.patch.object(subject, "_ProcessState", PendingState):
                    with _RuntimeSeams(
                        self,
                        (process,),
                        factory=factory,
                    ) as seams:
                        try:
                            self._run(subject.ProcessBudget(64, 64))
                        except BaseException as error:
                            raised = error

                if InterruptingPopList.injected:
                    self.assertIs(raised, interruption)
                else:
                    self.assertIsNone(raised)
                self.assertTrue(all(thread.join_calls for thread in factory.threads))
                self._assert_fake_clean(
                    process,
                    factory,
                    killed=InterruptingPopList.injected,
                    bounded=True,
                )

        publication_error = SystemExit("partial cleanup-complete publication")
        close_error = OSError("one pre-effect close failure")

        class GuardedCompletionState(subject._ProcessState):
            unsafe_publication = False

            def __setattr__(self, name, value):
                pending = self.__dict__.get("close_pending", ())
                if name == "cleanup_complete" and value and pending:
                    super().__setattr__(name, value)
                    type(self).unsafe_publication = True
                    raise publication_error
                super().__setattr__(name, value)

        process = _FakeProcess(
            close_error=close_error,
            close_before_error=True,
        )
        with mock.patch.object(subject, "_ProcessState", GuardedCompletionState):
            with _RuntimeSeams(self, (process,)) as seams:
                with self.assertRaises(BaseException):
                    self._run(subject.ProcessBudget(64, 64))

        self.assertFalse(GuardedCompletionState.unsafe_publication)
        self.assertTrue(process.stdout.closed)
        self.assertTrue(process.stderr.closed)
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def test_reader_completion_rechecks_absolute_deadline(self):
        clock = [10.0]
        release = threading.Event()
        process = _FakeProcess(stdout=b"trigger")
        factory = _RecordingThreadFactory()
        original = subject._reader_primary

        def monotonic():
            return clock[0]

        def consume(_chunk):
            self.assertTrue(release.wait(timeout=2))

        def finish_during_refresh(primary_errors, reader_errors):
            release.set()
            for thread in factory.threads:
                thread._thread.join(timeout=1)
            clock[0] = 11.0
            return original(primary_errors, reader_errors)

        time_proxy = _DelegatingModule(subject.time, monotonic=monotonic)
        with _RuntimeSeams(self, (process,), factory=factory) as seams:
            with mock.patch.object(subject, "time", time_proxy):
                with mock.patch.object(
                    subject,
                    "_reader_primary",
                    finish_during_refresh,
                ):
                    with self.assertRaises(subject.ProcessFailure) as raised:
                        self._run(
                            subject.ProcessBudget(64, 64),
                            consumer=consume,
                            deadline=10.5,
                        )

        self.assertEqual(raised.exception.kind, "timeout")
        self._assert_fake_clean(process, seams.factory, killed=True, bounded=True)

    def _run(self, budget, consumer=None, deadline=None):
        return subject.run_process(
            b"/usr/bin/tool",
            (b"--probe",),
            self.root,
            self.environment,
            time.monotonic() + 5 if deadline is None else deadline,
            budget,
            consumer=consumer,
        )

    def _assert_fake_clean(self, process, factory, *, killed, bounded=False):
        self.assertEqual(process.kill_calls, 0, "runner killed only direct child")
        self.assertTrue(process.stdout.closed)
        self.assertTrue(process.stderr.closed)
        self.assertGreaterEqual(process.stdout.close_calls, 1)
        self.assertGreaterEqual(process.stderr.close_calls, 1)
        started = [thread for thread in factory.threads if thread.started]
        self.assertTrue(started)
        for thread in started:
            self.assertTrue(thread.join_calls, "started reader was not joined")
            self.assertIsNone(thread.unhandled, "reader leaked an unhandled failure")
        if killed:
            self.assertLess(process.returncode or 0, 0)
        if bounded:
            cleanup_waits = [value for value in process.wait_calls if value is not None]
            cleanup_joins = [
                value
                for thread in started
                for value in thread.join_calls
                if value is not None
            ]
            self.assertTrue(cleanup_waits or cleanup_joins)
            self.assertTrue(
                any(value <= 1.1 for value in (*cleanup_waits, *cleanup_joins)),
                (cleanup_waits, cleanup_joins),
            )

    def _assert_process_terminated(self, process_id):
        deadline = time.monotonic() + 1.5
        while True:
            try:
                os.kill(process_id, 0)
            except ProcessLookupError:
                return
            status_path = "/proc/%d/stat" % process_id
            try:
                with open(status_path, "rb") as source:
                    status = source.read().split()[2]
            except (FileNotFoundError, IndexError, OSError):
                status = None
            if status == b"Z":
                return
            if time.monotonic() >= deadline:
                self.fail("descendant process is still live after group cleanup")
            time.sleep(0.02)

    def _terminate_fixture(self, group, descendant):
        if group is None or descendant is None:
            return
        try:
            actual_group = os.getpgid(descendant)
        except (ProcessLookupError, PermissionError):
            return
        if actual_group != group:
            return
        with contextlib.suppress(ProcessLookupError):
            if group == os.getpgrp():
                os.kill(descendant, signal.SIGKILL)
            else:
                os.killpg(group, signal.SIGKILL)

    def _cause_chain(self, primary):
        chain = []
        seen = set()
        current = primary
        while current is not None:
            self.assertNotIn(id(current), seen, "exception cause chain contains a cycle")
            seen.add(id(current))
            chain.append(current)
            current = current.__cause__
        return chain

    def _exception_graph_reaches(self, error, forbidden):
        pending = [error]
        seen = set()
        while pending:
            current = pending.pop()
            if current is None or id(current) in seen:
                continue
            if id(current) in forbidden:
                return True
            seen.add(id(current))
            pending.extend((current.__cause__, current.__context__))
        return False


def _selected_suite():
    stage = os.environ.get("PROCESS_RUNTIME_TEST_STAGE", "all")
    listed = tuple(name for values in STAGE_TESTS.values() for name in values)
    discovered = tuple(
        name for name in dir(ProcessRuntimeTest) if name.startswith("test_")
    )
    if set(listed) != set(discovered) or len(listed) != len(set(listed)):
        print(
            "PROCESS_RUNTIME_TEST_INVENTORY_MISMATCH listed=%r discovered=%r"
            % (listed, discovered),
            file=sys.stderr,
        )
        raise SystemExit(2)
    if stage == "all":
        names = listed
    elif stage in STAGE_TESTS:
        names = STAGE_TESTS[stage]
    else:
        print(
            "PROCESS_RUNTIME_TEST_STAGE_INVALID value=%s" % stage,
            file=sys.stderr,
        )
        raise SystemExit(2)
    return stage, unittest.TestSuite(ProcessRuntimeTest(name) for name in names)


if __name__ == "__main__":
    selected_stage, suite = _selected_suite()
    loaded = suite.countTestCases()
    expected = EXPECTED_TEST_COUNT if selected_stage == "all" else len(
        STAGE_TESTS[selected_stage]
    )
    if loaded != expected:
        print(
            "PROCESS_RUNTIME_TEST_COUNT_MISMATCH stage=%s expected=%d loaded=%d"
            % (selected_stage, expected, loaded),
            file=sys.stderr,
        )
        raise SystemExit(2)
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    print(
        "PROCESS_RUNTIME_TESTS_EXECUTED stage=%s count=%d"
        % (selected_stage, result.testsRun)
    )
    if result.wasSuccessful() and result.testsRun == expected:
        print(
            "PROCESS_RUNTIME_TESTS_PASSED stage=%s count=%d"
            % (selected_stage, result.testsRun)
        )
        raise SystemExit(0)
    raise SystemExit(1)
