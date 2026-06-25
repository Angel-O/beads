package protocol

import (
	"bytes"
	"strings"
	"testing"
)

func TestCanonicalizeNormalizesTimestamps(t *testing.T) {
	in := []byte(`{"id":"x","created_at":"2026-06-25T00:15:41Z","updated_at":"2026-06-25T00:15:41.123456Z","title":"not a 2026-06-25 date"}`)
	out, err := CanonicalizeJSON(in)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "2026-06-25T00:15:41Z") || strings.Contains(s, "2026-06-25T00:15:41.123456Z") {
		t.Fatalf("timestamps not normalized:\n%s", s)
	}
	if !strings.Contains(s, `"<TS>"`) {
		t.Fatalf("expected <TS> placeholder:\n%s", s)
	}
	// A string that merely embeds a date is not a timestamp value and must survive.
	if !strings.Contains(s, "not a 2026-06-25 date") {
		t.Fatalf("non-timestamp string was wrongly rewritten:\n%s", s)
	}
}

func TestCanonicalizeSortsObjectArrays(t *testing.T) {
	// Same logical set, different order -> identical canonical bytes.
	a := []byte(`[{"id":"b"},{"id":"a"},{"id":"c"}]`)
	b := []byte(`[{"id":"c"},{"id":"a"},{"id":"b"}]`)
	ca, err := CanonicalizeJSON(a)
	if err != nil {
		t.Fatalf("canonicalize a: %v", err)
	}
	cb, err := CanonicalizeJSON(b)
	if err != nil {
		t.Fatalf("canonicalize b: %v", err)
	}
	if !bytes.Equal(ca, cb) {
		t.Fatalf("array ordering not normalized:\n%s\nvs\n%s", ca, cb)
	}
}

func TestCanonicalizeIdempotent(t *testing.T) {
	in := []byte(`{"b":2,"a":1,"items":[{"id":"y","t":"2026-06-25T00:15:41Z"},{"id":"x"}]}`)
	once, err := CanonicalizeJSON(in)
	if err != nil {
		t.Fatalf("canonicalize once: %v", err)
	}
	twice, err := CanonicalizeJSON(once)
	if err != nil {
		t.Fatalf("canonicalize twice: %v", err)
	}
	if !bytes.Equal(once, twice) {
		t.Fatalf("canonicalize not idempotent:\n%s\nvs\n%s", once, twice)
	}
}

// TestCorpusDoubleRunByteIdentical generates the corpus twice, in two fresh
// stores, and asserts every blob is byte-identical. This is the determinism
// backstop: if any nondeterministic field survives canonicalization, it fails
// here at PR time rather than as flaky corpus drift later.
func TestCorpusDoubleRunByteIdentical(t *testing.T) {
	if testDoltPort == 0 {
		t.Skip("dolt container unavailable; double-run needs a live store")
	}
	for _, envelope := range []bool{false, true} {
		first := generateCorpus(t, envelope)
		second := generateCorpus(t, envelope)
		if len(first) != len(second) {
			t.Fatalf("envelope=%v: blob count differs: %d vs %d", envelope, len(first), len(second))
		}
		for name, a := range first {
			b, ok := second[name]
			if !ok {
				t.Fatalf("envelope=%v: %q missing on second run", envelope, name)
			}
			if !bytes.Equal(a, b) {
				t.Fatalf("envelope=%v: %q is nondeterministic across runs:\n--- run 1 ---\n%s\n--- run 2 ---\n%s", envelope, name, a, b)
			}
		}
	}
}
