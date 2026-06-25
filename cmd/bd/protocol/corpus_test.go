package protocol

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// updateCorpus regenerates the committed golden corpus instead of checking it.
// `make corpus-regen` sets this; ordinary CI runs leave it false so a wire
// change shows up as a hard diff failure that must be reviewed.
var updateCorpus = flag.Bool("corpus.update", false, "regenerate the committed golden corpus under testdata/corpus/")

const corpusGeneratedBy = "cmd/bd/protocol TestCorpusGolden"

// generateCorpus runs the PLAN in a FRESH workspace for one envelope mode and
// returns name -> canonicalized blob bytes. Each mode (and each call) gets its
// own isolated store because the create/dep steps are stateful and cannot
// repeat in a single database.
func generateCorpus(t *testing.T, envelope bool) map[string][]byte {
	t.Helper()
	w := newWorkspace(t)
	out := make(map[string][]byte)
	for _, c := range CorpusPlan() {
		env := w.env()
		if envelope {
			env = append(env, "BD_JSON_ENVELOPE=1")
		}
		cmd := exec.Command(w.bd, c.Args...)
		cmd.Dir = w.dir
		cmd.Env = env
		var stdout bytes.Buffer
		cmd.Stdout = &stdout // stdout only: bd writes human error banners to stderr
		_ = cmd.Run()        // the "error" capture exits non-zero by design; read stdout regardless

		canon, err := CanonicalizeJSON(stdout.Bytes())
		if err != nil {
			t.Fatalf("canonicalize %s (envelope=%v): %v\nraw stdout:\n%s", c.Name, envelope, err, stdout.Bytes())
		}
		// Guard against silently baking a failure into the corpus: only the
		// dedicated "error" capture may be an error envelope.
		if c.Name != "error" && isErrorEnvelope(canon, envelope) {
			t.Fatalf("capture %q (envelope=%v) produced an error envelope, not real output:\n%s\n(check the bd command in CorpusPlan)", c.Name, envelope, canon)
		}
		out[c.Name] = canon
	}
	return out
}

// isErrorEnvelope reports whether a canonicalized blob is bd's {error, ...}
// shape (flat) or {data:{error,...}} / {error,...} under the v2 envelope.
func isErrorEnvelope(blob []byte, envelope bool) bool {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(blob, &top); err != nil {
		return false // arrays (list/ready) are never error envelopes
	}
	if _, ok := top["error"]; ok {
		return true
	}
	if envelope {
		if data, ok := top["data"]; ok {
			var inner map[string]json.RawMessage
			if json.Unmarshal(data, &inner) == nil {
				_, ok := inner["error"]
				return ok
			}
		}
	}
	return false
}

func TestCorpusGolden(t *testing.T) {
	if testDoltPort == 0 {
		t.Skip("dolt container unavailable; corpus generation needs a live store")
	}

	modes := []struct {
		name     string
		envelope bool
	}{
		{"flat", false},
		{"envelope", true},
	}

	blobs := make(map[string]map[string][]byte, len(modes))
	for _, m := range modes {
		blobs[m.name] = generateCorpus(t, m.envelope)
	}

	dir := filepath.Join("testdata", "corpus")
	bdVer, bdCommit := bdVersionInfo(t)
	schemaVersion := schemaVersionFromBlobs(t, blobs)
	manifest := NewManifest(schemaVersion, bdVer, bdCommit, corpusGeneratedBy, CorpusPlan(), blobs)
	manifestBytes, err := MarshalManifest(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	if *updateCorpus {
		writeCorpus(t, dir, blobs, manifestBytes)
		t.Logf("regenerated corpus under %s (bd %s, commit %s)", dir, bdVer, bdCommit)
		return
	}

	for _, m := range modes {
		for name, got := range blobs[m.name] {
			path := filepath.Join(dir, m.name, name+".json")
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read committed %s: %v\nrun `make corpus-regen` to (re)generate the corpus", path, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("corpus drift in %s/%s.json\n--- committed ---\n%s\n--- live bd ---\n%s\nrun `make corpus-regen` and review the diff before committing", m.name, name, want, got)
			}
		}
	}

	wantManifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read committed manifest: %v\nrun `make corpus-regen`", err)
	}
	if !bytes.Equal(manifestBytes, wantManifest) {
		t.Fatalf("manifest drift\n--- committed ---\n%s\n--- live ---\n%s\nrun `make corpus-regen`", wantManifest, manifestBytes)
	}
}

func writeCorpus(t *testing.T, dir string, blobs map[string]map[string][]byte, manifest []byte) {
	t.Helper()
	for mode, byName := range blobs {
		modeDir := filepath.Join(dir, mode)
		if err := os.MkdirAll(modeDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", modeDir, err)
		}
		for name, data := range byName {
			if err := os.WriteFile(filepath.Join(modeDir, name+".json"), data, 0o644); err != nil {
				t.Fatalf("write %s/%s: %v", mode, name, err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// schemaVersionFromBlobs reads the schema_version every bd --json object carries
// (flat mode) so the manifest records the same canary the wire uses. A bump in
// bd's JSONSchemaVersion changes every blob, which the diff guard then catches.
func schemaVersionFromBlobs(t *testing.T, blobs map[string]map[string][]byte) int {
	t.Helper()
	raw, ok := blobs["flat"]["version"]
	if !ok {
		t.Fatal("corpus missing flat/version blob")
	}
	var v struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse schema_version from version blob: %v", err)
	}
	if v.SchemaVersion == 0 {
		t.Fatalf("version blob has no schema_version:\n%s", raw)
	}
	return v.SchemaVersion
}

var bdVersionRE = regexp.MustCompile(`(?i)bd version\s+(\S+)\s*(?:\(([^)]*)\))?`)

// bdVersionInfo runs `bd version` (plain) and parses "bd version <ver> (<commit>)"
// for the manifest provenance, e.g. "1.0.5" + "dev".
func bdVersionInfo(t *testing.T) (version, commit string) {
	t.Helper()
	w := newWorkspace(t)
	cmd := exec.Command(w.bd, "version")
	cmd.Dir = w.dir
	cmd.Env = w.env()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bd version: %v", err)
	}
	m := bdVersionRE.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		t.Fatalf("unrecognized bd version output: %q", out)
	}
	commit = m[2]
	if commit == "" {
		commit = "unknown"
	}
	return m[1], commit
}
