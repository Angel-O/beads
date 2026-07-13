"""POSIX single-threaded durable storage primitives for owner-only upgrade bundles."""

import contextlib
import errno
import fcntl
import hashlib
import io
import os
import secrets
import stat

CHUNK_SIZE = 1024 * 1024
CLEANUP_RETRIES = 3
STAGING_SCAN_LIMIT = 256
INCOMPLETE_MARKER = b"capture has not completed\n"
COMPLETE_MARKER = b"owner-only preservation bundle complete\n"
INCOMPLETE_TEMP = b".object-INCOMPLETE"
COMPLETE_TEMP = b".object-COMPLETE"
MARKER_TEMPS = {b"INCOMPLETE": INCOMPLETE_TEMP, b"COMPLETE": COMPLETE_TEMP}
STAGING_PREFIX, REAPING_PREFIX = b".bundle-staging-", b".bundle-reaping-"
REAPING_NAME = REAPING_PREFIX + b"0" * 32

class PreservationError(RuntimeError):
    """A preservation bundle could not be captured or verified safely."""

def _sanitize_failure_chain(failure, original, forbidden):
    current = failure
    while True:
        forbidden.add(id(current))
        current.__context__ = None
        current.__suppress_context__ = True
        following = current.__cause__
        if following is None or following is original:
            return current
        if id(following) in forbidden:
            current.__cause__ = None
            return current
        current = following

def _chain_failures(primary, failures):
    original = getattr(primary, "_preservation_original_cause", primary.__cause__)
    if original is primary: original = None
    forbidden = {id(primary)}
    if original is not None:
        forbidden.add(id(original))
    previous_tail = None
    current = primary.__cause__
    while current is not None and current is not original and id(current) not in forbidden:
        forbidden.add(id(current))
        current.__context__ = None
        current.__suppress_context__ = True
        previous_tail = current
        following = current.__cause__
        if following is primary or (following is not original and id(following) in forbidden):
            current.__cause__ = original
            break
        current = following
    head = tail = None
    for failure in failures:
        if failure is None or id(failure) in forbidden:
            continue
        failure_tail = _sanitize_failure_chain(failure, original, forbidden)
        if head is None:
            head = failure
        if tail is not None:
            tail.__cause__ = failure
        tail = failure_tail
    if head is None:
        return
    if tail.__cause__ is None:
        tail.__cause__ = original
    if previous_tail is None:
        primary.__cause__ = head
        primary._preservation_original_cause = original
    else:
        previous_tail.__cause__ = head
    primary._preservation_failure_tail = tail
    primary.__suppress_context__ = True

def _wrap_preservation_error(message, error):
    failure = PreservationError(message)
    tail = getattr(error, "_preservation_failure_tail", None)
    if tail is None:
        failure.__cause__ = error
    else:
        head = error.__cause__
        original = getattr(error, "_preservation_original_cause", None)
        error.__cause__ = original
        tail.__cause__ = error
        failure.__cause__ = head
        failure._preservation_original_cause = error
        failure._preservation_failure_tail = tail
        del error._preservation_original_cause
        del error._preservation_failure_tail
    failure.__suppress_context__ = True
    return failure

def _close_descriptors(primary, descriptors):
    failures = []
    for descriptor in descriptors:
        if descriptor is not None:
            try:
                os.close(descriptor)
            except BaseException as error:
                failures.append(error)
    if not failures:
        return
    if primary is not None:
        _chain_failures(primary, failures)
        return
    interruption = next(
        (failure for failure in failures if not isinstance(failure, Exception)),
        None,
    )
    if interruption is not None:
        _chain_failures(
            interruption,
            [failure for failure in failures if failure is not interruption],
        )
        raise interruption
    failure = PreservationError(
        "cannot close preservation bundle: %s"
        % "; ".join(str(error) for error in failures)
    )
    _chain_failures(failure, failures)
    raise failure

def _close_all(primary, *descriptors):
    _close_descriptors(primary, descriptors)
def sha256(data):
    return hashlib.sha256(data).hexdigest()

def display(value):
    return os.fsdecode(value).encode("utf-8", "backslashreplace").decode("utf-8")

def _reserved_temporary(top): return top.lower().startswith((STAGING_PREFIX, REAPING_PREFIX))

def validate_relative(value, label):
    if not value or value.startswith(b"/") or b"\0" in value:
        raise PreservationError("%s must be a non-empty relative path" % label)
    parts = value.split(b"/")
    if any(part in (b"", b".", b"..") for part in parts):
        message = "%s must not contain empty, dot, or parent components: %s" % (label, display(value))
        raise PreservationError(message)
    return b"/".join(parts)

def _hash_stream(source, output=None, limit=None):
    digest = hashlib.sha256()
    size = 0
    while True:
        request = CHUNK_SIZE if limit is None else min(CHUNK_SIZE, limit - size)
        if request <= 0:
            return digest.hexdigest(), size
        chunk = source.read(request)
        if not chunk:
            return digest.hexdigest(), size
        digest.update(chunk)
        size += len(chunk)
        if output is not None:
            output.write(chunk)

def _identity(info):
    return (info.st_dev, info.st_ino, info.st_mode, info.st_nlink, info.st_size, info.st_mtime_ns, info.st_ctime_ns)
def _namespace_identity(info):
    return info.st_dev, info.st_ino, stat.S_IFMT(info.st_mode)

def _anchor_identity(info): return (info.st_dev, info.st_ino, stat.S_IFMT(info.st_mode), info.st_uid, stat.S_IMODE(info.st_mode))
def _open_stable_source(path):
    try:
        before = os.lstat(path)
    except OSError as error:
        raise PreservationError("cannot inspect source file %s: %s" % (display(path), error)) from error
    if not stat.S_ISREG(before.st_mode):
        raise PreservationError("source is not a regular file: %s" % display(path))
    flags = os.O_RDONLY | os.O_CLOEXEC | os.O_NONBLOCK | getattr(os, "O_NOFOLLOW", 0)
    try:
        descriptor = os.open(path, flags)
    except OSError as error:
        raise PreservationError("cannot open source file %s: %s" % (display(path), error)) from error
    try:
        opened = os.fstat(descriptor)
        if _identity(before) != _identity(opened):
            raise PreservationError("file changed while it was being opened: %s" % display(path))
        return before, opened, descriptor
    except Exception:
        os.close(descriptor)
        raise

def _assert_source_unchanged(path, before, opened, source, size):
    try:
        after_open = os.fstat(source.fileno())
        after_path = os.lstat(path)
    except OSError as error:
        raise PreservationError("cannot recheck source file %s: %s" % (display(path), error)) from error
    expected = _identity(before)
    observed = (_identity(opened), _identity(after_open), _identity(after_path))
    if any(expected != current for current in observed) or size != before.st_size:
        raise PreservationError("file changed while it was being captured: %s" % display(path))

def file_sha256(path):
    return inspect_file(path)[0]

def inspect_file(path):
    before, opened, descriptor = _open_stable_source(path)
    with os.fdopen(descriptor, "rb", buffering=0) as source:
        digest, size = _hash_stream(source, limit=before.st_size + 1)
        _assert_source_unchanged(path, before, opened, source, size)
    return digest, size

class Bundle:
    def __init__(self, path):
        self.path = os.path.abspath(os.fsencode(path))
        parent = root = None
        primary = None
        try:
            parent, root = self._open_root_descriptors()
            self._record_anchors(parent, root)
            self._revalidate_open_anchor(parent, root, require_owner_only=False)
        except BaseException as error:
            primary = error
            raise
        finally:
            _close_all(primary, root, parent)
    @classmethod
    def create_started(cls, path):
        bundle = cls.__new__(cls)
        bundle.path = os.path.abspath(os.fsencode(path))
        parent = root = None
        staging = staging_identity = None
        marker_durable = published = False
        primary = None
        try:
            parent_path, name = bundle._path_parts()
            if _reserved_temporary(name):
                raise PreservationError("preservation bundle path uses a reserved basename")
            flags = os.O_RDONLY | os.O_CLOEXEC | os.O_DIRECTORY | getattr(os, "O_NOFOLLOW", 0)
            parent = os.open(parent_path, flags)
            parent_info = os.fstat(parent)
            bundle._require_owner_only(parent_info, parent_path, directory=True)
            # Cooperating processes serialize here; uncooperative same-UID mutation is outside the portable owner-only boundary.
            try:
                fcntl.flock(parent, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except OSError as error:
                if error.errno in (errno.EACCES, errno.EAGAIN):
                    raise PreservationError("preservation bundle parent is busy") from error
                raise
            bundle._parent_identity = _anchor_identity(parent_info)
            bundle._revalidate_parent(parent)
            bundle._require_destination_absent(parent, name)
            bundle._reap_abandoned_staging(parent)
            bundle._revalidate_parent(parent)
            staging = bundle._create_staging(parent)
            root = os.open(staging, flags, dir_fd=parent)
            fcntl.flock(root, fcntl.LOCK_EX)
            opened = os.fstat(root)
            created = os.stat(staging, dir_fd=parent, follow_symlinks=False)
            if _anchor_identity(created) != _anchor_identity(opened):
                raise PreservationError("preservation bundle staging root changed while opening")
            bundle._require_owner_only(opened, staging, directory=True)
            bundle._root_identity = _anchor_identity(opened)
            staging_identity = bundle._root_identity
            bundle._revalidate_staging(parent, root, staging)
            bundle._write_artifact(root, b"INCOMPLETE", INCOMPLETE_MARKER)
            os.fsync(root); marker_durable = True
            bundle._revalidate_parent(parent)
            bundle._revalidate_staging(parent, root, staging)
            # POSIX rename can replace an empty directory, so reject any
            # destination visible at the last descriptor-relative check.
            bundle._require_destination_absent(parent, name)
            os.rename(staging, name, src_dir_fd=parent, dst_dir_fd=parent)
            published = True; staging = None
            os.fsync(parent)
            bundle._revalidate_open_anchor(parent, root)
            bundle._require_active(root)
            bundle._revalidate_open_anchor(parent, root)
            return bundle
        except (OSError, ValueError) as error:
            primary = _wrap_preservation_error(
                "cannot create preservation bundle: %s" % error, error
            )
            raise primary
        except BaseException as error:
            primary = error
            raise
        finally:
            cleanup_primary = primary
            try:
                if not published and parent is not None and staging is not None:
                    bundle._cleanup_staging(
                        parent,
                        root,
                        staging,
                        staging_identity,
                        marker_durable,
                        primary,
                    )
            except BaseException as error:
                cleanup_primary = error
                raise
            finally:
                _close_all(cleanup_primary, root, parent)
    def revalidate_anchor(self):
        with self._root(exclusive=False): pass
    def artifact(self, relative, data):
        relative_bytes = validate_relative(os.fsencode(relative), "artifact path")
        top = relative_bytes.split(b"/", 1)[0]
        if (top.lower() in (b"incomplete", b"complete", b"objects")
                or top.lower().startswith(b".object-") or _reserved_temporary(top)):
            raise PreservationError("artifact path uses a reserved bundle namespace")
        with self._root(exclusive=True) as (_, root):
            self._require_active(root)
            return self._write_artifact(root, relative_bytes, data)

    def capture_file(self, path):
        before, opened, descriptor = _open_stable_source(path)
        with os.fdopen(descriptor, "rb", buffering=0) as source:
            return self.capture_stream(source, path, before, opened, before.st_size + 1)

    def capture_bytes(self, data):
        return self.capture_stream(io.BytesIO(data))[2]

    def capture_stream(self, source, path=None, before=None, opened=None, limit=None):
        with self._root(exclusive=True) as (_, root):
            self._require_active(root)
            temporary, descriptor = self._create_temporary(root)
            try:
                os.fchmod(descriptor, 0o600)
                with os.fdopen(descriptor, "wb") as output:
                    descriptor = None
                    digest, size = _hash_stream(source, output, limit)
                    self._sync_file(output)
                    written = _identity(os.fstat(output.fileno()))
                if path is not None:
                    if before is None or opened is None:
                        raise PreservationError("stable source metadata is required")
                    _assert_source_unchanged(path, before, opened, source, size)
                self._require_active(root)
                saved = self._install_object(root, temporary, digest, size, written)
                temporary = None
                return digest, size, saved
            finally:
                if descriptor is not None:
                    os.close(descriptor)
                if temporary is not None:
                    with contextlib.suppress(FileNotFoundError):
                        os.unlink(temporary, dir_fd=root)

    def verify_artifact(self, value):
        parts, expected_digest, expected_size = self._artifact_record(value)
        with self._root(exclusive=False) as (_, root):
            with self._directory(root, parts[:-1], create=False) as directory:
                digest, size = self._hash_entry(directory, parts[-1], expected_size)
        if digest != expected_digest or size != expected_size:
            raise PreservationError("preservation artifact hash mismatch: %s" % value["path"])

    def read_artifact(self, value, max_size):
        parts, expected_digest, expected_size = self._artifact_record(value)
        if type(max_size) is not int or max_size < 0 or expected_size > max_size:
            raise PreservationError("preservation artifact exceeds the read limit")
        with self._root(exclusive=False) as (_, root):
            with self._directory(root, parts[:-1], create=False) as directory:
                data = self._read_entry(directory, parts[-1], expected_size + 1)
        if len(data) != expected_size or sha256(data) != expected_digest:
            raise PreservationError("preservation artifact hash mismatch: %s" % value["path"])
        return data

    def _artifact_record(self, value):
        if not isinstance(value, dict):
            raise PreservationError("manifest artifact must be an object")
        relative = value.get("path")
        if not isinstance(relative, str):
            raise PreservationError("manifest contains an artifact without a path")
        expected_digest = value.get("sha256")
        expected_size = value.get("size")
        valid_digest = isinstance(expected_digest, str) and len(expected_digest) == 64 and all(character in "0123456789abcdef" for character in expected_digest)
        if not valid_digest or type(expected_size) is not int or expected_size < 0:
            raise PreservationError("manifest artifact has an invalid digest or size")
        relative_bytes = validate_relative(os.fsencode(relative), "artifact path")
        parts = relative_bytes.split(b"/")
        top = parts[0].lower()
        if (top in (b"incomplete", b"complete") or top.startswith(b".object-")
                or _reserved_temporary(top)):
            raise PreservationError("manifest artifact uses a reserved bundle namespace")
        canonical = [b"objects", b"sha256", expected_digest[:2].encode(), expected_digest.encode()]
        if top == b"objects" and parts != canonical:
            raise PreservationError("manifest object path is not canonical")
        return parts, expected_digest, expected_size

    def verify_permissions(self):
        with self._root(exclusive=False) as (_, root):
            self._snapshot(root, sync=False, integrity=False)

    def start(self):
        with self._root(exclusive=True) as (parent, root):
            entries = set(os.fsencode(entry) for entry in os.listdir(root))
            if entries - {b"INCOMPLETE", INCOMPLETE_TEMP}:
                raise PreservationError("preservation bundle must be empty before start")
            self._write_artifact(root, "INCOMPLETE", INCOMPLETE_MARKER)
            os.fsync(root)
            os.fsync(parent)

    def finish(self):
        with self._root(exclusive=True) as (parent, root):
            has_incomplete = self._entry_exists(root, b"INCOMPLETE")
            has_complete = self._entry_exists(root, b"COMPLETE")
            has_complete_temp = self._entry_exists(root, COMPLETE_TEMP)
            if not has_incomplete and not has_complete and not has_complete_temp:
                raise PreservationError("preservation bundle has not been started")
            if has_incomplete:
                self._require_marker(root, b"INCOMPLETE", INCOMPLETE_MARKER)
            if has_complete_temp and not has_incomplete:
                raise PreservationError("COMPLETE publication has no INCOMPLETE marker")
            if has_complete or has_complete_temp:
                self._publish_marker(root, b"COMPLETE", COMPLETE_MARKER)
                has_complete = True
            if has_complete:
                self._require_marker(root, b"COMPLETE", COMPLETE_MARKER)
            self._sync_tree(root)
            if not has_complete:
                self._write_artifact(root, "COMPLETE", COMPLETE_MARKER)
            os.fsync(root)
            os.fsync(parent)
            if has_incomplete:
                os.unlink(b"INCOMPLETE", dir_fd=root)
                os.fsync(root)
                os.fsync(parent)
                self._require_marker(root, b"COMPLETE", COMPLETE_MARKER)

    def _path_parts(self):
        parent_path = os.path.dirname(self.path)
        name = os.path.basename(self.path)
        if not name:
            raise PreservationError("preservation bundle path cannot be a filesystem root")
        return parent_path, name

    def _open_root_descriptors(self):
        parent_path, name = self._path_parts()
        flags = os.O_RDONLY | os.O_CLOEXEC | os.O_DIRECTORY | getattr(os, "O_NOFOLLOW", 0)
        parent = None
        try:
            parent = os.open(parent_path, flags)
            return parent, os.open(name, flags, dir_fd=parent)
        except OSError as error:
            primary = _wrap_preservation_error(
                "cannot open preservation bundle: %s" % error, error
            )
            _close_all(primary, parent)
            raise primary
        except BaseException as primary:
            _close_all(primary, parent)
            raise

    def _record_anchors(self, parent, root):
        self._parent_identity, self._root_identity = _anchor_identity(os.fstat(parent)), _anchor_identity(os.fstat(root))

    def _revalidate_parent(self, parent, require_owner_only=True):
        parent_path, _ = self._path_parts()
        try:
            opened = os.fstat(parent)
            current = os.stat(parent_path, follow_symlinks=False)
        except OSError as error:
            raise PreservationError("cannot revalidate preservation bundle parent: %s" % error) from error
        if require_owner_only:
            for info in (opened, current):
                self._require_owner_only(info, parent_path, directory=True)
        if any(_anchor_identity(info) != self._parent_identity for info in (opened, current)):
            raise PreservationError("preservation bundle parent anchor changed")

    def _revalidate_open_anchor(self, parent, root, require_owner_only=True):
        self._revalidate_parent(parent, require_owner_only)
        _, name = self._path_parts()
        try:
            opened = os.fstat(root)
            current = os.stat(name, dir_fd=parent, follow_symlinks=False)
        except OSError as error:
            raise PreservationError("cannot revalidate preservation bundle root: %s" % error) from error
        if require_owner_only:
            for info in (opened, current):
                self._require_owner_only(info, self.path, directory=True)
        if any(_anchor_identity(info) != self._root_identity for info in (opened, current)):
            raise PreservationError("preservation bundle root anchor changed")
        self._revalidate_parent(parent, require_owner_only)

    def _revalidate_staging(self, parent, root, staging):
        opened = os.fstat(root)
        current = os.stat(staging, dir_fd=parent, follow_symlinks=False)
        for info in (opened, current):
            self._require_owner_only(info, staging, directory=True)
        if any(_anchor_identity(info) != self._root_identity for info in (opened, current)):
            raise PreservationError("preservation bundle staging root changed")

    def _require_destination_absent(self, parent, name):
        try:
            os.stat(name, dir_fd=parent, follow_symlinks=False)
        except FileNotFoundError:
            return
        except OSError as error:
            raise PreservationError("cannot inspect preservation bundle destination: %s" % error) from error
        raise PreservationError("preservation bundle destination already exists")

    def _create_staging(self, parent):
        previous_umask = os.umask(0o077)
        try:
            for _ in range(100):
                name = os.fsencode(".bundle-staging-" + secrets.token_hex(16))
                try:
                    os.mkdir(name, 0o700, dir_fd=parent)
                    return name
                except FileExistsError:
                    continue
                except BaseException as primary:
                    self._cleanup_staging(parent, None, name, None, False, primary)
                    raise
        finally:
            os.umask(previous_umask)
        raise PreservationError("cannot allocate a unique preservation bundle staging root")

    def _reap_abandoned_staging(self, parent):
        candidates = []
        try:
            with os.scandir(parent) as entries:
                for count, entry in enumerate(entries, 1):
                    if count > STAGING_SCAN_LIMIT:
                        raise PreservationError(
                            "preservation bundle staging scan limit exceeded"
                        )
                    name = os.fsencode(entry.name)
                    lowered = name.lower()
                    if not (
                        lowered.startswith(STAGING_PREFIX)
                        or lowered.startswith(REAPING_PREFIX)
                    ):
                        continue
                    prefix = (
                        STAGING_PREFIX
                        if lowered.startswith(STAGING_PREFIX)
                        else REAPING_PREFIX
                    )
                    token = name[len(prefix):]
                    if (not name.startswith(prefix) or len(token) != 32
                            or any(byte not in b"0123456789abcdef" for byte in token)):
                        raise PreservationError(
                            "preservation bundle contains a malformed temporary staging name"
                        )
                    candidates.append(name)
        except PreservationError:
            raise
        except OSError as error:
            raise PreservationError(
                "cannot scan preservation bundle staging roots: %s" % error
            ) from error
        for name in sorted(candidates, key=lambda value: (value != REAPING_NAME, value)):
            self._reap_candidate(parent, name)

    def _reap_candidate(self, parent, name):
        root = tombstone = None
        primary = None
        try:
            before = os.stat(name, dir_fd=parent, follow_symlinks=False)
            self._require_cleanup_root(before, name)
            flags = os.O_RDONLY | os.O_CLOEXEC | os.O_DIRECTORY | getattr(os, "O_NOFOLLOW", 0)
            root = os.open(name, flags, dir_fd=parent)
            opened = os.fstat(root)
            self._require_cleanup_root(opened, name)
            expected = _anchor_identity(opened)
            if _anchor_identity(before) != expected:
                raise PreservationError("temporary staging root changed while opening")
            try:
                fcntl.flock(root, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except OSError as error:
                if error.errno in (errno.EACCES, errno.EAGAIN):
                    return
                raise
            self._revalidate_cleanup_root(parent, root, name, expected)
            has_marker = self._inspect_cleanup_root(parent, root, name, expected)
            if name == REAPING_NAME:
                self._empty_cleanup_root(parent, root, name, expected, has_marker)
                return

            created = False
            previous_umask = os.umask(0o077)
            try:
                try:
                    os.mkdir(REAPING_NAME, 0o700, dir_fd=parent)
                except FileExistsError:
                    pass
                else:
                    created = True
            finally:
                os.umask(previous_umask)
            if created:
                os.fsync(parent)
            before_tombstone = os.stat(
                REAPING_NAME, dir_fd=parent, follow_symlinks=False
            )
            self._require_cleanup_root(before_tombstone, REAPING_NAME)
            tombstone = os.open(REAPING_NAME, flags, dir_fd=parent)
            opened_tombstone = os.fstat(tombstone)
            self._require_cleanup_root(opened_tombstone, REAPING_NAME)
            tombstone_identity = _anchor_identity(opened_tombstone)
            if _anchor_identity(before_tombstone) != tombstone_identity:
                raise PreservationError("cleanup tombstone changed while opening")
            try:
                fcntl.flock(tombstone, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except OSError as error:
                if error.errno in (errno.EACCES, errno.EAGAIN):
                    raise PreservationError("preservation cleanup tombstone is busy") from error
                raise
            self._revalidate_cleanup_root(
                parent, tombstone, REAPING_NAME, tombstone_identity
            )
            tombstone_marker = self._inspect_cleanup_root(
                parent, tombstone, REAPING_NAME, tombstone_identity
            )
            self._empty_cleanup_root(
                parent, tombstone, REAPING_NAME, tombstone_identity, tombstone_marker
            )
            self._revalidate_cleanup_root(parent, root, name, expected)
            os.rename(name, REAPING_NAME, src_dir_fd=parent, dst_dir_fd=parent)
            os.fsync(parent)
            name = REAPING_NAME
            self._revalidate_cleanup_root(parent, root, name, expected)
            self._empty_cleanup_root(parent, root, name, expected, has_marker)
        except (OSError, ValueError) as error:
            primary = _wrap_preservation_error(
                "cannot reap preservation bundle staging root: %s" % error,
                error,
            )
            raise primary
        except BaseException as error:
            primary = error
            raise
        finally:
            _close_all(primary, tombstone, root)

    def _inspect_cleanup_root(self, parent, root, name, expected):
        entries = sorted(os.fsencode(entry) for entry in os.listdir(root))
        self._revalidate_cleanup_root(parent, root, name, expected)
        if not entries:
            return None
        allowed = ([INCOMPLETE_TEMP], [b"INCOMPLETE"], sorted((INCOMPLETE_TEMP, b"INCOMPLETE")))
        if entries not in allowed:
            raise PreservationError(
                "temporary staging root is not empty or exact INCOMPLETE evidence"
            )
        self._publish_marker(root, b"INCOMPLETE", INCOMPLETE_MARKER)
        os.fsync(parent)
        self._revalidate_cleanup_root(parent, root, name, expected)
        return b"INCOMPLETE"

    def _empty_cleanup_root(self, parent, root, name, expected, marker):
        if marker is not None:
            os.unlink(marker, dir_fd=root)
        os.fsync(root)
        self._revalidate_cleanup_root(parent, root, name, expected)
        if os.listdir(root):
            raise PreservationError("cleanup tombstone is not empty")
        self._revalidate_cleanup_root(parent, root, name, expected)

    def _revalidate_cleanup_root(self, parent, root, name, expected):
        opened = os.fstat(root)
        current = os.stat(name, dir_fd=parent, follow_symlinks=False)
        for info in (opened, current):
            self._require_cleanup_root(info, name)
        if any(_anchor_identity(info) != expected for info in (opened, current)):
            raise PreservationError("temporary staging root changed")

    @staticmethod
    def _require_cleanup_root(info, name):
        if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid()
                or stat.S_IMODE(info.st_mode) != 0o700):
            raise PreservationError(
                "temporary staging root is not an exact owner-only directory: %s"
                % display(name)
            )

    def _cleanup_staging(self, parent, root, staging, expected, marker_durable, primary):
        cleanup_root, opened_here = root, False
        try:
            if marker_durable:
                self._retry_cleanup(lambda: os.fsync(parent), "staging parent sync")
                return
            def inspect_staging():
                try:
                    return os.stat(staging, dir_fd=parent, follow_symlinks=False)
                except FileNotFoundError:
                    return None

            current = self._retry_cleanup(
                inspect_staging,
                "staging entry stat",
            )
            if current is None:
                self._retry_cleanup(lambda: os.fsync(parent), "removed staging parent sync")
                return
            self._require_owner_only(current, staging, directory=True)
            if expected is not None and _anchor_identity(current) != expected:
                raise PreservationError("preservation bundle staging root changed during cleanup")
            if cleanup_root is not None:
                opened = self._retry_cleanup(
                    lambda: os.fstat(cleanup_root), "staging descriptor stat"
                )
                self._require_owner_only(opened, staging, directory=True)
                if _anchor_identity(opened) != _anchor_identity(current):
                    raise PreservationError("preservation bundle staging root changed during cleanup")
            flags = os.O_RDONLY | os.O_CLOEXEC | os.O_DIRECTORY | getattr(os, "O_NOFOLLOW", 0)
            removed = False
            if cleanup_root is None:
                for _ in range(CLEANUP_RETRIES):
                    try:
                        os.rmdir(staging, dir_fd=parent)
                    except FileNotFoundError:
                        removed = True
                        break
                    except OSError:
                        pass
                    else:
                        removed = True
                        break
                    try:
                        cleanup_root = os.open(staging, flags, dir_fd=parent)
                    except OSError:
                        rechecked = self._retry_cleanup(
                            lambda: os.stat(
                                staging, dir_fd=parent, follow_symlinks=False
                            ),
                            "staging entry restat",
                        )
                        self._require_owner_only(rechecked, staging, directory=True)
                        if _anchor_identity(rechecked) != _anchor_identity(current):
                            raise PreservationError(
                                "preservation bundle staging root changed during cleanup"
                            )
                        current = rechecked
                        continue
                    opened_here = True
                    opened = self._retry_cleanup(
                        lambda: os.fstat(cleanup_root), "fallback descriptor stat"
                    )
                    current = self._retry_cleanup(
                        lambda: os.stat(
                            staging, dir_fd=parent, follow_symlinks=False
                        ),
                        "fallback entry stat",
                    )
                    for info in (opened, current):
                        self._require_owner_only(info, staging, directory=True)
                    if _anchor_identity(opened) != _anchor_identity(current):
                        raise PreservationError(
                            "preservation bundle staging root changed during cleanup"
                        )
                    break
                else:
                    raise PreservationError(
                        "cannot remove or open preservation bundle staging root"
                    )
            else:
                try:
                    os.rmdir(staging, dir_fd=parent)
                except FileNotFoundError:
                    removed = True
                except OSError:
                    pass
                else:
                    removed = True
            if removed:
                self._retry_cleanup(lambda: os.fsync(parent), "removed staging parent sync")
                return
            self._retry_cleanup(
                lambda: self._sync_cleanup_marker(cleanup_root),
                "INCOMPLETE marker durability",
            )
            self._retry_cleanup(lambda: os.fsync(cleanup_root), "staging root sync")
            self._retry_cleanup(lambda: os.fsync(parent), "retained staging parent sync")
        except BaseException as error:
            if error is primary:
                raise
            failure = PreservationError(
                "cannot make preservation bundle staging cleanup durable: %s" % error
            )
            failure.__cause__ = error
            if primary is None:
                raise failure
            _chain_failures(primary, [failure])
        finally:
            if opened_here:
                _close_descriptors(primary, [cleanup_root])

    @staticmethod
    def _retry_cleanup(operation, label):
        failure = None
        for _ in range(CLEANUP_RETRIES):
            try:
                return operation()
            except (OSError, PreservationError) as error:
                failure = error
        raise PreservationError("cannot complete %s: %s" % (label, failure)) from failure

    def _sync_cleanup_marker(self, root):
        self._publish_marker(root, b"INCOMPLETE", INCOMPLETE_MARKER)

    @contextlib.contextmanager
    def _root(self, exclusive):
        parent, root = self._open_root_descriptors()
        primary = None
        try:
            fcntl.flock(root, fcntl.LOCK_EX if exclusive else fcntl.LOCK_SH)
            self._revalidate_open_anchor(parent, root)
            try:
                yield parent, root
            except BaseException as body_error:
                try:
                    self._revalidate_open_anchor(parent, root)
                except BaseException as validation_error:
                    _chain_failures(body_error, [validation_error])
                raise
            else:
                self._revalidate_open_anchor(parent, root)
        except BaseException as error:
            primary = error
            raise
        finally:
            _close_all(primary, root, parent)

    @contextlib.contextmanager
    def _directory(self, root, parts, create):
        descriptors = [os.dup(root)]
        links = []
        flags = os.O_RDONLY | os.O_CLOEXEC | os.O_DIRECTORY | getattr(os, "O_NOFOLLOW", 0)
        primary = None
        try:
            for part in parts:
                current = descriptors[-1]
                created = False
                if create:
                    previous_umask = os.umask(0o077)
                    try:
                        os.mkdir(part, 0o700, dir_fd=current)
                    except FileExistsError:
                        pass
                    else:
                        created = True
                    finally:
                        os.umask(previous_umask)
                try:
                    following = os.open(part, flags, dir_fd=current)
                    descriptors.append(following)
                    if created:
                        os.fchmod(following, 0o700)
                    opened = os.fstat(following)
                    self._require_owner_only(opened, part, directory=True)
                except (OSError, ValueError) as error:
                    raise PreservationError("cannot open artifact directory %s: %s" % (display(part), error)) from error
                links.append((current, part, _namespace_identity(opened)))
            yield descriptors[-1]
            for parent, part, expected in links:
                current = os.stat(part, dir_fd=parent, follow_symlinks=False)
                self._require_owner_only(current, part, directory=True)
                if _namespace_identity(current) != expected:
                    raise PreservationError("artifact directory changed during operation: %s" % display(part))
        except BaseException as error:
            primary = error
            raise
        finally:
            _close_all(primary, *reversed(descriptors))

    @staticmethod
    def _require_marker_metadata(info, name, links, modes=(0o600,)):
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
                or info.st_nlink != links or stat.S_IMODE(info.st_mode) not in modes):
            raise PreservationError("lifecycle marker state is not canonical: %s" % display(name))
    def _marker_info(self, directory, name, links, modes=(0o600,)):
        try:
            info = os.stat(name, dir_fd=directory, follow_symlinks=False)
        except OSError as error:
            raise PreservationError("cannot inspect lifecycle marker %s: %s" % (display(name), error)) from error
        self._require_marker_metadata(info, name, links, modes)
        return info
    def _open_exact_marker(self, directory, name, expected, links):
        before = self._marker_info(directory, name, links)
        if before.st_size != len(expected):
            raise PreservationError("lifecycle marker size is not canonical: %s" % display(name))
        flags = os.O_RDONLY | os.O_CLOEXEC | os.O_NONBLOCK | getattr(os, "O_NOFOLLOW", 0)
        descriptor = None
        try:
            descriptor = os.open(name, flags, dir_fd=directory)
            opened = os.fstat(descriptor)
            self._require_marker_metadata(opened, name, links)
            if _identity(before) != _identity(opened):
                raise PreservationError("lifecycle marker changed while opening: %s" % display(name))
            data = os.read(descriptor, len(expected) + 1)
            after = os.fstat(descriptor)
            current = self._marker_info(directory, name, links)
            if any(_identity(info) != _identity(before) for info in (after, current)):
                raise PreservationError("lifecycle marker changed while reading: %s" % display(name))
            if data != expected:
                raise PreservationError("lifecycle marker bytes are not canonical: %s" % display(name))
            return descriptor, before
        except BaseException as error:
            _close_all(error, descriptor)
            raise
    def _verify_exact_marker(self, directory, name, expected, links):
        descriptor = primary = None
        try:
            descriptor, info = self._open_exact_marker(directory, name, expected, links)
            return info
        except BaseException as error:
            primary = error
            raise
        finally:
            _close_all(primary, descriptor)
    def _prepare_marker_temporary(self, directory, temporary, expected):
        if self._entry_exists(directory, temporary):
            before = self._marker_info(directory, temporary, 1, (0, 0o600))
            if stat.S_IMODE(before.st_mode) == 0:
                if before.st_size != 0:
                    raise PreservationError("zero-mode marker temporary is not empty")
                current = self._marker_info(directory, temporary, 1, (0,))
                if _identity(before) != _identity(current):
                    raise PreservationError("marker temporary changed before removal")
                os.unlink(temporary, dir_fd=directory)
                os.fsync(directory)

        descriptor = None
        try:
            if self._entry_exists(directory, temporary):
                descriptor, before = self._open_entry(directory, temporary, writable=True)
                if (stat.S_IMODE(before.st_mode) != 0o600
                        or before.st_size > len(expected)):
                    raise PreservationError("marker temporary metadata is not canonical")
                handle = os.fdopen(descriptor, "r+b", buffering=0, closefd=False)
                current_data = handle.read(len(expected) + 1)
                if (len(current_data) != before.st_size
                        or not expected.startswith(current_data)):
                    raise PreservationError("marker temporary bytes are not canonical")
            else:
                flags = os.O_RDWR | os.O_CLOEXEC | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
                descriptor = os.open(temporary, flags, 0, dir_fd=directory)
                os.fchmod(descriptor, 0o600)
                handle = os.fdopen(descriptor, "r+b", buffering=0, closefd=False)
            with handle:
                handle.seek(0)
                handle.truncate(0)
                remaining = memoryview(expected)
                while remaining:
                    written = handle.write(remaining)
                    if written is None or written <= 0:
                        raise PreservationError("cannot write lifecycle marker temporary")
                    remaining = remaining[written:]
                self._sync_file(handle)
                handle.seek(0)
                if handle.read(len(expected) + 1) != expected:
                    raise PreservationError("lifecycle marker temporary write was not stable")
            opened = os.fstat(descriptor)
            self._require_marker_metadata(opened, temporary, 1)
            current = self._marker_info(directory, temporary, 1)
            if _identity(opened) != _identity(current):
                raise PreservationError("lifecycle marker temporary changed after write")
            return descriptor, opened
        except BaseException as error:
            _close_all(error, descriptor)
            raise

    def _finish_marker_publication(
        self, directory, temporary, name, expected, descriptor, identity,
    ):
        opened = os.fstat(descriptor)
        self._require_marker_metadata(opened, temporary, 2)
        if ((opened.st_dev, opened.st_ino) != identity
                or os.pread(descriptor, len(expected) + 1, 0) != expected):
            raise PreservationError("pinned lifecycle marker changed during publication")
        temporary_info = self._marker_info(directory, temporary, 2)
        marker_info = self._marker_info(directory, name, 2)
        if any((info.st_dev, info.st_ino) != identity for info in (temporary_info, marker_info)):
            raise PreservationError("lifecycle marker links do not share an inode")
        os.fsync(descriptor)
        os.fsync(directory)
        opened = os.fstat(descriptor)
        self._require_marker_metadata(opened, temporary, 2)
        temporary_info = self._verify_exact_marker(directory, temporary, expected, 2)
        marker_info = self._verify_exact_marker(directory, name, expected, 2)
        if any((info.st_dev, info.st_ino) != identity for info in (temporary_info, marker_info)):
            raise PreservationError("lifecycle marker links changed before cleanup")
        if (opened.st_dev, opened.st_ino) != identity:
            raise PreservationError("pinned lifecycle marker changed before cleanup")
        os.unlink(temporary, dir_fd=directory)
        os.fsync(directory)
        if self._entry_exists(directory, temporary):
            raise PreservationError("lifecycle marker temporary reappeared after cleanup")
        marker_info = self._verify_exact_marker(directory, name, expected, 1)
        opened = os.fstat(descriptor)
        self._require_marker_metadata(opened, name, 1)
        if any((info.st_dev, info.st_ino) != identity for info in (opened, marker_info)):
            raise PreservationError("lifecycle marker changed after temporary cleanup")

    def _finish_final_marker(
        self, directory, temporary, name, expected, descriptor, identity,
    ):
        os.fsync(descriptor)
        os.fsync(directory)
        opened = os.fstat(descriptor)
        self._require_marker_metadata(opened, name, 1)
        current = self._marker_info(directory, name, 1)
        if (self._entry_exists(directory, temporary)
                or any((info.st_dev, info.st_ino) != identity for info in (opened, current))
                or os.pread(descriptor, len(expected) + 1, 0) != expected):
            raise PreservationError("final lifecycle marker changed while syncing")

    def _publish_marker(self, directory, name, expected):
        temporary = MARKER_TEMPS.get(name)
        if temporary is None or expected != (INCOMPLETE_MARKER if name == b"INCOMPLETE" else COMPLETE_MARKER):
            raise PreservationError("unknown lifecycle marker publication")
        descriptor = primary = None
        try:
            has_temporary = self._entry_exists(directory, temporary)
            has_marker = self._entry_exists(directory, name)
            if has_marker and not has_temporary:
                descriptor, info = self._open_exact_marker(
                    directory, name, expected, 1
                )
                identity = (info.st_dev, info.st_ino)
                self._finish_final_marker(
                    directory, temporary, name, expected, descriptor, identity
                )
                return
            if has_marker:
                descriptor, info = self._open_exact_marker(
                    directory, temporary, expected, 2
                )
            else:
                descriptor, info = self._prepare_marker_temporary(
                    directory, temporary, expected
                )
                try:
                    os.link(
                        temporary, name,
                        src_dir_fd=directory, dst_dir_fd=directory,
                        follow_symlinks=False,
                    )
                except FileExistsError:
                    pass
            identity = (info.st_dev, info.st_ino)
            self._finish_marker_publication(
                directory, temporary, name, expected, descriptor, identity
            )
        except BaseException as error:
            primary = error
            raise
        finally:
            _close_all(primary, descriptor)

    def _write_artifact(self, root, relative, data):
        relative_bytes = validate_relative(os.fsencode(relative), "artifact path")
        parts = relative_bytes.split(b"/")
        with self._directory(root, parts[:-1], create=True) as directory:
            if parts == [parts[-1]] and parts[-1] in MARKER_TEMPS:
                self._publish_marker(directory, parts[-1], data)
                return {"path": os.fsdecode(relative_bytes), "sha256": sha256(data), "size": len(data)}
            descriptor = None
            created = False
            try:
                flags = os.O_WRONLY | os.O_CLOEXEC | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
                descriptor = os.open(parts[-1], flags, 0o600, dir_fd=directory)
                created = True
                os.fchmod(descriptor, 0o600)
                handle = os.fdopen(descriptor, "wb")
                descriptor = None
                with handle:
                    handle.write(data)
                    self._sync_file(handle)
                    written = os.fstat(handle.fileno())
            except BaseException as primary:
                if descriptor is None and not isinstance(primary, Exception):
                    raise
                cleanup_failures = []
                if descriptor is not None:
                    try:
                        os.close(descriptor)
                    except BaseException as cleanup_error:
                        cleanup_failures.append(cleanup_error)
                if created:
                    try:
                        os.unlink(parts[-1], dir_fd=directory)
                    except FileNotFoundError:
                        pass
                    except BaseException as cleanup_error:
                        cleanup_failures.append(cleanup_error)
                if cleanup_failures:
                    _chain_failures(primary, cleanup_failures)
                raise
            current = self._require_entry(directory, parts[-1])
            if _identity(written) != _identity(current):
                raise PreservationError("artifact changed after it was written")
        return {"path": os.fsdecode(relative_bytes), "sha256": sha256(data), "size": len(data)}

    def _create_temporary(self, root):
        flags = os.O_WRONLY | os.O_CLOEXEC | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
        for _ in range(100):
            name = os.fsencode(".object-" + secrets.token_hex(16))
            try:
                return name, os.open(name, flags, 0o600, dir_fd=root)
            except FileExistsError:
                continue
        raise PreservationError("cannot allocate a unique temporary object")

    def _install_object(self, root, temporary, digest, size, written):
        relative = "objects/sha256/%s/%s" % (digest[:2], digest)
        parts = os.fsencode(relative).split(b"/")
        temporary_info = os.stat(temporary, dir_fd=root, follow_symlinks=False)
        if written != _identity(temporary_info):
            raise PreservationError("temporary content-addressed object changed before installation")
        linked = False
        with self._directory(root, parts[:-1], create=True) as directory:
            try:
                os.link(temporary, parts[-1], src_dir_fd=root, dst_dir_fd=directory, follow_symlinks=False)
                linked = True
            except FileExistsError:
                pass
            try:
                os.unlink(temporary, dir_fd=root)
                installed_digest, _ = self._hash_entry(directory, parts[-1], size)
                if installed_digest != digest:
                    raise PreservationError("content-addressed object collision or corruption: %s" % digest)
            except Exception:
                if linked:
                    with contextlib.suppress(FileNotFoundError):
                        os.unlink(parts[-1], dir_fd=directory)
                raise
        return {"path": relative, "sha256": digest, "size": size}

    def _require_active(self, root):
        if self._entry_exists(root, b"COMPLETE"):
            raise PreservationError("preservation bundle is already complete")
        if any(self._entry_exists(root, name) for name in MARKER_TEMPS.values()):
            raise PreservationError("preservation bundle has an unfinished marker publication")
        self._require_marker(root, b"INCOMPLETE", INCOMPLETE_MARKER)

    def _require_marker(self, root, name, expected):
        marker = self._read_entry(root, name, len(expected) + 1)
        if marker != expected:
            raise PreservationError("preservation bundle has an invalid %s marker" % display(name))

    def _entry_exists(self, directory, name):
        try:
            os.stat(name, dir_fd=directory, follow_symlinks=False)
            return True
        except FileNotFoundError:
            return False
        except OSError as error:
            raise PreservationError("cannot inspect preservation artifact %s: %s" % (display(name), error)) from error

    def _read_entry(self, directory, name, limit):
        descriptor, before = self._open_entry(directory, name)
        with os.fdopen(descriptor, "rb", buffering=0) as handle:
            data = handle.read(limit)
            after = os.fstat(handle.fileno())
        current = self._require_entry(directory, name)
        if _identity(before) != _identity(after) or _identity(before) != _identity(current):
            raise PreservationError("preservation artifact changed while reading: %s" % display(name))
        return data

    def _hash_entry(self, directory, name, expected_size=None):
        descriptor, before = self._open_entry(directory, name)
        with os.fdopen(descriptor, "rb", buffering=0) as handle:
            if expected_size is not None and before.st_size != expected_size:
                raise PreservationError("preservation artifact size mismatch: %s" % display(name))
            limit = None if expected_size is None else expected_size + 1
            digest, size = _hash_stream(handle, limit=limit)
            after = os.fstat(handle.fileno())
        current = self._require_entry(directory, name)
        if _identity(before) != _identity(after) or _identity(before) != _identity(current):
            raise PreservationError("preservation artifact changed while hashing: %s" % display(name))
        return digest, size

    def _open_entry(self, directory, name, writable=False):
        try:
            before = os.stat(name, dir_fd=directory, follow_symlinks=False)
            self._require_owner_only(before, name, directory=False)
        except OSError as error:
            raise PreservationError(
                "cannot inspect preservation artifact %s: %s" % (display(name), error)
            ) from error
        access = os.O_RDWR if writable else os.O_RDONLY
        flags = access | os.O_CLOEXEC | os.O_NONBLOCK | getattr(os, "O_NOFOLLOW", 0)
        try:
            descriptor = os.open(name, flags, dir_fd=directory)
        except OSError as error:
            raise PreservationError("cannot open preservation artifact %s: %s" % (display(name), error)) from error
        try:
            info = os.fstat(descriptor)
            self._require_owner_only(info, name, directory=False)
            if _identity(before) != _identity(info):
                raise PreservationError(
                    "preservation artifact changed while it was being opened: %s" % display(name)
                )
            return descriptor, info
        except BaseException as error:
            _close_all(error, descriptor)
            raise

    def _require_entry(self, directory, name, is_directory=False):
        try:
            info = os.stat(name, dir_fd=directory, follow_symlinks=False)
        except OSError as error:
            raise PreservationError("cannot inspect preservation artifact %s: %s" % (display(name), error)) from error
        self._require_owner_only(info, name, directory=is_directory)
        return info

    def _require_owner_only(self, info, path, directory):
        valid_type = stat.S_ISDIR(info.st_mode) if directory else stat.S_ISREG(info.st_mode)
        linked_elsewhere = not directory and info.st_nlink != 1
        if (not valid_type or linked_elsewhere or info.st_uid != os.geteuid()
                or stat.S_IMODE(info.st_mode) & 0o077):
            raise PreservationError("preservation artifact is not owner-only: %s" % display(path))

    def _namespace_digest(self, relative, directory):
        parts = relative.split(b"/")[1:]
        top = parts[0].lower()
        marker = {b"incomplete": b"INCOMPLETE", b"complete": b"COMPLETE"}.get(top)
        if marker is not None:
            if directory or parts != [marker]:
                raise PreservationError("lifecycle marker namespace is not canonical")
            return None
        if top.startswith(b".object-") or _reserved_temporary(top):
            raise PreservationError("preservation bundle contains an incomplete temporary object")
        if top != b"objects":
            return None
        hexadecimal = b"0123456789abcdef"
        if directory:
            valid = parts in ([b"objects"], [b"objects", b"sha256"])
            valid = valid or (len(parts) == 3 and parts[:2] == [b"objects", b"sha256"]
                              and len(parts[2]) == 2 and all(byte in hexadecimal for byte in parts[2]))
            digest = None
        else:
            digest = parts[-1]
            valid = (len(parts) == 4 and parts[:2] == [b"objects", b"sha256"]
                     and len(digest) == 64 and parts[2] == digest[:2]
                     and all(byte in hexadecimal for byte in digest))
        if not valid:
            raise PreservationError("content-addressed object namespace is not canonical")
        return digest

    def _snapshot(self, root, sync, integrity):
        def reject(error):
            raise PreservationError("cannot inspect preservation bundle: %s" % error) from error
        snapshot = {}
        for relative, directories, files, directory in os.fwalk(b".", topdown=False, onerror=reject, follow_symlinks=False, dir_fd=root):
            for name in files:
                descriptor, before = self._open_entry(directory, name, writable=sync)
                with os.fdopen(descriptor, "rb", buffering=0):
                    if sync:
                        os.fsync(descriptor)
                current = self._require_entry(directory, name)
                if _identity(before) != _identity(current):
                    raise PreservationError("artifact changed while inspecting: %s" % display(name))
                path = os.path.join(relative, name)
                digest = self._namespace_digest(path, directory=False)
                if digest is not None and integrity:
                    actual, size = self._hash_entry(directory, name, current.st_size)
                    if actual.encode() != digest or size != current.st_size:
                        raise PreservationError("content-addressed object digest mismatch")
                snapshot[path] = _identity(current)
            for name in directories:
                path = os.path.join(relative, name)
                info = self._require_entry(directory, name, is_directory=True)
                self._namespace_digest(path, directory=True)
                snapshot[path] = _identity(info)
            if sync:
                os.fsync(directory)
        return snapshot

    def _sync_tree(self, root):
        if self._snapshot(root, sync=True, integrity=False) != self._snapshot(root, sync=False, integrity=True):
            raise PreservationError("preservation bundle changed during durable verification")

    @staticmethod
    def _sync_file(handle):
        handle.flush()
        os.fsync(handle.fileno())
