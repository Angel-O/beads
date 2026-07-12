#!/usr/bin/env python3
import base64
import errno
import fcntl
import hashlib
import json
import os
import select
import signal
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest import mock

import upgrade_baseline_quiescence as subject


SCHEMA = "beads.upgrade.quiescence/v1"
NOW_NS = 2_000_000_000_000_000_000
OVERSIZED_RECEIPT_BYTES = 1024 * 1024 + 1
EXPECTED_TEST_COUNT = 21
AUTHORITY_KIND = "operator-supervisor-v1"


def _receipt_value(path, identity, common_dir, registry_raw, plan_digest, authority):
    common_identity = os.stat(common_dir, follow_symlinks=False)
    registry_digest = hashlib.sha256(registry_raw).hexdigest()
    encode = lambda value: base64.b64encode(value).decode("ascii")
    return {
        "schema": SCHEMA,
        "lease": {
            "id": "0123456789abcdef0123456789abcdef",
            "authority": authority,
            "epoch": 7,
            "issued_at_ns": NOW_NS - 500_000_000,
            "expires_at_ns": NOW_NS + 60_000_000_000,
            "lock": {
                "path_b64": encode(os.path.abspath(path)),
                "dev": identity.st_dev,
                "ino": identity.st_ino,
            },
        },
        "plan": {"sha256": plan_digest},
        "repository": {
            "git_common_dir_b64": encode(common_dir),
            "dev": common_identity.st_dev,
            "ino": common_identity.st_ino,
        },
        "registry": {
            "count": sum(
                field.startswith(b"worktree ") for field in registry_raw.split(b"\0")
            ),
            "sha256": registry_digest,
        },
        "writer_drain": {
            "drained": True,
            "write_capable_handle_count": 0,
        },
        "stability": {
            "digest": registry_digest,
            "samples": [
                {"observed_at_ns": NOW_NS - 2_000_000_000, "digest": registry_digest},
                {"observed_at_ns": NOW_NS - 1_000_000_000, "digest": registry_digest},
            ],
        },
    }


def _canonical_receipt(value):
    return (
        json.dumps(value, ensure_ascii=True, separators=(",", ":"), sort_keys=True)
        + "\n"
    ).encode("ascii")


def _write_all(descriptor, data):
    view = memoryview(data)
    while view:
        view = view[os.write(descriptor, view) :]


def _run_lease_death_child(arguments):
    if len(arguments) != 5:
        raise SystemExit("lease-death child requires five arguments")
    path = os.fsencode(arguments[0])
    plan_digest = arguments[1]
    common_dir = os.fsencode(arguments[2])
    registry_raw = base64.b64decode(arguments[3], validate=True)
    repository = os.fsencode(arguments[4])
    descriptor = os.open(
        path,
        os.O_RDWR | os.O_CREAT | os.O_EXCL | os.O_CLOEXEC,
        0o600,
    )
    try:
        os.fchmod(descriptor, 0o600)
        receipt = _receipt_value(
            path,
            os.fstat(descriptor),
            common_dir,
            registry_raw,
            plan_digest,
            {
                "kind": AUTHORITY_KIND,
                "pid": os.getpid(),
                "uid": os.geteuid(),
            },
        )
        _write_all(descriptor, _canonical_receipt(receipt))
        os.fsync(descriptor)
        os.lseek(descriptor, 0, os.SEEK_SET)
        fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        try:
            lease = subject.hold_quiescence(
                path,
                descriptor,
                plan_digest,
                common_dir,
                registry_raw,
                [repository, common_dir],
                now=NOW_NS,
            )
        finally:
            descriptor = None
        with lease:
            print("LEASE_READY", flush=True)
            while True:
                signal.pause()
    finally:
        if descriptor is not None:
            os.close(descriptor)


class QuiescencePrimitivesTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = os.fsencode(self.temporary.name)
        self.repo = os.path.join(self.root, b"repo")
        os.mkdir(self.repo, 0o700)
        self._git("init", "--quiet")
        self._git("config", "user.name", "Upgrade Test")
        self._git("config", "user.email", "upgrade-test@example.invalid")
        self._git("config", "core.hooksPath", ".git/hooks")
        tracked = os.path.join(self.repo, b"tracked")
        with open(tracked, "wb") as handle:
            handle.write(b"baseline\n")
        self._git("add", "tracked")
        self._git("commit", "--quiet", "-m", "baseline")

        common = self._git_output(
            "rev-parse", "--path-format=absolute", "--git-common-dir"
        ).removesuffix(b"\n")
        self.common_dir = os.path.realpath(common)
        self.registry_raw = self._git_output("worktree", "list", "--porcelain", "-z")
        self.plan_digest = hashlib.sha256(b"approved plan\n").hexdigest()
        self.protected_roots = [self.repo, self.common_dir]
        self.control = os.path.join(self.root, b"control")
        os.mkdir(self.control, 0o700)
        self.receipt_path = os.path.join(self.control, b"quiescence.receipt")

    def test_static_receipt_without_live_exclusive_lease_is_rejected(self):
        descriptor = self._create_receipt(self.receipt_path)
        self.assertTrue(self._contender_can_lock(self.receipt_path))
        self._assert_rejected(self.receipt_path, descriptor)
        self._assert_closed(descriptor)

    def test_held_descriptor_blocks_contenders_until_context_exit(self):
        descriptor = self._create_receipt(self.receipt_path, lock=True)
        with self._hold(self.receipt_path, descriptor) as lease:
            with self.assertRaises(OSError):
                os.fstat(descriptor)
            descriptor = None
            self.assertFalse(self._contender_can_lock(self.receipt_path))
            lease.revalidate()
            self.assertFalse(self._contender_can_lock(self.receipt_path))
        self.assertTrue(self._contender_can_lock(self.receipt_path))

    def test_current_supervisor_authority_is_accepted(self):
        descriptor = self._create_receipt(self.receipt_path, lock=True)
        with self._hold(self.receipt_path, descriptor) as lease:
            self._assert_closed(descriptor)
            lease.revalidate()
        self.assertTrue(self._contender_can_lock(self.receipt_path))

    def test_context_exception_releases_lease_immediately(self):
        descriptor = self._create_receipt(self.receipt_path, lock=True)
        with self.assertRaisesRegex(RuntimeError, "capture failed"):
            with self._hold(self.receipt_path, descriptor):
                self._assert_closed(descriptor)
                self.assertFalse(self._contender_can_lock(self.receipt_path))
                raise RuntimeError("capture failed")
        self.assertTrue(self._contender_can_lock(self.receipt_path))

        interrupted = os.path.join(self.control, b"interrupted-close.receipt")
        descriptor = self._create_receipt(interrupted, lock=True)
        original_dup = os.dup
        original_close = os.close
        retained = []

        def record_dup(value):
            duplicate = original_dup(value)
            retained.append(duplicate)
            return duplicate

        def interrupt_after_close(value):
            original_close(value)
            if value == descriptor:
                raise KeyboardInterrupt("injected descriptor handoff interruption")

        try:
            with mock.patch.object(subject.os, "dup", record_dup), mock.patch.object(
                subject.os, "close", interrupt_after_close
            ):
                with self.assertRaises(KeyboardInterrupt):
                    self._hold(interrupted, descriptor)
            self._assert_closed(retained[0])
            self.assertTrue(self._contender_can_lock(interrupted))
        finally:
            for duplicate in retained:
                self._close(duplicate)

    def test_interruption_before_close_cannot_leak_a_descriptor(self):
        path = os.path.join(self.control, b"interrupt-before-incoming-close.receipt")
        descriptor = self._create_receipt(path, lock=True)
        original_close = os.close
        interrupt = [descriptor]

        def interrupt_once(value):
            if interrupt and value == interrupt[0]:
                interrupt.clear()
                raise KeyboardInterrupt("injected before incoming descriptor close")
            original_close(value)

        with mock.patch.object(subject.os, "close", interrupt_once):
            with self.assertRaises(KeyboardInterrupt):
                self._hold(path, descriptor)
        self._assert_closed(descriptor)
        self.assertTrue(self._contender_can_lock(path))

        path = os.path.join(self.control, b"interrupt-before-retained-close.receipt")
        descriptor = self._create_receipt(path, lock=True)
        lease = self._hold(path, descriptor)
        retained = lease._descriptor
        self.addCleanup(self._close, retained)
        interrupt = [retained]
        with mock.patch.object(subject.os, "close", interrupt_once):
            with self.assertRaises(KeyboardInterrupt):
                lease.close()
        self._assert_closed(retained)
        self.assertTrue(self._contender_can_lock(path))

        path = os.path.join(self.control, b"interrupt-before-fstat.receipt")
        descriptor = self._create_receipt(path, lock=True)
        lease = self._hold(path, descriptor)
        retained = lease._descriptor
        self.addCleanup(self._close, retained)
        original_fstat = os.fstat
        interrupt = [True]

        def interrupt_fstat_once(value):
            if interrupt:
                interrupt.clear()
                raise KeyboardInterrupt("injected before descriptor identity read")
            return original_fstat(value)

        with mock.patch.object(subject.os, "fstat", interrupt_fstat_once):
            with self.assertRaises(KeyboardInterrupt):
                lease.close()
        self._assert_closed(retained)
        self.assertTrue(self._contender_can_lock(path))

    def test_close_is_identity_safe_and_serialized(self):
        path = os.path.join(self.control, b"close-reuse.receipt")
        descriptor = self._create_receipt(path, lock=True)
        lease = self._hold(path, descriptor)
        retained = lease._descriptor
        self.addCleanup(self._close, retained)
        replacement_path = os.path.join(self.control, b"unrelated")
        guards = [os.open(
            replacement_path,
            os.O_RDWR | os.O_CREAT | os.O_EXCL | os.O_CLOEXEC,
            0o600,
        )]
        while guards[-1] < retained:
            guards.append(os.open(replacement_path, os.O_RDWR | os.O_CLOEXEC))
        for guard in guards:
            self.addCleanup(self._close, guard)
        replacement = []
        original_close = os.close

        def close_reuse_then_interrupt(value):
            if value == retained and not replacement:
                original_close(value)
                reused = os.open(path, os.O_RDWR | os.O_CLOEXEC)
                self.assertEqual(reused, value, "test could not force descriptor-number reuse")
                replacement.append(reused)
                raise KeyboardInterrupt("injected after close and descriptor reuse")
            original_close(value)

        try:
            with mock.patch.object(subject.os, "close", close_reuse_then_interrupt):
                with self.assertRaises(KeyboardInterrupt):
                    lease.close()
            os.fstat(replacement[0])
            self.assertTrue(self._contender_can_lock(path))
        finally:
            for value in replacement:
                self._close(value)

        path = os.path.join(self.control, b"concurrent-close.receipt")
        descriptor = self._create_receipt(path, lock=True)
        lease = self._hold(path, descriptor)
        retained = lease._descriptor
        self.addCleanup(self._close, retained)
        close_fd = subject._close_fd
        entered = threading.Event()
        second_attempted = threading.Event()
        second_entered = threading.Event()
        release = threading.Event()
        calls = []
        errors = []
        calls_lock = threading.Lock()

        class ObservedLock:
            def __init__(self, wrapped):
                self.wrapped = wrapped
                self.attempts = 0

            def __enter__(self):
                self.attempts += 1
                if self.attempts == 2:
                    second_attempted.set()
                return self.wrapped.__enter__()

            def __exit__(self, *args):
                return self.wrapped.__exit__(*args)

        lease._close_lock = ObservedLock(lease._close_lock)

        def delayed_close(value, tombstone):
            with calls_lock:
                calls.append(value)
                if len(calls) == 2:
                    second_entered.set()
            entered.set()
            if not release.wait(3):
                raise AssertionError("timed out coordinating concurrent close")
            return close_fd(value, tombstone)

        def close_lease():
            try:
                lease.close()
            except BaseException as error:
                errors.append(error)

        with mock.patch.object(subject, "_close_fd", delayed_close):
            first = threading.Thread(target=close_lease)
            second = threading.Thread(target=close_lease)
            first.start()
            self.assertTrue(entered.wait(3), "first close did not reach the close seam")
            second.start()
            try:
                self.assertTrue(second_attempted.wait(3), "second close did not attempt lock acquisition")
                self.assertFalse(second_entered.is_set(), "concurrent close was not serialized")
            finally:
                release.set()
                first.join(3)
                second.join(3)
        self.assertFalse(first.is_alive() or second.is_alive(), "close worker did not terminate")
        self.assertEqual(errors, [])
        self.assertEqual(calls, [retained])
        self.assertTrue(self._contender_can_lock(path))

        path = os.path.join(self.control, b"fd-exhaustion-close.receipt")
        descriptor = self._create_receipt(path, lock=True)
        lease = self._hold(path, descriptor)
        retained = lease._descriptor
        self.addCleanup(self._close, retained)
        with mock.patch.object(
            subject.os,
            "pipe",
            side_effect=OSError(errno.EMFILE, "injected descriptor exhaustion"),
        ):
            lease.close()
        self._assert_closed(retained)
        self.assertTrue(self._contender_can_lock(path))

    def test_acquisition_reservation_consumes_on_failure(self):
        path = os.path.join(self.control, b"acquisition-emfile.receipt")
        descriptor = self._create_receipt(path, lock=True)
        with mock.patch.object(
            subject.os,
            "pipe",
            side_effect=OSError(errno.EMFILE, "injected acquisition exhaustion"),
        ):
            with self.assertRaises(OSError):
                self._hold(path, descriptor)
        self._assert_closed(descriptor)
        self.assertTrue(self._contender_can_lock(path))

        path = os.path.join(self.control, b"acquisition-peer-interrupt.receipt")
        descriptor = self._create_receipt(path, lock=True)
        original_pipe = os.pipe
        original_close = os.close
        pipe_descriptors = []
        interrupt = [True]

        def record_pipe():
            pair = original_pipe()
            pipe_descriptors.extend(pair)
            for value in pair:
                self.addCleanup(self._close, value)
            return pair

        def interrupt_peer_close(value):
            if pipe_descriptors and value == pipe_descriptors[1] and interrupt:
                interrupt.clear()
                raise KeyboardInterrupt("injected before reserved peer close")
            original_close(value)

        with mock.patch.object(subject.os, "pipe", record_pipe), mock.patch.object(
            subject.os,
            "close",
            interrupt_peer_close,
        ):
            lease = self._hold(path, descriptor)
        lease.close()
        self._assert_closed(descriptor)
        for value in pipe_descriptors:
            self._assert_closed(value)
        self.assertTrue(self._contender_can_lock(path))

    def test_process_death_releases_kernel_lease(self):
        registry_b64 = base64.b64encode(self.registry_raw).decode("ascii")
        process = subprocess.Popen(
            [
                sys.executable,
                "-B",
                os.path.abspath(__file__),
                "--lease-death-child",
                os.fsdecode(self.receipt_path),
                self.plan_digest,
                os.fsdecode(self.common_dir),
                registry_b64,
                os.fsdecode(self.repo),
            ],
            close_fds=True,
            env=os.environ.copy(),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        self.addCleanup(self._stop_process, process)
        ready, _, _ = select.select([process.stdout], [], [], 3)
        self.assertTrue(ready, "lease-holder child did not signal readiness")
        line = process.stdout.readline()
        if line != b"LEASE_READY\n":
            process.wait(timeout=3)
            stderr = process.stderr.read().decode("utf-8", "replace")
            self.fail("lease-holder child failed before readiness: %r\n%s" % (line, stderr))
        self.assertFalse(self._contender_can_lock(self.receipt_path))
        process.kill()
        self.assertEqual(process.wait(timeout=3), -signal.SIGKILL)
        self.assertTrue(self._contender_can_lock(self.receipt_path))

    def test_unlocked_descriptor_cannot_borrow_another_descriptors_lease(self):
        owner = self._create_receipt(self.receipt_path, lock=True)
        unrelated = os.open(self.receipt_path, os.O_RDWR | os.O_CLOEXEC)
        self.addCleanup(self._close, unrelated)
        self.assertFalse(self._contender_can_lock(self.receipt_path))
        self._assert_rejected(self.receipt_path, unrelated)
        self._assert_closed(unrelated)
        self.assertFalse(self._contender_can_lock(self.receipt_path))
        os.close(owner)
        owner = None
        self.assertTrue(self._contender_can_lock(self.receipt_path))

    def test_strict_types_digests_and_expiry_fail_closed(self):
        mutations = {
            "missing top-level field": lambda value: value.pop("schema"),
            "extra top-level field": lambda value: value.update(extra=None),
            "extra nested field": lambda value: value["lease"].update(extra=None),
            "wrong schema": lambda value: value.update(schema="beads.upgrade.quiescence/v2"),
            "invalid lock path base64": lambda value: value["lease"]["lock"].update(path_b64="!"),
            "noncanonical repository base64": lambda value: value["repository"].update(
                git_common_dir_b64=value["repository"]["git_common_dir_b64"] + "="
            ),
            "short lease id": lambda value: value["lease"].update(id="0123"),
            "nonhex lease id": lambda value: value["lease"].update(id="g" * 32),
            "legacy string authority": lambda value: value["lease"].update(authority="upgrade-test"),
            "missing authority field": lambda value: value["lease"]["authority"].pop("uid"),
            "extra authority field": lambda value: value["lease"]["authority"].update(extra=None),
            "wrong authority kind": lambda value: value["lease"]["authority"].update(kind="other"),
            "dead authority pid": lambda value: value["lease"]["authority"].update(
                pid=self._dead_pid()
            ),
            "wrong authority uid": lambda value: value["lease"]["authority"].update(
                uid=os.geteuid() + 1
            ),
            "bool authority pid": lambda value: value["lease"]["authority"].update(pid=True),
            "bool epoch": lambda value: value["lease"].update(epoch=True),
            "bool device": lambda value: value["lease"]["lock"].update(dev=True),
            "wrong lock device": lambda value: value["lease"]["lock"].update(
                dev=value["lease"]["lock"]["dev"] + 1
            ),
            "wrong lock inode": lambda value: value["lease"]["lock"].update(
                ino=value["lease"]["lock"]["ino"] + 1
            ),
            "wrong repository device": lambda value: value["repository"].update(
                dev=value["repository"]["dev"] + 1
            ),
            "wrong repository inode": lambda value: value["repository"].update(
                ino=value["repository"]["ino"] + 1
            ),
            "float registry count": lambda value: value["registry"].update(count=1.0),
            "wrong plan digest": lambda value: value["plan"].update(sha256="0" * 64),
            "wrong registry digest": lambda value: value["registry"].update(sha256="0" * 64),
            "wrong stability digest": lambda value: value["stability"].update(digest="0" * 64),
            "bool writer count": lambda value: value["writer_drain"].update(write_capable_handle_count=False),
            "writers not drained": lambda value: value["writer_drain"].update(drained=False),
            "writer still open": lambda value: value["writer_drain"].update(write_capable_handle_count=1),
            "missing stability samples": lambda value: value["stability"].update(samples=[]),
            "single stability sample": lambda value: value["stability"].update(
                samples=value["stability"]["samples"][:1]
            ),
            "mismatched stability sample": lambda value: value["stability"]["samples"][1].update(
                digest="0" * 64
            ),
            "reversed stability samples": lambda value: value["stability"].update(
                samples=list(reversed(value["stability"]["samples"]))
            ),
            "stability interval too short": lambda value: value["stability"]["samples"][1].update(
                observed_at_ns=value["stability"]["samples"][0]["observed_at_ns"] + 1
            ),
            "future stability sample": lambda value: value["stability"]["samples"][1].update(
                observed_at_ns=NOW_NS + 1
            ),
            "future issue time": lambda value: value["lease"].update(issued_at_ns=NOW_NS + 1),
            "reversed expiry": lambda value: value["lease"].update(
                expires_at_ns=value["lease"]["issued_at_ns"] - 1
            ),
            "expired lease": lambda value: value["lease"].update(expires_at_ns=NOW_NS),
        }
        for index, (name, mutate) in enumerate(mutations.items()):
            with self.subTest(name=name):
                path = os.path.join(self.control, b"invalid-%d.receipt" % index)
                descriptor = self._create_receipt(path, mutate=mutate, lock=True)
                self._assert_rejected(path, descriptor)
                self._assert_closed(descriptor)

    def test_empty_registry_and_empty_protected_roots_fail_closed(self):
        empty_registry = b"\0"
        empty_path = os.path.join(self.control, b"empty-registry.receipt")
        descriptor = self._create_receipt(
            empty_path, registry_raw=empty_registry, lock=True
        )
        self._assert_rejected(
            empty_path, descriptor, registry_raw=empty_registry
        )
        self._assert_closed(descriptor)

        unprotected_path = os.path.join(self.control, b"unprotected.receipt")
        descriptor = self._create_receipt(unprotected_path, lock=True)
        self._assert_rejected(
            unprotected_path, descriptor, protected_roots=[]
        )
        self._assert_closed(descriptor)

    def test_raw_json_is_strict_and_unambiguous(self):
        transforms = {
            "duplicate top-level key": lambda data: data.replace(
                b'"schema":', b'"schema":"shadow","schema":', 1
            ),
            "duplicate nested key": lambda data: data.replace(
                b'"epoch":7', b'"epoch":8,"epoch":7', 1
            ),
            "non-finite number": lambda data: data.replace(b'"epoch":7', b'"epoch":NaN', 1),
            "trailing JSON": lambda data: data + b"{}",
        }
        for index, (name, transform) in enumerate(transforms.items()):
            with self.subTest(name=name):
                path = os.path.join(self.control, b"raw-%d.receipt" % index)
                descriptor = self._create_receipt(path, transform=transform, lock=True)
                self._assert_rejected(path, descriptor)
                self._assert_closed(descriptor)

    def test_symlink_hardlink_exposed_and_inside_root_receipts_are_rejected(self):
        cases = []

        exposed = os.path.join(self.control, b"exposed.receipt")
        cases.append(("exposed", exposed, self._create_receipt(exposed, mode=0o640, lock=True)))

        hardlinked = os.path.join(self.control, b"hardlinked.receipt")
        hardlinked_descriptor = self._create_receipt(hardlinked, lock=True)
        os.link(hardlinked, os.path.join(self.control, b"hardlinked-alias"))
        cases.append(("hardlink", hardlinked, hardlinked_descriptor))

        target = os.path.join(self.control, b"symlink-target.receipt")
        alias = os.path.join(self.control, b"symlink.receipt")
        symlink_descriptor = self._create_receipt(target, advertised_path=alias, lock=True)
        os.symlink(target, alias)
        cases.append(("symlink", alias, symlink_descriptor))

        inside = os.path.join(self.repo, b"inside.receipt")
        cases.append(("inside protected root", inside, self._create_receipt(inside, lock=True)))

        for name, path, descriptor in cases:
            with self.subTest(name=name):
                self._assert_rejected(path, descriptor)
                self._assert_closed(descriptor)

    def test_oversized_sparse_receipt_is_rejected(self):
        descriptor = os.open(
            self.receipt_path,
            os.O_RDWR | os.O_CREAT | os.O_EXCL | os.O_CLOEXEC,
            0o600,
        )
        self.addCleanup(self._close, descriptor)
        os.fchmod(descriptor, 0o600)
        os.ftruncate(descriptor, OVERSIZED_RECEIPT_BYTES)
        fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        self._assert_rejected(self.receipt_path, descriptor)
        self._assert_closed(descriptor)

    def test_final_revalidation_detects_path_replacement(self):
        descriptor = self._create_receipt(self.receipt_path, lock=True)
        displaced = self.receipt_path + b".displaced"
        with self.assertRaises(subject.QuiescenceError):
            with self._hold(self.receipt_path, descriptor) as lease:
                with self.assertRaises(OSError):
                    os.fstat(descriptor)
                descriptor = None
                os.rename(self.receipt_path, displaced)
                replacement = os.open(
                    self.receipt_path,
                    os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_CLOEXEC,
                    0o600,
                )
                os.close(replacement)
                self.assertTrue(self._contender_can_lock(self.receipt_path))
                lease.revalidate()

    def test_final_revalidation_detects_mode_and_link_count_changes(self):
        cases = {
            "mode": lambda path: os.chmod(path, 0o640),
            "link count": lambda path: os.link(path, path + b".alias"),
        }
        for index, (name, mutate) in enumerate(cases.items()):
            with self.subTest(name=name):
                path = os.path.join(self.control, b"revalidate-%d.receipt" % index)
                descriptor = self._create_receipt(path, lock=True)
                with self.assertRaises(subject.QuiescenceError):
                    with self._hold(path, descriptor) as lease:
                        self._assert_closed(descriptor)
                        mutate(path)
                        lease.revalidate()

    def test_short_pread_preserves_the_shared_open_description_offset(self):
        descriptor = self._create_receipt(self.receipt_path, lock=True)
        observer = os.dup(descriptor)
        self.addCleanup(self._close, observer)
        os.lseek(observer, 17, os.SEEK_SET)
        real_pread = os.pread
        calls = []

        def short_pread(fd, length, offset):
            calls.append((length, offset))
            return real_pread(fd, min(length, 7), offset)

        with mock.patch.object(subject.os, "pread", side_effect=short_pread):
            with self._hold(self.receipt_path, descriptor) as lease:
                self._assert_closed(descriptor)
                self.assertEqual(os.lseek(observer, 0, os.SEEK_CUR), 17)
                lease.revalidate()
        self.assertGreater(len(calls), 1)

    def test_separate_git_admin_is_protected_but_prefix_sibling_is_allowed(self):
        separate = os.path.join(self.root, b"separate")
        worktree = os.path.join(separate, b"worktree")
        admin = os.path.join(separate, b"admin")
        os.mkdir(separate, 0o700)
        self._run_git(self.root, "init", "--quiet", "--separate-git-dir", admin, worktree)
        common = self._git_output_at(
            worktree, "rev-parse", "--path-format=absolute", "--git-common-dir"
        ).removesuffix(b"\n")
        common = os.path.realpath(common)
        registry = self._git_output_at(worktree, "worktree", "list", "--porcelain", "-z")
        protected = [worktree, common]

        unsafe = os.path.join(common, b"inside-admin.receipt")
        descriptor = self._create_receipt(
            unsafe, common_dir=common, registry_raw=registry, lock=True
        )
        self._assert_rejected(
            unsafe,
            descriptor,
            common_dir=common,
            registry_raw=registry,
            protected_roots=protected,
        )
        self._assert_closed(descriptor)

        sibling = worktree + b"-sibling"
        os.mkdir(sibling, 0o700)
        safe = os.path.join(sibling, b"outside.receipt")
        descriptor = self._create_receipt(
            safe, common_dir=common, registry_raw=registry, lock=True
        )
        with self._hold(
            safe,
            descriptor,
            common_dir=common,
            registry_raw=registry,
            protected_roots=protected,
        ):
            self._assert_closed(descriptor)

    def test_revalidation_uses_a_monotonic_expiry_deadline(self):
        descriptor = self._create_receipt(self.receipt_path, lock=True)
        tick = [9_000_000_000]
        lifetime = 60_000_000_000

        with self.assertRaises(subject.QuiescenceError):
            with self._hold(
                self.receipt_path,
                descriptor,
                monotonic_ns=lambda: tick[0],
            ) as lease:
                self._assert_closed(descriptor)
                tick[0] += lifetime - 1
                lease.revalidate()
                tick[0] += 1
                lease.revalidate()

        wall_reads = [0]
        tick = [9_000_000_000]
        path = os.path.join(self.control, b"long-capture.receipt")
        descriptor = self._create_receipt(path, lock=True)
        def wall_clock():
            wall_reads[0] += 1
            return NOW_NS

        with mock.patch.object(subject.time, "time_ns", wall_clock):
            with self._hold(
                path,
                descriptor,
                now=None,
                monotonic_ns=lambda: tick[0],
            ) as lease:
                tick[0] += 6_000_000_000
                with mock.patch.object(
                    subject.os,
                    "kill",
                    side_effect=AssertionError("revalidation queried transferred authority"),
                ):
                    lease.revalidate()
        self.assertEqual(wall_reads, [1])

    def test_acquisition_and_context_entry_cannot_extend_expiry(self):
        lifetime = 60_000_000_000
        tick = [9_000_000_000]
        path = os.path.join(self.control, b"expired-during-acquisition.receipt")
        descriptor = self._create_receipt(path, lock=True)
        validate_receipt = subject._validate_receipt

        def delayed_validation(*args, **kwargs):
            validated = validate_receipt(*args, **kwargs)
            tick[0] += lifetime
            return validated

        with mock.patch.object(subject, "_validate_receipt", delayed_validation):
            with self.assertRaises(subject.QuiescenceError):
                with self._hold(path, descriptor, monotonic_ns=lambda: tick[0]):
                    self.fail("capture began after acquisition consumed the lease lifetime")
        self.assertTrue(self._contender_can_lock(path))

        tick = [9_000_000_000]
        path = os.path.join(self.control, b"expired-before-entry.receipt")
        descriptor = self._create_receipt(path, lock=True)
        lease = self._hold(path, descriptor, monotonic_ns=lambda: tick[0])
        tick[0] += lifetime
        with self.assertRaises(subject.QuiescenceError):
            with lease:
                self.fail("capture began after the lease expired")
        self.assertTrue(self._contender_can_lock(path))

    def test_revalidation_cannot_finish_after_expiry(self):
        lifetime = 60_000_000_000
        tick = [9_000_000_000]
        path = os.path.join(self.control, b"expired-during-revalidation.receipt")
        descriptor = self._create_receipt(path, lock=True)
        lease = self._hold(path, descriptor, monotonic_ns=lambda: tick[0])
        read_receipt = subject._read_receipt

        def delayed_read(*args, **kwargs):
            data = read_receipt(*args, **kwargs)
            tick[0] += lifetime
            return data

        try:
            with mock.patch.object(subject, "_read_receipt", delayed_read):
                with self.assertRaises(subject.QuiescenceError):
                    lease.revalidate()
        finally:
            lease.close()
        self.assertTrue(self._contender_can_lock(path))

    def _hold(
        self,
        path,
        descriptor,
        *,
        common_dir=None,
        registry_raw=None,
        protected_roots=None,
        monotonic_ns=None,
        now=NOW_NS,
    ):
        timing = {"now": now}
        if monotonic_ns is not None:
            timing["monotonic_ns"] = monotonic_ns
        return subject.hold_quiescence(
            path,
            descriptor,
            self.plan_digest,
            common_dir if common_dir is not None else self.common_dir,
            registry_raw if registry_raw is not None else self.registry_raw,
            protected_roots if protected_roots is not None else self.protected_roots,
            **timing,
        )

    def _assert_rejected(self, path, descriptor, **hold_overrides):
        with self.assertRaises(subject.QuiescenceError):
            with self._hold(path, descriptor, **hold_overrides):
                self.fail("invalid receipt became a live lease")

    def _create_receipt(
        self,
        path,
        *,
        advertised_path=None,
        mode=0o600,
        mutate=None,
        transform=None,
        common_dir=None,
        registry_raw=None,
        lock=False,
    ):
        descriptor = os.open(
            path,
            os.O_RDWR | os.O_CREAT | os.O_EXCL | os.O_CLOEXEC,
            0o600,
        )
        self.addCleanup(self._close, descriptor)
        os.fchmod(descriptor, mode)
        identity = os.fstat(descriptor)
        common_dir = common_dir if common_dir is not None else self.common_dir
        registry_raw = registry_raw if registry_raw is not None else self.registry_raw
        advertised = os.path.abspath(advertised_path or path)
        receipt = _receipt_value(
            advertised,
            identity,
            common_dir,
            registry_raw,
            self.plan_digest,
            {
                "kind": AUTHORITY_KIND,
                "pid": os.getpid(),
                "uid": os.geteuid(),
            },
        )
        if mutate is not None:
            mutate(receipt)
        data = _canonical_receipt(receipt)
        if transform is not None:
            data = transform(data)
        _write_all(descriptor, data)
        os.fsync(descriptor)
        os.lseek(descriptor, 0, os.SEEK_SET)
        if lock:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        return descriptor

    def _contender_can_lock(self, path):
        code = """
import fcntl, os, sys
descriptor = os.open(sys.argv[1], os.O_RDONLY | os.O_CLOEXEC)
try:
    fcntl.flock(descriptor, fcntl.LOCK_SH | fcntl.LOCK_NB)
except BlockingIOError:
    raise SystemExit(23)
finally:
    os.close(descriptor)
"""
        result = subprocess.run(
            [sys.executable, "-B", "-c", code, path],
            check=False,
            close_fds=True,
            env=os.environ,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=3,
        )
        if result.returncode not in (0, 23):
            self.fail("lock contender failed unexpectedly: %s" % result.stderr.decode("utf-8", "replace"))
        return result.returncode == 0

    def _git(self, *args):
        self._run_git(self.repo, *args)

    def _git_output(self, *args):
        return self._git_output_at(self.repo, *args)

    def _run_git(self, cwd, *args):
        subprocess.run(
            ["git", *args],
            cwd=cwd,
            env=self._git_environment(),
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

    def _git_output_at(self, cwd, *args):
        return subprocess.run(
            ["git", *args],
            cwd=cwd,
            env=self._git_environment(),
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        ).stdout

    @staticmethod
    def _git_environment():
        environment = os.environ.copy()
        for name in tuple(environment):
            if name.startswith("GIT_") and name not in (
                "GIT_CONFIG_NOSYSTEM",
                "GIT_CONFIG_GLOBAL",
            ):
                environment.pop(name, None)
        environment["GIT_CONFIG_NOSYSTEM"] = "1"
        environment["GIT_CONFIG_GLOBAL"] = "/dev/null"
        return environment

    @staticmethod
    def _b64(value):
        return base64.b64encode(value).decode("ascii")

    @staticmethod
    def _registry_count(registry_raw):
        return sum(field.startswith(b"worktree ") for field in registry_raw.split(b"\0"))

    def _assert_closed(self, descriptor):
        with self.assertRaises(OSError):
            os.fstat(descriptor)

    @staticmethod
    def _dead_pid():
        for candidate in (2_147_483_647, 1_073_741_823, 536_870_911):
            try:
                os.kill(candidate, 0)
            except ProcessLookupError:
                return candidate
            except PermissionError:
                continue
        raise AssertionError("could not identify a demonstrably dead PID")

    @staticmethod
    def _stop_process(process):
        if process.poll() is None:
            process.kill()
            process.wait(timeout=3)

    @staticmethod
    def _close(descriptor):
        try:
            os.close(descriptor)
        except OSError:
            pass


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--lease-death-child":
        _run_lease_death_child(sys.argv[2:])
        raise SystemExit(0)
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(QuiescencePrimitivesTest)
    loaded = suite.countTestCases()
    if loaded != EXPECTED_TEST_COUNT:
        print(
            "QUIESCENCE_TEST_COUNT_MISMATCH expected=%d loaded=%d"
            % (EXPECTED_TEST_COUNT, loaded),
            file=sys.stderr,
        )
        raise SystemExit(2)
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    print("QUIESCENCE_TESTS_EXECUTED count=%d" % result.testsRun)
    if result.wasSuccessful() and result.testsRun == EXPECTED_TEST_COUNT:
        print("QUIESCENCE_TESTS_PASSED count=%d" % result.testsRun)
        raise SystemExit(0)
    raise SystemExit(1)
