#!/usr/bin/env python3
import base64
import errno
import fcntl
import hashlib
import json
import os
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

import upgrade_baseline_bundle as bundle_subject
import upgrade_baseline_quiescence as quiescence_subject


SCHEMA = "beads.upgrade.quiescence/v1"
AUTHORITY_KIND = "operator-supervisor-v1"
NOW_NS = 2_000_000_000_000_000_000
EXPECTED_TEST_COUNT = 2


class _BundleOSFaultProxy:
    def __init__(self, delegate, mkdir, fsync):
        self._delegate = delegate
        self.mkdir = mkdir
        self.fsync = fsync

    def __getattr__(self, name):
        return getattr(self._delegate, name)


def _receipt_value(path, identity, common_dir, registry_raw, plan_digest):
    common_identity = os.stat(common_dir, follow_symlinks=False)
    registry_digest = hashlib.sha256(registry_raw).hexdigest()

    def encode(value):
        return base64.b64encode(value).decode("ascii")

    return {
        "schema": SCHEMA,
        "lease": {
            "id": "0123456789abcdef0123456789abcdef",
            "authority": {
                "kind": AUTHORITY_KIND,
                "pid": os.getpid(),
                "uid": os.geteuid(),
            },
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
                field.startswith(b"worktree ")
                for field in registry_raw.split(b"\0")
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
                {
                    "observed_at_ns": NOW_NS - 2_000_000_000,
                    "digest": registry_digest,
                },
                {
                    "observed_at_ns": NOW_NS - 1_000_000_000,
                    "digest": registry_digest,
                },
            ],
        },
    }


class BundleQuiescenceCompositionTest(unittest.TestCase):
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
        self.registry_raw = self._git_output(
            "worktree", "list", "--porcelain", "-z"
        )
        self.plan_digest = hashlib.sha256(b"approved plan\n").hexdigest()
        self.protected_roots = [self.repo, self.common_dir]
        self.control = os.path.join(self.root, b"control")
        self.bundle_parent = os.path.join(self.root, b"bundles")
        os.mkdir(self.control, 0o700)
        os.mkdir(self.bundle_parent, 0o700)
        self.receipt_path = os.path.join(self.control, b"quiescence.receipt")

    def test_real_bundle_lifecycle_remains_durable_under_live_lease(self):
        incoming = self._create_receipt(self.receipt_path)
        lease = self._hold(self.receipt_path, incoming)
        retained = lease._descriptor
        reserve = tuple(lease._reserve)
        for descriptor in (retained, *reserve):
            self.addCleanup(self._close, descriptor)

        bundle_path = os.path.join(self.bundle_parent, b"preservation-bundle")
        payload = b"content-addressed upgrade payload\n"
        with lease:
            self._assert_closed(incoming)
            self.assertFalse(self._contender_can_lock(self.receipt_path))
            bundle = bundle_subject.Bundle.create_started(bundle_path)
            receipt_artifact = bundle.artifact(
                b"receipts/quiescence.receipt", lease.receipt_bytes
            )
            payload_artifact = bundle.capture_bytes(payload)
            bundle.finish()
            lease.revalidate()

            self.assertFalse(self._contender_can_lock(self.receipt_path))
            self.assertEqual(
                bundle.read_artifact(receipt_artifact, len(lease.receipt_bytes)),
                lease.receipt_bytes,
            )
            self.assertEqual(
                bundle.read_artifact(payload_artifact, len(payload)), payload
            )
            self.assertEqual(
                payload_artifact["sha256"], bundle_subject.sha256(payload)
            )
            self.assertEqual(
                payload_artifact["path"],
                "objects/sha256/%s/%s"
                % (
                    payload_artifact["sha256"][:2],
                    payload_artifact["sha256"],
                ),
            )
            with open(os.path.join(bundle_path, b"COMPLETE"), "rb") as marker:
                self.assertEqual(marker.read(), bundle_subject.COMPLETE_MARKER)
            self.assertFalse(os.path.lexists(os.path.join(bundle_path, b"INCOMPLETE")))
            self.assertFalse(
                os.path.lexists(os.path.join(bundle_path, bundle_subject.INCOMPLETE_TEMP))
            )
            self.assertFalse(
                os.path.lexists(os.path.join(bundle_path, bundle_subject.COMPLETE_TEMP))
            )
            self._assert_no_bundle_temporaries(self.bundle_parent, bundle_path)

        self.assertTrue(self._contender_can_lock(self.receipt_path))
        self.assertIsNone(lease._descriptor)
        self.assertIsNone(lease._reserve)
        for descriptor in (retained, *reserve):
            self._assert_closed(descriptor)

    def test_real_staging_and_lease_close_failures_preserve_exact_chain(self):
        incoming = self._create_receipt(self.receipt_path)
        lease = self._hold(self.receipt_path, incoming)
        retained = lease._descriptor
        reserve = tuple(lease._reserve)
        for descriptor in (retained, *reserve):
            self.addCleanup(self._close, descriptor)

        bundle_path = os.path.join(self.bundle_parent, b"failed-bundle")
        original_mkdir = bundle_subject.os.mkdir
        original_fsync = bundle_subject.os.fsync
        original_close_fd = quiescence_subject._close_fd
        underlying = OSError(errno.EBUSY, "injected staging mkdir underlying failure")
        staging_error = OSError(
            errno.ENOSPC, "injected staging mkdir operation failure"
        )
        staging_error.__cause__ = underlying
        staging_error.__suppress_context__ = True
        lease_close_error = OSError(
            errno.EIO, "injected lease close diagnostic after real close"
        )
        staging_names = []
        staging_parent_descriptors = []
        sync_errors = []
        close_calls = []
        bundle_errors = []

        def mkdir_then_fail(value, *args, **kwargs):
            result = original_mkdir(value, *args, **kwargs)
            encoded = os.fsencode(value)
            if encoded.startswith(bundle_subject.STAGING_PREFIX):
                staging_names.append(encoded)
                staging_parent_descriptors.append(kwargs["dir_fd"])
                raise staging_error
            return result

        def fail_removed_staging_parent_sync(descriptor):
            if (
                staging_parent_descriptors
                and descriptor == staging_parent_descriptors[0]
            ):
                error = OSError(
                    errno.EIO,
                    "injected removed-staging parent fsync failure attempt %d"
                    % (len(sync_errors) + 1),
                )
                sync_errors.append(error)
                raise error
            return original_fsync(descriptor)

        def close_lease_then_report(descriptor, tombstone, *args, **kwargs):
            self.assertEqual(descriptor, retained)
            result = original_close_fd(descriptor, tombstone, *args, **kwargs)
            self.assertIsNone(result)
            self._assert_closed(descriptor)
            close_calls.append(descriptor)
            return lease_close_error

        bundle_os = _BundleOSFaultProxy(
            bundle_subject.os,
            mkdir_then_fail,
            fail_removed_staging_parent_sync,
        )
        top_error = None
        with mock.patch.object(
            bundle_subject, "os", bundle_os
        ), mock.patch.object(
            quiescence_subject, "_close_fd", close_lease_then_report
        ):
            try:
                with lease:
                    try:
                        bundle_subject.Bundle.create_started(bundle_path)
                    except BaseException as error:
                        bundle_errors.append(error)
                        raise
            except BaseException as error:
                top_error = error

        self.assertEqual(len(bundle_errors), 1)
        bundle_error = bundle_errors[0]
        self.assertIsInstance(bundle_error, bundle_subject.PreservationError)
        self.assertEqual(len(staging_names), 1)
        self.assertEqual(len(staging_parent_descriptors), 1)
        self.assertEqual(len(sync_errors), bundle_subject.CLEANUP_RETRIES)
        self.assertEqual(close_calls, [retained])
        self.assertFalse(os.path.lexists(bundle_path))
        self._assert_no_bundle_temporaries(self.bundle_parent, bundle_path)
        self._assert_closed(incoming)
        self._assert_closed(staging_parent_descriptors[0])
        for descriptor in (retained, *reserve):
            self._assert_closed(descriptor)
        self.assertIsNone(lease._descriptor)
        self.assertIsNone(lease._reserve)
        self.assertTrue(self._contender_can_lock(self.receipt_path))

        self.assertIs(
            top_error,
            bundle_error,
            "lease close replaced the active preservation failure",
        )
        chain = self._acyclic_cause_chain(top_error)
        self.assertEqual(len(chain), 7, [str(error) for error in chain])
        self.assertIs(chain[0], bundle_error)
        self.assertIsInstance(chain[1], bundle_subject.PreservationError)
        self.assertEqual(
            str(chain[1]),
            "cannot make preservation bundle staging cleanup durable: "
            "cannot complete removed staging parent sync: %s" % sync_errors[-1],
        )
        self.assertIsInstance(chain[2], bundle_subject.PreservationError)
        self.assertEqual(
            str(chain[2]),
            "cannot complete removed staging parent sync: %s" % sync_errors[-1],
        )
        self.assertIs(chain[3], sync_errors[-1])
        self.assertIs(chain[4], lease_close_error)
        self.assertIs(chain[5], staging_error)
        self.assertIs(chain[6], underlying)
        self.assertIs(bundle_error._preservation_original_cause, staging_error)
        self.assertIs(bundle_error._preservation_failure_tail, lease_close_error)
        self.assertTrue(lease_close_error.__suppress_context__)

    def _hold(self, path, descriptor):
        return quiescence_subject.hold_quiescence(
            path,
            descriptor,
            self.plan_digest,
            self.common_dir,
            self.registry_raw,
            self.protected_roots,
            now=NOW_NS,
        )

    def _create_receipt(self, path):
        descriptor = os.open(
            path,
            os.O_RDWR | os.O_CREAT | os.O_EXCL | os.O_CLOEXEC,
            0o600,
        )
        self.addCleanup(self._close, descriptor)
        os.fchmod(descriptor, 0o600)
        receipt = _receipt_value(
            path,
            os.fstat(descriptor),
            self.common_dir,
            self.registry_raw,
            self.plan_digest,
        )
        data = (
            json.dumps(
                receipt,
                ensure_ascii=True,
                separators=(",", ":"),
                sort_keys=True,
            )
            + "\n"
        ).encode("ascii")
        view = memoryview(data)
        while view:
            view = view[os.write(descriptor, view) :]
        os.fsync(descriptor)
        os.lseek(descriptor, 0, os.SEEK_SET)
        fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        return descriptor

    def _assert_no_bundle_temporaries(self, parent, bundle_path):
        for name in os.listdir(parent):
            lowered = os.fsencode(name).lower()
            self.assertFalse(lowered.startswith(bundle_subject.STAGING_PREFIX))
            self.assertFalse(lowered.startswith(bundle_subject.REAPING_PREFIX))
        if os.path.lexists(bundle_path):
            for _, directories, files in os.walk(bundle_path):
                for name in (*directories, *files):
                    lowered = os.fsencode(name).lower()
                    self.assertFalse(lowered.startswith(b".object-"))
                    self.assertFalse(
                        lowered.startswith(bundle_subject.STAGING_PREFIX)
                    )
                    self.assertFalse(
                        lowered.startswith(bundle_subject.REAPING_PREFIX)
                    )

    def _acyclic_cause_chain(self, primary):
        chain = []
        identities = set()
        current = primary
        while current is not None:
            self.assertNotIn(
                id(current), identities, "production cause chain contains a cycle"
            )
            identities.add(id(current))
            chain.append(current)
            current = current.__cause__
        return chain

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
            self.fail(
                "lock contender failed unexpectedly: %s"
                % result.stderr.decode("utf-8", "replace")
            )
        return result.returncode == 0

    def _git(self, *args):
        subprocess.run(
            ["git", *args],
            cwd=self.repo,
            env=self._git_environment(),
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

    def _git_output(self, *args):
        return subprocess.run(
            ["git", *args],
            cwd=self.repo,
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

    def _assert_closed(self, descriptor):
        with self.assertRaises(OSError) as raised:
            os.fstat(descriptor)
        self.assertEqual(raised.exception.errno, errno.EBADF)

    @staticmethod
    def _close(descriptor):
        try:
            os.close(descriptor)
        except OSError:
            pass


if __name__ == "__main__":
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(
        BundleQuiescenceCompositionTest
    )
    loaded = suite.countTestCases()
    if loaded != EXPECTED_TEST_COUNT:
        print(
            "BUNDLE_QUIESCENCE_COMPOSITION_TEST_COUNT_MISMATCH "
            "expected=%d loaded=%d" % (EXPECTED_TEST_COUNT, loaded),
            file=sys.stderr,
        )
        raise SystemExit(2)
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    print("BUNDLE_QUIESCENCE_COMPOSITION_TESTS_EXECUTED count=%d" % result.testsRun)
    if result.wasSuccessful() and result.testsRun == EXPECTED_TEST_COUNT:
        print("BUNDLE_QUIESCENCE_COMPOSITION_TESTS_PASSED count=%d" % result.testsRun)
        raise SystemExit(0)
    raise SystemExit(1)
