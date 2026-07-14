#!/usr/bin/env python3
"""Direct certification tests for retained Git DIRC bytes."""
import ast
import builtins
import hashlib
import inspect
import os
import pathlib
import shutil
import struct
import subprocess
import sys
import tempfile
import typing
import unittest
from unittest import mock

import upgrade_baseline_git_index as subject


EXPECTED_TEST_COUNT = 13
FOCUSED_METHOD = "test_api_and_pure_runtime_contract"
ALL_TEST_METHODS = (
    FOCUSED_METHOD,
    "test_versions_formats_exact_and_zero_digests",
    "test_v2_v3_name_lengths_and_padding",
    "test_v2_extended_rejected_and_v3_intent_synthetic_accepted",
    "test_real_v3_intent_to_add_is_accepted",
    "test_regular_executable_symlink_modes_are_accepted",
    "test_v4_multi_entry_and_multibyte_removal",
    "test_v4_malformed_varints_and_expanded_lengths",
    "test_checksum_and_version_classification_order",
    "test_structural_corruption_wins_over_policy",
    "test_entry_policy_hazards",
    "test_mandatory_and_optional_extension_policy",
    "test_extension_framing_is_structural",
)
HASH_WIDTHS = {"sha1": 20, "sha256": 32}


class _CountingBytes(bytes):
    def __new__(cls, value, maximum_index_reads):
        instance = super().__new__(cls, value)
        instance.maximum_index_reads = maximum_index_reads
        instance.index_reads = 0
        return instance

    def __getitem__(self, key):
        if isinstance(key, int):
            self.index_reads += 1
            if self.index_reads > self.maximum_index_reads:
                raise AssertionError("varint decoder read past its removal bound")
        return super().__getitem__(key)


def _ofs_delta(value):
    encoded = [value & 0x7f]
    while value >> 7:
        value = (value >> 7) - 1
        encoded.append(0x80 | (value & 0x7f))
    return bytes(reversed(encoded))


def _entry(
    version,
    object_format,
    name,
    *,
    previous=b"",
    mode=0o100644,
    flags=0,
    extended=None,
    declared_length=None,
    padding_byte=0,
    raw_path=None,
    oid_byte=1,
):
    width = HASH_WIDTHS[object_format]
    length = min(len(name), 0xfff) if declared_length is None else declared_length
    entry_flags = (flags & 0xf000) | length
    if extended is not None:
        entry_flags |= 0x4000
    fixed = struct.pack(
        ">10I", 0, 0, 0, 0, 0, 0, mode, 0, 0, len(name)
    )
    fixed += bytes((oid_byte,)) * width + entry_flags.to_bytes(2, "big")
    if extended is not None:
        fixed += extended.to_bytes(2, "big")
    if raw_path is not None:
        return fixed + raw_path
    if version == 4:
        common = 0
        maximum = min(len(previous), len(name))
        while common < maximum and previous[common] == name[common]:
            common += 1
        remove = len(previous) - common
        return fixed + _ofs_delta(remove) + name[common:] + b"\0"
    value = fixed + name + b"\0"
    padding = (-len(value)) % 8
    return value + bytes((padding_byte,)) * padding


def _index(
    version=2,
    object_format="sha1",
    entries=(),
    extensions=(),
    *,
    zero_digest=False,
):
    body = b"DIRC" + version.to_bytes(4, "big")
    body += len(entries).to_bytes(4, "big")
    previous = b""
    for entry in entries:
        values = dict(entry)
        name = values.pop("name")
        body += _entry(
            version,
            object_format,
            name,
            previous=previous,
            **values,
        )
        previous = name
    for extension in extensions:
        signature, payload, *declared = extension
        size = declared[0] if declared else len(payload)
        body += signature + size.to_bytes(4, "big") + payload
    return _checked_body(body, object_format, zero_digest=zero_digest)


def _checked_body(body, object_format="sha1", *, zero_digest=False):
    width = HASH_WIDTHS[object_format]
    digest = b"\0" * width if zero_digest else hashlib.new(
        object_format, body, usedforsecurity=False
    ).digest()
    return body + digest


class GitIndexCertificationTest(unittest.TestCase):
    def assertStructural(self, data, object_format="sha1"):
        with self.assertRaises(subject.IndexFormatError) as raised:
            subject.has_unsupported_state(data, object_format)
        self.assertIs(type(raised.exception), subject.IndexFormatError)

    def test_api_and_pure_runtime_contract(self):
        self.assertTrue(issubclass(subject.IndexFormatError, ValueError))
        self.assertTrue(
            issubclass(subject.UnsupportedIndexError, subject.IndexFormatError)
        )
        signature = inspect.signature(subject.has_unsupported_state)
        self.assertEqual(tuple(signature.parameters), ("data", "object_format"))
        self.assertEqual(
            typing.get_type_hints(subject.has_unsupported_state),
            {"data": bytes, "object_format": str, "return": bool},
        )
        module_source = inspect.getsource(subject)
        parsed = ast.parse(module_source)
        imports = []
        for node in ast.walk(parsed):
            if isinstance(node, ast.Import):
                imports.extend(alias.name for alias in node.names)
            elif isinstance(node, ast.ImportFrom):
                imports.append(node.module)
        imports = tuple(imports)
        self.assertEqual(imports, ("hashlib",))
        self.assertLess(len(module_source.splitlines()), 100)
        for forbidden_name in ("os.", "pathlib", "subprocess", "open("):
            self.assertNotIn(forbidden_name, module_source)
        source = inspect.getsource(subject.has_unsupported_state)
        self.assertIn("previous_length", source)
        self.assertNotIn("data[offset:end]", source)
        self.assertGreaterEqual(
            source.count("_require(remove <= previous_length)"), 2
        )
        valid = _index()
        forbidden = AssertionError("certifier touched filesystem or process state")
        with mock.patch.object(builtins, "open", side_effect=forbidden), \
             mock.patch.object(os, "open", side_effect=forbidden), \
             mock.patch.object(os, "read", side_effect=forbidden), \
             mock.patch.object(os, "stat", side_effect=forbidden), \
             mock.patch.object(subprocess, "Popen", side_effect=forbidden), \
             mock.patch.object(subprocess, "run", side_effect=forbidden):
            self.assertIs(subject.has_unsupported_state(valid, "sha1"), False)
        for invalid in ("sha512", b"sha1", None):
            with self.subTest(object_format=invalid):
                with self.assertRaises(subject.UnsupportedIndexError):
                    subject.has_unsupported_state(valid, invalid)

    def test_versions_formats_exact_and_zero_digests(self):
        for object_format, width in HASH_WIDTHS.items():
            for version in (2, 3, 4):
                with self.subTest(object_format=object_format, version=version):
                    exact = _index(version, object_format)
                    expected = hashlib.new(
                        object_format, exact[:-width], usedforsecurity=False
                    ).digest()
                    self.assertEqual(exact[-width:], expected)
                    self.assertFalse(
                        subject.has_unsupported_state(exact, object_format)
                    )
                    skipped = _index(
                        version, object_format, zero_digest=True
                    )
                    self.assertEqual(skipped[-width:], b"\0" * width)
                    self.assertFalse(
                        subject.has_unsupported_state(skipped, object_format)
                    )

    def test_v2_v3_name_lengths_and_padding(self):
        for version in (2, 3):
            for object_format in HASH_WIDTHS:
                with self.subTest(version=version, object_format=object_format):
                    ordinary = _index(
                        version,
                        object_format,
                        ({"name": b"exact-name"},),
                    )
                    self.assertFalse(
                        subject.has_unsupported_state(ordinary, object_format)
                    )
                    one_nul = _index(
                        version,
                        object_format,
                        ({"name": b"a"},),
                    )
                    self.assertFalse(
                        subject.has_unsupported_state(one_nul, object_format)
                    )
                    mismatch = _index(
                        version,
                        object_format,
                        ({"name": b"exact-name", "declared_length": 9},),
                    )
                    self.assertStructural(mismatch, object_format)
                    nonzero_padding = _index(
                        version,
                        object_format,
                        ({"name": b"ab", "padding_byte": 1},),
                    )
                    self.assertStructural(nonzero_padding, object_format)
                    for length in (0xffe, 0xfff, 0x1000):
                        boundary = _index(
                            version,
                            object_format,
                            ({"name": b"n" * length},),
                        )
                        self.assertFalse(
                            subject.has_unsupported_state(
                                boundary, object_format
                            )
                        )
                    short_sentinel = _index(
                        version,
                        object_format,
                        ({"name": b"short", "declared_length": 0xfff},),
                    )
                    self.assertStructural(short_sentinel, object_format)
                    long_without_sentinel = _index(
                        version,
                        object_format,
                        ({"name": b"n" * 0xfff, "declared_length": 0xffe},),
                    )
                    self.assertStructural(
                        long_without_sentinel, object_format
                    )

    def test_v2_extended_rejected_and_v3_intent_synthetic_accepted(self):
        v2_extended = _index(
            2, "sha1", ({"name": b"v2", "extended": 0},)
        )
        self.assertStructural(v2_extended)
        for object_format in HASH_WIDTHS:
            for version in (3, 4):
                intent = _index(
                    version,
                    object_format,
                    ({"name": b"intent", "extended": 0x2000},),
                )
                self.assertFalse(
                    subject.has_unsupported_state(intent, object_format)
                )
                for reserved in (0x0001, 0x1000, 0x8000):
                    invalid = _index(
                        version,
                        object_format,
                        ({"name": b"reserved", "extended": reserved},),
                    )
                    self.assertStructural(invalid, object_format)

    def test_real_v3_intent_to_add_is_accepted(self):
        git = shutil.which("git")
        self.assertIsNotNone(git)
        git = os.path.realpath(git)
        environment = os.environ.copy()
        for name in tuple(environment):
            if name.startswith("GIT_"):
                environment.pop(name, None)
        environment.update({
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_CONFIG_GLOBAL": "/dev/null",
            "LC_ALL": "C",
        })
        with tempfile.TemporaryDirectory() as temporary:
            repo = pathlib.Path(temporary, "repo")
            commands = (
                (git, "init", "--quiet", "--initial-branch=main", repo),
                (git, "-C", repo, "config", "core.hooksPath", ".git/hooks"),
                (git, "-C", repo, "update-index", "--index-version", "3"),
            )
            for command in commands:
                subprocess.run(
                    tuple(map(os.fspath, command)),
                    env=environment,
                    stdin=subprocess.DEVNULL,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    check=True,
                    timeout=5,
                )
            pathlib.Path(repo, "intent").write_bytes(b"intent\n")
            subprocess.run(
                (git, "-C", repo, "add", "-N", "intent"),
                env=environment,
                stdin=subprocess.DEVNULL,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                check=True,
                timeout=5,
            )
            raw = pathlib.Path(repo, ".git", "index").read_bytes()
        self.assertEqual(raw[:8], b"DIRC\0\0\0\3")
        self.assertEqual(int.from_bytes(raw[8:12], "big"), 1)
        flags_at = 12 + 40 + HASH_WIDTHS["sha1"]
        self.assertTrue(int.from_bytes(raw[flags_at:flags_at + 2], "big") & 0x4000)
        self.assertTrue(int.from_bytes(raw[flags_at + 2:flags_at + 4], "big") & 0x2000)
        self.assertFalse(subject.has_unsupported_state(raw, "sha1"))

    def test_regular_executable_symlink_modes_are_accepted(self):
        for mode in (0o100644, 0o100755, 0o120000):
            for object_format in HASH_WIDTHS:
                with self.subTest(mode=oct(mode), object_format=object_format):
                    data = _index(
                        3,
                        object_format,
                        ({"name": b"entry", "mode": mode},),
                    )
                    self.assertFalse(
                        subject.has_unsupported_state(data, object_format)
                    )
        for mode in (
            0, 0o040755, 0o100600, 0o100664, 0o120777, 0o140000, 0o160755
        ):
            with self.subTest(invalid_mode=oct(mode)):
                invalid = _index(
                    2, "sha1", ({"name": b"entry", "mode": mode},)
                )
                self.assertStructural(invalid)
        high_mode = _index(
            2,
            "sha1",
            ({"name": b"entry", "mode": 0x80000000 | 0o100644},),
        )
        self.assertStructural(high_mode)
        for version in (2, 4):
            invalid_utf8 = _index(
                version, "sha1", ({"name": b"invalid-\xff-name"},)
            )
            self.assertFalse(
                subject.has_unsupported_state(invalid_utf8, "sha1")
            )

    def test_v4_multi_entry_and_multibyte_removal(self):
        self.assertEqual(
            tuple(_ofs_delta(value) for value in (0, 127, 128, 129)),
            (b"\0", b"\x7f", b"\x80\0", b"\x80\x01"),
        )
        first = b"a" * 132
        names = (first, b"a" * 130 + b"-next", b"z")
        self.assertGreater(len(_ofs_delta(len(names[1]))), 1)
        for object_format in HASH_WIDTHS:
            entries = tuple(
                {"name": name, "oid_byte": ordinal}
                for ordinal, name in enumerate(names, 1)
            )
            data = _index(4, object_format, entries)
            self.assertFalse(
                subject.has_unsupported_state(data, object_format)
            )
            for length in (0xffe, 0xfff, 0x1000):
                boundary = _index(
                    4,
                    object_format,
                    (
                        {"name": b"p" * length},
                        {"name": b"p" * (length - 1) + b"q"},
                    ),
                )
                self.assertFalse(
                    subject.has_unsupported_state(boundary, object_format)
                )
            append_then_empty_suffix = _index(
                4,
                object_format,
                (
                    {"name": b"prefix"},
                    {"name": b"prefix-more"},
                    {"name": b"prefix"},
                ),
            )
            self.assertFalse(
                subject.has_unsupported_state(
                    append_then_empty_suffix, object_format
                )
            )
            three_byte_name = b"x" * 16512
            self.assertEqual(len(_ofs_delta(len(three_byte_name))), 3)
            three_byte_removal = _index(
                4,
                object_format,
                ({"name": three_byte_name}, {"name": b"z"}),
            )
            self.assertFalse(
                subject.has_unsupported_state(
                    three_byte_removal, object_format
                )
            )

    def test_v4_malformed_varints_and_expanded_lengths(self):
        truncated = _index(
            4,
            "sha1",
            ({"name": b"x", "raw_path": b"\x80"},),
        )
        self.assertStructural(truncated)
        long_continuation = _CountingBytes(
            _index(
                4,
                "sha1",
                ({"name": b"x", "raw_path": b"\x80" * 4096 + b"\0"},),
            ),
            2,
        )
        self.assertStructural(long_continuation)
        self.assertEqual(long_continuation.index_reads, 2)
        first_over_removal = _index(
            4,
            "sha1",
            ({"name": b"x", "raw_path": _ofs_delta(1) + b"x\0"},),
        )
        self.assertStructural(first_over_removal)
        over_removal = _index(
            4,
            "sha1",
            (
                {"name": b"a"},
                {
                    "name": b"x",
                    "raw_path": _ofs_delta(2) + b"x\0",
                },
            ),
        )
        self.assertStructural(over_removal)
        wrong_expanded_length = _index(
            4,
            "sha1",
            (
                {"name": b"prefix-one"},
                {"name": b"prefix-two", "declared_length": 3},
            ),
        )
        self.assertStructural(wrong_expanded_length)
        short_sentinel = _index(
            4,
            "sha1",
            ({"name": b"short", "declared_length": 0xfff},),
        )
        self.assertStructural(short_sentinel)
        long_without_sentinel = _index(
            4,
            "sha1",
            ({"name": b"n" * 0xfff, "declared_length": 0xffe},),
        )
        self.assertStructural(long_without_sentinel)

    def test_checksum_and_version_classification_order(self):
        for short in (b"", b"DIRC", b"DIRC\0\0\0\2"):
            self.assertStructural(_checked_body(short))
        wrong_header = b"NOPE\0\0\0\2\0\0\0\0"
        self.assertStructural(_checked_body(wrong_header))
        header = b"DIRC\0\0\0\2\0\0\0\1"
        for partial in (b"\0" * 39, b"\0" * 59, b"\0" * 61):
            self.assertStructural(_checked_body(header + partial))
        v3_header = b"DIRC\0\0\0\3\0\0\0\1"
        extended = _entry(
            3, "sha1", b"x", extended=0, raw_path=b"x\0"
        )
        self.assertStructural(_checked_body(v3_header + extended[:63]))
        for object_format in HASH_WIDTHS:
            valid = _index(2, object_format)
            corrupt = valid[:-1] + bytes((valid[-1] ^ 1,))
            self.assertStructural(corrupt, object_format)
        unsupported_version = _index(5, "sha1")
        with self.assertRaises(subject.UnsupportedIndexError):
            subject.has_unsupported_state(unsupported_version, "sha1")
        corrupt_version = unsupported_version[:-1] + bytes(
            (unsupported_version[-1] ^ 1,)
        )
        self.assertStructural(corrupt_version)

    def test_structural_corruption_wins_over_policy(self):
        mandatory = _index(
            2,
            "sha1",
            extensions=((b"link", b""),),
        )
        corrupt = mandatory[:-1] + bytes((mandatory[-1] ^ 1,))
        self.assertStructural(corrupt)
        later_malformed = _index(
            3,
            "sha1",
            ({"name": b"hazard", "flags": 0x8000},),
            ((b"ABCD", b"x", 2),),
        )
        self.assertStructural(later_malformed)
        mandatory_then_malformed = _index(
            2,
            "sha1",
            extensions=((b"link", b"opaque"), (b"ABCD", b"x", 2)),
        )
        self.assertStructural(mandatory_then_malformed)
        zero_hash_malformed = _index(
            2,
            "sha1",
            extensions=((b"ABCD", b"x", 2),),
            zero_digest=True,
        )
        self.assertStructural(zero_hash_malformed)

    def test_entry_policy_hazards(self):
        cases = (
            ("assume-valid", {"flags": 0x8000}),
            ("stage-1", {"flags": 0x1000}),
            ("stage-2", {"flags": 0x2000}),
            ("stage-3", {"flags": 0x3000}),
            ("sparse-directory", {"mode": 0o040000}),
            ("gitlink", {"mode": 0o160000}),
        )
        for object_format in HASH_WIDTHS:
            for label, values in cases:
                with self.subTest(object_format=object_format, hazard=label):
                    version = 3 if "extended" in values else 2
                    data = _index(
                        version,
                        object_format,
                        ({"name": b"hazard", **values},),
                    )
                    self.assertTrue(
                        subject.has_unsupported_state(data, object_format)
                    )
            for version in (3, 4):
                skip = _index(
                    version,
                    object_format,
                    ({"name": b"skip", "extended": 0x4000},),
                )
                self.assertTrue(
                    subject.has_unsupported_state(skip, object_format)
                )

    def test_mandatory_and_optional_extension_policy(self):
        for object_format in HASH_WIDTHS:
            for signature in (
                b"link", b"sdir", b"abcd", b"1abc", b"!abc", b"@abc",
                b"[abc", b"\xffabc"
            ):
                with self.subTest(
                    object_format=object_format, signature=signature
                ):
                    data = _index(
                        2,
                        object_format,
                        extensions=((signature, b""),),
                    )
                    self.assertTrue(
                        subject.has_unsupported_state(data, object_format)
                    )
            opaque = _index(
                2,
                object_format,
                ({"name": b"filename-link-sdir"},),
                ((b"ABCD", b"link\0sdir"),),
            )
            self.assertFalse(
                subject.has_unsupported_state(opaque, object_format)
            )
            for signature in (b"Abcd", b"Zabc"):
                optional = _index(
                    2,
                    object_format,
                    extensions=((signature, b"link\0sdir"),),
                )
                self.assertFalse(
                    subject.has_unsupported_state(optional, object_format)
                )
            for version in (2, 4):
                empty_split = _index(
                    version,
                    object_format,
                    ({"name": b""},),
                    ((b"link", b"opaque split payload"),),
                )
                self.assertTrue(
                    subject.has_unsupported_state(
                        empty_split, object_format
                    )
                )
                empty_without_link = _index(
                    version, object_format, ({"name": b""},)
                )
                self.assertStructural(empty_without_link, object_format)
                empty_with_sdir = _index(
                    version,
                    object_format,
                    ({"name": b""},),
                    ((b"sdir", b""),),
                )
                self.assertStructural(empty_with_sdir, object_format)

    def test_extension_framing_is_structural(self):
        oversize = _index(
            2,
            "sha1",
            extensions=((b"ABCD", b"x", 2),),
        )
        self.assertStructural(oversize)
        padded = _index(
            2, "sha1", ({"name": b"ab"},)
        )
        self.assertEqual(padded[-21], 0)
        self.assertStructural(_checked_body(padded[:-21]))
        valid = _index(2, "sha1")
        width = HASH_WIDTHS["sha1"]
        body = valid[:-width]
        for trailing in (b"missing-nul", *(
            b"ABCDEFG"[:length] for length in range(1, 8)
        )):
            if trailing == b"missing-nul":
                missing_nul = _index(
                    2,
                    "sha1",
                    ({"name": b"name", "raw_path": b"name"},),
                )
                self.assertStructural(missing_nul)
            else:
                self.assertStructural(_checked_body(body + trailing))


def _selected_suite(stage):
    runtime = tuple(
        name
        for name, value in GitIndexCertificationTest.__dict__.items()
        if name.startswith("test_") and callable(value)
    )
    if (
        EXPECTED_TEST_COUNT != len(ALL_TEST_METHODS)
        or EXPECTED_TEST_COUNT != len(set(ALL_TEST_METHODS))
        or runtime != ALL_TEST_METHODS
    ):
        print(
            "GIT_INDEX_TEST_INVENTORY_MISMATCH listed=%r runtime=%r"
            % (ALL_TEST_METHODS, runtime),
            file=sys.stderr,
        )
        raise SystemExit(2)
    selected = ALL_TEST_METHODS if stage == "all" else (FOCUSED_METHOD,)
    if stage not in ("all", "focused"):
        print("unknown Git index test stage: %s" % stage, file=sys.stderr)
        raise SystemExit(2)
    return unittest.TestSuite(
        GitIndexCertificationTest(name) for name in selected
    ), len(selected)


if __name__ == "__main__":
    if len(sys.argv) == 1:
        stage = "all"
    elif len(sys.argv) == 3 and sys.argv[1] == "--stage":
        stage = sys.argv[2]
    else:
        print("usage: git_index_test.py [--stage all|focused]", file=sys.stderr)
        raise SystemExit(2)
    suite, expected = _selected_suite(stage)
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    print(
        "GIT_INDEX_TESTS_EXECUTED stage=%s count=%d"
        % (stage, result.testsRun)
    )
    if (
        result.wasSuccessful()
        and not result.skipped
        and not result.expectedFailures
        and not result.unexpectedSuccesses
        and result.testsRun == expected
    ):
        print(
            "GIT_INDEX_TESTS_PASSED stage=%s count=%d"
            % (stage, result.testsRun)
        )
        raise SystemExit(0)
    raise SystemExit(1)
