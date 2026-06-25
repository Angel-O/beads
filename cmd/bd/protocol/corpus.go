// corpus.go — producer-side logic for the Beads↔Gas City cross-version
// contract-test system (Phase 2).
//
// This file is the dependency-free, unit-testable core: it defines the
// deterministic command PLAN that exercises bd's stable CLI/JSON surface,
// a canonicalizer that strips nondeterminism (timestamps, ordering) so two
// independent runs produce byte-identical output, and the manifest that
// records the corpus's provenance.
//
// Gas City vendors the generated corpus (testdata/corpus/) and replays it
// against its own consumer to detect cross-version drift without needing a
// live bd. Everything here must stay free of test-only and bd-internal
// imports so it can be reasoned about (and reused) in isolation.
package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Capture is a single named command in the corpus PLAN. Name is the stable
// blob filename (without extension); Args is the exact bd argument vector
// (excluding the bd binary itself and the global --json flag, which the
// runner appends where appropriate).
type Capture struct {
	Name string
	Args []string
}

// Fixed issue IDs used by the PLAN. Pinning IDs removes the largest source
// of nondeterminism (generated hash IDs) so the only thing left to
// canonicalize is timestamps.
const (
	CorpusRootID = "corpus-root"
	CorpusDepID  = "corpus-dep"
)

// CorpusPlan returns the ordered, deterministic list of captures. The order
// matters: create steps must run before the read steps that observe them,
// and the dependency must be added before dep_list is captured.
//
// Each Capture's Args are passed verbatim to bd. Read commands include
// --json so the output is the structured contract surface; the "error"
// capture deliberately targets a missing issue to pin the error envelope.
func CorpusPlan() []Capture {
	return []Capture{
		// --force lets us pin custom IDs despite bd's per-database random ID
		// prefix; pinned IDs make every downstream read deterministic.
		{
			Name: "create_root",
			Args: []string{"create", "Corpus root issue", "--id", CorpusRootID, "--force", "--priority", "1", "--type", "feature", "--description", "deterministic corpus root", "--json"},
		},
		{
			Name: "create_dep",
			Args: []string{"create", "Corpus dependency issue", "--id", CorpusDepID, "--force", "--priority", "2", "--type", "task", "--description", "deterministic corpus dependency", "--json"},
		},
		{
			Name: "dep_add",
			Args: []string{"dep", "add", CorpusRootID, CorpusDepID, "--type", "blocks", "--json"},
		},
		{
			Name: "show",
			Args: []string{"show", CorpusRootID, "--json"},
		},
		{
			Name: "list",
			Args: []string{"list", "--all", "--json"},
		},
		{
			Name: "ready",
			Args: []string{"ready", "--json"},
		},
		{
			Name: "dep_list",
			Args: []string{"dep", "list", CorpusRootID, "--json"},
		},
		{
			Name: "count",
			Args: []string{"count", "--json"},
		},
		{
			Name: "version",
			Args: []string{"version", "--json"},
		},
		// "error" pins the {error, schema_version} envelope bd emits on stdout
		// for a missing issue — gascity's isBdNotFound classifier depends on it.
		{
			Name: "error",
			Args: []string{"show", "nonexistent-xyz-corpus", "--json"},
		},
	}
}

// timestampRE matches RFC3339 / RFC3339Nano timestamps anywhere in a string
// value. We anchor it to the full string so we only replace values that are
// timestamps in their entirety, not strings that happen to embed a date.
var timestampRE = regexp.MustCompile(
	`^\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$`,
)

// tsPlaceholder is the canonical stand-in for any timestamp value.
const tsPlaceholder = "<TS>"

// CanonicalizeJSON normalizes a bd JSON blob so that two independent runs
// produce byte-identical output:
//
//  1. Any string value that is an RFC3339/RFC3339Nano timestamp is replaced
//     with "<TS>".
//  2. Any array whose elements are all objects is stably sorted by the
//     element's "id" field (falling back to the element's canonical-JSON
//     form when "id" is absent or equal), so list/ready/sql ordering is
//     deterministic regardless of storage iteration order.
//  3. The result is re-marshalled with 2-space indentation and sorted keys
//     (Go's encoding/json sorts map keys), yielding a minimal, byte-stable
//     diff.
//
// The input need not be a single object; arrays and scalars are handled.
func CanonicalizeJSON(raw []byte) ([]byte, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("canonicalize: decode: %w", err)
	}

	canon := canonValue(v)

	out, err := marshalCanonical(canon)
	if err != nil {
		return nil, fmt.Errorf("canonicalize: marshal: %w", err)
	}
	return out, nil
}

// canonValue recursively normalizes a decoded JSON value in place.
func canonValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			t[k] = canonValue(child)
		}
		return t
	case []any:
		for i, child := range t {
			t[i] = canonValue(child)
		}
		sortObjectArray(t)
		return t
	case string:
		if timestampRE.MatchString(t) {
			return tsPlaceholder
		}
		return t
	default:
		// json.Number, bool, nil — already deterministic.
		return v
	}
}

// sortObjectArray stably sorts arr in place iff every element is an object.
// Elements are ordered by their "id" string when present; ties (or missing
// ids) fall back to the element's canonical-JSON encoding so the order is
// fully determined by content.
func sortObjectArray(arr []any) {
	for _, e := range arr {
		if _, ok := e.(map[string]any); !ok {
			return // mixed or scalar array — preserve original order
		}
	}
	sort.SliceStable(arr, func(i, j int) bool {
		return elemSortKey(arr[i]) < elemSortKey(arr[j])
	})
}

// elemSortKey derives a stable ordering key for an object array element:
// its "id" field plus its canonical-JSON form as a tiebreaker. Including the
// canonical form guarantees a total order even when ids collide or are
// absent.
func elemSortKey(e any) string {
	idPart := ""
	if m, ok := e.(map[string]any); ok {
		if id, ok := m["id"].(string); ok {
			idPart = id
		}
	}
	body, err := marshalCanonical(e)
	if err != nil {
		return idPart
	}
	return idPart + "\x00" + string(body)
}

// marshalCanonical encodes v with sorted keys (json default) and 2-space
// indentation, without HTML-escaping, so the bytes are stable and minimal.
func marshalCanonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// json.Encoder appends a trailing newline; keep it for clean diffs.
	return buf.Bytes(), nil
}

// BlobMeta records a single corpus blob's provenance: the bd command that
// produced it and the SHA-256 of its canonicalized bytes.
type BlobMeta struct {
	Cmd    string `json:"cmd"`
	SHA256 string `json:"sha256"`
}

// Manifest is the corpus index. It pins the schema version and the live bd
// version/commit the corpus was generated from, plus a per-blob checksum so
// consumers (Gas City) can detect tampering or partial vendoring.
type Manifest struct {
	SchemaVersion int                 `json:"schema_version"`
	BDVersion     string              `json:"bd_version"`
	BDCommit      string              `json:"bd_commit"`
	GeneratedBy   string              `json:"generated_by"`
	Canonicalized bool                `json:"canonicalized"`
	Blobs         map[string]BlobMeta `json:"blobs"`
}

// NewManifest builds a Manifest from the captured (mode → name → bytes)
// corpus. The blob key is "<mode>/<name>" (e.g. "flat/show",
// "envelope/show") and cmd is the joined bd argument vector for that
// capture. bytes are the already-canonicalized blob contents; the SHA is
// computed over them so the manifest validates exactly what's on disk.
func NewManifest(schemaVersion int, bdVersion, bdCommit, generatedBy string, plan []Capture, blobs map[string]map[string][]byte) Manifest {
	cmdByName := make(map[string]string, len(plan))
	for _, c := range plan {
		cmdByName[c.Name] = "bd " + joinArgs(c.Args)
	}

	out := make(map[string]BlobMeta)
	for mode, byName := range blobs {
		for name, data := range byName {
			sum := sha256.Sum256(data)
			out[mode+"/"+name] = BlobMeta{
				Cmd:    cmdByName[name],
				SHA256: hex.EncodeToString(sum[:]),
			}
		}
	}

	return Manifest{
		SchemaVersion: schemaVersion,
		BDVersion:     bdVersion,
		BDCommit:      bdCommit,
		GeneratedBy:   generatedBy,
		Canonicalized: true,
		Blobs:         out,
	}
}

// MarshalManifest encodes a Manifest deterministically (sorted keys, 2-space
// indent) so committing it produces stable diffs.
func MarshalManifest(m Manifest) ([]byte, error) {
	return marshalCanonical(m)
}

// joinArgs renders an argument vector as a single shell-ish string for the
// manifest's cmd field. Args containing spaces are quoted so multi-word titles
// and descriptions read correctly.
func joinArgs(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsRune(a, ' ') {
			parts[i] = `"` + a + `"`
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
