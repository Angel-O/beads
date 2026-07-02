//go:build cgo

// DERISK SPIKE test for issue gastownhall/beads#4547 Route A (the uowstore
// adapter). It round-trips create -> get -> search -> ready -> close through the
// BD_SPIKE_UOWSTORE store (storage.DoltStorage implemented over the unit-of-work
// stack) and asserts the JSON output SHAPE matches the ordinary embedded store
// path for the same operations.
//
// Why this doesn't call `bd init --proxied-server`: that command is still gated
// (init.go: "--proxied-server is not yet implemented"), and this spike must not
// lift the gate. Instead the proxied workspace is bootstrapped the way the store
// path opens it: a metadata.json in proxied-server mode, then a first read
// command that boots the managed proxy + child dolt sql-server and auto-creates
// the schema (uow/dolt_sql_provider.go initSchema). issue_prefix — which normal
// `bd init` seeds via SetConfig — is seeded here directly over the proxy, since
// the spike store deliberately overrides only ~7 methods and SetConfig is not
// among them.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage/dbproxy/proxy"
)

// spikeProxiedEnv is bdProxiedEnv plus the spike flag and a fixed actor so the
// two backends mint audit rows the same way.
func spikeProxiedEnv(dir string) []string {
	return append(bdProxiedEnv(dir),
		"BD_SPIKE_UOWSTORE=1",
		"BEADS_SKIP_IDENTITY_CHECK=1",
		"BEADS_ACTOR=spiketester",
	)
}

func spikeEmbeddedEnv(dir string) []string {
	return append(bdEnv(dir),
		"BEADS_SKIP_IDENTITY_CHECK=1",
		"BEADS_ACTOR=spiketester",
	)
}

func spikeRun(t *testing.T, bd, dir string, env []string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(bd, args...)
	cmd.Dir = dir
	cmd.Env = env
	return runCombined(cmd)
}

func runCombined(cmd *exec.Cmd) (string, string, error) {
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// jsonSlice extracts the first top-level JSON array from bd output and decodes
// it into a slice of maps. bd sometimes prefixes warnings before the payload.
func jsonSlice(t *testing.T, out string) []map[string]any {
	t.Helper()
	start := strings.Index(out, "[")
	if start < 0 {
		if strings.Contains(out, "null") {
			return nil
		}
		t.Fatalf("no JSON array in output:\n%s", out)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out[start:]), &arr); err != nil {
		t.Fatalf("parse JSON array: %v\nraw: %s", err, out[start:])
	}
	return arr
}

// jsonObject extracts the first top-level JSON object (or first element of an
// array) from bd output.
func jsonObject(t *testing.T, out string) map[string]any {
	t.Helper()
	start := strings.IndexAny(out, "[{")
	if start < 0 {
		t.Fatalf("no JSON in output:\n%s", out)
	}
	s := out[start:]
	if strings.HasPrefix(s, "[") {
		var arr []map[string]any
		if err := json.Unmarshal([]byte(s), &arr); err != nil {
			t.Fatalf("parse JSON array: %v\nraw: %s", err, s)
		}
		if len(arr) == 0 {
			t.Fatalf("empty JSON array where object expected:\n%s", s)
		}
		return arr[0]
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		t.Fatalf("parse JSON object: %v\nraw: %s", err, s)
	}
	return obj
}

// volatileKeys are per-issue fields whose VALUES legitimately differ between two
// independent backends/runs (minted IDs, timestamps, per-workspace source repo).
// The spike compares the SHAPE (key set) and the stable fields, not these.
var volatileKeys = map[string]bool{
	"id":          true,
	"created_at":  true,
	"updated_at":  true,
	"closed_at":   true,
	"source_repo": true,
}

// normalizeIssue returns a copy with volatile values blanked and empty/null
// values dropped, so two backends can be compared on key set + stable values.
func normalizeIssue(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		if isEmptyJSON(v) {
			continue
		}
		if volatileKeys[k] {
			out[k] = "<volatile>"
			continue
		}
		out[k] = v
	}
	return out
}

func isEmptyJSON(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	case float64:
		return false
	case bool:
		return false
	default:
		return false
	}
}

func keySet(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func assertSameKeySet(t *testing.T, label string, a, b map[string]any) {
	t.Helper()
	na, nb := normalizeIssue(a), normalizeIssue(b)
	missing := map[string]bool{}
	for k := range na {
		if _, ok := nb[k]; !ok {
			missing["embedded-missing:"+k] = true
		}
	}
	for k := range nb {
		if _, ok := na[k]; !ok {
			missing["spike-missing:"+k] = true
		}
	}
	if len(missing) > 0 {
		diff := make([]string, 0, len(missing))
		for k := range missing {
			diff = append(diff, k)
		}
		t.Errorf("%s: normalized key sets differ: %v\n spike keys=%v\n embedded keys=%v",
			label, diff, keySet(na), keySet(nb))
	}
}

// assertStableFieldsEqual compares the non-volatile field values the round-trip
// controls (title, status, priority, issue_type). These MUST match exactly
// across the two backends for the same operation.
func assertStableFieldsEqual(t *testing.T, label string, a, b map[string]any) {
	t.Helper()
	for _, f := range []string{"title", "status", "priority", "issue_type"} {
		if fmt.Sprint(a[f]) != fmt.Sprint(b[f]) {
			t.Errorf("%s: field %q differs: spike=%v embedded=%v", label, f, a[f], b[f])
		}
	}
}

// setupSpikeProxiedWorkspace bootstraps a proxied-server workspace WITHOUT the
// gated init command, then boots the provider and seeds issue_prefix.
func setupSpikeProxiedWorkspace(t *testing.T, bd, prefix string) (dir string, env []string, proj proxiedProject) {
	t.Helper()
	dir = t.TempDir()
	initGitRepoAt(t, dir)
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	database := sanitizePrefixForDB(prefix)
	meta := fmt.Sprintf(`{"database":"beads.db","dolt_mode":"proxied-server","dolt_database":%q,"project_id":"spike-%s"}`,
		database, prefix)
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(meta), 0o644); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}

	proxyRoot := filepath.Join(beadsDir, "proxieddb")
	proj = proxiedProject{dir: dir, beadsDir: beadsDir, proxyRoot: proxyRoot, database: database, prefix: prefix}
	t.Cleanup(func() {
		if err := proxy.Shutdown(proxyRoot); err != nil {
			t.Logf("proxy.Shutdown(%s): %v", proxyRoot, err)
		}
	})
	shutdownProxyOnInterrupt(t, proxyRoot)

	env = spikeProxiedEnv(dir)

	// Boot the managed proxy + child server (this also auto-creates the schema).
	if _, stderr, err := spikeRun(t, bd, dir, env, "list", "--json"); err != nil {
		t.Fatalf("spike boot (list) failed: %v\n%s", err, stderr)
	}

	// Seed issue_prefix / issue_id_mode directly over the proxy — the spike store
	// does not override SetConfig, and normal init (which would seed these) is gated.
	db := openProxiedDB(t, proj)
	if _, err := db.Exec(
		"REPLACE INTO config (`key`, value) VALUES ('issue_prefix', ?), ('issue_id_mode', 'hash')",
		prefix,
	); err != nil {
		t.Fatalf("seed issue_prefix: %v", err)
	}
	return dir, env, proj
}

func TestSpikeUOWStore_RoundTrip(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// --- spike (proxied uowstore) workspace ---
	spikeDir, spikeEnv, _ := setupSpikeProxiedWorkspace(t, bd, "spikep")

	// --- embedded reference workspace ---
	embDir, _, _ := bdInit(t, bd, "--prefix", "embp")
	embEnv := spikeEmbeddedEnv(embDir)

	const title = "Round trip task"

	// create
	spikeCreate, se, err := spikeRun(t, bd, spikeDir, spikeEnv, "create", title, "--json", "-p", "1")
	if err != nil {
		t.Fatalf("spike create failed: %v\n%s", err, se)
	}
	embCreate, ee, err := spikeRun(t, bd, embDir, embEnv, "create", title, "--json", "-p", "1")
	if err != nil {
		t.Fatalf("embedded create failed: %v\n%s", err, ee)
	}
	spikeIssue := jsonObject(t, spikeCreate)
	embIssue := jsonObject(t, embCreate)
	assertSameKeySet(t, "create", spikeIssue, embIssue)
	assertStableFieldsEqual(t, "create", spikeIssue, embIssue)
	if got := fmt.Sprint(spikeIssue["status"]); got != "open" {
		t.Errorf("spike create status = %q, want open", got)
	}

	spikeID := fmt.Sprint(spikeIssue["id"])
	embID := fmt.Sprint(embIssue["id"])

	// get (show)
	spikeShow, se2, err := spikeRun(t, bd, spikeDir, spikeEnv, "show", spikeID, "--json")
	if err != nil {
		t.Fatalf("spike show failed: %v\n%s", err, se2)
	}
	embShow, ee2, err := spikeRun(t, bd, embDir, embEnv, "show", embID, "--json")
	if err != nil {
		t.Fatalf("embedded show failed: %v\n%s", err, ee2)
	}
	assertSameKeySet(t, "show", jsonObject(t, spikeShow), jsonObject(t, embShow))
	assertStableFieldsEqual(t, "show", jsonObject(t, spikeShow), jsonObject(t, embShow))

	// search (list)
	spikeList, _, err := spikeRun(t, bd, spikeDir, spikeEnv, "list", "--json")
	if err != nil {
		t.Fatalf("spike list failed: %v", err)
	}
	embList, _, err := spikeRun(t, bd, embDir, embEnv, "list", "--json")
	if err != nil {
		t.Fatalf("embedded list failed: %v", err)
	}
	spikeListArr, embListArr := jsonSlice(t, spikeList), jsonSlice(t, embList)
	if len(spikeListArr) != 1 || len(embListArr) != 1 {
		t.Fatalf("list count mismatch: spike=%d embedded=%d", len(spikeListArr), len(embListArr))
	}
	assertSameKeySet(t, "list", spikeListArr[0], embListArr[0])

	// ready
	spikeReady, _, err := spikeRun(t, bd, spikeDir, spikeEnv, "ready", "--json")
	if err != nil {
		t.Fatalf("spike ready failed: %v", err)
	}
	embReady, _, err := spikeRun(t, bd, embDir, embEnv, "ready", "--json")
	if err != nil {
		t.Fatalf("embedded ready failed: %v", err)
	}
	spikeReadyArr, embReadyArr := jsonSlice(t, spikeReady), jsonSlice(t, embReady)
	if len(spikeReadyArr) != 1 || len(embReadyArr) != 1 {
		t.Fatalf("ready count mismatch: spike=%d embedded=%d", len(spikeReadyArr), len(embReadyArr))
	}
	assertSameKeySet(t, "ready", spikeReadyArr[0], embReadyArr[0])

	// close
	spikeClose, se3, err := spikeRun(t, bd, spikeDir, spikeEnv, "close", spikeID, "--json")
	if err != nil {
		t.Fatalf("spike close failed: %v\n%s", err, se3)
	}
	embClose, ee3, err := spikeRun(t, bd, embDir, embEnv, "close", embID, "--json")
	if err != nil {
		t.Fatalf("embedded close failed: %v\n%s", err, ee3)
	}
	if got := fmt.Sprint(jsonObject(t, spikeClose)["status"]); got != "closed" {
		t.Errorf("spike close status = %q, want closed", got)
	}
	assertSameKeySet(t, "close", jsonObject(t, spikeClose), jsonObject(t, embClose))
	assertStableFieldsEqual(t, "close", jsonObject(t, spikeClose), jsonObject(t, embClose))

	// ready after close: both empty (denormalized is_blocked / ready recompute).
	spikeReady2, _, err := spikeRun(t, bd, spikeDir, spikeEnv, "ready", "--json")
	if err != nil {
		t.Fatalf("spike ready-after-close failed: %v", err)
	}
	if arr := jsonSlice(t, spikeReady2); len(arr) != 0 {
		t.Errorf("spike ready after close = %d issues, want 0", len(arr))
	}
}
