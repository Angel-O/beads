package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

func TestPlaneCommandWiring(t *testing.T) {
	if planeCmd.Use != "plane" {
		t.Errorf("Use = %q", planeCmd.Use)
	}
	subs := map[string]bool{}
	for _, c := range planeCmd.Commands() {
		subs[c.Name()] = true
	}
	for _, want := range []string{"sync", "status"} {
		if !subs[want] {
			t.Errorf("plane command missing %q subcommand", want)
		}
	}
	for _, flag := range []string{"pull", "push", "dry-run", "prefer-local", "prefer-plane", "create-only", "state", "include-ephemeral"} {
		if planeSyncCmd.Flags().Lookup(flag) == nil {
			t.Errorf("plane sync missing --%s flag", flag)
		}
	}
}

// TestPlaneSyncFlagDefaults pins the registered default for every plane sync
// flag. --state defaults to "all" (sync everything) and --include-ephemeral
// defaults to false (wisps stay local unless explicitly requested).
func TestPlaneSyncFlagDefaults(t *testing.T) {
	defaults := map[string]string{
		"pull":              "false",
		"push":              "false",
		"dry-run":           "false",
		"prefer-local":      "false",
		"prefer-plane":      "false",
		"create-only":       "false",
		"state":             "all",
		"include-ephemeral": "false",
	}
	for name, want := range defaults {
		f := planeSyncCmd.Flags().Lookup(name)
		if f == nil {
			t.Errorf("plane sync missing --%s flag", name)
			continue
		}
		if f.DefValue != want {
			t.Errorf("--%s default = %q, want %q", name, f.DefValue, want)
		}
	}
}

func TestPlaneStatusCounts(t *testing.T) {
	ref := func(s string) *string { return &s }
	issues := []*types.Issue{
		{ID: "bd-1", ExternalRef: ref("https://plane.example.com/acme/projects/11111111-2222-3333-4444-555555555555/issues/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")},
		{ID: "bd-2", ExternalRef: ref("https://linear.app/team/issue/T-9")},
		{ID: "bd-3"},
		{ID: "bd-4", ExternalRef: ref("plane:acme/11111111-2222-3333-4444-555555555555/bbbbbbbb-bbbb-cccc-dddd-eeeeeeeeeeee")},
	}

	withRef, pending := planeStatusCounts(issues)
	if withRef != 2 {
		t.Errorf("withRef = %d, want 2", withRef)
	}
	// bd-3 has no external ref at all -> pending push candidate;
	// bd-2 belongs to another tracker -> NOT pending for plane.
	if pending != 1 {
		t.Errorf("pending = %d, want 1", pending)
	}
}

// fakePlaneStore satisfies storage.DoltStorage via an embedded nil interface
// (any unimplemented method panics, mirroring fakeFallbackStore in
// auto_import_upgrade_unit_test.go). It serves config reads for
// validatePlaneConfig / Tracker.Init and local issues for the push path.
type fakePlaneStore struct {
	storage.DoltStorage // nil — panics on any non-overridden method
	config              map[string]string
	issues              []*types.Issue
}

func (f *fakePlaneStore) GetConfig(_ context.Context, key string) (string, error) {
	return f.config[key], nil
}

func (f *fakePlaneStore) SearchIssues(_ context.Context, _ string, _ types.IssueFilter) ([]*types.Issue, error) {
	return f.issues, nil
}

// clearPlaneEnv blanks every environment variable validatePlaneConfig and
// Tracker.Init consult, including the viper-bound BD_ variant of the
// yaml-only secret.
func clearPlaneEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"PLANE_API_KEY", "PLANE_BASE_URL", "PLANE_WORKSPACE", "PLANE_PROJECT_ID",
		"BD_PLANE_API_KEY",
	} {
		t.Setenv(name, "")
	}
}

// TestPlaneValidateConfigMessages pins the specific validatePlaneConfig
// error message for each missing config key, in precedence order, and that
// env vars or store config satisfy each requirement.
func TestPlaneValidateConfigMessages(t *testing.T) {
	saveAndRestoreGlobals(t)
	oldRootCtx := rootCtx
	rootCtx = context.Background()
	t.Cleanup(func() { rootCtx = oldRootCtx })

	tests := []struct {
		name    string
		env     map[string]string
		config  map[string]string
		wantErr string // substring; "" means expect success
	}{
		{
			name:    "missing api key",
			wantErr: "Plane API key not configured",
		},
		{
			name:    "missing base url",
			env:     map[string]string{"PLANE_API_KEY": "key"},
			wantErr: "plane.base_url not configured",
		},
		{
			name:    "missing workspace",
			env:     map[string]string{"PLANE_API_KEY": "key"},
			config:  map[string]string{"plane.base_url": "https://plane.example.com"},
			wantErr: "plane.workspace not configured",
		},
		{
			name: "missing project id",
			env:  map[string]string{"PLANE_API_KEY": "key", "PLANE_WORKSPACE": "acme"},
			config: map[string]string{
				"plane.base_url": "https://plane.example.com",
			},
			wantErr: "plane.project_id not configured",
		},
		{
			name: "fully configured via env",
			env: map[string]string{
				"PLANE_API_KEY":    "key",
				"PLANE_BASE_URL":   "https://plane.example.com",
				"PLANE_WORKSPACE":  "acme",
				"PLANE_PROJECT_ID": "11111111-2222-3333-4444-555555555555",
			},
		},
		{
			name: "fully configured via store config",
			env:  map[string]string{"PLANE_API_KEY": "key"},
			config: map[string]string{
				"plane.base_url":   "https://plane.example.com",
				"plane.workspace":  "acme",
				"plane.project_id": "11111111-2222-3333-4444-555555555555",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearPlaneEnv(t)
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			store = &fakePlaneStore{config: tc.config}
			storeActive = true

			err := validatePlaneConfig()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validatePlaneConfig() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validatePlaneConfig() = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("validatePlaneConfig() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

// setPlaneSyncFlag sets a flag on the package-global planeSyncCmd and
// restores its default when the test finishes.
func setPlaneSyncFlag(t *testing.T, name, value string) {
	t.Helper()
	f := planeSyncCmd.Flags().Lookup(name)
	if f == nil {
		t.Fatalf("plane sync missing --%s flag", name)
	}
	if err := planeSyncCmd.Flags().Set(name, value); err != nil {
		t.Fatalf("setting --%s=%s: %v", name, value, err)
	}
	t.Cleanup(func() {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
}

// planeSyncMutexHelperEnv marks the re-exec child for
// TestPlaneSyncPreferFlagsMutuallyExclusive.
const planeSyncMutexHelperEnv = "BD_TEST_PLANE_SYNC_MUTEX_HELPER"

// TestPlaneSyncPreferFlagsMutuallyExclusive pins that --prefer-local and
// --prefer-plane together are rejected with a fatal error. FatalError calls
// os.Exit(1), so the assertion runs in a re-exec'd child process.
func TestPlaneSyncPreferFlagsMutuallyExclusive(t *testing.T) {
	if os.Getenv(planeSyncMutexHelperEnv) == "1" {
		_ = planeSyncCmd.Flags().Set("prefer-local", "true")
		_ = planeSyncCmd.Flags().Set("prefer-plane", "true")
		runPlaneSync(planeSyncCmd, nil) // must FatalError -> os.Exit(1)
		os.Exit(0)                      // reached only if the guard did not fire
	}

	cmd := exec.Command(os.Args[0], "-test.run", "TestPlaneSyncPreferFlagsMutuallyExclusive$")
	cmd.Env = append(os.Environ(), planeSyncMutexHelperEnv+"=1")
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected child to exit non-zero, got err=%v\noutput:\n%s", err, out)
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("child exit code = %d, want 1\noutput:\n%s", exitErr.ExitCode(), out)
	}
	if !strings.Contains(string(out), "cannot use both --prefer-local and --prefer-plane") {
		t.Errorf("child output missing mutual-exclusion message:\n%s", out)
	}
}

// runPlaneSyncDryRunPush drives the real runPlaneSync handler in-process for
// a --push --dry-run sync against a fake store. Dry-run pushes never hit the
// network (the engine only prints what it would create), so this exercises
// the full flag -> SyncOptions -> engine chain without a live Plane.
func runPlaneSyncDryRunPush(t *testing.T, issues []*types.Issue, extraFlags map[string]string) string {
	t.Helper()
	saveAndRestoreGlobals(t)
	oldRootCtx := rootCtx
	oldJSONOutput := jsonOutput
	rootCtx = context.Background()
	jsonOutput = false
	t.Cleanup(func() {
		rootCtx = oldRootCtx
		jsonOutput = oldJSONOutput
	})

	clearPlaneEnv(t)
	t.Setenv("PLANE_API_KEY", "key")
	t.Setenv("PLANE_BASE_URL", "https://plane.example.com")
	t.Setenv("PLANE_WORKSPACE", "acme")
	t.Setenv("PLANE_PROJECT_ID", "11111111-2222-3333-4444-555555555555")

	store = &fakePlaneStore{issues: issues}
	storeActive = true

	setPlaneSyncFlag(t, "push", "true")
	setPlaneSyncFlag(t, "dry-run", "true")
	for name, value := range extraFlags {
		setPlaneSyncFlag(t, name, value)
	}

	return captureADOStdout(t, func() {
		runPlaneSync(planeSyncCmd, nil)
	})
}

// TestPlaneSyncDryRunPushEphemeralFiltering pins the flag-to-SyncOptions
// mapping for ephemeral issues: ExcludeEphemeral is true by default (wisps
// stay local) and false when --include-ephemeral is set.
func TestPlaneSyncDryRunPushEphemeralFiltering(t *testing.T) {
	issues := []*types.Issue{
		{ID: "bd-1", Title: "persistent issue", Status: types.StatusOpen, IssueType: types.TypeTask},
		{ID: "bd-wisp-1", Title: "ephemeral wisp", Status: types.StatusOpen, IssueType: types.TypeTask, Ephemeral: true},
	}

	t.Run("default excludes ephemeral", func(t *testing.T) {
		out := runPlaneSyncDryRunPush(t, issues, nil)
		if !strings.Contains(out, "Would create in Plane: persistent issue") {
			t.Errorf("persistent issue not pushed:\n%s", out)
		}
		if strings.Contains(out, "ephemeral wisp") {
			t.Errorf("ephemeral wisp pushed by default, want excluded:\n%s", out)
		}
	})

	t.Run("--include-ephemeral pushes wisps", func(t *testing.T) {
		out := runPlaneSyncDryRunPush(t, issues, map[string]string{"include-ephemeral": "true"})
		if !strings.Contains(out, "Would create in Plane: persistent issue") {
			t.Errorf("persistent issue not pushed:\n%s", out)
		}
		if !strings.Contains(out, "Would create in Plane: ephemeral wisp") {
			t.Errorf("ephemeral wisp not pushed with --include-ephemeral:\n%s", out)
		}
	})
}

// TestPlaneSyncDryRunPushStateFilter pins that --state reaches
// SyncOptions.State: --state open skips closed issues on push.
func TestPlaneSyncDryRunPushStateFilter(t *testing.T) {
	issues := []*types.Issue{
		{ID: "bd-1", Title: "open issue", Status: types.StatusOpen, IssueType: types.TypeTask},
		{ID: "bd-2", Title: "closed issue", Status: types.StatusClosed, IssueType: types.TypeTask},
	}

	out := runPlaneSyncDryRunPush(t, issues, map[string]string{"state": "open"})
	if !strings.Contains(out, "Would create in Plane: open issue") {
		t.Errorf("open issue not pushed with --state open:\n%s", out)
	}
	if strings.Contains(out, "closed issue") {
		t.Errorf("closed issue pushed despite --state open:\n%s", out)
	}
}
