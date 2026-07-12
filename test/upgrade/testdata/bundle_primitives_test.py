#!/usr/bin/env python3
import contextlib
import os
import select
import shutil
import stat
import subprocess
import sys
import tempfile
import unittest

import upgrade_baseline_bundle as subject


class BundlePrimitivesTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.bundle_path = os.path.join(self.temporary.name, "bundle")
        os.mkdir(self.bundle_path, 0o700)
        self.bundle = subject.Bundle(self.bundle_path)
        self.bundle.start()

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
            ".ObJeCt-forged",
            ".object-forged",
        ):
            with self.subTest(path=path):
                with self.assertRaises(subject.PreservationError):
                    self.bundle.artifact(path, b"reserved")

    def test_finish_rejects_structurally_invalid_reserved_namespaces(self):
        cases = ("objects-file", "mixed-temp", "mixed-lifecycle", "empty-object-tree", "mixed-objects")
        for index, case in enumerate(cases):
            with self.subTest(case=case):
                path = os.path.join(self.temporary.name, "invalid-namespace-%d" % index)
                os.mkdir(path, 0o700)
                bundle = subject.Bundle(path)
                bundle.start()
                if case in ("objects-file", "mixed-temp", "mixed-lifecycle"):
                    name = {"objects-file": "objects", "mixed-temp": ".ObJeCt-forged", "mixed-lifecycle": "Complete"}[case]
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

    def test_descriptor_verification_rejects_reserved_namespaces(self):
        marker = {
            "path": "INCOMPLETE",
            "sha256": subject.sha256(subject.INCOMPLETE_MARKER),
            "size": len(subject.INCOMPLETE_MARKER),
        }
        with self.assertRaises(subject.PreservationError):
            self.bundle.verify_artifact(marker)
        with self.assertRaises(subject.PreservationError):
            self.bundle.read_artifact(marker, marker["size"])

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
    unittest.main(verbosity=2)
