#!/usr/bin/env python3
"""Pure, bounded certification of retained Git index bytes."""
import hashlib


class IndexFormatError(ValueError):
    """The retained bytes are not a structurally valid Git index."""


class UnsupportedIndexError(IndexFormatError):
    """The retained bytes use an unsupported index-level format."""


def _require(condition):
    if not condition:
        raise IndexFormatError()


def _name_length_is_exact(encoded, actual):
    return encoded == (0xfff if actual >= 0xfff else actual)


def has_unsupported_state(data: bytes, object_format: str) -> bool:
    """Validate a retained v2-v4 index and report unsupported semantics."""
    try:
        hash_bytes = {"sha1": 20, "sha256": 32}[object_format]
    except (KeyError, TypeError) as cause:
        raise UnsupportedIndexError() from cause
    _require(isinstance(data, bytes))
    trailer = len(data) - hash_bytes
    _require(trailer >= 12 and data[:4] == b"DIRC")
    expected = hashlib.new(
        object_format, data[:trailer], usedforsecurity=False
    ).digest()
    _require(data[trailer:] in (expected, b"\0" * hash_bytes))
    version = int.from_bytes(data[4:8], "big")
    if version not in (2, 3, 4):
        raise UnsupportedIndexError()

    offset, previous_length, unsupported = 12, 0, False
    empty_entry = link_extension = False
    for _entry in range(int.from_bytes(data[8:12], "big")):
        start = offset
        flags_at = start + 40 + hash_bytes
        _require(flags_at + 2 <= trailer)
        mode = int.from_bytes(data[start + 24:start + 28], "big")
        if mode in (0o040000, 0o160000):
            unsupported = True
        else:
            _require(mode in (0o100644, 0o100755, 0o120000))
        flags = int.from_bytes(data[flags_at:flags_at + 2], "big")
        unsupported |= bool(flags & (0x8000 | 0x3000))
        offset = flags_at + 2
        if flags & 0x4000:
            _require(version >= 3 and offset + 2 <= trailer)
            extended = int.from_bytes(data[offset:offset + 2], "big")
            _require(not (extended & ~0x6000))
            unsupported |= bool(extended & 0x4000)
            offset += 2

        if version == 4:
            _require(offset < trailer)
            byte = data[offset]
            offset += 1
            remove = byte & 0x7f
            _require(remove <= previous_length)
            while byte & 0x80:
                _require(offset < trailer)
                byte = data[offset]
                offset += 1
                remove = ((remove + 1) << 7) | (byte & 0x7f)
                _require(remove <= previous_length)
            end = data.find(b"\0", offset, trailer)
            _require(end >= 0)
            name_length = previous_length - remove + end - offset
            offset = end + 1
        else:
            end = data.find(b"\0", offset, trailer)
            _require(end >= 0)
            name_length = end - offset
            offset = start + ((end + 8 - start) & ~7)
            _require(offset <= trailer and not any(data[end + 1:offset]))
        _require(_name_length_is_exact(flags & 0xfff, name_length))
        empty_entry |= name_length == 0
        previous_length = name_length

    while offset < trailer:
        _require(offset + 8 <= trailer)
        signature = data[offset:offset + 4]
        size = int.from_bytes(data[offset + 4:offset + 8], "big")
        offset += 8
        _require(size <= trailer - offset)
        link_extension |= signature == b"link"
        unsupported |= not 65 <= signature[0] <= 90
        offset += size
    # Empty replacement names are valid only when `link` certifies split index.
    _require(offset == trailer and (not empty_entry or link_extension))
    return unsupported
