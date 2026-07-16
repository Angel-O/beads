package creds

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A CommandSource with a DialHost surfaces the destination to the helper via
// BEADS_EXEC_INFO: origin "bd", the versioned apiVersion, and the canonical dial host.
func TestResolveInjectsExecInfoContent(t *testing.T) {
	resetCache(t)

	var got string
	credRunner = func(_ context.Context, _, execInfo string) ([]byte, error) {
		got = execInfo
		return []byte("tok-abc"), nil
	}

	src := CommandSource{
		Command:  "gettoken",
		Kind:     KindIdentity,
		Label:    "BEADS_DOLT_CREDENTIAL_COMMAND",
		DialHost: "GW.Example.COM",
		DialPort: 3306,
		Database: "bd_prj_x",
	}
	cred, ok, err := src.Resolve(context.Background())
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if cred.Value != "tok-abc" {
		t.Fatalf("value = %q, want tok-abc", cred.Value)
	}

	var p struct {
		APIVersion string `json:"apiVersion"`
		Origin     string `json:"origin"`
		Spec       struct {
			DialHost string `json:"dialHost"`
			DialPort int    `json:"dialPort"`
			Database string `json:"database"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(got), &p); err != nil {
		t.Fatalf("exec-info is not valid JSON (%q): %v", got, err)
	}
	if p.APIVersion != "beads.dev/credential-exec/v1" {
		t.Fatalf("apiVersion = %q", p.APIVersion)
	}
	if p.Origin != "bd" {
		t.Fatalf("origin = %q, want bd", p.Origin)
	}
	if p.Spec.DialHost != "gw.example.com" {
		t.Fatalf("dialHost = %q, want gw.example.com (canonical)", p.Spec.DialHost)
	}
	if p.Spec.DialPort != 3306 || p.Spec.Database != "bd_prj_x" {
		t.Fatalf("dialPort/database = %d/%q", p.Spec.DialPort, p.Spec.Database)
	}
}

// A password-command source (no DialHost) injects no BEADS_EXEC_INFO.
func TestResolveNoDialHostNoExecInfo(t *testing.T) {
	resetCache(t)

	var got string
	sawRun := false
	credRunner = func(_ context.Context, _, execInfo string) ([]byte, error) {
		got = execInfo
		sawRun = true
		return []byte("s3cr3t"), nil
	}
	src := CommandSource{Command: "vault read x", Kind: KindSecret, Label: "PW"}
	if _, _, err := src.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !sawRun || got != "" {
		t.Fatalf("expected no exec-info for a destination-less source, got %q", got)
	}
}

// The real runner injects BEADS_EXEC_INFO, replaces any inherited value, and passes
// every other parent variable through untouched.
func TestCredRunnerEnvInjection(t *testing.T) {
	resetCache(t)
	t.Setenv(ExecInfoEnvVar, "STALE-must-be-stripped")
	t.Setenv("HELPER_OWN_VAR", "keepme")

	dir := t.TempDir()
	out := filepath.Join(dir, "env.txt")
	src := CommandSource{
		Command:  "env > " + out + "; printf tok-canary",
		Kind:     KindIdentity,
		DialHost: "GW.Example.COM",
		DialPort: 3306,
		Database: "bd_prj_x",
	}
	cred, ok, err := src.Resolve(context.Background())
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if cred.Value != "tok-canary" {
		t.Fatalf("value = %q", cred.Value)
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading canary env: %v", err)
	}
	var execInfoLines []string
	var sawHelperVar bool
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, ExecInfoEnvVar+"="); ok {
			execInfoLines = append(execInfoLines, v)
		}
		if line == "HELPER_OWN_VAR=keepme" {
			sawHelperVar = true
		}
	}
	if len(execInfoLines) != 1 {
		t.Fatalf("expected exactly one %s in child env, got %d: %v", ExecInfoEnvVar, len(execInfoLines), execInfoLines)
	}
	if strings.Contains(execInfoLines[0], "STALE") {
		t.Fatalf("inherited %s was not stripped: %q", ExecInfoEnvVar, execInfoLines[0])
	}
	if !strings.Contains(execInfoLines[0], `"dialHost":"gw.example.com"`) {
		t.Fatalf("child %s missing canonical dialHost: %q", ExecInfoEnvVar, execInfoLines[0])
	}
	if !sawHelperVar {
		t.Fatal("helper's own parent env var did not pass through")
	}
}

// A warm token minted for host A is never served for a dial to host B: the canonical
// host is part of the cache key, so B is a cache MISS and the helper runs again.
func TestWarmCacheCrossHostMiss(t *testing.T) {
	resetCache(t)
	var calls int
	credRunner = func(_ context.Context, _, _ string) ([]byte, error) {
		calls++
		return []byte(fmt.Sprintf("tok-%d", calls)), nil
	}
	mk := func(host string) CommandSource {
		return CommandSource{Command: "gettoken", Kind: KindIdentity, DialHost: host, DialPort: 3306}
	}

	if _, _, err := mk("gw-a.example").Resolve(context.Background()); err != nil {
		t.Fatalf("resolve A: %v", err)
	}
	if _, _, err := mk("gw-a.example").Resolve(context.Background()); err != nil {
		t.Fatalf("resolve A again: %v", err)
	}
	if calls != 1 {
		t.Fatalf("host A should be a warm cache hit on the second resolve, calls=%d", calls)
	}
	if _, _, err := mk("gw-b.example").Resolve(context.Background()); err != nil {
		t.Fatalf("resolve B: %v", err)
	}
	if calls != 2 {
		t.Fatalf("host B must miss the cache (security control), calls=%d", calls)
	}
}

// ResolveForDial keys on the per-dial host and returns the source's Kind.
func TestResolveForDialHostKeying(t *testing.T) {
	resetCache(t)
	var calls int
	var lastExec string
	credRunner = func(_ context.Context, _, execInfo string) ([]byte, error) {
		calls++
		lastExec = execInfo
		return []byte("tok"), nil
	}
	src := CommandSource{Command: "gettoken", Kind: KindIdentity} // host supplied per dial

	cred, err := ResolveForDial(context.Background(), src, "GW.Example.COM", 3306, "db")
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	if cred.Kind != KindIdentity {
		t.Fatalf("kind = %v, want KindIdentity", cred.Kind)
	}
	if !strings.Contains(lastExec, `"dialHost":"gw.example.com"`) {
		t.Fatalf("per-dial exec-info missing canonical host: %q", lastExec)
	}
	if _, err := ResolveForDial(context.Background(), src, "GW.Example.COM", 3306, "db"); err != nil {
		t.Fatalf("dial A again: %v", err)
	}
	if calls != 1 {
		t.Fatalf("same host should be a cache hit, calls=%d", calls)
	}
	if _, err := ResolveForDial(context.Background(), src, "other.example", 3306, "db"); err != nil {
		t.Fatalf("dial B: %v", err)
	}
	if calls != 2 {
		t.Fatalf("different host must miss, calls=%d", calls)
	}
}

func TestResolveForDialRejectsNonCommandSource(t *testing.T) {
	if _, err := ResolveForDial(context.Background(), EnvSource{Var: "X"}, "gw.example", 3306, ""); err == nil {
		t.Fatal("expected an error resolving a non-command source per dial")
	}
}

// Invalidate forces the next resolution for the same (command, host) to re-run.
func TestInvalidateForcesRemint(t *testing.T) {
	resetCache(t)
	var calls int
	credRunner = func(_ context.Context, _, _ string) ([]byte, error) {
		calls++
		return []byte("tok"), nil
	}
	src := CommandSource{Command: "gettoken", Kind: KindIdentity, DialHost: "gw.example"}
	if _, _, err := src.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, _, err := src.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve again: %v", err)
	}
	if calls != 1 {
		t.Fatalf("second resolve should hit the cache, calls=%d", calls)
	}
	Invalidate(src)
	if _, _, err := src.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve after invalidate: %v", err)
	}
	if calls != 2 {
		t.Fatalf("resolve after invalidate must re-run the helper, calls=%d", calls)
	}
}
