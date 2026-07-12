#!/usr/bin/env python3
"""Live repository-bound quiescence leases for baseline preservation."""

import base64
import errno
import fcntl
import hashlib
import json
import os
import stat
import threading
import time

SCHEMA = "beads.upgrade.quiescence/v1"
AUTHORITY_KIND = "operator-supervisor-v1"
MAX_RECEIPT_BYTES = 64 * 1024
MAX_REGISTRY_BYTES = 16 * 1024 * 1024
MAX_LEASE_NS = 24 * 60 * 60 * 1_000_000_000
MAX_HANDOFF_NS = 5 * 1_000_000_000
MIN_STABILITY_NS = 1_000_000_000
HEX = frozenset("0123456789abcdef")

class QuiescenceError(RuntimeError):
    """A repository quiescence lease could not be certified safely."""

def _identity(info):
    return (
        info.st_dev,
        info.st_ino,
        info.st_mode,
        info.st_nlink,
        info.st_uid,
        info.st_size,
        info.st_mtime_ns,
        info.st_ctime_ns,
    )

def _namespace(info):
    return info.st_dev, info.st_ino, stat.S_IFMT(info.st_mode)

def _strict_int(value, label, minimum=0):
    if type(value) is not int or value < minimum:
        raise QuiescenceError("%s must be an integer >= %d" % (label, minimum))
    return value

def _digest(value, label, length=64):
    if not isinstance(value, str) or len(value) != length or any(c not in HEX for c in value):
        raise QuiescenceError("%s must be %d lowercase hexadecimal characters" % (label, length))
    return value

def _mapping(value, keys, label):
    if not isinstance(value, dict) or set(value) != set(keys):
        raise QuiescenceError("%s has missing or unknown fields" % label)
    return value

def _decode_path(value, label):
    if not isinstance(value, str):
        raise QuiescenceError("%s must be canonical base64" % label)
    try:
        decoded = base64.b64decode(value, validate=True)
    except (ValueError, TypeError) as error:
        raise QuiescenceError("%s must be canonical base64" % label) from error
    if (
        base64.b64encode(decoded).decode("ascii") != value
        or not decoded
        or b"\0" in decoded
    ):
        raise QuiescenceError("%s must be canonical base64" % label)
    if not os.path.isabs(decoded):
        raise QuiescenceError("%s must encode an absolute path" % label)
    if os.path.normpath(b"/" + decoded.lstrip(b"/")) != decoded:
        raise QuiescenceError("%s must encode a canonical path" % label)
    return decoded

def _unique_object(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise QuiescenceError("quiescence receipt contains a duplicate JSON key: %s" % key)
        value[key] = item
    return value

def _reject_constant(value):
    raise QuiescenceError("quiescence receipt contains a non-finite number: %s" % value)

def _parse(data):
    try:
        value = json.loads(
            data.decode("ascii"),
            object_pairs_hook=_unique_object,
            parse_constant=_reject_constant,
        )
    except (UnicodeDecodeError, json.JSONDecodeError, RecursionError) as error:
        raise QuiescenceError("invalid quiescence receipt: %s" % error) from error
    canonical = (json.dumps(value, ensure_ascii=True, separators=(",", ":"), sort_keys=True) + "\n").encode("ascii")
    if data != canonical:
        raise QuiescenceError("quiescence receipt is not canonical JSON")
    return value


def _require_owner_file(info, label):
    if (
        not stat.S_ISREG(info.st_mode)
        or info.st_uid != os.geteuid()
        or stat.S_IMODE(info.st_mode) != 0o600
        or info.st_nlink != 1
    ):
        raise QuiescenceError("%s must be an owner-only, single-link regular file" % label)


def _inside(candidate, root):
    try:
        return os.path.commonpath((candidate, root)) == root
    except ValueError:
        return False


def _require_external(path, roots):
    absolute = os.path.abspath(path)
    resolved = os.path.realpath(absolute)
    protected_identities = {identity for _, _, identity in roots}
    for root_absolute, root_resolved, _ in roots:
        if any(
            _inside(candidate, protected)
            for candidate in (absolute, resolved)
            for protected in (root_absolute, root_resolved)
        ):
            raise QuiescenceError("quiescence receipt must be outside every protected repository path")
    ancestor = os.path.dirname(absolute)
    while True:
        try:
            if _namespace(os.stat(ancestor)) in protected_identities:
                raise QuiescenceError(
                    "quiescence receipt must be outside every protected repository path"
                )
        except FileNotFoundError:
            pass
        except QuiescenceError:
            raise
        except OSError as error:
            raise QuiescenceError("cannot inspect quiescence receipt ancestry: %s" % error) from error
        parent = os.path.dirname(ancestor)
        if parent == ancestor:
            break
        ancestor = parent


def _prepare_roots(roots, common):
    if isinstance(roots, (str, bytes)):
        raise QuiescenceError("protected roots must be a non-empty path collection")
    roots = list(roots)
    if not roots:
        raise QuiescenceError("protected roots must not be empty")
    prepared = []
    for root in roots + [common]:
        absolute = os.path.abspath(os.fsencode(root))
        resolved = os.path.realpath(absolute)
        info = os.stat(resolved, follow_symlinks=False)
        if not stat.S_ISDIR(info.st_mode):
            raise QuiescenceError("protected repository root is unavailable")
        prepared.append((absolute, resolved, _namespace(info)))
    return tuple(prepared)


def _revalidate_roots(prepared):
    for absolute, resolved, identity in prepared:
        if os.path.realpath(absolute) != resolved or _namespace(os.stat(resolved, follow_symlinks=False)) != identity:
            raise QuiescenceError("protected repository root identity changed")
    return prepared


def _control_parent(path, expected=None):
    parent = os.path.dirname(path)
    info = os.lstat(parent)
    valid = stat.S_ISDIR(info.st_mode) and info.st_uid == os.geteuid() and not stat.S_IMODE(info.st_mode) & 0o077
    if not valid or (expected is not None and _namespace(info) != expected):
        raise QuiescenceError("quiescence receipt parent must remain owner-only")
    return _namespace(info)


def _path_info(path):
    try:
        return os.lstat(path)
    except OSError as error:
        raise QuiescenceError("cannot inspect quiescence receipt: %s" % error) from error


def _require_path_identity(path, expected):
    current = _path_info(path)
    _require_owner_file(current, "quiescence receipt")
    if _identity(current) != expected:
        raise QuiescenceError("quiescence receipt path or identity changed")


def _read_receipt(descriptor, path, expected):
    before = os.fstat(descriptor)
    _require_owner_file(before, "quiescence receipt")
    if _identity(before) != expected or before.st_size > MAX_RECEIPT_BYTES:
        raise QuiescenceError("quiescence receipt changed or exceeds the read limit")
    chunks = []
    offset = 0
    while offset < before.st_size:
        chunk = os.pread(descriptor, min(16 * 1024, before.st_size - offset), offset)
        if not chunk:
            raise QuiescenceError("quiescence receipt was truncated while reading")
        chunks.append(chunk)
        offset += len(chunk)
    after = os.fstat(descriptor)
    if _identity(after) != expected:
        raise QuiescenceError("quiescence receipt changed while reading")
    _require_path_identity(path, expected)
    return b"".join(chunks)


def _would_block(error):
    return isinstance(error, OSError) and error.errno in (errno.EACCES, errno.EAGAIN, errno.EWOULDBLOCK)


def _close_fd(descriptor, tombstone):
    interrupted = None
    try:
        identity = _namespace(os.fstat(tombstone))
    except BaseException as error:
        interrupted = error
        identity = _namespace(os.fstat(tombstone))
    try:
        os.dup2(tombstone, descriptor, inheritable=False)
    except BaseException as error:
        interrupted = interrupted or error
        os.dup2(tombstone, descriptor, inheritable=False)
    try:
        os.close(descriptor)
    except BaseException as error:
        interrupted = interrupted or error
        try:
            current = os.fstat(descriptor)
        except OSError as state:
            if state.errno != errno.EBADF:
                raise
        else:
            if _namespace(current) == identity:
                os.close(descriptor)
    return interrupted

def _prove_exclusive_owner(descriptor, path):
    flags = os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW
    try:
        probe = os.open(path, flags)
    except OSError as error:
        raise QuiescenceError("cannot open quiescence receipt for lock verification: %s" % error) from error
    try:
        try:
            fcntl.flock(probe, fcntl.LOCK_SH | fcntl.LOCK_NB)
        except OSError as error:
            if not _would_block(error):
                raise QuiescenceError("cannot verify quiescence lease: %s" % error) from error
        else:
            fcntl.flock(probe, fcntl.LOCK_UN)
            raise QuiescenceError("quiescence receipt has no live exclusive lease")
    finally:
        os.close(probe)
    try:
        fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError as error:
        if _would_block(error):
            raise QuiescenceError("the supplied descriptor does not own the exclusive lease") from error
        raise QuiescenceError("cannot verify the supplied lease descriptor: %s" % error) from error


def _registry_records(raw):
    if not isinstance(raw, bytes) or len(raw) > MAX_REGISTRY_BYTES or not raw.endswith(b"\0\0"):
        raise QuiescenceError("worktree registry must be bounded porcelain-v1 NUL data")
    encoded_records = raw[:-2].split(b"\0\0")
    if not encoded_records or any(not record for record in encoded_records):
        raise QuiescenceError("worktree registry must contain at least one worktree")
    records = []
    paths = set()
    head_width = None
    for index, encoded in enumerate(encoded_records):
        fields = encoded.split(b"\0")
        parsed = {}
        for field_index, field in enumerate(fields):
            key, separator, value = field.partition(b" ")
            if key not in (b"worktree", b"HEAD", b"branch", b"detached", b"bare", b"locked", b"prunable"):
                raise QuiescenceError("worktree registry contains an unknown field")
            if key in parsed or (key == b"worktree") != (field_index == 0):
                raise QuiescenceError("worktree registry contains a duplicate or misplaced field")
            if key in (b"worktree", b"HEAD", b"branch") and (not separator or not value):
                raise QuiescenceError("worktree registry contains a field without a value")
            if key in (b"detached", b"bare") and separator:
                raise QuiescenceError("worktree registry contains a valued flag field")
            if key in (b"locked", b"prunable") and separator and not value:
                raise QuiescenceError("worktree registry contains an empty reason")
            if key == b"prunable" and not separator:
                raise QuiescenceError("worktree registry contains a prunable flag without a reason")
            parsed[key] = value if separator else True
        path = parsed.get(b"worktree")
        if not isinstance(path, bytes) or not os.path.isabs(path):
            raise QuiescenceError("worktree registry contains a malformed path record")
        canonical_path = os.path.normpath(b"/" + path.lstrip(b"/"))
        if canonical_path != path or canonical_path in paths:
            raise QuiescenceError("worktree registry contains a malformed path record")
        paths.add(canonical_path)
        is_bare = parsed.get(b"bare") is True
        if is_bare:
            if index != 0 or set(parsed) != {b"worktree", b"bare"}:
                raise QuiescenceError("worktree registry contains a malformed bare record")
        else:
            head = parsed.get(b"HEAD")
            lineage = int(b"branch" in parsed) + int(parsed.get(b"detached") is True)
            if (
                not isinstance(head, bytes)
                or len(head) not in (40, 64)
                or any(chr(byte) not in HEX for byte in head)
                or lineage != 1
            ):
                raise QuiescenceError("worktree registry contains a malformed checkout record")
            if head_width is None:
                head_width = len(head)
            elif len(head) != head_width:
                raise QuiescenceError("worktree registry mixes object ID widths")
        records.append(parsed)
    return tuple(records)


def _registry_count(raw):
    return len(_registry_records(raw))


def _validate_receipt_semantics(
    data, expected_plan_sha256, expected_git_common_dir, registry_raw,
    live=None, validation_now=None,
):
    value = _parse(data)
    _mapping(
        value,
        ("schema", "lease", "plan", "repository", "registry", "writer_drain", "stability"),
        "quiescence receipt",
    )
    if value["schema"] != SCHEMA:
        raise QuiescenceError("unsupported quiescence receipt schema")
    lease = _mapping(
        value["lease"],
        ("id", "authority", "epoch", "issued_at_ns", "expires_at_ns", "lock"),
        "lease",
    )
    _digest(lease["id"], "lease.id", length=32)
    authority = _mapping(lease["authority"], ("kind", "pid", "uid"), "lease.authority")
    if authority["kind"] != AUTHORITY_KIND:
        raise QuiescenceError("lease authority kind is unsupported")
    authority_pid = _strict_int(authority["pid"], "lease.authority.pid", 1)
    if _strict_int(authority["uid"], "lease.authority.uid") != os.geteuid():
        raise QuiescenceError("lease authority belongs to a different user")
    if live is not None:
        try:
            os.kill(authority_pid, 0)
        except (OSError, ValueError) as error:
            raise QuiescenceError("lease authority process is not live") from error
    _strict_int(lease["epoch"], "lease.epoch", 1)
    issued = _strict_int(lease["issued_at_ns"], "lease.issued_at_ns", 0)
    expires = _strict_int(lease["expires_at_ns"], "lease.expires_at_ns", 1)
    if live is not None:
        path, identity, live_now = live
        validation_now = live_now if validation_now is None else validation_now
    if type(validation_now) is not int:
        raise QuiescenceError("receipt validation time must be an integer nanosecond timestamp")
    if issued > validation_now or expires <= issued or expires - issued > MAX_LEASE_NS:
        raise QuiescenceError("quiescence lease is future-issued, expired, reversed, or too long")
    if live is not None:
        if validation_now - issued > MAX_HANDOFF_NS or expires <= validation_now:
            raise QuiescenceError("quiescence lease is future-issued, expired, reversed, or too long")
    lock = _mapping(lease["lock"], ("path_b64", "dev", "ino"), "lease.lock")
    lock_path = _decode_path(lock["path_b64"], "lease.lock.path_b64")
    lock_identity = (
        _strict_int(lock["dev"], "lease.lock.dev"),
        _strict_int(lock["ino"], "lease.lock.ino"),
    )
    if live is not None and lock_path != path:
        raise QuiescenceError("quiescence lease path does not match the held descriptor")
    if live is not None and lock_identity != identity[:2]:
        raise QuiescenceError("quiescence lease identity does not match the held descriptor")
    plan = _mapping(value["plan"], ("sha256",), "plan")
    if _digest(plan["sha256"], "plan.sha256") != _digest(expected_plan_sha256, "expected plan SHA-256"):
        raise QuiescenceError("quiescence lease is bound to a different plan")
    common = os.path.realpath(os.path.abspath(os.fsencode(expected_git_common_dir)))
    common_info = os.stat(common, follow_symlinks=False)
    if not stat.S_ISDIR(common_info.st_mode):
        raise QuiescenceError("Git common directory is unavailable")
    repository = _mapping(value["repository"], ("git_common_dir_b64", "dev", "ino"), "repository")
    if _decode_path(repository["git_common_dir_b64"], "repository.git_common_dir_b64") != common:
        raise QuiescenceError("quiescence lease is bound to a different Git common directory")
    if _strict_int(repository["dev"], "repository.dev") != common_info.st_dev or _strict_int(repository["ino"], "repository.ino") != common_info.st_ino:
        raise QuiescenceError("Git common directory identity drifted")
    registry_records = _registry_records(registry_raw)
    if registry_records[0].get(b"bare") is True:
        bare_path = os.path.realpath(os.path.abspath(registry_records[0][b"worktree"]))
        try:
            bare_info = os.stat(bare_path, follow_symlinks=False)
        except OSError as error:
            raise QuiescenceError("bare primary worktree is unavailable: %s" % error) from error
        if _namespace(bare_info) != _namespace(common_info):
            raise QuiescenceError("bare primary worktree does not match the Git common directory")
    registry_count = len(registry_records)
    registry_digest = hashlib.sha256(registry_raw).hexdigest()
    registry = _mapping(value["registry"], ("count", "sha256"), "registry")
    if _strict_int(registry["count"], "registry.count") != registry_count:
        raise QuiescenceError("registered worktree count drifted")
    if _digest(registry["sha256"], "registry.sha256") != registry_digest:
        raise QuiescenceError("registered worktree digest drifted")
    drain = _mapping(value["writer_drain"], ("drained", "write_capable_handle_count"), "writer_drain")
    if drain["drained"] is not True or _strict_int(drain["write_capable_handle_count"], "writer drain count") != 0:
        raise QuiescenceError("writer drain is not complete")
    stability = _mapping(value["stability"], ("digest", "samples"), "stability")
    if _digest(stability["digest"], "stability.digest") != registry_digest:
        raise QuiescenceError("stability digest does not match the registry")
    samples = stability["samples"]
    if not isinstance(samples, list) or len(samples) != 2:
        raise QuiescenceError("stability requires exactly two samples")
    previous = None
    for index, sample in enumerate(samples):
        sample = _mapping(sample, ("observed_at_ns", "digest"), "stability sample")
        observed = _strict_int(sample["observed_at_ns"], "sample timestamp", 0)
        if previous is not None and observed <= previous:
            raise QuiescenceError("stability sample timestamps must increase")
        if observed > issued or _digest(sample["digest"], "sample digest") != registry_digest:
            raise QuiescenceError("stability sample is not covered by the lease")
        previous = observed
    if samples[1]["observed_at_ns"] - samples[0]["observed_at_ns"] < MIN_STABILITY_NS or issued - previous > MAX_HANDOFF_NS:
        raise QuiescenceError("stability samples are too close together or stale")
    return value, expires, lock_path


def _validate_receipt(
    data, path, identity, expected_plan_sha256,
    expected_git_common_dir, registry_raw, now,
):
    value, expires, _ = _validate_receipt_semantics(
        data,
        expected_plan_sha256,
        expected_git_common_dir,
        registry_raw,
        live=(path, identity, now),
    )
    return value, expires


def validate_historical_receipt(
    data, expected_plan_sha256, expected_git_common_dir,
    registry_raw, protected_roots, *, now=None,
):
    """Validate stored evidence without requiring its old lease to remain live."""
    if type(data) is not bytes or len(data) > MAX_RECEIPT_BYTES:
        raise QuiescenceError("historical quiescence receipt exceeds the read limit")
    if now is not None and type(now) is not int:
        raise QuiescenceError("now must be an integer nanosecond timestamp")
    current = time.time_ns() if now is None else now
    common = os.path.realpath(os.path.abspath(os.fsencode(expected_git_common_dir)))
    roots = _prepare_roots(protected_roots, common)
    value, _, lock_path = _validate_receipt_semantics(
        data,
        expected_plan_sha256,
        expected_git_common_dir,
        registry_raw,
        validation_now=current,
    )
    _require_external(lock_path, _revalidate_roots(roots))
    return value


class RepositoryLease:
    """An exclusively held local-filesystem lease consumed from its caller."""
    def __init__(
        self, descriptor, path, identity, receipt_bytes, monotonic_clock,
        monotonic_deadline, protected_roots, parent_identity, reserve,
    ):
        self._descriptor = descriptor
        self._path = path
        self._identity = identity
        self._receipt_bytes = receipt_bytes
        self._monotonic_clock = monotonic_clock
        self._monotonic_deadline = monotonic_deadline
        self._protected_roots = protected_roots
        self._parent_identity = parent_identity
        self._reserve = reserve
        self._close_lock = threading.Lock()

    @property
    def receipt_bytes(self):
        return self._receipt_bytes
    @property
    def receipt_sha256(self):
        return hashlib.sha256(self._receipt_bytes).hexdigest()
    def __enter__(self):
        if self._descriptor is None:
            raise QuiescenceError("quiescence lease is already closed")
        if self._monotonic_clock() >= self._monotonic_deadline:
            self.close()
            raise QuiescenceError("quiescence lease expired before capture")
        return self
    def __exit__(self, exc_type, exc_value, traceback):
        try:
            if exc_type is None:
                self.revalidate()
        finally:
            self.close()
        return False
    def close(self):
        with self._close_lock:
            descriptor = self._descriptor
            if descriptor is not None:
                error = _close_fd(descriptor, self._reserve[0])
                self._descriptor = None
                reserve, self._reserve = self._reserve, None
                for value in reserve:
                    os.close(value)
                if error is not None:
                    raise error

    def revalidate(self):
        if self._descriptor is None:
            raise QuiescenceError("quiescence lease is already closed")
        if self._monotonic_clock() >= self._monotonic_deadline:
            raise QuiescenceError("quiescence lease expired during capture")
        _prove_exclusive_owner(self._descriptor, self._path)
        _require_external(self._path, _revalidate_roots(self._protected_roots))
        _control_parent(self._path, self._parent_identity)
        data = _read_receipt(self._descriptor, self._path, self._identity)
        if data != self._receipt_bytes:
            raise QuiescenceError("quiescence receipt bytes changed")
        if self._monotonic_clock() >= self._monotonic_deadline:
            raise QuiescenceError("quiescence lease expired during revalidation")
        return self

    final_revalidate = revalidate


def hold_quiescence(
    receipt_path, descriptor, expected_plan_sha256, expected_git_common_dir,
    registry_raw, protected_roots, *, now=None, monotonic_ns=None,
):
    """Consume a trusted supervisor's local-filesystem cooperative lease.

    The issuer must drain every writer and hand off its sole locked descriptor.
    Portable flock cannot defend against malicious same-UID handoff races.
    """
    if not all(hasattr(os, name) for name in ("O_CLOEXEC", "O_NOFOLLOW", "pread")):
        raise QuiescenceError("quiescence leases require POSIX CLOEXEC, NOFOLLOW, and pread")
    if type(descriptor) is not int or descriptor < 3:
        raise QuiescenceError("quiescence lease descriptor must be an inherited non-stdio fd")
    retained = tombstone = peer = None
    incoming = descriptor
    try:
        tombstone, peer = os.pipe()
        try:
            retained = os.dup(incoming)
        except OSError as error:
            raise QuiescenceError("cannot duplicate quiescence lease descriptor: %s" % error) from error
        error = _close_fd(incoming, tombstone)
        incoming = None
        if error is not None:
            raise error
        os.set_inheritable(retained, False)
        if fcntl.fcntl(retained, fcntl.F_GETFL) & os.O_ACCMODE != os.O_RDWR:
            raise QuiescenceError("quiescence lease descriptor must be open read-write")
        path = os.path.abspath(os.fsencode(receipt_path))
        common = os.path.realpath(os.path.abspath(os.fsencode(expected_git_common_dir)))
        roots = _prepare_roots(protected_roots, common)
        _require_external(path, _revalidate_roots(roots))
        parent_identity = _control_parent(path)
        info = os.fstat(retained)
        _require_owner_file(info, "quiescence receipt")
        identity = _identity(info)
        _require_path_identity(path, identity)
        _prove_exclusive_owner(retained, path)
        data = _read_receipt(retained, path, identity)
        if now is not None and type(now) is not int:
            raise QuiescenceError("now must be an integer nanosecond timestamp")
        wall_clock = (lambda: now) if now is not None else time.time_ns
        monotonic_clock = time.monotonic_ns if monotonic_ns is None else monotonic_ns
        if not callable(monotonic_clock):
            raise QuiescenceError("monotonic_ns must be callable")
        anchor = monotonic_clock()
        current = wall_clock()
        _, expires = _validate_receipt(
            data,
            path,
            identity,
            expected_plan_sha256,
            expected_git_common_dir,
            registry_raw,
            current,
        )
        deadline = anchor + (expires - current)
        if monotonic_clock() >= deadline:
            raise QuiescenceError("quiescence lease expired during acquisition")
        return RepositoryLease(
            retained,
            path,
            identity,
            data,
            monotonic_clock,
            deadline,
            roots,
            parent_identity,
            (tombstone, peer),
        )
    except BaseException:
        cleanup_error = None
        if tombstone is None:
            try:
                fcntl.flock(incoming, fcntl.LOCK_UN)
            finally:
                os.close(incoming)
        else:
            for value in (incoming, retained):
                if value is not None:
                    error = _close_fd(value, tombstone)
                    cleanup_error = cleanup_error or error
            os.close(tombstone)
            os.close(peer)
        if cleanup_error is not None:
            raise cleanup_error
        raise
