#!/usr/bin/env python3
import contextlib
import os
import select
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import threading
import unittest

import upgrade_baseline_bundle as subject

EXPECTED_TEST_COUNT = 104


class BundlePrimitivesTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.bundle_path = os.path.join(self.temporary.name, "bundle")
        os.mkdir(self.bundle_path, 0o700)
        self.bundle = subject.Bundle(self.bundle_path)
        self.bundle.start()

    def _durable_staging(self, parent, name, marker=subject.INCOMPLETE_MARKER):
        path = os.path.join(parent, name)
        os.mkdir(path, 0o700)
        root = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
        try:
            if marker is not None:
                marker_path = os.path.join(path, "INCOMPLETE")
                with open(marker_path, "xb") as handle:
                    handle.write(marker)
                    handle.flush()
                    os.fsync(handle.fileno())
                os.chmod(marker_path, 0o600)
            os.fsync(root)
        finally:
            os.close(root)
        parent_descriptor = os.open(parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(parent_descriptor)
        finally:
            os.close(parent_descriptor)
        return path

    def test_abandoned_staging_is_reaped_before_new_bundle_allocation(self):
        parent = os.path.join(self.temporary.name, "reap-abandoned")
        output = os.path.join(parent, "bundle")
        os.mkdir(parent, 0o700)
        staging = self._durable_staging(
            parent, ".bundle-staging-" + "1" * 32
        )

        bundle = subject.Bundle.create_started(output)

        self.assertFalse(os.path.lexists(staging))
        self.assertTrue(os.path.isfile(os.path.join(output, "INCOMPLETE")))
        bundle.revalidate_anchor()

    def test_busy_staging_is_never_reaped_until_creator_releases_lock(self):
        parent = os.path.join(self.temporary.name, "reap-busy")
        first_output = os.path.join(parent, "first")
        second_output = os.path.join(parent, "second")
        os.mkdir(parent, 0o700)
        staging = self._durable_staging(
            parent, ".bundle-staging-" + "2" * 32
        )
        holder = subprocess.Popen(
            [
                sys.executable,
                "-B",
                "-c",
                (
                    "import fcntl,os,sys; "
                    "fd=os.open(sys.argv[1],os.O_RDONLY|os.O_DIRECTORY); "
                    "fcntl.flock(fd,fcntl.LOCK_EX); print('locked',flush=True); "
                    "sys.stdin.buffer.read(1)"
                ),
                staging,
            ],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            env=os.environ,
        )
        try:
            ready, _, _ = select.select([holder.stdout], [], [], 2)
            self.assertTrue(ready, "staging lock holder did not become ready")
            self.assertEqual(holder.stdout.readline(), b"locked\n")
            subject.Bundle.create_started(first_output)
            self.assertTrue(os.path.isdir(staging))
            holder.stdin.write(b"x")
            holder.stdin.close()
            holder.wait(timeout=2)
            subject.Bundle.create_started(second_output)
            self.assertFalse(os.path.lexists(staging))
        finally:
            if holder.poll() is None:
                holder.terminate()
                holder.wait(timeout=2)
            if holder.stdin is not None and not holder.stdin.closed:
                holder.stdin.close()
            if holder.stdout is not None:
                holder.stdout.close()

    def test_staging_reaper_rejects_every_noncanonical_candidate_unchanged(self):
        cases = (
            "malformed-name", "symlink", "mode", "bad-marker",
            "prefix-mode", "final-prefix", "final-zero",
            "zero-mode-content", "hardlink", "extra",
        )
        for index, case in enumerate(cases):
            with self.subTest(case=case):
                parent = os.path.join(self.temporary.name, "invalid-staging-%d" % index)
                output = os.path.join(parent, "bundle")
                os.mkdir(parent, 0o700)
                name = ".bundle-staging-" + ("%x" % index) * 32
                if case == "malformed-name":
                    name = ".bundle-staging-short"
                if case == "symlink":
                    target = self._durable_staging(parent, "candidate-target")
                    candidate = os.path.join(parent, name)
                    os.symlink(target, candidate)
                else:
                    candidate = self._durable_staging(
                        parent,
                        name,
                        (
                            b"not canonical\n"
                            if case == "bad-marker"
                            else subject.INCOMPLETE_MARKER[:7]
                            if case in ("prefix-mode", "final-prefix")
                            else b""
                            if case == "final-zero"
                            else subject.INCOMPLETE_MARKER
                        ),
                    )
                if case == "mode":
                    os.chmod(candidate, 0o750)
                elif case == "prefix-mode":
                    os.chmod(os.path.join(candidate, "INCOMPLETE"), 0o400)
                elif case == "zero-mode-content":
                    os.chmod(os.path.join(candidate, "INCOMPLETE"), 0)
                elif case == "hardlink":
                    os.link(
                        os.path.join(candidate, "INCOMPLETE"),
                        os.path.join(parent, "linked-marker"),
                    )
                elif case == "extra":
                    extra = os.path.join(candidate, "extra")
                    with open(extra, "xb") as handle:
                        handle.write(b"unexpected")
                    os.chmod(extra, 0o600)
                before = os.lstat(candidate)

                with self.assertRaises(subject.PreservationError):
                    subject.Bundle.create_started(output)

                after = os.lstat(candidate)
                self.assertEqual(
                    (after.st_dev, after.st_ino, after.st_mode, after.st_nlink),
                    (before.st_dev, before.st_ino, before.st_mode, before.st_nlink),
                )
                self.assertFalse(os.path.lexists(output))

    def test_staging_reaper_rejects_malformed_marker_temporary_unchanged(self):
        temporary_name = ".object-INCOMPLETE"
        cases = (
            "content", "mode", "hardlink", "directory",
            "cross-inode", "third-link",
        )
        for index, case in enumerate(cases):
            with self.subTest(case=case):
                parent = os.path.join(self.temporary.name, "invalid-marker-temp-%d" % index)
                output = os.path.join(parent, "bundle")
                os.mkdir(parent, 0o700)
                candidate = os.path.join(parent, ".bundle-staging-" + ("%x" % index) * 32)
                os.mkdir(candidate, 0o700)
                temporary = os.path.join(candidate, temporary_name)
                if case == "directory":
                    os.mkdir(temporary, 0o700)
                else:
                    with open(temporary, "xb") as handle:
                        if case == "content":
                            handle.write(b"not an unpublished zero marker")
                        elif case in ("cross-inode", "third-link"):
                            handle.write(subject.INCOMPLETE_MARKER)
                    os.chmod(temporary, 0o400 if case == "mode" else 0o600)
                if case == "hardlink":
                    os.link(temporary, os.path.join(parent, "linked-marker-temp"))
                final = os.path.join(candidate, "INCOMPLETE")
                if case == "cross-inode":
                    with open(final, "xb") as handle:
                        handle.write(subject.INCOMPLETE_MARKER)
                    os.chmod(final, 0o600)
                elif case == "third-link":
                    os.link(temporary, final)
                    os.link(temporary, os.path.join(parent, "third-marker-link"))
                before = os.lstat(temporary)
                final_before = (
                    os.lstat(final)
                    if case in ("cross-inode", "third-link")
                    else None
                )

                with self.assertRaises(subject.PreservationError):
                    subject.Bundle.create_started(output)

                after = os.lstat(temporary)
                self.assertEqual(
                    (after.st_dev, after.st_ino, after.st_mode, after.st_nlink, after.st_size),
                    (before.st_dev, before.st_ino, before.st_mode, before.st_nlink, before.st_size),
                )
                if final_before is not None:
                    final_after = os.lstat(final)
                    self.assertEqual(
                        (
                            final_after.st_dev, final_after.st_ino,
                            final_after.st_mode, final_after.st_nlink,
                            final_after.st_size,
                        ),
                        (
                            final_before.st_dev, final_before.st_ino,
                            final_before.st_mode, final_before.st_nlink,
                            final_before.st_size,
                        ),
                    )
                self.assertFalse(os.path.lexists(output))

    def test_staging_reaper_recovers_nonzero_canonical_marker_temporary(self):
        for index, data in enumerate(
            (subject.INCOMPLETE_MARKER[:7], subject.INCOMPLETE_MARKER)
        ):
            with self.subTest(size=len(data)):
                parent = os.path.join(self.temporary.name, "nonzero-marker-temp-%d" % index)
                output = os.path.join(parent, "bundle")
                os.mkdir(parent, 0o700)
                staging = os.path.join(parent, ".bundle-staging-" + ("%x" % index) * 32)
                os.mkdir(staging, 0o700)
                temporary = os.path.join(staging, ".object-INCOMPLETE")
                with open(temporary, "xb") as handle:
                    handle.write(data)
                    handle.flush()
                    os.fsync(handle.fileno())
                os.chmod(temporary, 0o600)
                root = os.open(staging, os.O_RDONLY | os.O_DIRECTORY)
                parent_descriptor = os.open(parent, os.O_RDONLY | os.O_DIRECTORY)
                try:
                    os.fsync(root)
                    os.fsync(parent_descriptor)
                finally:
                    os.close(root)
                    os.close(parent_descriptor)

                recovered = subject.Bundle.create_started(output)

                recovered.revalidate_anchor()
                self.assertFalse(os.path.lexists(staging))
                with open(os.path.join(output, "INCOMPLETE"), "rb") as marker:
                    self.assertEqual(marker.read(), subject.INCOMPLETE_MARKER)

    def test_marker_publication_race_never_replaces_final_name(self):
        parent = os.path.join(self.temporary.name, "marker-publication-race")
        output = os.path.join(parent, "bundle")
        os.mkdir(parent, 0o700)
        original_rename = subject.os.rename
        original_link = subject.os.link
        replacement = []
        payload = b"raced final marker must survive"

        def install_replacement(destination, directory):
            if replacement:
                return
            descriptor = os.open(
                destination,
                os.O_WRONLY | os.O_CREAT | os.O_EXCL,
                0o600,
                dir_fd=directory,
            )
            try:
                os.write(descriptor, payload)
                os.fchmod(descriptor, 0o600)
                replacement.append(os.fstat(descriptor))
            finally:
                os.close(descriptor)

        def race_rename(source, destination, *args, **kwargs):
            if os.fsencode(source) == b".object-INCOMPLETE" and os.fsencode(destination) == b"INCOMPLETE":
                install_replacement(destination, kwargs["dst_dir_fd"])
            return original_rename(source, destination, *args, **kwargs)

        def race_link(source, destination, *args, **kwargs):
            if os.fsencode(source) == b".object-INCOMPLETE" and os.fsencode(destination) == b"INCOMPLETE":
                install_replacement(destination, kwargs["dst_dir_fd"])
            return original_link(source, destination, *args, **kwargs)

        subject.os.rename = race_rename
        subject.os.link = race_link
        self.addCleanup(setattr, subject.os, "rename", original_rename)
        self.addCleanup(setattr, subject.os, "link", original_link)
        failure = None
        try:
            subject.Bundle.create_started(output)
        except subject.PreservationError as error:
            failure = error

        self.assertTrue(replacement)
        matching = []
        for root, _, files in os.walk(parent):
            for name in files:
                path = os.path.join(root, name)
                info = os.lstat(path)
                if (info.st_dev, info.st_ino) == (
                    replacement[0].st_dev,
                    replacement[0].st_ino,
                ):
                    matching.append(path)
        self.assertEqual(len(matching), 1)
        with open(matching[0], "rb") as handle:
            self.assertEqual(handle.read(), payload)
        self.assertEqual(stat.S_IMODE(os.lstat(matching[0]).st_mode), 0o600)
        self.assertIsNotNone(failure)
        self.assertFalse(os.path.lexists(output))

    def test_marker_publication_rejects_temporary_substitution_at_link_seam(self):
        path = os.path.join(self.temporary.name, "marker-temp-substitution")
        os.mkdir(path, 0o700)
        bundle = subject.Bundle(path)
        original_link = subject.os.link
        replacement = []

        def substitute_before_link(source, destination, *args, **kwargs):
            if os.fsencode(source) == b".object-INCOMPLETE" and not replacement:
                directory = kwargs["src_dir_fd"]
                os.unlink(source, dir_fd=directory)
                descriptor = os.open(
                    source,
                    os.O_WRONLY | os.O_CREAT | os.O_EXCL,
                    0o600,
                    dir_fd=directory,
                )
                try:
                    os.write(descriptor, subject.INCOMPLETE_MARKER)
                    os.fchmod(descriptor, 0o600)
                    replacement.append(os.fstat(descriptor))
                finally:
                    os.close(descriptor)
            return original_link(source, destination, *args, **kwargs)

        subject.os.link = substitute_before_link
        self.addCleanup(setattr, subject.os, "link", original_link)
        with self.assertRaisesRegex(
            subject.PreservationError, "lifecycle marker"
        ):
            bundle.start()

        temporary = os.lstat(os.path.join(path, ".object-INCOMPLETE"))
        final = os.lstat(os.path.join(path, "INCOMPLETE"))
        self.assertEqual(
            (temporary.st_dev, temporary.st_ino),
            (replacement[0].st_dev, replacement[0].st_ino),
        )
        self.assertEqual(
            (final.st_dev, final.st_ino),
            (replacement[0].st_dev, replacement[0].st_ino),
        )
        self.assertEqual(temporary.st_nlink, 2)

    def test_final_marker_recovery_rejects_swap_during_directory_sync(self):
        path = os.path.join(self.temporary.name, "final-marker-sync-swap")
        os.mkdir(path, 0o700)
        bundle = subject.Bundle(path)
        bundle.start()
        original_fsync = subject.os.fsync
        replacement = []

        def swap_after_directory_sync(descriptor):
            result = original_fsync(descriptor)
            info = os.fstat(descriptor)
            if stat.S_ISDIR(info.st_mode) and not replacement:
                os.unlink(b"INCOMPLETE", dir_fd=descriptor)
                marker = os.open(
                    b"INCOMPLETE",
                    os.O_WRONLY | os.O_CREAT | os.O_EXCL,
                    0o600,
                    dir_fd=descriptor,
                )
                try:
                    os.write(marker, subject.INCOMPLETE_MARKER)
                    os.fchmod(marker, 0o600)
                    replacement.append(os.fstat(marker))
                finally:
                    os.close(marker)
            return result

        subject.os.fsync = swap_after_directory_sync
        self.addCleanup(setattr, subject.os, "fsync", original_fsync)
        with self.assertRaises(subject.PreservationError):
            bundle.start()

        current = os.lstat(os.path.join(path, "INCOMPLETE"))
        self.assertEqual(
            (current.st_dev, current.st_ino, current.st_mode, current.st_size),
            (
                replacement[0].st_dev,
                replacement[0].st_ino,
                replacement[0].st_mode,
                replacement[0].st_size,
            ),
        )

    def test_zero_marker_normalization_never_chmods_substituted_name(self):
        parent = os.path.join(self.temporary.name, "zero-marker-substitution")
        output = os.path.join(parent, "bundle")
        os.mkdir(parent, 0o700)
        candidate = self._durable_staging(
            parent, ".bundle-staging-" + "a" * 32, b""
        )
        marker = os.path.join(candidate, "INCOMPLETE")
        os.chmod(marker, 0)
        original_marker = os.lstat(marker)
        original_chmod = subject.os.chmod
        original_unlink = subject.os.unlink
        original_open = subject.os.open
        replacement = []
        payload = b"must not be chmodded"

        def swap_before_path_chmod(value, mode, *args, **kwargs):
            if os.fsencode(value) == b"INCOMPLETE" and not replacement:
                directory = kwargs["dir_fd"]
                original_unlink(value, dir_fd=directory)
                descriptor = original_open(
                    value,
                    os.O_WRONLY | os.O_CREAT | os.O_EXCL,
                    0o400,
                    dir_fd=directory,
                )
                try:
                    os.write(descriptor, payload)
                    os.fchmod(descriptor, 0o400)
                    replacement.append(os.fstat(descriptor))
                finally:
                    os.close(descriptor)
            return original_chmod(value, mode, *args, **kwargs)

        subject.os.chmod = swap_before_path_chmod
        self.addCleanup(setattr, subject.os, "chmod", original_chmod)
        with self.assertRaises(subject.PreservationError):
            subject.Bundle.create_started(output)
        self.assertFalse(os.path.lexists(output))

        current = os.lstat(marker)
        if replacement:
            expected = replacement[0]
            self.assertEqual(
                (current.st_dev, current.st_ino, current.st_mode, current.st_size),
                (expected.st_dev, expected.st_ino, expected.st_mode, expected.st_size),
            )
            with open(marker, "rb") as handle:
                self.assertEqual(handle.read(), payload)
        else:
            self.assertEqual(
                (current.st_dev, current.st_ino, current.st_mode, current.st_size),
                (
                    original_marker.st_dev,
                    original_marker.st_ino,
                    original_marker.st_mode,
                    original_marker.st_size,
                ),
            )

    def test_staging_reaper_resumes_after_claim_rename_interruption(self):
        parent = os.path.join(self.temporary.name, "resume-claim")
        output = os.path.join(parent, "bundle")
        os.mkdir(parent, 0o700)
        self._durable_staging(parent, ".bundle-staging-" + "3" * 32)
        original_rename = subject.os.rename
        interrupted = False

        def interrupt_after_claim(source, destination, *args, **kwargs):
            nonlocal interrupted
            result = original_rename(source, destination, *args, **kwargs)
            if os.fsdecode(destination).startswith(".bundle-reaping-") and not interrupted:
                interrupted = True
                raise KeyboardInterrupt("injected staging claim interruption")
            return result

        subject.os.rename = interrupt_after_claim
        try:
            with self.assertRaisesRegex(KeyboardInterrupt, "claim interruption"):
                subject.Bundle.create_started(output)
        finally:
            subject.os.rename = original_rename
        self.assertTrue(interrupted)
        reaping = os.fsdecode(
            getattr(subject, "REAPING_NAME", b".bundle-reaping-" + b"0" * 32)
        )
        self.assertEqual(
            [name for name in os.listdir(parent) if name.startswith(".bundle-reaping-")],
            [reaping],
        )

        subject.Bundle.create_started(output)
        self.assertEqual(os.listdir(os.path.join(parent, reaping)), [])

    def test_staging_reaper_resumes_after_marker_unlink_interruption(self):
        parent = os.path.join(self.temporary.name, "resume-unlink")
        output = os.path.join(parent, "bundle")
        os.mkdir(parent, 0o700)
        self._durable_staging(parent, ".bundle-staging-" + "4" * 32)
        original_unlink = subject.os.unlink
        interrupted = False

        def interrupt_after_marker_unlink(name, *args, **kwargs):
            nonlocal interrupted
            result = original_unlink(name, *args, **kwargs)
            if os.fsdecode(name) == "INCOMPLETE" and not interrupted:
                interrupted = True
                raise KeyboardInterrupt("injected staging unlink interruption")
            return result

        subject.os.unlink = interrupt_after_marker_unlink
        try:
            with self.assertRaisesRegex(KeyboardInterrupt, "unlink interruption"):
                subject.Bundle.create_started(output)
        finally:
            subject.os.unlink = original_unlink
        self.assertTrue(interrupted)
        claimed = [
            name for name in os.listdir(parent) if name.startswith(".bundle-reaping-")
        ]
        reaping = os.fsdecode(
            getattr(subject, "REAPING_NAME", b".bundle-reaping-" + b"0" * 32)
        )
        self.assertEqual(claimed, [reaping])
        self.assertEqual(os.listdir(os.path.join(parent, claimed[0])), [])

        subject.Bundle.create_started(output)
        self.assertEqual(os.listdir(os.path.join(parent, claimed[0])), [])

    def test_staging_reaper_rejects_path_substitution_without_deleting_replacement(self):
        parent = os.path.join(self.temporary.name, "reap-substitution")
        output = os.path.join(parent, "bundle")
        name = ".bundle-staging-" + "5" * 32
        os.mkdir(parent, 0o700)
        candidate = self._durable_staging(parent, name)
        moved = candidate + "-moved"
        original_open = subject.os.open
        substituted = False

        def substitute_after_candidate_open(value, *args, **kwargs):
            nonlocal substituted
            descriptor = original_open(value, *args, **kwargs)
            if os.fsdecode(value) == name and not substituted:
                os.rename(candidate, moved)
                self._durable_staging(parent, name)
                substituted = True
            return descriptor

        subject.os.open = substitute_after_candidate_open
        try:
            with self.assertRaises(subject.PreservationError):
                subject.Bundle.create_started(output)
        finally:
            subject.os.open = original_open
        self.assertTrue(substituted)
        self.assertTrue(os.path.isdir(candidate))
        self.assertTrue(os.path.isdir(moved))
        self.assertFalse(os.path.lexists(output))

        final_parent = os.path.join(self.temporary.name, "reap-final-substitution")
        final_output = os.path.join(final_parent, "bundle")
        os.mkdir(final_parent, 0o700)
        final_staging = self._durable_staging(
            final_parent, ".bundle-staging-" + "6" * 32
        )
        final_inode = os.lstat(final_staging).st_ino
        original_listdir = subject.os.listdir
        final_swap = {}

        def swap_claim_after_final_list(value):
            result = original_listdir(value)
            claims = [
                entry for entry in original_listdir(final_parent)
                if entry.startswith(".bundle-reaping-")
            ]
            claimed_inode = None
            if claims:
                claimed_inode = os.lstat(os.path.join(final_parent, claims[0])).st_ino
            if (isinstance(value, int) and os.fstat(value).st_ino == final_inode
                    and claimed_inode == final_inode and result == [] and not final_swap):
                claimed = os.path.join(final_parent, claims[0])
                moved_claim = claimed + "-moved"
                os.rename(claimed, moved_claim)
                os.mkdir(claimed, 0o700)
                final_swap.update(claimed=claimed, moved=moved_claim)
            return result

        subject.os.listdir = swap_claim_after_final_list
        try:
            with self.assertRaises(subject.PreservationError):
                subject.Bundle.create_started(final_output)
        finally:
            subject.os.listdir = original_listdir
        self.assertTrue(final_swap)
        self.assertTrue(os.path.isdir(final_swap["claimed"]))
        self.assertTrue(os.path.isdir(final_swap["moved"]))
        self.assertFalse(os.path.lexists(final_output))

        claim_parent = os.path.join(self.temporary.name, "reap-claim-serialization")
        claim_output = os.path.join(claim_parent, "bundle")
        os.mkdir(claim_parent, 0o700)
        self._durable_staging(
            claim_parent, ".bundle-staging-" + "7" * 32
        )
        original_rename = subject.os.rename
        claim_ready = threading.Event()
        release_claim = threading.Event()
        attacker_done = threading.Event()
        destination = {}
        errors = []
        acquired_before_output = []

        def pause_before_claim(source, target, *args, **kwargs):
            if os.fsdecode(target).startswith(".bundle-reaping-") and not destination:
                destination["name"] = os.fsdecode(target)
                claim_ready.set()
                if not release_claim.wait(3):
                    raise AssertionError("timed out pausing staging claim")
            return original_rename(source, target, *args, **kwargs)

        def create_claimed_output():
            try:
                subject.Bundle.create_started(claim_output)
            except BaseException as error:
                errors.append(error)

        def attempt_cooperative_swap():
            descriptor = os.open(claim_parent, os.O_RDONLY | os.O_DIRECTORY)
            try:
                subject.fcntl.flock(descriptor, subject.fcntl.LOCK_EX)
                before_output = not os.path.lexists(claim_output)
                acquired_before_output.append(before_output)
                if before_output:
                    target = os.path.join(claim_parent, destination["name"])
                    if os.path.lexists(target):
                        os.rename(target, target + "-moved")
                    os.mkdir(target, 0o700)
            finally:
                os.close(descriptor)
                attacker_done.set()

        subject.os.rename = pause_before_claim
        creator = threading.Thread(target=create_claimed_output, name="claim-creator")
        attacker = threading.Thread(target=attempt_cooperative_swap, name="claim-attacker")
        try:
            creator.start()
            self.assertTrue(claim_ready.wait(3), "creator did not reach claim rename")
            attacker.start()
            attacker_finished_early = attacker_done.wait(0.2)
        finally:
            release_claim.set()
            creator.join(3)
            attacker.join(3)
            subject.os.rename = original_rename
        self.assertFalse(creator.is_alive() or attacker.is_alive())
        self.assertFalse(attacker_finished_early)
        self.assertEqual(acquired_before_output, [False])
        self.assertEqual(errors, [])
        self.assertTrue(os.path.isfile(os.path.join(claim_output, "INCOMPLETE")))

    def test_staging_scan_limit_fails_before_allocating_new_staging(self):
        parent = os.path.join(self.temporary.name, "bounded-scan")
        output = os.path.join(parent, "bundle")
        os.mkdir(parent, 0o700)
        limit = getattr(subject, "STAGING_SCAN_LIMIT", 256)
        for index in range(limit + 1):
            path = os.path.join(parent, "entry-%04d" % index)
            with open(path, "xb"):
                pass
            os.chmod(path, 0o600)

        with self.assertRaisesRegex(subject.PreservationError, "scan limit"):
            subject.Bundle.create_started(output)

        self.assertFalse(os.path.lexists(output))
        self.assertFalse(
            any(name.startswith(".bundle-staging-") for name in os.listdir(parent))
        )

    def test_create_started_returns_only_after_durable_incomplete_marker(self):
        path = os.path.join(self.temporary.name, "created-bundle")
        parent_inode = os.lstat(self.temporary.name).st_ino
        original_fsync = subject.os.fsync
        original_rename = subject.os.rename
        original_link = subject.os.link
        events = []

        def record_fsync(descriptor):
            info = os.fstat(descriptor)
            if stat.S_ISREG(info.st_mode):
                events.append("marker-file-sync")
            elif info.st_ino == parent_inode:
                events.append("parent-sync")
            else:
                events.append("staging-root-sync")
            return original_fsync(descriptor)

        def record_rename(source, destination, *args, **kwargs):
            events.append("publish")
            return original_rename(source, destination, *args, **kwargs)

        def record_link(source, destination, *args, **kwargs):
            if os.fsencode(destination) == b"INCOMPLETE":
                events.append("marker-publish")
            return original_link(source, destination, *args, **kwargs)

        subject.os.fsync = record_fsync
        subject.os.rename = record_rename
        subject.os.link = record_link
        self.addCleanup(setattr, subject.os, "fsync", original_fsync)
        self.addCleanup(setattr, subject.os, "rename", original_rename)
        self.addCleanup(setattr, subject.os, "link", original_link)
        bundle = subject.Bundle.create_started(path)
        self.assertEqual(
            events,
            [
                "marker-file-sync", "marker-publish", "marker-file-sync",
                "staging-root-sync", "staging-root-sync", "staging-root-sync",
                "publish", "parent-sync",
            ],
        )
        with open(os.path.join(path, "INCOMPLETE"), "rb") as marker:
            self.assertEqual(marker.read(), subject.INCOMPLETE_MARKER)
        self.assertEqual(stat.S_IMODE(os.lstat(path).st_mode), 0o700)
        bundle.revalidate_anchor()

    def test_create_started_rejects_parent_substitution_before_return(self):
        parent = os.path.join(self.temporary.name, "anchored-parent")
        moved = parent + "-moved"
        path = os.path.join(parent, "created-bundle")
        os.mkdir(parent, 0o700)
        original_rename = subject.os.rename
        substituted = False

        def substitute_after_publication(source, destination, *args, **kwargs):
            nonlocal substituted
            result = original_rename(source, destination, *args, **kwargs)
            if os.fsdecode(destination) == os.path.basename(path) and not substituted:
                original_rename(parent, moved)
                os.mkdir(parent, 0o700)
                os.mkdir(path, 0o700)
                substituted = True
            return result

        subject.os.rename = substitute_after_publication
        try:
            with self.assertRaises(subject.PreservationError):
                subject.Bundle.create_started(path)
            self.assertTrue(substituted)
            self.assertFalse(os.path.lexists(os.path.join(path, "INCOMPLETE")))
            with open(os.path.join(moved, "created-bundle", "INCOMPLETE"), "rb") as marker:
                self.assertEqual(marker.read(), subject.INCOMPLETE_MARKER)
        finally:
            subject.os.rename = original_rename

    def test_create_started_serializes_finish_until_started_return(self):
        path = os.path.join(self.temporary.name, "serialized-bundle")
        parent_inode = os.lstat(self.temporary.name).st_ino
        original_fsync = subject.os.fsync
        original_flock = subject.fcntl.flock
        published = threading.Event()
        release_creator = threading.Event()
        finish_attempted = threading.Event()
        finish_done = threading.Event()
        order = []
        errors = []

        def pause_creator_after_publish(descriptor):
            result = original_fsync(descriptor)
            info = os.fstat(descriptor)
            if (
                threading.current_thread().name == "bundle-creator"
                and stat.S_ISDIR(info.st_mode)
                and info.st_ino == parent_inode
                and not published.is_set()
            ):
                published.set()
                if not release_creator.wait(3):
                    raise AssertionError("timed out pausing bundle creator")
            return result

        def record_finish_lock_attempt(descriptor, operation):
            if threading.current_thread().name == "bundle-finisher":
                finish_attempted.set()
            return original_flock(descriptor, operation)

        def create():
            try:
                subject.Bundle.create_started(path)
                order.append("create-return")
            except BaseException as error:
                errors.append(error)

        def finish(bundle):
            try:
                bundle.finish()
                order.append("finish-return")
            except BaseException as error:
                errors.append(error)
            finally:
                finish_done.set()

        subject.os.fsync = pause_creator_after_publish
        subject.fcntl.flock = record_finish_lock_attempt
        creator = threading.Thread(target=create, name="bundle-creator")
        finisher = None
        try:
            creator.start()
            self.assertTrue(published.wait(3), "bundle creator did not publish")
            contender = subject.Bundle(path)
            finisher = threading.Thread(
                target=finish, args=(contender,), name="bundle-finisher"
            )
            finisher.start()
            self.assertTrue(finish_attempted.wait(3), "finish did not attempt the root lock")
            self.assertFalse(
                finish_done.wait(0.2),
                "finish acquired the root before create_started returned",
            )
        finally:
            release_creator.set()
            creator.join(3)
            if finisher is not None:
                finisher.join(3)
            subject.os.fsync = original_fsync
            subject.fcntl.flock = original_flock
        self.assertFalse(creator.is_alive() or finisher is None or finisher.is_alive())
        self.assertEqual(errors, [])
        self.assertCountEqual(order, ["create-return", "finish-return"])

    def test_create_started_death_before_marker_never_publishes_output(self):
        parent = os.path.join(self.temporary.name, "death-parent")
        path = os.path.join(parent, "created-bundle")
        os.mkdir(parent, 0o700)
        code = """
import os, signal, sys
import upgrade_baseline_bundle as subject
original = subject.Bundle._write_artifact
def die_before_marker(bundle, root, relative, data):
    if os.fsencode(relative) == b"INCOMPLETE":
        os.kill(os.getpid(), signal.SIGKILL)
    return original(bundle, root, relative, data)
subject.Bundle._write_artifact = die_before_marker
subject.Bundle.create_started(sys.argv[1])
"""
        result = subprocess.run(
            [sys.executable, "-B", "-c", code, path],
            check=False,
            env=os.environ,
            timeout=2,
        )
        self.assertEqual(result.returncode, -signal.SIGKILL)
        self.assertFalse(os.path.lexists(path))
        recovered = subject.Bundle.create_started(path)
        recovered.revalidate_anchor()
        self.assertTrue(os.path.isfile(os.path.join(path, "INCOMPLETE")))

    def test_create_started_recovers_marker_publication_crash_states(self):
        code = r'''
import os, signal, sys
import upgrade_baseline_bundle as subject
os.umask(0o777)
state = sys.argv[2]
temporary = b".object-INCOMPLETE"
if state == "temp-create":
    original_open = subject.os.open
    def die_after_marker_create(value, *args, **kwargs):
        descriptor = original_open(value, *args, **kwargs)
        if os.fsencode(value) == temporary:
            os.kill(os.getpid(), signal.SIGKILL)
        return descriptor
    subject.os.open = die_after_marker_create
elif state == "temp-fchmod":
    original_fchmod = subject.os.fchmod
    def die_after_marker_fchmod(descriptor, mode):
        result = original_fchmod(descriptor, mode)
        if mode == 0o600 and os.fstat(descriptor).st_size == 0:
            os.kill(os.getpid(), signal.SIGKILL)
        return result
    subject.os.fchmod = die_after_marker_fchmod
elif state == "link":
    original_link = subject.os.link
    def die_after_marker_link(source, destination, *args, **kwargs):
        result = original_link(source, destination, *args, **kwargs)
        if os.fsencode(source) == temporary and os.fsencode(destination) == b"INCOMPLETE":
            os.kill(os.getpid(), signal.SIGKILL)
        return result
    subject.os.link = die_after_marker_link
elif state == "unlink":
    original_unlink = subject.os.unlink
    def die_after_marker_temp_unlink(value, *args, **kwargs):
        result = original_unlink(value, *args, **kwargs)
        if os.fsencode(value) == temporary:
            os.kill(os.getpid(), signal.SIGKILL)
        return result
    subject.os.unlink = die_after_marker_temp_unlink
else:
    def die_after_prefix(handle):
        handle.seek(0)
        handle.truncate(0)
        handle.write(subject.INCOMPLETE_MARKER[:7])
        handle.flush()
        os.kill(os.getpid(), signal.SIGKILL)
    subject.Bundle._sync_file = staticmethod(die_after_prefix)
subject.Bundle.create_started(sys.argv[1])
'''
        cases = (
            ("temp-create", ((".object-INCOMPLETE", 0, 0o000),)),
            ("temp-fchmod", ((".object-INCOMPLETE", 0, 0o600),)),
            (
                "link",
                (
                    (".object-INCOMPLETE", len(subject.INCOMPLETE_MARKER), 0o600),
                    ("INCOMPLETE", len(subject.INCOMPLETE_MARKER), 0o600),
                ),
            ),
            ("unlink", (("INCOMPLETE", len(subject.INCOMPLETE_MARKER), 0o600),)),
            ("prefix", ((".object-INCOMPLETE", 7, 0o600),)),
        )
        for state, expected_entries in cases:
            with self.subTest(state=state):
                parent = os.path.join(self.temporary.name, "marker-death-" + state)
                output = os.path.join(parent, "bundle")
                os.mkdir(parent, 0o700)
                result = subprocess.run(
                    [sys.executable, "-B", "-c", code, output, state],
                    check=False,
                    env=os.environ,
                    timeout=2,
                )
                self.assertEqual(result.returncode, -signal.SIGKILL)
                self.assertFalse(os.path.lexists(output))
                staging = [
                    name for name in os.listdir(parent)
                    if name.startswith(".bundle-staging-")
                ]
                self.assertEqual(len(staging), 1)
                identities = []
                for expected_name, expected_size, expected_mode in expected_entries:
                    marker = os.path.join(parent, staging[0], expected_name)
                    info = os.lstat(marker)
                    identities.append((info.st_dev, info.st_ino))
                    self.assertEqual(info.st_size, expected_size)
                    self.assertEqual(stat.S_IMODE(info.st_mode), expected_mode)
                    self.assertEqual(info.st_nlink, len(expected_entries))
                if len(identities) == 2:
                    self.assertEqual(identities[0], identities[1])

                recovered = subject.Bundle.create_started(output)
                recovered.revalidate_anchor()
                self.assertFalse(os.path.lexists(os.path.join(parent, staging[0])))
                self.assertTrue(os.path.isfile(os.path.join(output, "INCOMPLETE")))

    def test_parent_lock_serializes_the_staging_mkdir_to_flock_window(self):
        parent = os.path.join(self.temporary.name, "mkdir-flock-serialization")
        first_output = os.path.join(parent, "first")
        second_output = os.path.join(parent, "second")
        os.mkdir(parent, 0o700)
        original_create = subject.Bundle._create_staging
        first_created = threading.Event()
        release_first = threading.Event()
        second_done = threading.Event()
        errors = {}

        def pause_after_first_mkdir(bundle, descriptor):
            name = original_create(bundle, descriptor)
            if threading.current_thread().name == "first-creator":
                first_created.set()
                if not release_first.wait(3):
                    raise AssertionError("timed out pausing after staging mkdir")
            return name

        def create(label, path, done=None):
            try:
                subject.Bundle.create_started(path)
            except BaseException as error:
                errors[label] = error
            finally:
                if done is not None:
                    done.set()

        subject.Bundle._create_staging = pause_after_first_mkdir
        first = threading.Thread(
            target=create, args=("first", first_output), name="first-creator"
        )
        second = threading.Thread(
            target=create,
            args=("second", second_output, second_done),
            name="second-creator",
        )
        try:
            first.start()
            self.assertTrue(first_created.wait(3), "first creator did not allocate staging")
            second.start()
            second_finished_early = second_done.wait(0.2)
        finally:
            release_first.set()
            first.join(3)
            second.join(3)
            subject.Bundle._create_staging = original_create
        self.assertFalse(first.is_alive() or second.is_alive())
        self.assertTrue(second_finished_early)
        self.assertNotIn("first", errors)
        self.assertIsInstance(errors.get("second"), subject.PreservationError)
        self.assertRegex(str(errors["second"]), "parent.*busy")
        self.assertTrue(os.path.isfile(os.path.join(first_output, "INCOMPLETE")))
        self.assertFalse(os.path.lexists(second_output))

        subject.Bundle.create_started(second_output)
        self.assertTrue(os.path.isfile(os.path.join(second_output, "INCOMPLETE")))

    def test_create_started_does_not_replace_existing_empty_destination(self):
        path = os.path.join(self.temporary.name, "existing-destination")
        os.mkdir(path, 0o700)
        original = os.lstat(path)
        with self.assertRaises(subject.PreservationError):
            subject.Bundle.create_started(path)
        current = os.lstat(path)
        self.assertEqual((current.st_dev, current.st_ino), (original.st_dev, original.st_ino))
        self.assertEqual(os.listdir(path), [])

    def test_create_started_closes_legacy_mkdir_constructor_replacement_gap(self):
        legacy_parent = os.path.join(self.temporary.name, "legacy-creation")
        legacy_path = os.path.join(legacy_parent, "bundle")
        original_root = legacy_path + "-original"
        os.mkdir(legacy_parent, 0o700)
        os.mkdir(legacy_path, 0o700)
        os.rename(legacy_path, original_root)
        os.mkdir(legacy_path, 0o700)

        legacy = subject.Bundle(legacy_path)
        legacy.start()
        legacy.finish()
        self.assertFalse(os.path.lexists(os.path.join(original_root, "COMPLETE")))
        with open(os.path.join(legacy_path, "COMPLETE"), "rb") as marker:
            self.assertEqual(marker.read(), subject.COMPLETE_MARKER)

        safe_parent = os.path.join(self.temporary.name, "anchored-creation")
        safe_path = os.path.join(safe_parent, "bundle")
        os.mkdir(safe_parent, 0o700)
        original_write = subject.Bundle._write_artifact
        replacement_created = False

        def replace_requested_path_after_marker(bundle, root, relative, data):
            nonlocal replacement_created
            result = original_write(bundle, root, relative, data)
            if bundle.path == os.fsencode(safe_path) and os.fsencode(relative) == b"INCOMPLETE":
                os.mkdir(safe_path, 0o700)
                replacement_created = True
            return result

        subject.Bundle._write_artifact = replace_requested_path_after_marker
        try:
            with self.assertRaises(subject.PreservationError):
                subject.Bundle.create_started(safe_path)
        finally:
            subject.Bundle._write_artifact = original_write
        self.assertTrue(replacement_created)
        self.assertEqual(os.listdir(safe_path), [])
        staging = [
            name for name in os.listdir(safe_parent)
            if name.startswith(".bundle-staging-")
        ]
        self.assertEqual(len(staging), 1)
        with open(os.path.join(safe_parent, staging[0], "INCOMPLETE"), "rb") as marker:
            self.assertEqual(marker.read(), subject.INCOMPLETE_MARKER)
        self.assertFalse(os.path.lexists(os.path.join(safe_parent, staging[0], "COMPLETE")))

    def test_finish_recovers_zero_or_partial_complete_publication_after_process_death(self):
        code = r'''
import os, signal, sys
import upgrade_baseline_bundle as subject
state = sys.argv[2]
if state == "zero":
    original_open = subject.os.open
    def die_after_complete_create(value, *args, **kwargs):
        descriptor = original_open(value, *args, **kwargs)
        if os.fsencode(value) in (b"COMPLETE", b".object-COMPLETE"):
            os.kill(os.getpid(), signal.SIGKILL)
        return descriptor
    subject.os.open = die_after_complete_create
else:
    def die_after_complete_prefix(handle):
        handle.seek(0)
        handle.truncate(0)
        handle.write(subject.COMPLETE_MARKER[:9])
        handle.flush()
        os.kill(os.getpid(), signal.SIGKILL)
    subject.Bundle._sync_file = staticmethod(die_after_complete_prefix)
subject.Bundle(sys.argv[1]).finish()
'''
        for state in ("zero", "prefix"):
            with self.subTest(state=state):
                parent = os.path.join(self.temporary.name, "complete-death-" + state)
                path = os.path.join(parent, "bundle")
                os.mkdir(parent, 0o700)
                subject.Bundle.create_started(path)
                result = subprocess.run(
                    [sys.executable, "-B", "-c", code, path, state],
                    check=False,
                    env=os.environ,
                    timeout=2,
                )
                self.assertEqual(result.returncode, -signal.SIGKILL)
                self.assertTrue(os.path.isfile(os.path.join(path, "INCOMPLETE")))

                subject.Bundle(path).finish()

                self.assertFalse(os.path.lexists(os.path.join(path, "INCOMPLETE")))
                self.assertFalse(os.path.lexists(os.path.join(path, ".object-COMPLETE")))
                with open(os.path.join(path, "COMPLETE"), "rb") as marker:
                    self.assertEqual(marker.read(), subject.COMPLETE_MARKER)

    def test_create_started_cleans_empty_staging_after_open_or_fstat_failure(self):
        for case in ("open", "fstat"):
            with self.subTest(case=case):
                parent = os.path.join(self.temporary.name, "cleanup-" + case)
                path = os.path.join(parent, "bundle")
                os.mkdir(parent, 0o700)
                parent_inode = os.lstat(parent).st_ino
                original = getattr(subject.os, case)
                failed = False

                def fail_once(value, *args, **kwargs):
                    nonlocal failed
                    if case == "open":
                        staging = os.fsdecode(value).startswith(".bundle-staging-")
                    else:
                        info = original(value)
                        staging = stat.S_ISDIR(info.st_mode) and info.st_ino != parent_inode
                    if staging and not failed:
                        failed = True
                        raise OSError("injected staging %s failure" % case)
                    return original(value, *args, **kwargs)

                setattr(subject.os, case, fail_once)
                try:
                    with self.assertRaisesRegex(
                        subject.PreservationError, "injected staging %s failure" % case
                    ):
                        subject.Bundle.create_started(path)
                finally:
                    setattr(subject.os, case, original)
                self.assertTrue(failed)
                self.assertFalse(os.path.lexists(path))
                self.assertFalse(
                    any(name.startswith(".bundle-staging-") for name in os.listdir(parent))
                )

    def test_create_started_cleans_staging_after_mkdir_side_effect_interruption(self):
        cases = (
            ("keyboard", KeyboardInterrupt("injected staging mkdir interruption")),
            ("system-exit", SystemExit("injected staging mkdir exit")),
        )
        for label, interruption in cases:
            with self.subTest(label=label):
                parent = os.path.join(self.temporary.name, "mkdir-side-effect-" + label)
                path = os.path.join(parent, "bundle")
                os.mkdir(parent, 0o700)
                parent_inode = os.lstat(parent).st_ino
                original_mkdir = subject.os.mkdir
                original_fsync = subject.os.fsync
                interrupted = False
                parent_synced = False

                def interrupt_after_mkdir(value, *args, **kwargs):
                    nonlocal interrupted
                    result = original_mkdir(value, *args, **kwargs)
                    if os.fsdecode(value).startswith(".bundle-staging-"):
                        interrupted = True
                        raise interruption
                    return result

                def record_parent_sync(descriptor):
                    nonlocal parent_synced
                    if os.fstat(descriptor).st_ino == parent_inode:
                        parent_synced = True
                    return original_fsync(descriptor)

                subject.os.mkdir = interrupt_after_mkdir
                subject.os.fsync = record_parent_sync
                try:
                    with self.assertRaises(type(interruption)) as raised:
                        subject.Bundle.create_started(path)
                finally:
                    subject.os.mkdir = original_mkdir
                    subject.os.fsync = original_fsync
                self.assertIs(raised.exception, interruption)
                self.assertTrue(interrupted)
                self.assertFalse(os.path.lexists(path))
                staging = [
                    name for name in os.listdir(parent)
                    if name.startswith(".bundle-staging-")
                ]
                self.assertEqual(staging, [])
                self.assertTrue(parent_synced)

    def test_staging_mkdir_cleanup_precedes_wrapper_close_and_original_cause(self):
        parent = os.path.join(self.temporary.name, "mkdir-cleanup-order")
        path = os.path.join(parent, "bundle")
        os.mkdir(parent, 0o700)
        parent_inode = os.lstat(parent).st_ino
        original_mkdir = subject.os.mkdir
        original_open = subject.os.open
        original_fsync = subject.os.fsync
        original_close = subject.os.close
        underlying = OSError("injected staging mkdir underlying failure")
        body_error = OSError("injected staging mkdir body failure")
        body_error.__cause__ = underlying
        parent_descriptor = None
        sync_attempts = 0
        parent_closed = False

        def mkdir_then_fail(value, *args, **kwargs):
            result = original_mkdir(value, *args, **kwargs)
            if os.fsdecode(value).startswith(".bundle-staging-"):
                raise body_error
            return result

        def track_parent_open(value, *args, **kwargs):
            nonlocal parent_descriptor
            descriptor = original_open(value, *args, **kwargs)
            if value == os.fsencode(parent):
                parent_descriptor = descriptor
            return descriptor

        def fail_cleanup_parent_sync(descriptor):
            nonlocal sync_attempts
            if os.fstat(descriptor).st_ino == parent_inode:
                sync_attempts += 1
                raise OSError("injected cleanup parent sync failure")
            return original_fsync(descriptor)

        def close_parent_then_fail(descriptor):
            nonlocal parent_closed
            original_close(descriptor)
            if descriptor == parent_descriptor:
                parent_closed = True
                raise OSError("injected parent close failure")

        subject.os.mkdir = mkdir_then_fail
        subject.os.open = track_parent_open
        subject.os.fsync = fail_cleanup_parent_sync
        subject.os.close = close_parent_then_fail
        try:
            with self.assertRaisesRegex(
                subject.PreservationError, "cannot create preservation bundle"
            ) as raised:
                subject.Bundle.create_started(path)
        finally:
            subject.os.mkdir = original_mkdir
            subject.os.open = original_open
            subject.os.fsync = original_fsync
            subject.os.close = original_close
        messages = []
        current = raised.exception.__cause__
        while current is not None and len(messages) < 12:
            messages.append(str(current))
            current = current.__cause__
        cleanup = next(i for i, message in enumerate(messages) if "staging cleanup durable" in message)
        sync = messages.index("injected cleanup parent sync failure")
        close = messages.index("injected parent close failure")
        body = messages.index("injected staging mkdir body failure")
        cause = messages.index("injected staging mkdir underlying failure")
        self.assertTrue(cleanup < sync < close < body < cause, messages)
        self.assertEqual(sync_attempts, subject.CLEANUP_RETRIES)
        self.assertTrue(parent_closed)
        self.assertFalse(os.path.lexists(path))
        self.assertFalse(any(name.startswith(".bundle-staging-") for name in os.listdir(parent)))

    def test_marker_cleanup_precedes_outer_cleanup_closes_and_original_cause(self):
        parent = os.path.join(self.temporary.name, "marker-cleanup-order")
        path = os.path.join(parent, "bundle")
        os.mkdir(parent, 0o700)
        parent_inode = os.lstat(parent).st_ino
        original_open = subject.os.open
        original_fchmod = subject.os.fchmod
        original_fsync = subject.os.fsync
        original_close = subject.os.close
        underlying = OSError("injected marker underlying failure")
        body_error = OSError("injected marker body failure")
        body_error.__cause__ = underlying
        descriptors = {}
        sync_attempts = 0
        close_attempts = []
        permission_failed = False
        marker_close_failed = False

        def track_open(value, *args, **kwargs):
            descriptor = original_open(value, *args, **kwargs)
            if value == os.fsencode(parent):
                descriptors["parent"] = descriptor
            elif os.fsdecode(value).startswith(".bundle-staging-"):
                descriptors["root"] = descriptor
            elif value in (b"INCOMPLETE", subject.INCOMPLETE_TEMP):
                descriptors["marker"] = descriptor
            return descriptor

        def fail_marker_permissions(descriptor, mode):
            nonlocal permission_failed
            if descriptor == descriptors.get("marker") and not permission_failed:
                permission_failed = True
                raise body_error
            return original_fchmod(descriptor, mode)

        def fail_cleanup_parent_sync(descriptor):
            nonlocal sync_attempts
            if os.fstat(descriptor).st_ino == parent_inode:
                sync_attempts += 1
                raise OSError("injected outer cleanup parent sync failure")
            return original_fsync(descriptor)

        def close_then_fail(descriptor):
            nonlocal marker_close_failed
            original_close(descriptor)
            for label in ("marker", "root", "parent"):
                if descriptor == descriptors.get(label):
                    if label == "marker" and marker_close_failed:
                        return
                    if label == "marker":
                        marker_close_failed = True
                    close_attempts.append(label)
                    raise OSError("injected %s close failure" % label)

        subject.os.open = track_open
        subject.os.fchmod = fail_marker_permissions
        subject.os.fsync = fail_cleanup_parent_sync
        subject.os.close = close_then_fail
        try:
            with self.assertRaisesRegex(
                subject.PreservationError, "cannot create preservation bundle"
            ) as raised:
                subject.Bundle.create_started(path)
        finally:
            subject.os.open = original_open
            subject.os.fchmod = original_fchmod
            subject.os.fsync = original_fsync
            subject.os.close = original_close
        messages = []
        current = raised.exception.__cause__
        while current is not None and len(messages) < 16:
            messages.append(str(current))
            current = current.__cause__
        marker_close = messages.index("injected marker close failure")
        cleanup = next(i for i, message in enumerate(messages) if "staging cleanup durable" in message)
        sync = messages.index("injected outer cleanup parent sync failure")
        root_close = messages.index("injected root close failure")
        parent_close = messages.index("injected parent close failure")
        body = messages.index("injected marker body failure")
        cause = messages.index("injected marker underlying failure")
        self.assertTrue(
            marker_close < cleanup < sync < root_close < parent_close < body < cause,
            messages,
        )
        self.assertEqual(sync_attempts, subject.CLEANUP_RETRIES)
        self.assertEqual(close_attempts, ["marker", "root", "parent"])
        self.assertFalse(os.path.lexists(path))

    def test_create_started_marks_residue_after_transient_rmdir_failure(self):
        parent = os.path.join(self.temporary.name, "cleanup-residue")
        path = os.path.join(parent, "bundle")
        os.mkdir(parent, 0o700)
        parent_inode = os.lstat(parent).st_ino
        original_open = subject.os.open
        original_rmdir = subject.os.rmdir
        original_fsync = subject.os.fsync
        open_failures = 0
        rmdir_failures = 0
        syncs = []

        def fail_staging_open_twice(value, *args, **kwargs):
            nonlocal open_failures
            if os.fsdecode(value).startswith(".bundle-staging-") and open_failures < 2:
                open_failures += 1
                raise OSError("injected staging open failure")
            return original_open(value, *args, **kwargs)

        def fail_staging_rmdir_twice(value, *args, **kwargs):
            nonlocal rmdir_failures
            if os.fsdecode(value).startswith(".bundle-staging-") and rmdir_failures < 2:
                rmdir_failures += 1
                raise OSError("injected staging rmdir failure")
            return original_rmdir(value, *args, **kwargs)

        def record_sync(descriptor):
            info = os.fstat(descriptor)
            if stat.S_ISREG(info.st_mode):
                syncs.append("marker-file")
            elif info.st_ino == parent_inode:
                syncs.append("parent")
            else:
                syncs.append("staging-root")
            return original_fsync(descriptor)

        subject.os.open = fail_staging_open_twice
        subject.os.rmdir = fail_staging_rmdir_twice
        subject.os.fsync = record_sync
        try:
            with self.assertRaisesRegex(subject.PreservationError, "staging open failure"):
                subject.Bundle.create_started(path)
        finally:
            subject.os.open = original_open
            subject.os.rmdir = original_rmdir
            subject.os.fsync = original_fsync
        self.assertEqual(open_failures, 2)
        self.assertEqual(rmdir_failures, 2)
        self.assertFalse(os.path.lexists(path))
        staging = [name for name in os.listdir(parent) if name.startswith(".bundle-staging-")]
        if staging:
            self.assertEqual(len(staging), 1)
            with open(os.path.join(parent, staging[0], "INCOMPLETE"), "rb") as marker:
                self.assertEqual(marker.read(), subject.INCOMPLETE_MARKER)
            self.assertEqual(
                syncs,
                [
                    "marker-file", "marker-file", "staging-root",
                    "staging-root", "staging-root", "parent",
                ],
            )
        else:
            self.assertEqual(syncs, ["parent"])

    def test_cleanup_fsyncs_existing_marker_after_write_interrupt(self):
        for marker_state in ("exact", "partial"):
            with self.subTest(marker_state=marker_state):
                parent = os.path.join(self.temporary.name, "interrupted-marker-" + marker_state)
                path = os.path.join(parent, "bundle")
                os.mkdir(parent, 0o700)
                parent_inode = os.lstat(parent).st_ino
                original_sync_file = subject.Bundle._sync_file
                original_fsync = subject.os.fsync
                interrupted = False
                syncs = []

                def interrupt_after_flush(bundle, handle):
                    nonlocal interrupted
                    if not interrupted:
                        interrupted = True
                        if marker_state == "partial":
                            handle.seek(0)
                            handle.truncate(0)
                            handle.write(subject.INCOMPLETE_MARKER[:7])
                        handle.flush()
                        raise KeyboardInterrupt("injected marker sync interruption")
                    return original_sync_file(handle)

                def record_sync(descriptor):
                    info = os.fstat(descriptor)
                    if stat.S_ISREG(info.st_mode):
                        syncs.append("marker-file")
                    elif info.st_ino == parent_inode:
                        syncs.append("parent")
                    else:
                        syncs.append("staging-root")
                    return original_fsync(descriptor)

                subject.Bundle._sync_file = interrupt_after_flush
                subject.os.fsync = record_sync
                try:
                    with self.assertRaisesRegex(KeyboardInterrupt, "marker sync interruption"):
                        subject.Bundle.create_started(path)
                finally:
                    subject.Bundle._sync_file = staticmethod(original_sync_file)
                    subject.os.fsync = original_fsync
                self.assertTrue(interrupted)
                self.assertFalse(os.path.lexists(path))
                staging = [
                    name for name in os.listdir(parent)
                    if name.startswith(".bundle-staging-")
                ]
                self.assertEqual(len(staging), 1)
                with open(os.path.join(parent, staging[0], "INCOMPLETE"), "rb") as marker:
                    self.assertEqual(marker.read(), subject.INCOMPLETE_MARKER)
                self.assertEqual(
                    syncs,
                    [
                        "marker-file", "marker-file", "staging-root",
                        "staging-root", "staging-root", "parent",
                    ],
                )

    def test_cleanup_reaches_durable_state_after_transient_failures(self):
        cases = (
            "entry-stat",
            "marker-create",
            "marker-file",
            "marker-root",
            "removed-parent",
            "removed-parent-persistent",
        )
        for case in cases:
            with self.subTest(case=case):
                parent = os.path.join(self.temporary.name, "cleanup-" + case)
                path = os.path.join(parent, "bundle")
                os.mkdir(parent, 0o700)
                parent_inode = os.lstat(parent).st_ino
                original_open = subject.os.open
                original_rmdir = subject.os.rmdir
                original_stat = subject.os.stat
                original_fsync = subject.os.fsync
                original_write = subject.Bundle._write_artifact
                original_publish = subject.Bundle._publish_marker
                open_failures = rmdir_failures = injected_failures = 0
                successful_syncs = []

                def fail_initial_staging_open(value, *args, **kwargs):
                    nonlocal open_failures
                    if os.fsdecode(value).startswith(".bundle-staging-") and not open_failures:
                        open_failures += 1
                        raise OSError("injected primary staging open failure")
                    return original_open(value, *args, **kwargs)

                def fail_first_staging_rmdir(value, *args, **kwargs):
                    nonlocal rmdir_failures
                    if (
                        case != "entry-stat"
                        and not case.startswith("removed-parent")
                        and os.fsdecode(value).startswith(".bundle-staging-")
                        and not rmdir_failures
                    ):
                        rmdir_failures += 1
                        raise OSError("injected staging rmdir failure")
                    return original_rmdir(value, *args, **kwargs)

                def fail_cleanup_stat_once(value, *args, **kwargs):
                    nonlocal injected_failures
                    if (
                        case == "entry-stat"
                        and os.fsdecode(value).startswith(".bundle-staging-")
                        and not injected_failures
                    ):
                        injected_failures += 1
                        raise OSError("injected cleanup entry stat failure")
                    return original_stat(value, *args, **kwargs)

                def fail_marker_create_once(bundle, root, relative, data):
                    nonlocal injected_failures
                    if case == "marker-create" and not injected_failures:
                        injected_failures += 1
                        raise OSError("injected marker creation failure")
                    return original_publish(bundle, root, relative, data)

                def fail_sync_once(descriptor):
                    nonlocal injected_failures
                    info = os.fstat(descriptor)
                    label = (
                        "marker-file" if stat.S_ISREG(info.st_mode)
                        else "parent" if info.st_ino == parent_inode
                        else "staging-root"
                    )
                    target = {
                        "marker-file": "marker-file",
                        "marker-root": "staging-root",
                        "removed-parent": "parent",
                        "removed-parent-persistent": "parent",
                    }.get(case)
                    persistent = case == "removed-parent-persistent"
                    if label == target and (persistent or not injected_failures):
                        injected_failures += 1
                        raise OSError("injected %s sync failure" % label)
                    result = original_fsync(descriptor)
                    successful_syncs.append(label)
                    return result

                subject.os.open = fail_initial_staging_open
                subject.os.rmdir = fail_first_staging_rmdir
                subject.os.stat = fail_cleanup_stat_once
                subject.os.fsync = fail_sync_once
                subject.Bundle._publish_marker = fail_marker_create_once
                try:
                    with self.assertRaisesRegex(
                        subject.PreservationError, "primary staging open failure"
                    ) as raised:
                        subject.Bundle.create_started(path)
                finally:
                    subject.os.open = original_open
                    subject.os.rmdir = original_rmdir
                    subject.os.stat = original_stat
                    subject.os.fsync = original_fsync
                    subject.Bundle._write_artifact = original_write
                    subject.Bundle._publish_marker = original_publish
                self.assertEqual(open_failures, 1)
                expected_failures = (
                    subject.CLEANUP_RETRIES
                    if case == "removed-parent-persistent"
                    else 1
                )
                self.assertEqual(injected_failures, expected_failures)
                self.assertFalse(os.path.lexists(path))
                staging = [
                    name for name in os.listdir(parent)
                    if name.startswith(".bundle-staging-")
                ]
                if case == "entry-stat" or case.startswith("removed-parent"):
                    self.assertEqual(staging, [])
                    expected_syncs = [] if case.endswith("persistent") else ["parent"]
                    self.assertEqual(successful_syncs, expected_syncs)
                    if case.endswith("persistent"):
                        chain = []
                        current = raised.exception
                        while current is not None and len(chain) < 8:
                            chain.append(str(current))
                            current = current.__cause__
                        self.assertTrue(
                            any("removed staging parent sync" in message for message in chain)
                        )
                else:
                    self.assertEqual(len(staging), 1)
                    with open(os.path.join(parent, staging[0], "INCOMPLETE"), "rb") as marker:
                        self.assertEqual(marker.read(), subject.INCOMPLETE_MARKER)
                    self.assertEqual(
                        successful_syncs[-3:],
                        ["staging-root", "staging-root", "parent"],
                    )

    def test_cleanup_chains_fallback_descriptor_close_failure(self):
        parent = os.path.join(self.temporary.name, "cleanup-close")
        path = os.path.join(parent, "bundle")
        os.mkdir(parent, 0o700)
        original_open = subject.os.open
        original_rmdir = subject.os.rmdir
        original_close = subject.os.close
        descriptors = {}
        open_failed = rmdir_failed = fallback_closed = parent_closed = False

        def force_fallback_open(value, *args, **kwargs):
            nonlocal open_failed
            if os.fsdecode(value).startswith(".bundle-staging-") and not open_failed:
                open_failed = True
                raise OSError("injected primary staging open failure")
            descriptor = original_open(value, *args, **kwargs)
            if value == os.fsencode(parent):
                descriptors["parent"] = descriptor
            elif os.fsdecode(value).startswith(".bundle-staging-"):
                descriptors["fallback"] = descriptor
            return descriptor

        def fail_first_rmdir(value, *args, **kwargs):
            nonlocal rmdir_failed
            if os.fsdecode(value).startswith(".bundle-staging-") and not rmdir_failed:
                rmdir_failed = True
                raise OSError("injected staging rmdir failure")
            return original_rmdir(value, *args, **kwargs)

        def close_then_fail_fallback(descriptor):
            nonlocal fallback_closed, parent_closed
            original_close(descriptor)
            if descriptor == descriptors.get("fallback"):
                fallback_closed = True
                raise OSError("injected fallback cleanup close failure")
            if descriptor == descriptors.get("parent"):
                parent_closed = True

        subject.os.open = force_fallback_open
        subject.os.rmdir = fail_first_rmdir
        subject.os.close = close_then_fail_fallback
        try:
            with self.assertRaisesRegex(
                subject.PreservationError, "primary staging open failure"
            ) as raised:
                subject.Bundle.create_started(path)
        finally:
            subject.os.open = original_open
            subject.os.rmdir = original_rmdir
            subject.os.close = original_close
        chain = []
        current = raised.exception
        while current is not None and len(chain) < 8:
            chain.append(str(current))
            current = current.__cause__
        self.assertTrue(open_failed and rmdir_failed and fallback_closed and parent_closed)
        self.assertTrue(any("fallback cleanup close failure" in message for message in chain))
        self.assertTrue(any("primary staging open failure" in message for message in chain))

    def test_failure_chaining_preserves_chronology_across_cleanup_phases(self):
        primary = subject.PreservationError("injected primary failure")
        underlying = OSError("injected underlying body failure")
        first = OSError("injected first cleanup failure")
        contextual = OSError("injected contextual cleanup failure")
        second = OSError("injected second cleanup failure")
        primary.__cause__ = underlying
        contextual.__context__ = primary
        second.__cause__ = primary

        subject._chain_failures(primary, [first, first])
        subject._chain_failures(primary, [first, contextual, contextual, second])
        subject._chain_failures(primary, [second, contextual])

        chain = []
        current = primary.__cause__
        while current is not None and len(chain) < 8:
            chain.append(current)
            current = current.__cause__
        self.assertEqual(
            [str(error) for error in chain],
            [
                "injected first cleanup failure",
                "injected contextual cleanup failure",
                "injected second cleanup failure",
                "injected underlying body failure",
            ],
        )
        self.assertEqual(len({id(error) for error in chain}), len(chain))
        self.assertNotIn(primary, chain)
        self.assertIsNone(contextual.__context__)
        self.assertTrue(all(error.__suppress_context__ for error in chain[:-1]))

        cyclic_primary = subject.PreservationError("injected self-caused primary")
        cyclic_primary.__cause__ = cyclic_primary
        cycle_cleanup = OSError("injected self-cause cleanup failure")
        subject._chain_failures(cyclic_primary, [cycle_cleanup])
        self.assertIs(cyclic_primary.__cause__, cycle_cleanup)
        self.assertIsNone(cycle_cleanup.__cause__)

    def test_close_all_attempts_every_descriptor_after_base_exception(self):
        for active_primary in (False, True):
            with self.subTest(active_primary=active_primary):
                descriptors = list(os.pipe())
                original_close = subject.os.close
                interruption = KeyboardInterrupt("injected descriptor close interruption")
                attempts = []

                def close_then_interrupt(descriptor):
                    attempts.append(descriptor)
                    original_close(descriptor)
                    if descriptor == descriptors[0]:
                        raise interruption

                subject.os.close = close_then_interrupt
                try:
                    if active_primary:
                        primary = subject.PreservationError("injected close body failure")

                        def unwind_body_error():
                            try:
                                raise primary
                            finally:
                                subject._close_all(primary, *descriptors)

                        caught = None
                        try:
                            unwind_body_error()
                        except BaseException as error:
                            caught = error
                        else:
                            self.fail("descriptor cleanup did not report the body failure")
                        self.assertIs(caught, primary)
                        self.assertIs(primary.__cause__, interruption)
                    else:
                        caught = None
                        try:
                            subject._close_descriptors(None, descriptors)
                        except BaseException as error:
                            caught = error
                        else:
                            self.fail("descriptor cleanup did not report the interruption")
                        self.assertIs(caught, interruption)
                finally:
                    subject.os.close = original_close
                    for descriptor in descriptors:
                        with contextlib.suppress(OSError):
                            original_close(descriptor)
                self.assertEqual(attempts, descriptors)

    def test_close_descriptors_never_demotes_interrupt_based_on_failure_order(self):
        for order in ("error-first", "interrupt-first"):
            with self.subTest(order=order):
                descriptors = list(os.pipe())
                original_close = subject.os.close
                interruption = KeyboardInterrupt("injected ordered close interruption")
                ordinary = OSError("injected ordered close error")
                failures = (
                    [ordinary, interruption]
                    if order == "error-first"
                    else [interruption, ordinary]
                )
                by_descriptor = dict(zip(descriptors, failures))
                attempts = []

                def close_then_fail(descriptor):
                    attempts.append(descriptor)
                    original_close(descriptor)
                    raise by_descriptor[descriptor]

                subject.os.close = close_then_fail
                caught = None
                try:
                    try:
                        subject._close_descriptors(None, descriptors)
                    except BaseException as error:
                        caught = error
                finally:
                    subject.os.close = original_close
                    for descriptor in descriptors:
                        with contextlib.suppress(OSError):
                            original_close(descriptor)
                self.assertIs(caught, interruption)
                self.assertEqual(attempts, descriptors)
                self.assertIs(interruption.__cause__, ordinary)

    def test_directory_preserves_body_error_and_attempts_all_closes(self):
        original_dup = subject.os.dup
        original_open = subject.os.open
        original_close = subject.os.close
        labels = {}
        attempts = []

        def track_dup(descriptor):
            duplicate = original_dup(descriptor)
            labels[duplicate] = "root-dup"
            return duplicate

        def track_directory_open(value, *args, **kwargs):
            descriptor = original_open(value, *args, **kwargs)
            if value in (b"first", b"second"):
                labels[descriptor] = os.fsdecode(value)
            return descriptor

        def close_then_fail(descriptor):
            original_close(descriptor)
            label = labels.get(descriptor)
            if label is not None:
                attempts.append(label)
                raise OSError("injected %s close failure" % label)

        with self.bundle._root(exclusive=True) as (_, root):
            subject.os.dup = track_dup
            subject.os.open = track_directory_open
            subject.os.close = close_then_fail
            try:
                with self.assertRaisesRegex(
                    subject.PreservationError, "injected directory body failure"
                ) as raised:
                    with self.bundle._directory(root, [b"first", b"second"], create=True):
                        raise subject.PreservationError("injected directory body failure")
            finally:
                subject.os.dup = original_dup
                subject.os.open = original_open
                subject.os.close = original_close
        chain = []
        current = raised.exception
        while current is not None and len(chain) < 8:
            chain.append(str(current))
            current = current.__cause__
        self.assertEqual(attempts, ["second", "first", "root-dup"])
        for label in attempts:
            self.assertTrue(any("%s close failure" % label in message for message in chain))

    def test_create_started_preserves_primary_error_and_attempts_all_closes(self):
        for case in ("parent-close", "root-close", "success-close"):
            with self.subTest(case=case):
                parent = os.path.join(self.temporary.name, case)
                path = os.path.join(parent, "bundle")
                os.mkdir(parent, 0o700)
                original_open = subject.os.open
                original_fstat = subject.os.fstat
                original_close = subject.os.close
                descriptors = {}
                primary_failed = False
                parent_closed = False

                def track_open(value, *args, **kwargs):
                    nonlocal primary_failed
                    if (
                        case == "parent-close"
                        and os.fsdecode(value).startswith(".bundle-staging-")
                        and not primary_failed
                    ):
                        primary_failed = True
                        raise OSError("injected primary staging open failure")
                    descriptor = original_open(value, *args, **kwargs)
                    if value == os.fsencode(parent):
                        descriptors["parent"] = descriptor
                    elif os.fsdecode(value).startswith(".bundle-staging-"):
                        descriptors["root"] = descriptor
                    return descriptor

                def fail_root_fstat_once(descriptor):
                    nonlocal primary_failed
                    if (
                        case == "root-close"
                        and descriptor == descriptors.get("root")
                        and not primary_failed
                    ):
                        primary_failed = True
                        raise OSError("injected primary staging fstat failure")
                    return original_fstat(descriptor)

                def close_then_fail(descriptor):
                    nonlocal parent_closed
                    original_close(descriptor)
                    if descriptor == descriptors.get("parent"):
                        parent_closed = True
                    if (
                        case in ("root-close", "success-close")
                        and descriptor == descriptors.get("root")
                    ) or (
                        case == "parent-close" and descriptor == descriptors.get("parent")
                    ):
                        raise OSError("injected %s failure" % case)

                subject.os.open = track_open
                subject.os.fstat = fail_root_fstat_once
                subject.os.close = close_then_fail
                try:
                    expected = (
                        "cannot close preservation bundle"
                        if case == "success-close"
                        else "injected primary staging"
                    )
                    with self.assertRaisesRegex(subject.PreservationError, expected) as raised:
                        subject.Bundle.create_started(path)
                finally:
                    subject.os.open = original_open
                    subject.os.fstat = original_fstat
                    subject.os.close = original_close
                self.assertEqual(primary_failed, case != "success-close")
                self.assertTrue(parent_closed)
                self.assertIsInstance(raised.exception.__cause__, OSError)
                self.assertIn(case, str(raised.exception.__cause__))

    def test_create_started_reports_close_failure_inside_caller_exception_handler(self):
        parent = os.path.join(self.temporary.name, "ambient-create-close")
        path = os.path.join(parent, "bundle")
        os.mkdir(parent, 0o700)
        original_open = subject.os.open
        original_close = subject.os.close
        root_descriptor = None

        def track_root_open(value, *args, **kwargs):
            nonlocal root_descriptor
            descriptor = original_open(value, *args, **kwargs)
            if os.fsdecode(value).startswith(".bundle-staging-"):
                root_descriptor = descriptor
            return descriptor

        def close_root_then_fail(descriptor):
            original_close(descriptor)
            if descriptor == root_descriptor:
                raise OSError("injected ambient create close failure")

        subject.os.open = track_root_open
        subject.os.close = close_root_then_fail
        caller_error = ValueError("unrelated caller error")
        try:
            try:
                raise caller_error
            except ValueError:
                with self.assertRaisesRegex(
                    subject.PreservationError, "cannot close preservation bundle"
                ):
                    subject.Bundle.create_started(path)
        finally:
            subject.os.open = original_open
            subject.os.close = original_close
        self.assertIsNone(caller_error.__cause__)
        with open(os.path.join(path, "INCOMPLETE"), "rb") as marker:
            self.assertEqual(marker.read(), subject.INCOMPLETE_MARKER)

    def test_root_context_preserves_body_error_and_attempts_parent_close(self):
        original_open = subject.os.open
        original_close = subject.os.close
        original_active = subject.Bundle._require_active
        descriptors = {}
        parent_closed = False

        def track_open(value, *args, **kwargs):
            descriptor = original_open(value, *args, **kwargs)
            if value == os.fsencode(self.temporary.name):
                descriptors["parent"] = descriptor
            elif value == b"bundle":
                descriptors["root"] = descriptor
            return descriptor

        def fail_operation(bundle, root):
            raise subject.PreservationError("injected bundle body failure")

        def close_root_then_fail(descriptor):
            nonlocal parent_closed
            original_close(descriptor)
            if descriptor == descriptors.get("parent"):
                parent_closed = True
            if descriptor == descriptors.get("root"):
                raise OSError("injected root close failure")

        subject.os.open = track_open
        subject.os.close = close_root_then_fail
        subject.Bundle._require_active = fail_operation
        try:
            with self.assertRaisesRegex(
                subject.PreservationError, "injected bundle body failure"
            ) as raised:
                self.bundle.capture_bytes(b"must not be captured")
        finally:
            subject.os.open = original_open
            subject.os.close = original_close
            subject.Bundle._require_active = original_active
        self.assertTrue(parent_closed)
        self.assertIsInstance(raised.exception.__cause__, OSError)
        self.assertIn("root close failure", str(raised.exception.__cause__))

    def test_root_context_preserves_body_error_when_exit_validation_fails(self):
        original = self.bundle._revalidate_open_anchor
        calls = 0

        def fail_exit_validation(parent, root, require_owner_only=True):
            nonlocal calls
            calls += 1
            if calls == 2:
                raise subject.PreservationError("injected root exit drift")
            return original(parent, root, require_owner_only)

        self.bundle._revalidate_open_anchor = fail_exit_validation
        try:
            with self.assertRaisesRegex(
                subject.PreservationError, "injected root body failure"
            ) as raised:
                with self.bundle._root(exclusive=True):
                    raise subject.PreservationError("injected root body failure")
        finally:
            del self.bundle._revalidate_open_anchor
        self.assertEqual(calls, 2)
        self.assertIsInstance(raised.exception.__cause__, subject.PreservationError)
        self.assertIn("root exit drift", str(raised.exception.__cause__))

    def test_finish_reports_close_failure_inside_caller_exception_handler(self):
        original_open = subject.os.open
        original_close = subject.os.close
        root_descriptor = None

        def track_root_open(value, *args, **kwargs):
            nonlocal root_descriptor
            descriptor = original_open(value, *args, **kwargs)
            if value == b"bundle":
                root_descriptor = descriptor
            return descriptor

        def close_root_then_fail(descriptor):
            original_close(descriptor)
            if descriptor == root_descriptor:
                raise OSError("injected ambient finish close failure")

        subject.os.open = track_root_open
        subject.os.close = close_root_then_fail
        caller_error = ValueError("unrelated finish caller error")
        try:
            try:
                raise caller_error
            except ValueError:
                with self.assertRaisesRegex(
                    subject.PreservationError, "cannot close preservation bundle"
                ):
                    self.bundle.finish()
        finally:
            subject.os.open = original_open
            subject.os.close = original_close
        self.assertIsNone(caller_error.__cause__)
        self.assertTrue(os.path.isfile(os.path.join(self.bundle_path, "COMPLETE")))

    def test_wrapped_root_open_error_places_close_before_original_cause(self):
        original_open = subject.os.open
        original_close = subject.os.close
        underlying = OSError("injected root-open underlying failure")
        body_error = OSError("injected root-open body failure")
        body_error.__cause__ = underlying
        parent_descriptor = None

        def fail_root_open(value, *args, **kwargs):
            nonlocal parent_descriptor
            if value == b"bundle":
                raise body_error
            descriptor = original_open(value, *args, **kwargs)
            if value == os.fsencode(self.temporary.name):
                parent_descriptor = descriptor
            return descriptor

        def close_parent_then_fail(descriptor):
            original_close(descriptor)
            if descriptor == parent_descriptor:
                raise OSError("injected root-open parent close failure")

        subject.os.open = fail_root_open
        subject.os.close = close_parent_then_fail
        try:
            with self.assertRaisesRegex(
                subject.PreservationError, "cannot open preservation bundle"
            ) as raised:
                subject.Bundle(self.bundle_path)
        finally:
            subject.os.open = original_open
            subject.os.close = original_close
        messages = []
        current = raised.exception.__cause__
        while current is not None and len(messages) < 8:
            messages.append(str(current))
            current = current.__cause__
        self.assertEqual(
            messages,
            [
                "injected root-open parent close failure",
                "injected root-open body failure",
                "injected root-open underlying failure",
            ],
        )

    def test_constructor_closes_parent_when_root_open_is_interrupted(self):
        original_open = subject.os.open
        parent_descriptor = []

        def interrupt_root_open(value, *args, **kwargs):
            if value == b"bundle":
                raise KeyboardInterrupt("injected root-open interruption")
            descriptor = original_open(value, *args, **kwargs)
            if value == os.fsencode(self.temporary.name):
                parent_descriptor.append(descriptor)
            return descriptor

        subject.os.open = interrupt_root_open
        try:
            with self.assertRaisesRegex(KeyboardInterrupt, "root-open interruption"):
                subject.Bundle(self.bundle_path)
        finally:
            subject.os.open = original_open
        self.assertEqual(len(parent_descriptor), 1)
        with self.assertRaises(OSError):
            os.fstat(parent_descriptor[0])

    def test_create_started_rechecks_destination_immediately_before_publish(self):
        path = os.path.join(self.temporary.name, "late-destination")
        original_write = subject.Bundle._write_artifact
        original_rmdir = subject.os.rmdir
        original_fsync = subject.os.fsync
        parent_inode = os.lstat(self.temporary.name).st_ino
        rmdir_calls = []
        parent_syncs = []

        def inject_after_marker(bundle, root, relative, data):
            result = original_write(bundle, root, relative, data)
            if bundle.path == os.fsencode(path) and os.fsencode(relative) == b"INCOMPLETE":
                os.mkdir(path, 0o700)
            return result

        def forbid_marker_stripping_cleanup(*args, **kwargs):
            rmdir_calls.append(args[0])
            raise OSError("injected staging rmdir failure")

        def record_parent_sync(descriptor):
            if os.fstat(descriptor).st_ino == parent_inode:
                parent_syncs.append(descriptor)
            return original_fsync(descriptor)

        subject.Bundle._write_artifact = inject_after_marker
        subject.os.rmdir = forbid_marker_stripping_cleanup
        subject.os.fsync = record_parent_sync
        self.addCleanup(setattr, subject.Bundle, "_write_artifact", original_write)
        self.addCleanup(setattr, subject.os, "rmdir", original_rmdir)
        self.addCleanup(setattr, subject.os, "fsync", original_fsync)
        with self.assertRaises(subject.PreservationError):
            subject.Bundle.create_started(path)
        self.assertEqual(os.listdir(path), [])
        staging = [
            name for name in os.listdir(self.temporary.name)
            if name.startswith(".bundle-staging-")
        ]
        self.assertEqual(len(staging), 1)
        with open(os.path.join(self.temporary.name, staging[0], "INCOMPLETE"), "rb") as marker:
            self.assertEqual(marker.read(), subject.INCOMPLETE_MARKER)
        self.assertEqual(rmdir_calls, [])
        self.assertEqual(len(parent_syncs), 1)

    def test_create_started_cleans_pinned_staging_after_path_stat_failure(self):
        path = os.path.join(self.temporary.name, "failed-staging-stat")
        original_stat = subject.os.stat
        failed = False

        def fail_first_staging_stat(value, *args, **kwargs):
            nonlocal failed
            if os.fsdecode(value).startswith(".bundle-staging-") and not failed:
                failed = True
                raise OSError("injected staging stat failure")
            return original_stat(value, *args, **kwargs)

        subject.os.stat = fail_first_staging_stat
        self.addCleanup(setattr, subject.os, "stat", original_stat)
        with self.assertRaisesRegex(subject.PreservationError, "injected staging stat failure"):
            subject.Bundle.create_started(path)
        self.assertTrue(failed)
        self.assertFalse(os.path.lexists(path))
        self.assertFalse(any(name.startswith(".bundle-staging-") for name in os.listdir(self.temporary.name)))

    def test_fixed_anchor_rejects_parent_root_and_mode_drift(self):
        cases = ("parent", "root", "parent-mode", "root-mode")
        for case in cases:
            with self.subTest(case=case):
                parent = os.path.join(self.temporary.name, "anchor-" + case)
                path = os.path.join(parent, "bundle")
                os.mkdir(parent, 0o700)
                bundle = subject.Bundle.create_started(path)
                try:
                    if case == "parent":
                        moved = parent + "-moved"
                        os.rename(parent, moved)
                        os.mkdir(parent, 0o700)
                        os.rename(os.path.join(moved, "bundle"), path)
                    elif case == "root":
                        os.rename(path, path + "-moved")
                        os.mkdir(path, 0o700)
                    elif case == "parent-mode":
                        os.chmod(parent, 0o500)
                    else:
                        os.chmod(path, 0o500)
                    with self.assertRaises(subject.PreservationError):
                        bundle.revalidate_anchor()
                finally:
                    if case == "parent-mode":
                        os.chmod(parent, 0o700)
                    elif case == "root-mode":
                        os.chmod(path, 0o700)

    def test_fixed_anchor_rejects_synthetic_owner_drift(self):
        for case in ("parent-owner", "root-owner"):
            with self.subTest(case=case):
                parent = os.path.join(self.temporary.name, case)
                path = os.path.join(parent, "bundle")
                os.mkdir(parent, 0o700)
                bundle = subject.Bundle.create_started(path)
                original_stat = subject.os.stat
                injected = False

                def drift_owner(value, *args, **kwargs):
                    nonlocal injected
                    info = original_stat(value, *args, **kwargs)
                    parent_match = case == "parent-owner" and value == os.fsencode(parent)
                    root_match = (
                        case == "root-owner"
                        and value == b"bundle"
                        and kwargs.get("dir_fd") is not None
                    )
                    if (parent_match or root_match) and not injected:
                        fields = list(info)
                        fields[4] = info.st_uid + 1
                        injected = True
                        return os.stat_result(fields)
                    return info

                subject.os.stat = drift_owner
                try:
                    with self.assertRaisesRegex(subject.PreservationError, "not owner-only"):
                        bundle.revalidate_anchor()
                finally:
                    subject.os.stat = original_stat
                self.assertTrue(injected)

    def test_existing_bundle_pins_parent_identity_across_operations(self):
        parent = os.path.join(self.temporary.name, "existing-parent")
        moved = parent + "-moved"
        path = os.path.join(parent, "bundle")
        os.mkdir(parent, 0o700)
        os.mkdir(path, 0o700)
        bundle = subject.Bundle(path)
        bundle.start()
        os.rename(parent, moved)
        os.mkdir(parent, 0o700)
        os.rename(os.path.join(moved, "bundle"), path)
        with self.assertRaises(subject.PreservationError):
            bundle.capture_bytes(b"must not write through a replacement parent")

    def test_start_rejects_exposed_root_before_writing_marker(self):
        path = os.path.join(self.temporary.name, "exposed-bundle")
        os.mkdir(path, 0o700)
        os.chmod(path, 0o750)
        exposed = subject.Bundle(path)
        with self.assertRaisesRegex(subject.PreservationError, "not owner-only"):
            exposed.start()
        self.assertFalse(os.path.lexists(os.path.join(path, "INCOMPLETE")))

    def test_capture_bytes_deduplicates_at_canonical_owner_only_path(self):
        content = b"arbitrary\x00bytes\xff\n"
        first = self.bundle.capture_bytes(content)
        second = self.bundle.capture_bytes(content)
        self.assertEqual(first, second)
        self.assertEqual(first["path"], "objects/sha256/%s/%s" % (first["sha256"][:2], first["sha256"]))
        self.bundle.verify_artifact(first)
        self.assertEqual(self.bundle.read_artifact(first, len(content)), content)
        path = os.path.join(self.bundle_path, first["path"])
        self.assertEqual(stat.S_IMODE(os.lstat(path).st_mode), 0o600)

    def test_capture_stream_reads_in_bounded_chunks(self):
        content = b"x" * (subject.CHUNK_SIZE * 2 + 17)

        class StrictStream:
            def __init__(self, data):
                self.data = data
                self.offset = 0
                self.requests = []

            def read(self, size):
                self.requests.append(size)
                if size > subject.CHUNK_SIZE:
                    raise AssertionError("stream read exceeded CHUNK_SIZE")
                chunk = self.data[self.offset : self.offset + size]
                self.offset += len(chunk)
                return chunk

        source = StrictStream(content)
        _, size, saved = self.bundle.capture_stream(source)
        self.assertEqual(size, len(content))
        self.assertGreater(len(source.requests), 2)
        self.assertLessEqual(max(source.requests), subject.CHUNK_SIZE)
        self.bundle.verify_artifact(saved)

    def test_capture_rejects_temporary_name_replacement_before_install(self):
        original = subject.Bundle._install_object
        replaced = False

        def replace_before_install(bundle, root, temporary, *args):
            nonlocal replaced
            os.unlink(temporary, dir_fd=root)
            descriptor = os.open(
                temporary,
                os.O_WRONLY | os.O_CREAT | os.O_EXCL,
                0o600,
                dir_fd=root,
            )
            with os.fdopen(descriptor, "wb") as handle:
                handle.write(b"replacement bytes")
            replaced = True
            return original(bundle, root, temporary, *args)

        subject.Bundle._install_object = replace_before_install
        self.addCleanup(setattr, subject.Bundle, "_install_object", original)
        with self.assertRaises(subject.PreservationError):
            self.bundle.capture_bytes(b"original bytes")
        self.assertTrue(replaced)

    def test_capture_rejects_temporary_mutation_during_link(self):
        original = subject.os.link
        mutated = False

        def mutate_before_link(source, target, **kwargs):
            nonlocal mutated
            descriptor = os.open(source, os.O_WRONLY, dir_fd=kwargs["src_dir_fd"])
            with os.fdopen(descriptor, "wb") as handle:
                handle.write(b"tampered bytes")
            mutated = True
            return original(source, target, **kwargs)

        subject.os.link = mutate_before_link
        self.addCleanup(setattr, subject.os, "link", original)
        content = b"original bytes"
        with self.assertRaises(subject.PreservationError):
            self.bundle.capture_bytes(content)
        self.assertTrue(mutated)
        digest = subject.sha256(content)
        installed = os.path.join(self.bundle_path, "objects", "sha256", digest[:2], digest)
        self.assertFalse(os.path.lexists(installed))

    def test_linked_target_is_removed_when_temporary_unlink_fails(self):
        original = subject.os.unlink
        failed = False

        def fail_temporary_once(path, *args, **kwargs):
            nonlocal failed
            if os.fsdecode(path).startswith(".object-") and not failed:
                failed = True
                raise OSError("injected temporary unlink failure")
            return original(path, *args, **kwargs)

        subject.os.unlink = fail_temporary_once
        self.addCleanup(setattr, subject.os, "unlink", original)
        content = b"unlink cleanup"
        with self.assertRaisesRegex(OSError, "injected temporary unlink failure"):
            self.bundle.capture_bytes(content)
        self.assertTrue(failed)
        digest = subject.sha256(content)
        installed = os.path.join(self.bundle_path, "objects", "sha256", digest[:2], digest)
        self.assertFalse(os.path.lexists(installed))

    def test_atomic_object_install_rejects_raced_target(self):
        original = subject.os.link
        injected = False

        def race_with_corrupt_target(source, target, **kwargs):
            nonlocal injected
            if not injected:
                injected = True
                descriptor = os.open(
                    target,
                    os.O_WRONLY | os.O_CREAT | os.O_EXCL,
                    0o600,
                    dir_fd=kwargs["dst_dir_fd"],
                )
                with os.fdopen(descriptor, "wb") as handle:
                    handle.write(b"corrupt raced object")
            return original(source, target, **kwargs)

        subject.os.link = race_with_corrupt_target
        self.addCleanup(setattr, subject.os, "link", original)
        with self.assertRaises(subject.PreservationError):
            self.bundle.capture_bytes(b"original object")
        self.assertTrue(injected)
        digest = subject.sha256(b"original object")
        raced = os.path.join(self.bundle_path, "objects", "sha256", digest[:2], digest)
        with open(raced, "rb") as handle:
            self.assertEqual(handle.read(), b"corrupt raced object")

    def test_existing_corrupt_cas_object_is_rejected(self):
        saved = self.bundle.capture_bytes(b"original")
        with open(os.path.join(self.bundle_path, saved["path"]), "wb") as handle:
            handle.write(b"corrupt!")
        with self.assertRaisesRegex(subject.PreservationError, "collision or corruption"):
            self.bundle.capture_bytes(b"original")

    def test_dedup_hashes_existing_cas_object_once(self):
        content = b"deduplicated"
        self.bundle.capture_bytes(content)
        original = subject.Bundle._hash_entry
        calls = []

        def count_hash(*args, **kwargs):
            calls.append((args, kwargs))
            return original(*args, **kwargs)

        subject.Bundle._hash_entry = count_hash
        self.addCleanup(setattr, subject.Bundle, "_hash_entry", original)
        self.bundle.capture_bytes(content)
        self.assertEqual(len(calls), 1)

    def test_verify_permissions_does_not_rehash_cas_payloads(self):
        self.bundle.capture_bytes(b"permission-only check")
        original = subject._hash_stream

        def forbid_hash(*_args, **_kwargs):
            raise AssertionError("permission verification must not hash payload bytes")

        subject._hash_stream = forbid_hash
        self.addCleanup(setattr, subject, "_hash_stream", original)
        self.bundle.verify_permissions()

    def test_finish_rejects_corrupt_cas_object(self):
        saved = self.bundle.capture_bytes(b"original")
        with open(os.path.join(self.bundle_path, saved["path"]), "wb") as handle:
            handle.write(b"corrupt!")
        with self.assertRaisesRegex(subject.PreservationError, "content-addressed"):
            self.bundle.finish()
        self.assertFalse(os.path.lexists(os.path.join(self.bundle_path, "COMPLETE")))
        self.assertTrue(os.path.lexists(os.path.join(self.bundle_path, "INCOMPLETE")))

    def test_finish_hashes_cas_objects_with_a_stable_size_bound(self):
        self.bundle.capture_bytes(b"bounded finish hash")
        original = subject._hash_stream
        limits = []

        def record_limit(source, output=None, limit=None):
            limits.append(limit)
            return original(source, output, limit)

        subject._hash_stream = record_limit
        self.addCleanup(setattr, subject, "_hash_stream", original)
        self.bundle.finish()
        self.assertTrue(limits)
        self.assertNotIn(None, limits)

    def test_artifact_verification_rejects_escape_hash_and_size(self):
        saved = self.bundle.artifact("receipts/value", b"receipt")
        for invalid in (
            {**saved, "path": "../escape"},
            {**saved, "path": "missing"},
            {**saved, "sha256": "0" * 64},
            {**saved, "size": saved["size"] + 1},
            {**saved, "size": float(saved["size"])},
        ):
            with self.subTest(invalid=invalid):
                with self.assertRaises(subject.PreservationError):
                    self.bundle.verify_artifact(invalid)

    def test_artifact_verification_rejects_exposed_file(self):
        saved = self.bundle.artifact("receipts/exposed", b"receipt")
        os.chmod(os.path.join(self.bundle_path, saved["path"]), 0o640)
        with self.assertRaisesRegex(subject.PreservationError, "not owner-only"):
            self.bundle.verify_artifact(saved)

    def test_open_entry_closes_descriptor_when_post_open_fstat_is_interrupted(self):
        saved = self.bundle.artifact("receipts/fstat-interrupt", b"receipt")
        original_open = subject.os.open
        original_fstat = subject.os.fstat
        opened = []

        def track_open(value, *args, **kwargs):
            descriptor = original_open(value, *args, **kwargs)
            if os.fsencode(value) == b"fstat-interrupt":
                opened.append(descriptor)
            return descriptor

        def interrupt_fstat(descriptor):
            if descriptor in opened:
                raise KeyboardInterrupt("injected post-open fstat interruption")
            return original_fstat(descriptor)

        subject.os.open = track_open
        subject.os.fstat = interrupt_fstat
        try:
            with self.assertRaisesRegex(KeyboardInterrupt, "post-open fstat"):
                self.bundle.verify_artifact(saved)
        finally:
            subject.os.open = original_open
            subject.os.fstat = original_fstat
        self.assertEqual(len(opened), 1)
        with self.assertRaises(OSError):
            os.fstat(opened[0])

    def test_artifact_writer_rejects_escape(self):
        outside = os.path.join(self.temporary.name, "escape")
        with self.assertRaises(subject.PreservationError):
            self.bundle.artifact("../escape", b"must stay inside")
        self.assertFalse(os.path.lexists(outside))

    def test_artifact_writer_rejects_symlinked_directory(self):
        outside = os.path.join(self.temporary.name, "outside")
        os.mkdir(outside, 0o700)
        os.symlink(outside, os.path.join(self.bundle_path, "linked"))
        with self.assertRaises(subject.PreservationError):
            self.bundle.artifact("linked/escape", b"must stay inside")
        self.assertFalse(os.path.lexists(os.path.join(outside, "escape")))

    def test_artifact_writer_rejects_reserved_namespaces(self):
        for path in (
            "COMPLETE",
            "Complete",
            "INCOMPLETE/child",
            "OBJECTS/not-cas",
            "objects/not-cas",
            ".object-INCOMPLETE",
            ".object-COMPLETE",
            ".ObJeCt-forged",
            ".object-forged",
        ):
            with self.subTest(path=path):
                with self.assertRaises(subject.PreservationError):
                    self.bundle.artifact(path, b"reserved")

    def test_bundle_staging_and_reaping_namespaces_are_always_reserved(self):
        names = (
            ".bundle-staging-" + "a" * 32,
            ".BuNdLe-StAgInG-" + "b" * 32 + "/child",
            ".bundle-reaping-" + "c" * 32,
            ".BuNdLe-ReApInG-" + "d" * 32 + "/child",
        )
        for name in names:
            with self.subTest(name=name):
                with self.assertRaisesRegex(subject.PreservationError, "reserved"):
                    self.bundle.artifact(name, b"reserved")
                record = {"path": name, "sha256": "0" * 64, "size": 0}
                with self.assertRaisesRegex(subject.PreservationError, "reserved"):
                    self.bundle.verify_artifact(record)

        for index, prefix in enumerate((".bundle-staging-", ".BuNdLe-ReApInG-")):
            with self.subTest(output_prefix=prefix):
                parent = os.path.join(self.temporary.name, "reserved-output-%d" % index)
                os.mkdir(parent, 0o700)
                self._durable_staging(parent, ".bundle-staging-malformed")
                output = os.path.join(parent, prefix + "8" * 32)
                with self.assertRaisesRegex(subject.PreservationError, "reserved"):
                    subject.Bundle.create_started(output)
                self.assertFalse(os.path.lexists(output))

        active_parent = os.path.join(self.temporary.name, "active-reserved-output")
        ordinary_output = os.path.join(active_parent, "ordinary")
        os.mkdir(active_parent, 0o700)
        active_path = self._durable_staging(
            active_parent, ".bundle-staging-" + "9" * 32
        )
        active = subject.Bundle(active_path)
        active_descriptor = os.open(active_path, os.O_RDONLY | os.O_DIRECTORY)
        try:
            subject.fcntl.flock(active_descriptor, subject.fcntl.LOCK_EX)
            subject.Bundle.create_started(ordinary_output)
            self.assertTrue(os.path.isfile(os.path.join(active_path, "INCOMPLETE")))
        finally:
            os.close(active_descriptor)
        active.revalidate_anchor()

    def test_finish_rejects_staging_or_reaping_debris_before_complete(self):
        cases = (
            (".bundle-staging-" + "e" * 32, True),
            (".bundle-reaping-" + "f" * 32, False),
        )
        for index, (name, marker) in enumerate(cases):
            with self.subTest(name=name):
                path = os.path.join(self.temporary.name, "reserved-debris-%d" % index)
                os.mkdir(path, 0o700)
                bundle = subject.Bundle(path)
                bundle.start()
                debris = os.path.join(path, name)
                os.mkdir(debris, 0o700)
                if marker:
                    marker_path = os.path.join(debris, "INCOMPLETE")
                    with open(marker_path, "xb") as handle:
                        handle.write(subject.INCOMPLETE_MARKER)
                    os.chmod(marker_path, 0o600)

                with self.assertRaisesRegex(subject.PreservationError, "temporary"):
                    bundle.finish()

                self.assertTrue(os.path.isfile(os.path.join(path, "INCOMPLETE")))
                self.assertFalse(os.path.lexists(os.path.join(path, "COMPLETE")))

    def test_finish_rejects_structurally_invalid_reserved_namespaces(self):
        cases = (
            "objects-file", "mixed-temp", "mixed-complete-temp",
            "mixed-lifecycle", "empty-object-tree", "mixed-objects",
        )
        for index, case in enumerate(cases):
            with self.subTest(case=case):
                path = os.path.join(self.temporary.name, "invalid-namespace-%d" % index)
                os.mkdir(path, 0o700)
                bundle = subject.Bundle(path)
                bundle.start()
                if case in ("objects-file", "mixed-temp", "mixed-complete-temp", "mixed-lifecycle"):
                    name = {
                        "objects-file": "objects",
                        "mixed-temp": ".object-INCOMPLETE",
                        "mixed-complete-temp": ".object-COMPLETE",
                        "mixed-lifecycle": "Complete",
                    }[case]
                    with open(os.path.join(path, name), "wb") as handle:
                        handle.write(b"invalid reserved entry")
                    os.chmod(os.path.join(path, name), 0o600)
                elif case == "empty-object-tree":
                    os.mkdir(os.path.join(path, "objects"), 0o700)
                    os.mkdir(os.path.join(path, "objects", "custom"), 0o700)
                else:
                    os.mkdir(os.path.join(path, "OBJECTS"), 0o700)
                    os.mkdir(os.path.join(path, "OBJECTS", "sha256"), 0o700)
                    os.mkdir(os.path.join(path, "OBJECTS", "sha256", "aa"), 0o700)
                with self.assertRaises(subject.PreservationError):
                    bundle.finish()

    def test_artifact_failure_removes_partial_final_name(self):
        original = subject.os.fsync

        def fail_file_sync(descriptor):
            if stat.S_ISREG(os.fstat(descriptor).st_mode):
                raise OSError("injected file sync failure")
            return original(descriptor)

        subject.os.fsync = fail_file_sync
        self.addCleanup(setattr, subject.os, "fsync", original)
        with self.assertRaisesRegex(OSError, "injected"):
            self.bundle.artifact("receipts/partial", b"must not remain")
        self.assertFalse(os.path.lexists(os.path.join(self.bundle_path, "receipts", "partial")))

    def test_artifact_fchmod_failure_removes_partial_final_name(self):
        original = subject.os.fchmod

        def fail_permissions(descriptor, mode):
            if stat.S_ISREG(os.fstat(descriptor).st_mode):
                raise OSError("injected permission failure")
            return original(descriptor, mode)

        subject.os.fchmod = fail_permissions
        self.addCleanup(setattr, subject.os, "fchmod", original)
        with self.assertRaisesRegex(OSError, "injected permission failure"):
            self.bundle.artifact("receipts/permission-partial", b"must not remain")
        self.assertFalse(
            os.path.lexists(os.path.join(self.bundle_path, "receipts", "permission-partial"))
        )

    def test_artifact_cleanup_preserves_body_error_and_attempts_close_and_unlink(self):
        cases = (
            ("ordinary", OSError("injected artifact body failure"), False),
            ("interrupt", KeyboardInterrupt("injected artifact body interruption"), True),
        )
        for label, body_error, unlink_fails in cases:
            with self.subTest(label=label):
                relative = "receipts/cleanup-%s" % label
                path = os.path.join(self.bundle_path, relative)
                original_fchmod = subject.os.fchmod
                original_close = subject.os.close
                original_unlink = subject.os.unlink
                artifact_descriptor = None
                close_attempted = False
                unlink_attempted = False
                events = []

                def interrupt_permissions(descriptor, mode):
                    nonlocal artifact_descriptor
                    if stat.S_ISREG(os.fstat(descriptor).st_mode):
                        artifact_descriptor = descriptor
                        events.append("body")
                        raise body_error
                    return original_fchmod(descriptor, mode)

                def close_then_fail(descriptor):
                    nonlocal close_attempted
                    original_close(descriptor)
                    if descriptor == artifact_descriptor:
                        close_attempted = True
                        events.append("close")
                        raise OSError("injected artifact close failure")

                def record_unlink(value, *args, **kwargs):
                    nonlocal unlink_attempted
                    if value == os.fsencode(os.path.basename(path)):
                        unlink_attempted = True
                        events.append("unlink")
                        if unlink_fails:
                            raise OSError("injected artifact unlink failure")
                    return original_unlink(value, *args, **kwargs)

                subject.os.fchmod = interrupt_permissions
                subject.os.close = close_then_fail
                subject.os.unlink = record_unlink
                try:
                    with self.assertRaises(type(body_error)) as raised:
                        self.bundle.artifact(relative, b"must not remain")
                finally:
                    subject.os.fchmod = original_fchmod
                    subject.os.close = original_close
                    subject.os.unlink = original_unlink
                self.assertIs(raised.exception, body_error)
                chain = []
                current = raised.exception.__cause__
                while current is not None and len(chain) < 8:
                    chain.append(str(current))
                    current = current.__cause__
                self.assertTrue(close_attempted)
                self.assertTrue(unlink_attempted)
                self.assertEqual(events, ["body", "close", "unlink"])
                expected_chain = ["injected artifact close failure"]
                if unlink_fails:
                    expected_chain.append("injected artifact unlink failure")
                    self.assertTrue(os.path.lexists(path))
                    original_unlink(path)
                else:
                    self.assertFalse(os.path.lexists(path))
                self.assertEqual(chain, expected_chain)

    def test_new_directory_permission_race_does_not_chmod_outside_target(self):
        outside = os.path.join(self.temporary.name, "outside-mode-target")
        os.mkdir(outside, 0o700)
        os.chmod(outside, 0o500)
        original = subject.os.chmod
        swapped = False

        def replace_with_symlink(path, mode, *args, **kwargs):
            nonlocal swapped
            if path == b"new-directory" and not swapped:
                created = os.path.join(self.bundle_path, "new-directory")
                os.rename(created, created + "-original")
                os.symlink(outside, created)
                swapped = True
            return original(path, mode, *args, **kwargs)

        subject.os.chmod = replace_with_symlink
        self.addCleanup(setattr, subject.os, "chmod", original)
        saved = self.bundle.artifact("new-directory/value", b"payload")
        self.bundle.verify_artifact(saved)
        self.assertFalse(swapped)
        self.assertEqual(stat.S_IMODE(os.stat(outside).st_mode), 0o500)

    def test_capture_file_rejects_change_during_stream(self):
        path = os.path.join(self.temporary.name, "changing.bin")
        with open(path, "wb") as handle:
            handle.write(b"before")
        original = subject._hash_stream

        def mutate_after_hash(source, output=None, limit=None):
            result = original(source, output, limit)
            with open(path, "ab") as handle:
                handle.write(b"-after")
            return result

        subject._hash_stream = mutate_after_hash
        self.addCleanup(setattr, subject, "_hash_stream", original)
        with self.assertRaisesRegex(subject.PreservationError, "changed while it was being captured"):
            self.bundle.capture_file(path)

    def test_capture_file_rejects_symlink_even_when_sizes_match(self):
        target_name = "target.bin"
        target = os.path.join(self.temporary.name, target_name)
        with open(target, "wb") as handle:
            handle.write(b"x" * len(target_name))
        link = os.path.join(self.temporary.name, "link")
        os.symlink(target_name, link)
        with self.assertRaises(subject.PreservationError):
            self.bundle.capture_file(link)

    def test_capture_file_rejects_opened_descriptor_mismatch(self):
        source = os.path.join(self.temporary.name, "source")
        replacement = os.path.join(self.temporary.name, "replacement")
        for path, content in ((source, b"source"), (replacement, b"other!")):
            with open(path, "wb") as handle:
                handle.write(content)
        original = subject.os.open

        def redirect_open(path, flags, *args, **kwargs):
            if path == source and "dir_fd" not in kwargs:
                return original(replacement, flags, *args, **kwargs)
            return original(path, flags, *args, **kwargs)

        subject.os.open = redirect_open
        self.addCleanup(setattr, subject.os, "open", original)
        with self.assertRaisesRegex(subject.PreservationError, "changed while it was being opened"):
            self.bundle.capture_file(source)

    def test_capture_file_uses_original_size_bound(self):
        path = os.path.join(self.temporary.name, "bounded")
        with open(path, "wb") as handle:
            handle.write(b"bounded source")
        original = subject._hash_stream
        limits = []

        def record_limit(source, output=None, limit=None):
            limits.append(limit)
            return original(source, output, limit)

        subject._hash_stream = record_limit
        self.addCleanup(setattr, subject, "_hash_stream", original)
        self.bundle.capture_file(path)
        self.assertIn(os.path.getsize(path) + 1, limits)

    def test_capture_file_supports_non_utf8_name(self):
        path = os.path.join(os.fsencode(self.temporary.name), b"source-\xff")
        descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "wb") as handle:
            handle.write(b"payload")
        _, _, saved = self.bundle.capture_file(path)
        self.bundle.verify_artifact(saved)

    def test_permissions_block_complete_publication(self):
        exposed = os.path.join(self.bundle_path, "exposed")
        with open(exposed, "wb") as handle:
            handle.write(b"data")
        os.chmod(exposed, 0o640)
        with self.assertRaisesRegex(subject.PreservationError, "not owner-only"):
            self.bundle.finish()
        self.assertFalse(os.path.lexists(os.path.join(self.bundle_path, "COMPLETE")))
        self.assertTrue(os.path.lexists(os.path.join(self.bundle_path, "INCOMPLETE")))

    def test_completion_marker_replaces_durable_incomplete_marker(self):
        self.bundle.capture_bytes(b"payload")
        self.bundle.finish()
        self.bundle.finish()
        self.assertTrue(os.path.isfile(os.path.join(self.bundle_path, "COMPLETE")))
        self.assertFalse(os.path.lexists(os.path.join(self.bundle_path, "INCOMPLETE")))
        self.bundle.verify_permissions()

    def test_finish_without_start_does_not_publish_complete(self):
        path = os.path.join(self.temporary.name, "not-started")
        os.mkdir(path, 0o700)
        not_started = subject.Bundle(path)
        with self.assertRaises(subject.PreservationError):
            not_started.finish()
        self.assertFalse(os.path.lexists(os.path.join(path, "COMPLETE")))

    def test_mutation_requires_active_incomplete_marker(self):
        for operation in ("artifact", "capture_bytes"):
            with self.subTest(operation=operation):
                path = os.path.join(self.temporary.name, "not-started-" + operation)
                os.mkdir(path, 0o700)
                bundle = subject.Bundle(path)
                with self.assertRaises(subject.PreservationError):
                    if operation == "artifact":
                        bundle.artifact("payload", b"data")
                    else:
                        bundle.capture_bytes(b"data")
                self.assertEqual(os.listdir(path), [])

        self.bundle.finish()
        with self.assertRaises(subject.PreservationError):
            self.bundle.capture_bytes(b"after complete")
        with self.assertRaises(subject.PreservationError):
            self.bundle.artifact("after-complete", b"data")

        publishing_path = os.path.join(self.temporary.name, "complete-publication")
        os.mkdir(publishing_path, 0o700)
        publishing = subject.Bundle(publishing_path)
        publishing.start()
        temporary = os.path.join(publishing_path, ".object-COMPLETE")
        with open(temporary, "xb"):
            pass
        os.chmod(temporary, 0o600)
        with self.assertRaisesRegex(subject.PreservationError, "publication"):
            publishing.capture_bytes(b"during complete publication")
        with self.assertRaisesRegex(subject.PreservationError, "publication"):
            publishing.artifact("during-publication", b"data")

    def test_start_rejects_nonempty_root(self):
        path = os.path.join(self.temporary.name, "nonempty")
        os.mkdir(path, 0o700)
        with open(os.path.join(path, "orphan"), "wb") as handle:
            handle.write(b"payload")
        os.chmod(os.path.join(path, "orphan"), 0o600)
        with self.assertRaises(subject.PreservationError):
            subject.Bundle(path).start()
        self.assertFalse(os.path.lexists(os.path.join(path, "INCOMPLETE")))

    def test_restrictive_umask_preserves_owner_permissions(self):
        path = os.path.join(self.temporary.name, "restrictive-umask")
        os.mkdir(path, 0o700)
        bundle = subject.Bundle(path)
        previous = os.umask(0o777)
        try:
            bundle.start()
            bundle.artifact("nested/value", b"artifact")
            bundle.capture_bytes(b"object")
            bundle.finish()
        finally:
            os.umask(previous)
        bundle.verify_permissions()

    def test_marker_umask_side_effect_then_interrupt_never_leaks_process_umask(self):
        path = os.path.join(self.temporary.name, "interrupted-marker-umask")
        os.mkdir(path, 0o700)
        bundle = subject.Bundle(path)
        original_umask = subject.os.umask
        previous_umask = original_umask(0o027)
        interrupted = None

        def interrupt_after_side_effect(mode):
            if mode == 0:
                original_umask(mode)
                raise KeyboardInterrupt("injected post-umask interruption")
            return original_umask(mode)

        try:
            subject.os.umask = interrupt_after_side_effect
            try:
                bundle.start()
            except KeyboardInterrupt as error:
                interrupted = error
            current_umask = original_umask(0o027)
            self.assertEqual(current_umask, 0o027)
        finally:
            subject.os.umask = original_umask
            original_umask(previous_umask)

        if interrupted is None:
            marker = os.lstat(os.path.join(path, "INCOMPLETE"))
            self.assertEqual(stat.S_IMODE(marker.st_mode), 0o600)

    def test_descriptor_verification_rejects_reserved_namespaces(self):
        marker = {
            "path": "INCOMPLETE",
            "sha256": subject.sha256(subject.INCOMPLETE_MARKER),
            "size": len(subject.INCOMPLETE_MARKER),
        }
        temporaries = tuple(
            {"path": path, "sha256": subject.sha256(b""), "size": 0}
            for path in (".object-INCOMPLETE", ".object-COMPLETE")
        )
        for reserved in (marker, *temporaries):
            with self.assertRaises(subject.PreservationError):
                self.bundle.verify_artifact(reserved)
            with self.assertRaises(subject.PreservationError):
                self.bundle.read_artifact(reserved, reserved["size"])

        path = os.path.join(self.bundle_path, "objects", "custom")
        os.makedirs(path, mode=0o700)
        payload = b"noncanonical object"
        with open(os.path.join(path, "value"), "wb") as handle:
            handle.write(payload)
        os.chmod(os.path.join(path, "value"), 0o600)
        noncanonical = {
            "path": "objects/custom/value",
            "sha256": subject.sha256(payload),
            "size": len(payload),
        }
        with self.assertRaises(subject.PreservationError):
            self.bundle.verify_artifact(noncanonical)

    def test_hardlinked_artifact_blocks_completion(self):
        saved = self.bundle.artifact("receipts/hardlinked", b"payload")
        os.link(os.path.join(self.bundle_path, saved["path"]), os.path.join(self.temporary.name, "outside-link"))
        with self.assertRaises(subject.PreservationError):
            self.bundle.finish()
        self.assertFalse(os.path.lexists(os.path.join(self.bundle_path, "COMPLETE")))

    def test_bytes_artifact_path_round_trips(self):
        saved = self.bundle.artifact(b"receipts/non-utf8-\xff", b"payload")
        self.assertIsInstance(saved["path"], str)
        self.bundle.verify_artifact(saved)

    def test_bounded_artifact_read_and_fail_fast_size_check(self):
        saved = self.bundle.artifact("receipts/bounded", b"payload")
        with self.assertRaises(subject.PreservationError):
            self.bundle.read_artifact(saved, saved["size"] - 1)
        with open(os.path.join(self.bundle_path, saved["path"]), "ab") as handle:
            handle.write(b"unexpected growth")
        original = subject._hash_stream
        called = False

        def forbid_hash(*args, **kwargs):
            nonlocal called
            called = True
            raise AssertionError("oversized artifact must fail before hashing")

        subject._hash_stream = forbid_hash
        self.addCleanup(setattr, subject, "_hash_stream", original)
        with self.assertRaises(subject.PreservationError):
            self.bundle.verify_artifact(saved)
        self.assertFalse(called)

    def test_exposed_directory_rejection_closes_descriptor(self):
        exposed = os.path.join(self.bundle_path, "exposed-directory")
        os.mkdir(exposed, 0o700)
        os.chmod(exposed, 0o750)
        original_open = subject.os.open
        original_close = subject.os.close
        tracked = set()
        closed = set()

        def track_open(path, *args, **kwargs):
            descriptor = original_open(path, *args, **kwargs)
            if path == b"exposed-directory":
                tracked.add(descriptor)
            return descriptor

        def track_close(descriptor):
            if descriptor in tracked:
                closed.add(descriptor)
            return original_close(descriptor)

        subject.os.open = track_open
        subject.os.close = track_close
        self.addCleanup(setattr, subject.os, "open", original_open)
        self.addCleanup(setattr, subject.os, "close", original_close)
        with self.assertRaises(subject.PreservationError):
            self.bundle.artifact("exposed-directory/payload", b"data")
        self.assertTrue(tracked)
        self.assertEqual(tracked, closed)

    def test_fifo_fails_fast_without_complete_publication(self):
        fifo = os.path.join(self.bundle_path, "fifo")
        os.mkfifo(fifo, 0o600)
        code = (
            "import sys, upgrade_baseline_bundle as m; b=m.Bundle(sys.argv[1]); "
            "\ntry: b.finish()\nexcept m.PreservationError: pass\nelse: raise SystemExit('accepted fifo')"
        )
        subprocess.run(
            [sys.executable, "-c", code, self.bundle_path],
            check=True,
            env=os.environ,
            timeout=2,
        )
        self.assertFalse(os.path.lexists(os.path.join(self.bundle_path, "COMPLETE")))

    def test_root_swap_cannot_redirect_artifact_write(self):
        outside = os.path.join(self.temporary.name, "outside-root")
        moved = os.path.join(self.temporary.name, "moved-bundle")
        os.mkdir(outside, 0o700)
        original = subject.Bundle._require_active
        swapped = False

        def swap_after_root_open(bundle, root):
            nonlocal swapped
            if bundle is self.bundle and not swapped:
                os.rename(self.bundle_path, moved)
                os.symlink(outside, self.bundle_path)
                swapped = True
            return original(bundle, root)

        subject.Bundle._require_active = swap_after_root_open
        try:
            with self.assertRaises(subject.PreservationError):
                self.bundle.artifact("receipts/anchored", b"payload")
            self.assertTrue(swapped)
            self.assertTrue(os.path.isfile(os.path.join(moved, "receipts", "anchored")))
            self.assertFalse(os.path.lexists(os.path.join(outside, "receipts")))
        finally:
            subject.Bundle._require_active = original
            if os.path.islink(self.bundle_path):
                os.unlink(self.bundle_path)
                os.rename(moved, self.bundle_path)

    def test_root_replacement_between_operations_is_rejected(self):
        moved = os.path.join(self.temporary.name, "original-bundle")
        os.rename(self.bundle_path, moved)
        os.mkdir(self.bundle_path, 0o700)
        replacement = subject.Bundle(self.bundle_path)
        replacement.start()
        try:
            with self.assertRaises(subject.PreservationError):
                self.bundle.capture_bytes(b"must target the original bundle")
        finally:
            shutil.rmtree(self.bundle_path)
            os.rename(moved, self.bundle_path)

    def test_parent_swap_during_operation_is_rejected(self):
        moved_parent = self.temporary.name + "-moved"
        original = subject.Bundle._require_active
        swapped = False

        def swap_after_active(bundle, root):
            nonlocal swapped
            original(bundle, root)
            if bundle is self.bundle and not swapped:
                os.rename(self.temporary.name, moved_parent)
                os.mkdir(self.temporary.name, 0o700)
                os.mkdir(self.bundle_path, 0o700)
                swapped = True

        subject.Bundle._require_active = swap_after_active
        try:
            with self.assertRaises(subject.PreservationError):
                self.bundle.artifact("receipts/parent-anchored", b"payload")
            self.assertTrue(swapped)
        finally:
            subject.Bundle._require_active = original
            if os.path.isdir(self.temporary.name):
                shutil.rmtree(self.temporary.name)
                os.rename(moved_parent, self.temporary.name)

    def test_component_swap_cannot_redirect_artifact_write(self):
        self.bundle.artifact("receipts/seed", b"seed")
        receipts = os.path.join(self.bundle_path, "receipts")
        moved = os.path.join(self.bundle_path, "receipts-moved")
        outside = os.path.join(self.temporary.name, "outside-component")
        os.mkdir(outside, 0o700)
        original = subject.Bundle._directory
        swapped = False

        @contextlib.contextmanager
        def swap_after_component_open(bundle, root, parts, create):
            nonlocal swapped
            with original(bundle, root, parts, create) as directory:
                if bundle is self.bundle and parts == [b"receipts"] and not swapped:
                    os.rename(receipts, moved)
                    os.symlink(outside, receipts)
                    swapped = True
                yield directory

        subject.Bundle._directory = swap_after_component_open
        try:
            with self.assertRaises(subject.PreservationError):
                self.bundle.artifact("receipts/anchored", b"payload")
            self.assertTrue(swapped)
            self.assertTrue(os.path.isfile(os.path.join(moved, "anchored")))
            self.assertFalse(os.path.lexists(os.path.join(outside, "anchored")))
        finally:
            subject.Bundle._directory = original
            if os.path.islink(receipts):
                os.unlink(receipts)
                os.rename(moved, receipts)

    def test_finish_syncs_payloads_before_complete_publication(self):
        self.bundle.artifact("receipts/one", b"one")
        self.bundle.artifact("receipts/two", b"two")
        events = []
        original_sync = subject.os.fsync
        original_write = subject.Bundle._write_artifact
        original_unlink = subject.os.unlink

        def record_sync(descriptor):
            kind = "dir-sync" if stat.S_ISDIR(os.fstat(descriptor).st_mode) else "file-sync"
            events.append(kind)
            return original_sync(descriptor)

        def record_write(bundle, root, relative, data):
            if relative == "COMPLETE":
                events.append("complete-write")
            return original_write(bundle, root, relative, data)

        def record_unlink(path, *args, **kwargs):
            if os.fsdecode(path) == "INCOMPLETE":
                events.append("unlink-incomplete")
            return original_unlink(path, *args, **kwargs)

        subject.os.fsync = record_sync
        subject.Bundle._write_artifact = record_write
        subject.os.unlink = record_unlink
        self.addCleanup(setattr, subject.os, "fsync", original_sync)
        self.addCleanup(setattr, subject.Bundle, "_write_artifact", original_write)
        self.addCleanup(setattr, subject.os, "unlink", original_unlink)
        self.bundle.finish()

        publish = events.index("complete-write")
        unlink = events.index("unlink-incomplete")
        self.assertGreaterEqual(events[:publish].count("file-sync"), 3)
        self.assertIn("dir-sync", events[:publish])
        self.assertIn("file-sync", events[publish:unlink])
        self.assertIn("dir-sync", events[publish:unlink])
        self.assertIn("dir-sync", events[unlink:])

    def test_finish_fails_closed_on_tree_walk_error(self):
        original = subject.os.fwalk

        def fail_walk(*args, **kwargs):
            kwargs["onerror"](OSError("injected walk failure"))
            if False:
                yield None

        subject.os.fwalk = fail_walk
        self.addCleanup(setattr, subject.os, "fwalk", original)
        with self.assertRaisesRegex(subject.PreservationError, "injected walk failure"):
            self.bundle.finish()
        self.assertFalse(os.path.lexists(os.path.join(self.bundle_path, "COMPLETE")))
        self.assertTrue(os.path.lexists(os.path.join(self.bundle_path, "INCOMPLETE")))

    def test_finish_resumes_after_complete_before_incomplete_removal(self):
        original = subject.os.unlink
        injected = False

        def fail_once(path, *args, **kwargs):
            nonlocal injected
            if os.fsdecode(path) == "INCOMPLETE" and not injected:
                injected = True
                raise OSError("injected unlink failure")
            return original(path, *args, **kwargs)

        subject.os.unlink = fail_once
        try:
            with self.assertRaisesRegex(OSError, "injected unlink failure"):
                self.bundle.finish()
        finally:
            subject.os.unlink = original
        self.assertTrue(os.path.isfile(os.path.join(self.bundle_path, "COMPLETE")))
        self.assertTrue(os.path.isfile(os.path.join(self.bundle_path, "INCOMPLETE")))
        self.bundle.finish()
        self.bundle.finish()
        self.assertFalse(os.path.lexists(os.path.join(self.bundle_path, "INCOMPLETE")))

    def test_finish_rejects_leaf_swap_after_file_sync(self):
        saved = self.bundle.artifact("receipts/swapped", b"original")
        target = os.path.join(self.bundle_path, saved["path"])
        original_inode = os.lstat(target).st_ino
        original_sync = subject.os.fsync
        swapped = False

        def swap_after_sync(descriptor):
            nonlocal swapped
            result = original_sync(descriptor)
            info = os.fstat(descriptor)
            if stat.S_ISREG(info.st_mode) and info.st_ino == original_inode and not swapped:
                os.rename(target, target + ".old")
                with open(target, "wb") as handle:
                    handle.write(b"replacement")
                swapped = True
            return result

        subject.os.fsync = swap_after_sync
        self.addCleanup(setattr, subject.os, "fsync", original_sync)
        with self.assertRaises(subject.PreservationError):
            self.bundle.finish()
        self.assertTrue(swapped)
        self.assertFalse(os.path.lexists(os.path.join(self.bundle_path, "COMPLETE")))

    def test_finish_rejects_subtree_swap_after_directory_sync(self):
        self.bundle.artifact("receipts/payload", b"original")
        receipts = os.path.join(self.bundle_path, "receipts")
        receipts_inode = os.lstat(receipts).st_ino
        original_sync = subject.os.fsync
        swapped = False

        def swap_after_sync(descriptor):
            nonlocal swapped
            result = original_sync(descriptor)
            info = os.fstat(descriptor)
            if stat.S_ISDIR(info.st_mode) and info.st_ino == receipts_inode and not swapped:
                os.rename(receipts, receipts + ".old")
                os.mkdir(receipts, 0o700)
                with open(os.path.join(receipts, "unseen"), "wb") as handle:
                    handle.write(b"unsynced replacement")
                os.chmod(os.path.join(receipts, "unseen"), 0o640)
                swapped = True
            return result

        subject.os.fsync = swap_after_sync
        self.addCleanup(setattr, subject.os, "fsync", original_sync)
        with self.assertRaises(subject.PreservationError):
            self.bundle.finish()
        self.assertTrue(swapped)
        self.assertFalse(os.path.lexists(os.path.join(self.bundle_path, "COMPLETE")))

    def test_finish_rejects_entry_added_after_walk_snapshot(self):
        self.bundle.artifact("receipts/payload", b"original")
        receipts_inode = os.lstat(os.path.join(self.bundle_path, "receipts")).st_ino
        original_sync = subject.os.fsync
        inserted = False

        def insert_after_child_sync(descriptor):
            nonlocal inserted
            result = original_sync(descriptor)
            info = os.fstat(descriptor)
            if stat.S_ISDIR(info.st_mode) and info.st_ino == receipts_inode and not inserted:
                late = os.path.join(self.bundle_path, "receipts", "late-exposed")
                with open(late, "wb") as handle:
                    handle.write(b"not in the walk snapshot")
                os.chmod(late, 0o640)
                inserted = True
            return result

        subject.os.fsync = insert_after_child_sync
        self.addCleanup(setattr, subject.os, "fsync", original_sync)
        with self.assertRaises(subject.PreservationError):
            self.bundle.finish()
        self.assertTrue(inserted)
        self.assertFalse(os.path.lexists(os.path.join(self.bundle_path, "COMPLETE")))

    def test_finish_rechecks_same_name_after_directory_sync(self):
        saved = self.bundle.artifact("receipts/same-name", b"original")
        target = os.path.join(self.bundle_path, saved["path"])
        target_inode = os.lstat(target).st_ino
        original = subject.Bundle._require_entry
        swapped = False

        def swap_after_identity_check(bundle, directory, name, *args, **kwargs):
            nonlocal swapped
            info = original(bundle, directory, name, *args, **kwargs)
            if name == b"same-name" and info.st_ino == target_inode and not swapped:
                replacement = os.path.join(self.temporary.name, "same-name-replacement")
                with open(replacement, "wb") as handle:
                    handle.write(b"replacement")
                os.chmod(replacement, 0o600)
                os.replace(replacement, target)
                swapped = True
            return info

        subject.Bundle._require_entry = swap_after_identity_check
        self.addCleanup(setattr, subject.Bundle, "_require_entry", original)
        with self.assertRaises(subject.PreservationError):
            self.bundle.finish()
        self.assertTrue(swapped)
        self.assertFalse(os.path.lexists(os.path.join(self.bundle_path, "COMPLETE")))

    def test_finish_rechecks_directory_identity_after_parent_sync(self):
        self.bundle.artifact("receipts/payload", b"original")
        receipts = os.path.join(self.bundle_path, "receipts")
        receipts_inode = os.lstat(receipts).st_ino
        old_receipts = os.path.join(self.temporary.name, "old-receipts")
        replacement = os.path.join(self.temporary.name, "replacement-receipts")
        os.mkdir(replacement, 0o700)
        original = subject.Bundle._require_owner_only
        swapped = False

        def swap_after_identity_check(bundle, info, path, directory):
            nonlocal swapped
            original(bundle, info, path, directory)
            if directory and info.st_ino == receipts_inode and not swapped:
                os.rename(receipts, old_receipts)
                os.rename(replacement, receipts)
                swapped = True

        subject.Bundle._require_owner_only = swap_after_identity_check
        self.addCleanup(setattr, subject.Bundle, "_require_owner_only", original)
        with self.assertRaises(subject.PreservationError):
            self.bundle.finish()
        self.assertTrue(swapped)
        self.assertFalse(os.path.lexists(os.path.join(self.bundle_path, "COMPLETE")))

    def test_finish_rechecks_descendants_after_root_sync(self):
        saved = self.bundle.capture_bytes(b"original")
        target = os.path.join(self.bundle_path, saved["path"])
        root_inode = os.lstat(self.bundle_path).st_ino
        original = subject.os.fsync
        mutated = False

        def mutate_when_root_syncs(descriptor):
            nonlocal mutated
            info = os.fstat(descriptor)
            if stat.S_ISDIR(info.st_mode) and info.st_ino == root_inode and not mutated:
                with open(target, "wb") as handle:
                    handle.write(b"corrupt!")
                mutated = True
            return original(descriptor)

        subject.os.fsync = mutate_when_root_syncs
        self.addCleanup(setattr, subject.os, "fsync", original)
        with self.assertRaises(subject.PreservationError):
            self.bundle.finish()
        self.assertTrue(mutated)
        self.assertFalse(os.path.lexists(os.path.join(self.bundle_path, "COMPLETE")))

    def test_flock_serializes_exclusive_bundle_operations(self):
        code = (
            "import sys,time,upgrade_baseline_bundle as m; b=m.Bundle(sys.argv[1]); "
            "exclusive=sys.argv[2]=='exclusive'; "
            "print('attempting',flush=True); "
            "\nwith b._root(exclusive): print('locked',flush=True); time.sleep(30)"
        )

        def start(mode):
            return subprocess.Popen(
                [sys.executable, "-c", code, self.bundle_path, mode],
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                bufsize=0,
                env=os.environ,
            )

        def wait_for_line(process, expected):
            ready, _, _ = select.select([process.stdout], [], [], 2)
            self.assertTrue(ready, "lock subprocess did not become ready")
            self.assertEqual(process.stdout.readline(), (expected + "\n").encode())

        for holder_mode in ("exclusive", "shared"):
            with self.subTest(holder=holder_mode, contender="exclusive"):
                holder = start(holder_mode)
                contender = None
                try:
                    wait_for_line(holder, "attempting")
                    wait_for_line(holder, "locked")
                    contender = start("exclusive")
                    wait_for_line(contender, "attempting")
                    ready, _, _ = select.select([contender.stdout], [], [], 0.2)
                    self.assertFalse(ready, "exclusive contender bypassed the held lock")
                    holder.terminate()
                    holder.wait(timeout=2)
                    wait_for_line(contender, "locked")
                finally:
                    for process in (holder, contender):
                        if process is not None and process.poll() is None:
                            process.terminate()
                            process.wait(timeout=2)
                        if process is not None and process.stdout is not None:
                            process.stdout.close()

    def test_trailing_separator_bundle_root_does_not_hang(self):
        path = os.path.join(self.temporary.name, "trailing")
        os.mkdir(path, 0o700)
        code = "import sys, upgrade_baseline_bundle as m; b=m.Bundle(sys.argv[1]); b.start(); b.finish()"
        subprocess.run(
            [sys.executable, "-c", code, path + os.sep],
            check=True,
            env=os.environ,
            timeout=2,
        )


if __name__ == "__main__":
    if len(sys.argv) > 1:
        unittest.main(verbosity=2)
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(BundlePrimitivesTest)
    loaded = suite.countTestCases()
    if loaded != EXPECTED_TEST_COUNT:
        print(
            "BUNDLE_TEST_COUNT_MISMATCH expected=%d loaded=%d"
            % (EXPECTED_TEST_COUNT, loaded),
            file=sys.stderr,
        )
        raise SystemExit(2)
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    print("BUNDLE_TESTS_EXECUTED count=%d" % result.testsRun)
    if result.wasSuccessful() and result.testsRun == EXPECTED_TEST_COUNT:
        print("BUNDLE_TESTS_PASSED count=%d" % result.testsRun)
        raise SystemExit(0)
    raise SystemExit(1)
