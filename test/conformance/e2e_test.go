//go:build e2e

package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

// scenario is a named sequence of bd CLI invocations. IDs are pinned with --id so
// they match across backends; only timestamps/paths need normalization.
//
// unordered marks a scenario whose JSON-array output has no contractual order: bd
// sorts by priority then created_at, and Postgres's whole-second timestamps tie where
// Dolt's sub-second ones do not, so equal-priority items created in the same second
// can legitimately differ in order. For these, array elements are sorted by id before
// comparison (matching the bts-rs oracle's multiset semantics for list output).
type scenario struct {
	name      string
	steps     [][]string
	unordered bool
}

// corpus is the backend-agnostic CLI surface exercised end-to-end. It is deliberately
// compact (the gc lifecycle + config), and grows over time; the bts-rs 523-scenario
// differential oracle (scripts/run-oracle-p.sh) remains the deep gate.
var corpus = []scenario{
	{name: "create-show", steps: [][]string{
		{"create", "First task", "--id", "cf-a1", "-t", "task", "--json"},
		{"show", "cf-a1", "--json"},
	}},
	{name: "list", unordered: true, steps: [][]string{
		{"create", "one", "--id", "cf-l1", "-t", "task"},
		{"create", "two", "--id", "cf-l2", "-t", "bug"},
		{"list", "--json"},
	}},
	{name: "dep-ready-gating", steps: [][]string{
		{"create", "root", "--id", "cf-r1", "-t", "task"},
		{"create", "blocked", "--id", "cf-r2", "-t", "task"},
		{"dep", "add", "cf-r2", "cf-r1"},
		{"ready", "--json"},
		{"close", "cf-r1"},
		{"ready", "--json"},
	}},
	{name: "close-reopen", steps: [][]string{
		{"create", "x", "--id", "cf-c1", "-t", "task"},
		{"close", "cf-c1"},
		{"show", "cf-c1", "--json"},
		{"reopen", "cf-c1"},
		{"show", "cf-c1", "--json"},
	}},
	{name: "update", steps: [][]string{
		{"create", "y", "--id", "cf-u1", "-t", "task"},
		{"update", "cf-u1", "-p", "0"},
		{"show", "cf-u1", "--json"},
	}},
	{name: "label", steps: [][]string{
		{"create", "z", "--id", "cf-b1", "-t", "task"},
		{"label", "add", "cf-b1", "urgent"},
		{"show", "cf-b1", "--json"},
	}},
	{name: "config", steps: [][]string{
		{"config", "set", "custom.foo", "bar"},
		{"config", "get", "custom.foo"},
		{"config", "unset", "custom.foo"},
		{"config", "get", "custom.foo"},
	}},
	// Deferred surfaces, present to exercise XFail classification against the
	// postgres profile's allowlist (the reference passes these).
	{name: "stats", steps: [][]string{
		{"create", "s", "--id", "cf-s1", "-t", "task"},
		{"stats", "--json"},
	}},
	// A fresh issue is never stale, so this exercises the GetStaleIssues query +
	// dialect translation and asserts the empty result matches the reference on every
	// backend. The found path (aged updated_at) is covered at the store level by the
	// conformance RunDeferredReads gate, which the CLI can't reach deterministically.
	{name: "stale", steps: [][]string{
		{"create", "st", "--id", "cf-st1", "-t", "task"},
		{"stale", "--days", "1", "--json"},
	}},
	{name: "update-no-history-demote", steps: [][]string{
		{"create", "d", "--id", "cf-d1", "-t", "task"},
		{"update", "cf-d1", "--no-history", "--json"},
	}},
}

// TestConformanceE2E runs every corpus scenario on the reference backend
// (dolt-embedded) and each available candidate, and asserts byte-equal normalized
// output. A candidate divergence is a genuine failure unless the scenario is on that
// profile's XFail allowlist (reported as XFAIL, never masked); an XFAIL that starts
// matching is itself flagged so the allowlist can only shrink.
func TestConformanceE2E(t *testing.T) {
	bin := buildBD(t)
	ref := Reference()
	cands := Candidates()
	if len(cands) == 0 {
		t.Skip("no candidate backends available (set BEADS_PG_TEST_URL for the postgres profile)")
	}
	for _, sc := range corpus {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			refOut := runScenario(t, bin, ref, sc)
			for _, cand := range cands {
				diff := firstDiff(refOut, runScenario(t, bin, cand, sc))
				_, xfail := cand.XFail[sc.name]
				switch {
				case diff == "" && xfail:
					t.Errorf("[%s] %q is XFail (%s) but now MATCHES the reference — remove it from the profile's XFail",
						cand.Name, sc.name, cand.XFail[sc.name])
				case diff == "":
					// pass
				case xfail:
					t.Logf("[%s] XFAIL %q (%s)", cand.Name, sc.name, cand.XFail[sc.name])
				default:
					t.Errorf("[%s] %q diverges from the %s reference:\n%s", cand.Name, sc.name, ref.Name, diff)
				}
			}
		})
	}
}

func runScenario(t *testing.T, bin string, p BackendProfile, sc scenario) []string {
	t.Helper()
	ws := &Workspace{Dir: t.TempDir()}
	if p.NewHandle != nil {
		ws.Handle = p.NewHandle()
	}
	if p.Teardown != nil {
		t.Cleanup(func() { p.Teardown(ws) })
	}
	var env []string
	if p.Env != nil {
		env = p.Env(ws)
	}
	initArgs := append([]string{"init", "-p", "cf", "--quiet"}, p.InitArgs(ws)...)
	if _, stderr, code := runBd(bin, ws.Dir, env, initArgs...); code != 0 {
		t.Fatalf("[%s] bd init failed (exit %d): %s", p.Name, code, stderr)
	}

	var results []string
	for _, step := range sc.steps {
		so, se, code := runBd(bin, ws.Dir, env, step...)
		out := normalize(so, ws)
		if sc.unordered {
			out = sortJSONArrayByID(out)
		}
		results = append(results, fmt.Sprintf("$ bd %s | exit=%d\nout: %s\nerr: %s",
			strings.Join(step, " "), code, out, normalize(se, ws)))
	}
	return results
}

// sortJSONArrayByID canonicalizes a top-level JSON array by sorting its elements on
// "id", so a non-contractual list order does not read as a divergence. Non-array or
// unparseable input is returned unchanged. Both backends are processed identically, so
// re-marshaling's formatting is irrelevant to the comparison.
func sortJSONArrayByID(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "[") {
		return s
	}
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(t), &arr); err != nil {
		return s
	}
	sort.SliceStable(arr, func(i, j int) bool { return jsonID(arr[i]) < jsonID(arr[j]) })
	out, err := json.MarshalIndent(arr, "", "  ")
	if err != nil {
		return s
	}
	return string(out)
}

func jsonID(raw json.RawMessage) string {
	var m struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &m)
	return m.ID
}

var (
	reTimestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)
	// Onboarding tips are random (probabilistic, BEADS_TIP_SEED-seeded) and not part
	// of the storage contract — strip them so they never cause a false divergence.
	reTip = regexp.MustCompile(`\n*💡 Tip:[^\n]*`)
)

// normalize removes cross-backend and cross-run noise: workspace path, schema handle,
// timestamps, and random tip banners. Pinned IDs need no normalization.
func normalize(s string, ws *Workspace) string {
	s = strings.ReplaceAll(s, ws.Dir, "<DIR>")
	if ws.Handle != "" {
		s = strings.ReplaceAll(s, ws.Handle, "<SCHEMA>")
	}
	s = reTimestamp.ReplaceAllString(s, "<TS>")
	s = reTip.ReplaceAllString(s, "")
	return strings.TrimRight(s, "\n")
}

func firstDiff(ref, cand []string) string {
	n := len(ref)
	if len(cand) < n {
		n = len(cand)
	}
	for i := 0; i < n; i++ {
		if ref[i] != cand[i] {
			return fmt.Sprintf("step %d:\n--- reference ---\n%s\n--- candidate ---\n%s", i, ref[i], cand[i])
		}
	}
	if len(ref) != len(cand) {
		return fmt.Sprintf("step count differs: reference=%d candidate=%d", len(ref), len(cand))
	}
	return ""
}

func runBd(bin, dir string, env []string, args ...string) (stdout, stderr string, code int) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	// Pin the tip RNG so any tip that slips past normalization is at least identical
	// across backends; the profile env may override.
	cmd.Env = append(os.Environ(), "BEADS_TIP_SEED=1")
	cmd.Env = append(cmd.Env, env...)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return o.String(), e.String(), ee.ExitCode()
		}
		return o.String(), e.String(), -1
	}
	return o.String(), e.String(), 0
}

var (
	buildOnce sync.Once
	bdBin     string
	bdErr     error
)

// buildBD builds the bd binary once per test process (matching the gms_pure_go tag
// used everywhere else) and returns its absolute path.
func buildBD(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		bin := filepath.Join(t.TempDir(), "bd-e2e")
		cmd := exec.Command("go", "build", "-tags", "gms_pure_go", "-o", bin, "./cmd/bd")
		cmd.Dir = repoRoot()
		if out, err := cmd.CombinedOutput(); err != nil {
			bdErr = fmt.Errorf("build bd: %v\n%s", err, out)
			return
		}
		bdBin = bin
	})
	if bdErr != nil {
		t.Fatal(bdErr)
	}
	return bdBin
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0) // <repo>/test/conformance/e2e_test.go
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}
