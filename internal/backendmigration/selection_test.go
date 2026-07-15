package backendmigration

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/workspaceidentity"
)

type fakeSourceWitness struct {
	revalidations []error
	revalidateAt  int
	closeErr      error
	closes        int
}

type fakeFilesystemProbeFailure struct{ cause error }

func (e *fakeFilesystemProbeFailure) Error() string           { return "filesystem probe failed" }
func (e *fakeFilesystemProbeFailure) Unwrap() error           { return e.cause }
func (e *fakeFilesystemProbeFailure) FilesystemProbeFailure() {}

func (w *fakeSourceWitness) Revalidate() error {
	w.revalidateAt++
	if w.revalidateAt <= len(w.revalidations) {
		return w.revalidations[w.revalidateAt-1]
	}
	return nil
}

func (w *fakeSourceWitness) InspectEmbeddedDoltFilesystem() (workspaceidentity.FilesystemSnapshot, error) {
	return workspaceidentity.FilesystemSnapshot{}, nil
}

func (w *fakeSourceWitness) Close() error {
	w.closes++
	return w.closeErr
}

type selectionFixture struct {
	workspace   string
	metadata    []byte
	shape       shapeObservation
	witness     *fakeSourceWitness
	observes    int
	binds       int
	parses      int
	filesystems int
}

func newSelectionFixture(t *testing.T, cfg configfile.Config) *selectionFixture {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(filepath.Join(workspace, "embeddeddolt"), 0o700); err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, configfile.ConfigFileName), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	shape := shapeObservation{
		root:     selectionFixtureObject(t, workspace),
		current:  selectionFixtureObject(t, filepath.Join(workspace, configfile.ConfigFileName)),
		provider: selectionFixtureObject(t, filepath.Join(workspace, "embeddeddolt")),
	}
	return &selectionFixture{workspace: workspace, metadata: metadata, shape: shape, witness: &fakeSourceWitness{}}
}

func selectionFixtureObject(t *testing.T, path string) observedObject {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return observedObject{present: true, canonical: filepath.Clean(path), info: info}
}

func (f *selectionFixture) dependencies() selectionDependencies {
	return selectionDependencies{
		platform:      func() (bool, bool, error) { return true, false, nil },
		embeddedBuild: true,
		observe: func(string) (shapeObservation, error) {
			f.observes++
			return f.shape, nil
		},
		bind: func(string, int64) (sourceWitness, []byte, error) {
			f.binds++
			return f.witness, append([]byte(nil), f.metadata...), nil
		},
		parseMetadata: func(data []byte) (*configfile.Config, error) {
			f.parses++
			return configfile.ParseReadOnlyMetadata(data)
		},
		inspectFS: func(sourceWitness) (workspaceidentity.FilesystemSnapshot, error) {
			f.filesystems++
			return workspaceidentity.FilesystemSnapshot{}, nil
		},
		equalFS:     func(workspaceidentity.FilesystemSnapshot, workspaceidentity.FilesystemSnapshot) bool { return true },
		qualifiedFS: func(workspaceidentity.FilesystemSnapshot) bool { return true },
	}
}

func validSelectionRequest(workspace string) SelectionRequest {
	return SelectionRequest{
		Workspace:        workspace,
		TargetBackend:    configfile.BackendPostgres,
		Selector:         SelectorPhysicalWorkspace,
		AmbientSelection: AmbientSelectionAbsent,
	}
}

func requireRefusal(t *testing.T, candidate SourceShapeCandidate, err error, code RefusalCode, reason RefusalReason) *Refusal {
	t.Helper()
	if candidate != (SourceShapeCandidate{}) {
		t.Fatalf("candidate=%#v, want zero", candidate)
	}
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("error=%v, want *Refusal", err)
	}
	if refusal.Code != code || refusal.Reason != reason || refusal.Retryable != (code == CodeWorkspaceChanged) ||
		refusal.Effect != effectNone || err.Error() != string(code) {
		t.Fatalf("refusal=%#v error=%q, want %s/%s", refusal, err, code, reason)
	}
	return refusal
}

func TestInspectSourceShapeRefusesRedirectWithoutReadingTarget(t *testing.T) {
	requireNativeLinuxEmbeddedBuild(t)
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "redirect-target-must-not-be-read")
	if err := os.WriteFile(filepath.Join(beadsDir, "redirect"), []byte(target), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeTree := digestTree(t, beadsDir)

	candidate, err := InspectSourceShape(SelectionRequest{
		Workspace:        beadsDir,
		TargetBackend:    "postgres",
		Selector:         SelectorPhysicalWorkspace,
		AmbientSelection: AmbientSelectionAbsent,
	})
	if candidate != (SourceShapeCandidate{}) {
		t.Fatalf("redirect selection returned candidate %#v", candidate)
	}
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != CodeWorkspaceShapeUnsupported || refusal.Reason != ReasonRedirect ||
		refusal.Retryable || refusal.Effect != effectNone || err.Error() != string(CodeWorkspaceShapeUnsupported) {
		if err == nil {
			t.Fatal("redirect selection was admitted as a source-shape candidate")
		}
		t.Fatalf("redirect refusal = %#v, %v", refusal, err)
	}
	if afterTree := digestTree(t, beadsDir); afterTree != beforeTree {
		t.Fatal("redirect refusal changed workspace bytes, paths, or modes")
	}
}

func TestInspectSourceShapeEarlyPrecedenceHasNoSourceAccess(t *testing.T) {
	base := validSelectionRequest(filepath.Join(string(filepath.Separator), "tmp", ".beads"))
	platformErr := errors.New("platform probe failed")
	tests := []struct {
		name          string
		request       SelectionRequest
		platform      func() (bool, bool, error)
		embeddedBuild bool
		code          RefusalCode
		reason        RefusalReason
		platformCalls int
		rawCause      error
	}{
		{name: "mysql target first", request: func() SelectionRequest {
			r := base
			r.TargetBackend = configfile.BackendMySQL
			r.Selector = SelectorDatabase
			return r
		}(), embeddedBuild: true, code: CodePairUnsupported, reason: ReasonTargetBackend},
		{name: "empty target", request: func() SelectionRequest { r := base; r.TargetBackend = ""; return r }(), embeddedBuild: true, code: CodePairUnsupported, reason: ReasonTargetBackend},
		{name: "dolt target", request: func() SelectionRequest { r := base; r.TargetBackend = configfile.BackendDolt; return r }(), embeddedBuild: true, code: CodePairUnsupported, reason: ReasonTargetBackend},
		{name: "sqlite target", request: func() SelectionRequest { r := base; r.TargetBackend = configfile.BackendSQLite; return r }(), embeddedBuild: true, code: CodePairUnsupported, reason: ReasonTargetBackend},
		{name: "unknown target", request: func() SelectionRequest { r := base; r.TargetBackend = "unknown"; return r }(), embeddedBuild: true, code: CodePairUnsupported, reason: ReasonTargetBackend},
		{name: "platform probe", request: base, platform: func() (bool, bool, error) { return false, false, platformErr }, embeddedBuild: true, code: CodeWorkspaceUnverifiable, reason: ReasonPlatformProbe, platformCalls: 1, rawCause: platformErr},
		{name: "operating system", request: base, platform: func() (bool, bool, error) { return false, false, nil }, embeddedBuild: true, code: CodePlatformUnsupported, reason: ReasonOperatingSystem, platformCalls: 1},
		{name: "wsl", request: base, platform: func() (bool, bool, error) { return true, true, nil }, embeddedBuild: true, code: CodePlatformUnsupported, reason: ReasonWSL, platformCalls: 1},
		{name: "embedded build", request: base, platform: func() (bool, bool, error) { return true, false, nil }, code: CodePlatformUnsupported, reason: ReasonEmbeddedBuild, platformCalls: 1},
		{name: "database selector before path", request: func() SelectionRequest {
			r := base
			r.Selector = SelectorDatabase
			r.Workspace = filepath.Join(base.Workspace, "embeddeddolt")
			return r
		}(), platform: func() (bool, bool, error) { return true, false, nil }, embeddedBuild: true, code: CodeWorkspaceShapeUnsupported, reason: ReasonSelector, platformCalls: 1},
		{name: "zero selector", request: func() SelectionRequest { r := base; r.Selector = 0; return r }(), platform: func() (bool, bool, error) { return true, false, nil }, embeddedBuild: true, code: CodeWorkspaceShapeUnsupported, reason: ReasonSelector, platformCalls: 1},
		{name: "invalid selector", request: func() SelectionRequest { r := base; r.Selector = 99; return r }(), platform: func() (bool, bool, error) { return true, false, nil }, embeddedBuild: true, code: CodeWorkspaceShapeUnsupported, reason: ReasonSelector, platformCalls: 1},
		{name: "workspace env selector", request: func() SelectionRequest { r := base; r.Selector = SelectorWorkspaceEnv; return r }(), platform: func() (bool, bool, error) { return true, false, nil }, embeddedBuild: true, code: CodeWorkspaceShapeUnsupported, reason: ReasonSelector, platformCalls: 1},
		{name: "worktree shared selector", request: func() SelectionRequest { r := base; r.Selector = SelectorWorktreeShared; return r }(), platform: func() (bool, bool, error) { return true, false, nil }, embeddedBuild: true, code: CodeWorkspaceShapeUnsupported, reason: ReasonSelector, platformCalls: 1},
		{name: "ambiguous selector", request: func() SelectionRequest { r := base; r.Selector = SelectorAmbiguous; return r }(), platform: func() (bool, bool, error) { return true, false, nil }, embeddedBuild: true, code: CodeWorkspaceShapeUnsupported, reason: ReasonSelector, platformCalls: 1},
		{name: "unknown ambient", request: func() SelectionRequest { r := base; r.AmbientSelection = AmbientSelectionUnknown; return r }(), platform: func() (bool, bool, error) { return true, false, nil }, embeddedBuild: true, code: CodeWorkspaceUnverifiable, reason: ReasonRequest, platformCalls: 1},
		{name: "invalid ambient", request: func() SelectionRequest { r := base; r.AmbientSelection = 99; return r }(), platform: func() (bool, bool, error) { return true, false, nil }, embeddedBuild: true, code: CodeWorkspaceUnverifiable, reason: ReasonRequest, platformCalls: 1},
		{name: "present ambient", request: func() SelectionRequest { r := base; r.AmbientSelection = AmbientSelectionPresent; return r }(), platform: func() (bool, bool, error) { return true, false, nil }, embeddedBuild: true, code: CodeWorkspaceShapeUnsupported, reason: ReasonAmbientSelection, platformCalls: 1},
		{name: "invalid utf8", request: func() SelectionRequest {
			r := base
			r.Workspace = string([]byte{'/', 0xff, '.', 'b', 'e', 'a', 'd', 's'})
			return r
		}(), platform: func() (bool, bool, error) { return true, false, nil }, embeddedBuild: true, code: CodeWorkspaceUnverifiable, reason: ReasonRequest, platformCalls: 1},
		{name: "control path", request: func() SelectionRequest { r := base; r.Workspace = "/tmp/control\x1b/.beads"; return r }(), platform: func() (bool, bool, error) { return true, false, nil }, embeddedBuild: true, code: CodeWorkspaceUnverifiable, reason: ReasonRequest, platformCalls: 1},
		{name: "relative alias", request: func() SelectionRequest { r := base; r.Workspace = ".beads"; return r }(), platform: func() (bool, bool, error) { return true, false, nil }, embeddedBuild: true, code: CodeWorkspaceShapeUnsupported, reason: ReasonWorkspaceAlias, platformCalls: 1},
		{name: "unclean alias", request: func() SelectionRequest { r := base; r.Workspace = "/tmp/../tmp/.beads"; return r }(), platform: func() (bool, bool, error) { return true, false, nil }, embeddedBuild: true, code: CodeWorkspaceShapeUnsupported, reason: ReasonWorkspaceAlias, platformCalls: 1},
		{name: "wrong basename", request: func() SelectionRequest { r := base; r.Workspace = "/tmp/beads"; return r }(), platform: func() (bool, bool, error) { return true, false, nil }, embeddedBuild: true, code: CodeWorkspaceShapeUnsupported, reason: ReasonWorkspaceAlias, platformCalls: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			platformCalls, sourceCalls := 0, 0
			platform := test.platform
			if platform == nil {
				platform = func() (bool, bool, error) { return true, false, nil }
			}
			deps := selectionDependencies{
				platform: func() (bool, bool, error) {
					platformCalls++
					return platform()
				},
				embeddedBuild: test.embeddedBuild,
				observe:       func(string) (shapeObservation, error) { sourceCalls++; return shapeObservation{}, nil },
				bind:          func(string, int64) (sourceWitness, []byte, error) { sourceCalls++; return nil, nil, nil },
				parseMetadata: func([]byte) (*configfile.Config, error) { sourceCalls++; return nil, nil },
				inspectFS: func(sourceWitness) (workspaceidentity.FilesystemSnapshot, error) {
					sourceCalls++
					return workspaceidentity.FilesystemSnapshot{}, nil
				},
				equalFS: func(workspaceidentity.FilesystemSnapshot, workspaceidentity.FilesystemSnapshot) bool {
					sourceCalls++
					return true
				},
				qualifiedFS: func(workspaceidentity.FilesystemSnapshot) bool { sourceCalls++; return true },
			}
			candidate, err := inspectSourceShapeWith(test.request, deps)
			requireRefusal(t, candidate, err, test.code, test.reason)
			if platformCalls != test.platformCalls || sourceCalls != 0 {
				t.Fatalf("platform calls=%d source calls=%d, want %d/0", platformCalls, sourceCalls, test.platformCalls)
			}
			if test.rawCause != nil && errors.Is(err, test.rawCause) {
				t.Fatalf("error=%v exposed raw operational cause", err)
			}
		})
	}
}

func TestInspectSourceShapePrebindShapesRequireStableSecondObservation(t *testing.T) {
	fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
	redirect := fixture.shape.current
	tests := []struct {
		name   string
		shape  shapeObservation
		code   RefusalCode
		reason RefusalReason
	}{
		{name: "physical alias", shape: func() shapeObservation { s := fixture.shape; s.root.canonical += "-alias"; return s }(), code: CodeWorkspaceShapeUnsupported, reason: ReasonWorkspaceAlias},
		{name: "redirect", shape: func() shapeObservation { s := fixture.shape; s.redirect = redirect; return s }(), code: CodeWorkspaceShapeUnsupported, reason: ReasonRedirect},
		{name: "legacy only", shape: func() shapeObservation {
			s := fixture.shape
			s.legacy = s.current
			s.current = observedObject{}
			return s
		}(), code: CodeWorkspaceShapeUnsupported, reason: ReasonLegacyMetadata},
		{name: "shadow legacy", shape: func() shapeObservation { s := fixture.shape; s.legacy = s.current; return s }(), code: CodeWorkspaceShapeUnsupported, reason: ReasonShadowLegacyMetadata},
		{name: "missing metadata", shape: func() shapeObservation { s := fixture.shape; s.current = observedObject{}; return s }(), code: CodeWorkspaceUnverifiable, reason: ReasonMetadata},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps := fixture.dependencies()
			observes, binds := 0, 0
			deps.observe = func(string) (shapeObservation, error) { observes++; return test.shape, nil }
			deps.bind = func(string, int64) (sourceWitness, []byte, error) { binds++; return nil, nil, nil }
			candidate, err := inspectSourceShapeWith(validSelectionRequest(fixture.workspace), deps)
			requireRefusal(t, candidate, err, test.code, test.reason)
			if observes != 2 || binds != 0 {
				t.Fatalf("observes=%d binds=%d, want 2/0", observes, binds)
			}
		})
	}

	t.Run("drift outranks redirect", func(t *testing.T) {
		first := fixture.shape
		first.redirect = redirect
		second := first
		second.provider = observedObject{}
		calls := 0
		deps := fixture.dependencies()
		deps.observe = func(string) (shapeObservation, error) {
			calls++
			if calls == 1 {
				return first, nil
			}
			return second, nil
		}
		candidate, err := inspectSourceShapeWith(validSelectionRequest(fixture.workspace), deps)
		requireRefusal(t, candidate, err, CodeWorkspaceChanged, ReasonWorkspaceObservation)
		if !errors.Is(err, workspaceidentity.ErrChanged) {
			t.Fatalf("drift error=%v, want ErrChanged", err)
		}
	})

	t.Run("second observation failure outranks redirect", func(t *testing.T) {
		first := fixture.shape
		first.redirect = redirect
		calls := 0
		deps := fixture.dependencies()
		deps.observe = func(string) (shapeObservation, error) {
			calls++
			if calls == 1 {
				return first, nil
			}
			return shapeObservation{}, &shapeObservationError{reason: ReasonProvider, cause: os.ErrPermission}
		}
		candidate, err := inspectSourceShapeWith(validSelectionRequest(fixture.workspace), deps)
		requireRefusal(t, candidate, err, CodeWorkspaceUnverifiable, ReasonProvider)
		if !errors.Is(err, os.ErrPermission) {
			t.Fatalf("observation error=%v, want permission cause", err)
		}
	})
}

func TestInspectSourceShapeMetadataAndProviderPrecedence(t *testing.T) {
	absoluteProviderPath := filepath.Join(t.TempDir(), "private-provider")
	tests := []struct {
		name        string
		mutate      func(*configfile.Config)
		mutateShape func(*shapeObservation)
		code        RefusalCode
		reason      RefusalReason
		filesystem  bool
		candidate   bool
	}{
		{name: "candidate", mutate: func(c *configfile.Config) {
			c.Database = configfile.BackendDolt
			c.DoltDatabase = "custom_allowed"
			c.ProjectID = "allowed"
		}, filesystem: true, candidate: true},
		{name: "explicit postgres source before missing provider", mutate: func(c *configfile.Config) { c.Backend = configfile.BackendPostgres; c.DoltMode = "" }, mutateShape: func(s *shapeObservation) { s.provider = observedObject{} }, code: CodePairUnsupported, reason: ReasonSourceBackend},
		{name: "explicit mysql source", mutate: func(c *configfile.Config) { c.Backend = configfile.BackendMySQL; c.DoltMode = "" }, code: CodePairUnsupported, reason: ReasonSourceBackend},
		{name: "explicit sqlite source", mutate: func(c *configfile.Config) { c.Backend = configfile.BackendSQLite; c.DoltMode = "" }, code: CodePairUnsupported, reason: ReasonSourceBackend},
		{name: "absent backend", mutate: func(c *configfile.Config) { c.Backend = "" }, code: CodeWorkspaceShapeUnsupported, reason: ReasonMetadataValues},
		{name: "absent mode", mutate: func(c *configfile.Config) { c.DoltMode = "" }, code: CodeWorkspaceShapeUnsupported, reason: ReasonDoltMode},
		{name: "mixed case mode", mutate: func(c *configfile.Config) { c.DoltMode = "Embedded" }, code: CodeWorkspaceShapeUnsupported, reason: ReasonDoltMode},
		{name: "server mode", mutate: func(c *configfile.Config) { c.DoltMode = configfile.DoltModeServer }, code: CodeWorkspaceShapeUnsupported, reason: ReasonDoltMode},
		{name: "custom provider path", mutate: func(c *configfile.Config) { c.DoltDataDir = "embeddeddolt" }, code: CodeWorkspaceShapeUnsupported, reason: ReasonCustomProviderPath},
		{name: "absolute legacy provider path", mutate: func(c *configfile.Config) { c.Database = absoluteProviderPath }, code: CodeWorkspaceShapeUnsupported, reason: ReasonCustomProviderPath},
		{name: "legacy database value", mutate: func(c *configfile.Config) { c.Database = "beads.db" }, code: CodeWorkspaceShapeUnsupported, reason: ReasonMetadataValues},
		{name: "server host", mutate: func(c *configfile.Config) { c.DoltServerHost = "127.0.0.1" }, code: CodeWorkspaceShapeUnsupported, reason: ReasonServerConfiguration},
		{name: "server port", mutate: func(c *configfile.Config) { c.DoltServerPort = 3307 }, code: CodeWorkspaceShapeUnsupported, reason: ReasonServerConfiguration},
		{name: "server socket", mutate: func(c *configfile.Config) { c.DoltServerSocket = "socket" }, code: CodeWorkspaceShapeUnsupported, reason: ReasonServerConfiguration},
		{name: "server user", mutate: func(c *configfile.Config) { c.DoltServerUser = "root" }, code: CodeWorkspaceShapeUnsupported, reason: ReasonServerConfiguration},
		{name: "server tls", mutate: func(c *configfile.Config) { c.DoltServerTLS = true }, code: CodeWorkspaceShapeUnsupported, reason: ReasonServerConfiguration},
		{name: "remotes api", mutate: func(c *configfile.Config) { c.DoltRemotesAPIPort = 8080 }, code: CodeWorkspaceShapeUnsupported, reason: ReasonServerConfiguration},
		{name: "global database", mutate: func(c *configfile.Config) { c.GlobalDoltDatabase = "global" }, code: CodeWorkspaceShapeUnsupported, reason: ReasonServerConfiguration},
		{name: "global project", mutate: func(c *configfile.Config) { c.GlobalProjectID = "global" }, code: CodeWorkspaceShapeUnsupported, reason: ReasonServerConfiguration},
		{name: "postgres dsn", mutate: func(c *configfile.Config) { c.PostgresDSN = "secret-never-rendered" }, code: CodeWorkspaceShapeUnsupported, reason: ReasonForeignProviderConfiguration},
		{name: "postgres schema", mutate: func(c *configfile.Config) { c.PostgresSchema = "private" }, code: CodeWorkspaceShapeUnsupported, reason: ReasonForeignProviderConfiguration},
		{name: "mysql dsn", mutate: func(c *configfile.Config) { c.MySQLDSN = "secret-never-rendered" }, code: CodeWorkspaceShapeUnsupported, reason: ReasonForeignProviderConfiguration},
		{name: "mysql database", mutate: func(c *configfile.Config) { c.MySQLDatabase = "private" }, code: CodeWorkspaceShapeUnsupported, reason: ReasonForeignProviderConfiguration},
		{name: "sqlite path", mutate: func(c *configfile.Config) { c.SQLitePath = "private.db" }, code: CodeWorkspaceShapeUnsupported, reason: ReasonForeignProviderConfiguration},
		{name: "server artifact", mutateShape: func(s *shapeObservation) { s.artifacts[0] = s.provider }, code: CodeWorkspaceShapeUnsupported, reason: ReasonServerArtifact},
		{name: "missing provider", mutateShape: func(s *shapeObservation) { s.provider = observedObject{} }, code: CodeWorkspaceUnverifiable, reason: ReasonProvider},
		{name: "provider alias", mutateShape: func(s *shapeObservation) { s.provider.canonical += "-alias" }, code: CodeWorkspaceShapeUnsupported, reason: ReasonProviderPath},
		{name: "unsupported filesystem", filesystem: true, code: CodePlatformUnsupported, reason: ReasonFilesystem},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded}
			if test.mutate != nil {
				test.mutate(&cfg)
			}
			fixture := newSelectionFixture(t, cfg)
			if test.mutateShape != nil {
				test.mutateShape(&fixture.shape)
			}
			deps := fixture.dependencies()
			if test.filesystem && !test.candidate {
				deps.qualifiedFS = func(workspaceidentity.FilesystemSnapshot) bool { return false }
			}
			candidate, err := inspectSourceShapeWith(validSelectionRequest(fixture.workspace), deps)
			if test.candidate {
				want := SourceShapeCandidate{SourceBackend: configfile.BackendDolt, TargetBackend: configfile.BackendPostgres}
				if err != nil || candidate != want {
					t.Fatalf("candidate=%#v err=%v, want %#v", candidate, err, want)
				}
			} else {
				requireRefusal(t, candidate, err, test.code, test.reason)
			}
			if fixture.observes != 3 || fixture.binds != 1 || fixture.parses != 1 || fixture.witness.closes != 1 || fixture.witness.revalidateAt != 4 {
				t.Fatalf("observes=%d binds=%d parses=%d revalidates=%d closes=%d", fixture.observes, fixture.binds, fixture.parses, fixture.witness.revalidateAt, fixture.witness.closes)
			}
			wantFilesystem := 0
			if test.filesystem {
				wantFilesystem = 2
			}
			if fixture.filesystems != wantFilesystem {
				t.Fatalf("filesystem calls=%d, want %d", fixture.filesystems, wantFilesystem)
			}
			if err != nil && (strings.Contains(err.Error(), fixture.workspace) || strings.Contains(err.Error(), "secret-never-rendered")) {
				t.Fatalf("safe refusal leaked dynamic text: %q", err)
			}
		})
	}
}

func TestInspectSourceShapePostBindDriftAndCleanupPrecedence(t *testing.T) {
	newFixture := func(t *testing.T) *selectionFixture {
		t.Helper()
		return newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeServer})
	}

	t.Run("final shape drift replaces static refusal", func(t *testing.T) {
		fixture := newFixture(t)
		deps := fixture.dependencies()
		calls := 0
		deps.observe = func(string) (shapeObservation, error) {
			calls++
			shape := fixture.shape
			if calls == 3 {
				shape.provider = observedObject{}
			}
			return shape, nil
		}
		candidate, err := inspectSourceShapeWith(validSelectionRequest(fixture.workspace), deps)
		requireRefusal(t, candidate, err, CodeWorkspaceChanged, ReasonWorkspaceObservation)
		if !errors.Is(err, workspaceidentity.ErrChanged) {
			t.Fatalf("shape drift error=%v, want ErrChanged", err)
		}
	})

	t.Run("authoritative shape drift replaces static refusal", func(t *testing.T) {
		fixture := newFixture(t)
		deps := fixture.dependencies()
		calls := 0
		deps.observe = func(string) (shapeObservation, error) {
			calls++
			shape := fixture.shape
			if calls == 2 {
				shape.provider = observedObject{}
			}
			return shape, nil
		}
		candidate, err := inspectSourceShapeWith(validSelectionRequest(fixture.workspace), deps)
		requireRefusal(t, candidate, err, CodeWorkspaceChanged, ReasonWorkspaceObservation)
		if !errors.Is(err, workspaceidentity.ErrChanged) || fixture.witness.closes != 1 {
			t.Fatalf("authoritative drift error=%v closes=%d", err, fixture.witness.closes)
		}
	})

	t.Run("witness drift replaces static refusal", func(t *testing.T) {
		fixture := newFixture(t)
		fixture.witness.revalidations = []error{nil, nil, workspaceidentity.ErrChanged}
		candidate, err := inspectSourceShapeWith(validSelectionRequest(fixture.workspace), fixture.dependencies())
		requireRefusal(t, candidate, err, CodeWorkspaceChanged, ReasonWorkspaceObservation)
		if !errors.Is(err, workspaceidentity.ErrChanged) {
			t.Fatalf("witness drift error=%v, want ErrChanged", err)
		}
	})

	t.Run("cleanup is sole primary and preserves drift", func(t *testing.T) {
		fixture := newFixture(t)
		fixture.witness.revalidations = []error{nil, nil, workspaceidentity.ErrChanged}
		fixture.witness.closeErr = errors.Join(workspaceidentity.ErrCleanup, workspaceidentity.ErrUnverifiable)
		candidate, err := inspectSourceShapeWith(validSelectionRequest(fixture.workspace), fixture.dependencies())
		requireRefusal(t, candidate, err, CodeWorkspaceUnverifiable, ReasonCleanup)
		if !errors.Is(err, workspaceidentity.ErrCleanup) || !errors.Is(err, workspaceidentity.ErrChanged) {
			t.Fatalf("cleanup error=%v, want cleanup and displaced drift causes", err)
		}
		var first *Refusal
		if !errors.As(err, &first) || first.Code != CodeWorkspaceUnverifiable {
			t.Fatalf("cleanup primary=%#v", first)
		}
	})

	t.Run("bind cleanup maps directly to cleanup", func(t *testing.T) {
		fixture := newFixture(t)
		deps := fixture.dependencies()
		deps.bind = func(string, int64) (sourceWitness, []byte, error) {
			return nil, nil, errors.Join(workspaceidentity.ErrCleanup, workspaceidentity.ErrChanged)
		}
		candidate, err := inspectSourceShapeWith(validSelectionRequest(fixture.workspace), deps)
		requireRefusal(t, candidate, err, CodeWorkspaceUnverifiable, ReasonCleanup)
		if !errors.Is(err, workspaceidentity.ErrCleanup) || !errors.Is(err, workspaceidentity.ErrChanged) {
			t.Fatalf("bind cleanup error=%v, want both causes", err)
		}
	})

	for _, state := range []struct {
		name      string
		qualified bool
	}{
		{name: "qualified to qualified filesystem drift", qualified: true},
		{name: "unsupported to unsupported filesystem drift", qualified: false},
	} {
		t.Run(state.name, func(t *testing.T) {
			fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
			deps := fixture.dependencies()
			deps.qualifiedFS = func(workspaceidentity.FilesystemSnapshot) bool { return state.qualified }
			deps.equalFS = func(left, right workspaceidentity.FilesystemSnapshot) bool {
				if deps.qualifiedFS(left) != state.qualified || deps.qualifiedFS(right) != state.qualified {
					t.Fatalf("filesystem state changed before equality comparison")
				}
				return false
			}
			candidate, err := inspectSourceShapeWith(validSelectionRequest(fixture.workspace), deps)
			requireRefusal(t, candidate, err, CodeWorkspaceChanged, ReasonWorkspaceObservation)
			if !errors.Is(err, workspaceidentity.ErrChanged) {
				t.Fatalf("filesystem drift error=%v, want ErrChanged", err)
			}
		})
	}

	t.Run("filesystem cleanup never becomes probe failure", func(t *testing.T) {
		fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
		deps := fixture.dependencies()
		deps.inspectFS = func(sourceWitness) (workspaceidentity.FilesystemSnapshot, error) {
			return workspaceidentity.FilesystemSnapshot{}, errors.Join(workspaceidentity.ErrCleanup, workspaceidentity.ErrUnverifiable)
		}
		candidate, err := inspectSourceShapeWith(validSelectionRequest(fixture.workspace), deps)
		requireRefusal(t, candidate, err, CodeWorkspaceUnverifiable, ReasonCleanup)
		if !errors.Is(err, workspaceidentity.ErrCleanup) {
			t.Fatalf("filesystem cleanup error=%v", err)
		}
	})
}

func TestInspectSourceShapeEveryPostBindRevalidationFrontier(t *testing.T) {
	for frontier := 1; frontier <= 4; frontier++ {
		for _, failure := range []struct {
			name   string
			err    error
			code   RefusalCode
			reason RefusalReason
		}{
			{name: "changed", err: workspaceidentity.ErrChanged, code: CodeWorkspaceChanged, reason: ReasonWorkspaceObservation},
			{name: "unverifiable", err: workspaceidentity.ErrUnverifiable, code: CodeWorkspaceUnverifiable, reason: ReasonMetadata},
		} {
			t.Run(fmt.Sprintf("frontier_%d_%s", frontier, failure.name), func(t *testing.T) {
				fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
				fixture.witness.revalidations = make([]error, frontier)
				fixture.witness.revalidations[frontier-1] = failure.err
				candidate, err := inspectSourceShapeWith(validSelectionRequest(fixture.workspace), fixture.dependencies())
				requireRefusal(t, candidate, err, failure.code, failure.reason)
				if !errors.Is(err, failure.err) || fixture.witness.closes != 1 {
					t.Fatalf("frontier error=%v closes=%d, want cause and one close", err, fixture.witness.closes)
				}
			})
		}
	}
}

func TestInspectSourceShapeEveryPostBindObservationAndProbeFrontier(t *testing.T) {
	for _, observeFrontier := range []int{2, 3} {
		t.Run(fmt.Sprintf("observation_%d", observeFrontier), func(t *testing.T) {
			fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
			deps := fixture.dependencies()
			calls := 0
			deps.observe = func(string) (shapeObservation, error) {
				calls++
				if calls == observeFrontier {
					return shapeObservation{}, &shapeObservationError{reason: ReasonProvider, cause: os.ErrPermission}
				}
				return fixture.shape, nil
			}
			candidate, err := inspectSourceShapeWith(validSelectionRequest(fixture.workspace), deps)
			requireRefusal(t, candidate, err, CodeWorkspaceUnverifiable, ReasonProvider)
			if !errors.Is(err, os.ErrPermission) || fixture.witness.closes != 1 {
				t.Fatalf("observation error=%v closes=%d", err, fixture.witness.closes)
			}
		})
	}

	for _, probeFrontier := range []int{1, 2} {
		for _, failure := range []struct {
			name   string
			err    error
			reason RefusalReason
		}{
			{name: "probe", err: &fakeFilesystemProbeFailure{cause: workspaceidentity.ErrUnverifiable}, reason: ReasonFilesystemProbe},
			{name: "witness revalidation", err: workspaceidentity.ErrUnverifiable, reason: ReasonMetadata},
		} {
			t.Run(fmt.Sprintf("filesystem_%d_%s", probeFrontier, failure.name), func(t *testing.T) {
				fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
				deps := fixture.dependencies()
				calls := 0
				deps.inspectFS = func(sourceWitness) (workspaceidentity.FilesystemSnapshot, error) {
					calls++
					if calls == probeFrontier {
						return workspaceidentity.FilesystemSnapshot{}, failure.err
					}
					return workspaceidentity.FilesystemSnapshot{}, nil
				}
				candidate, err := inspectSourceShapeWith(validSelectionRequest(fixture.workspace), deps)
				requireRefusal(t, candidate, err, CodeWorkspaceUnverifiable, failure.reason)
				if !errors.Is(err, workspaceidentity.ErrUnverifiable) || fixture.witness.closes != 1 {
					t.Fatalf("filesystem error=%v closes=%d", err, fixture.witness.closes)
				}
			})
		}
	}
}

func TestInspectSourceShapeStrictParseFailureStillStabilizes(t *testing.T) {
	fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
	fixture.metadata = []byte(`{"backend":"dolt","unknown":"private"}`)
	candidate, err := inspectSourceShapeWith(validSelectionRequest(fixture.workspace), fixture.dependencies())
	requireRefusal(t, candidate, err, CodeWorkspaceUnverifiable, ReasonMetadata)
	if fixture.observes != 3 || fixture.witness.revalidateAt != 4 || fixture.witness.closes != 1 {
		t.Fatalf("parse failure observes=%d revalidates=%d closes=%d", fixture.observes, fixture.witness.revalidateAt, fixture.witness.closes)
	}
}

func TestRefusalExposesOnlySafeSentinelIdentities(t *testing.T) {
	raw := errors.New("private operational text")
	err := refusal(CodeWorkspaceUnverifiable, ReasonMetadata, false,
		errors.Join(fmt.Errorf("secret metadata value: %w", os.ErrPermission), raw))
	if err.Error() != string(CodeWorkspaceUnverifiable) || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("safe refusal=%v permission=%v", err, errors.Is(err, os.ErrPermission))
	}
	if errors.Is(err, raw) || errors.Unwrap(err) != nil {
		t.Fatalf("raw cause remains accessible: is=%v unwrap=%v", errors.Is(err, raw), errors.Unwrap(err))
	}
	for _, rendered := range []string{fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
		if strings.Contains(rendered, "secret metadata value") || strings.Contains(rendered, "private operational text") {
			t.Fatalf("refusal rendering leaked raw cause: %q", rendered)
		}
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		t.Fatalf("raw typed cause escaped through errors.As: %#v", pathErr)
	}

	fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
	fixture.metadata = []byte(`{"backend":"dolt","super_secret_metadata_field":"private-value"}`)
	_, inspectErr := inspectSourceShapeWith(validSelectionRequest(fixture.workspace), fixture.dependencies())
	if inspectErr == nil || errors.Unwrap(inspectErr) != nil ||
		strings.Contains(fmt.Sprintf("%+v", inspectErr), "super_secret") || strings.Contains(fmt.Sprintf("%#v", inspectErr), "private-value") {
		t.Fatalf("strict-parse refusal exposed raw chain: %+v / %#v", inspectErr, inspectErr)
	}
}

func TestInspectSourceShapeCandidateHasNoAmbientOrFilesystemEffects(t *testing.T) {
	fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
	beforeMetadata, err := os.ReadFile(filepath.Join(fixture.workspace, configfile.ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	beforeEntries, err := os.ReadDir(fixture.workspace)
	if err != nil {
		t.Fatal(err)
	}
	credentialCanary := filepath.Join(t.TempDir(), "credential-helper-ran")
	t.Setenv("BEADS_DOLT_CREDENTIAL_COMMAND", fmt.Sprintf("touch %s", credentialCanary))
	environmentWithCanary := append([]string(nil), os.Environ()...)

	candidate, err := inspectSourceShapeWith(validSelectionRequest(fixture.workspace), fixture.dependencies())
	if err != nil || candidate != (SourceShapeCandidate{SourceBackend: configfile.BackendDolt, TargetBackend: configfile.BackendPostgres}) {
		t.Fatalf("candidate=%#v err=%v", candidate, err)
	}
	afterMetadata, readErr := os.ReadFile(filepath.Join(fixture.workspace, configfile.ConfigFileName))
	afterEntries, dirErr := os.ReadDir(fixture.workspace)
	if readErr != nil || dirErr != nil || !reflect.DeepEqual(beforeMetadata, afterMetadata) || entryNames(beforeEntries) != entryNames(afterEntries) {
		t.Fatalf("workspace changed: read=%v dir=%v bytes_equal=%v before=%q after=%q", readErr, dirErr, reflect.DeepEqual(beforeMetadata, afterMetadata), entryNames(beforeEntries), entryNames(afterEntries))
	}
	if !reflect.DeepEqual(environmentWithCanary, os.Environ()) {
		t.Fatal("inspection mutated the environment")
	}
	if _, statErr := os.Stat(credentialCanary); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("credential helper canary was touched: %v", statErr)
	}
}

func entryNames(entries []os.DirEntry) string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return strings.Join(names, "\x00")
}

func digestTree(t *testing.T, root string) [sha256.Size]byte {
	t.Helper()
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%q %v %d %d\x00", relative, info.Mode(), info.Size(), info.ModTime().UnixNano())
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(hash, "link:%q\x00", target)
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			hash.Write(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}

func TestInspectSourceShapeRejectsUnsafeFilesystemObjects(t *testing.T) {
	requireNativeLinuxEmbeddedBuild(t)
	baseConfig := []byte(`{"backend":"dolt","dolt_mode":"embedded"}`)
	t.Run("root symlink", func(t *testing.T) {
		parent := t.TempDir()
		target := filepath.Join(parent, "target", ".beads")
		if err := os.MkdirAll(target, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(parent, ".beads")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		candidate, err := InspectSourceShape(validSelectionRequest(link))
		requireRefusal(t, candidate, err, CodeWorkspaceUnverifiable, ReasonWorkspace)
	})

	t.Run("ancestor alias", func(t *testing.T) {
		parent := t.TempDir()
		realParent := filepath.Join(parent, "real")
		workspace := filepath.Join(realParent, ".beads")
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		aliasParent := filepath.Join(parent, "alias")
		if err := os.Symlink(realParent, aliasParent); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		candidate, err := InspectSourceShape(validSelectionRequest(filepath.Join(aliasParent, ".beads")))
		requireRefusal(t, candidate, err, CodeWorkspaceShapeUnsupported, ReasonWorkspaceAlias)
	})

	t.Run("hardlinked metadata", func(t *testing.T) {
		workspace := filepath.Join(t.TempDir(), ".beads")
		if err := os.Mkdir(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		metadata := filepath.Join(workspace, configfile.ConfigFileName)
		if err := os.WriteFile(metadata, baseConfig, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(metadata, filepath.Join(t.TempDir(), "metadata-link")); err != nil {
			t.Skipf("hard links unavailable: %v", err)
		}
		candidate, err := InspectSourceShape(validSelectionRequest(workspace))
		requireRefusal(t, candidate, err, CodeWorkspaceUnverifiable, ReasonMetadata)
	})

	t.Run("provider symlink", func(t *testing.T) {
		workspace := filepath.Join(t.TempDir(), ".beads")
		if err := os.Mkdir(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, configfile.ConfigFileName), baseConfig, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), filepath.Join(workspace, "embeddeddolt")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		candidate, err := InspectSourceShape(validSelectionRequest(workspace))
		requireRefusal(t, candidate, err, CodeWorkspaceUnverifiable, ReasonProvider)
	})

	t.Run("unsafe server artifact", func(t *testing.T) {
		workspace := filepath.Join(t.TempDir(), ".beads")
		if err := os.MkdirAll(filepath.Join(workspace, "embeddeddolt"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, configfile.ConfigFileName), baseConfig, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("private-target", filepath.Join(workspace, "dolt-server.pid")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		candidate, err := InspectSourceShape(validSelectionRequest(workspace))
		requireRefusal(t, candidate, err, CodeWorkspaceUnverifiable, ReasonWorkspace)
	})

	t.Run("unsafe workspace artifact precedes unsafe metadata", func(t *testing.T) {
		workspace := filepath.Join(t.TempDir(), ".beads")
		if err := os.Mkdir(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("private-metadata", filepath.Join(workspace, configfile.ConfigFileName)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if err := os.Symlink("private-artifact", filepath.Join(workspace, "dolt-server.pid")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		candidate, err := InspectSourceShape(validSelectionRequest(workspace))
		requireRefusal(t, candidate, err, CodeWorkspaceUnverifiable, ReasonWorkspace)
	})
}

func requireNativeLinuxEmbeddedBuild(t *testing.T) {
	t.Helper()
	native, wsl, err := probeNativeLinux()
	if err != nil || !native || wsl || !embeddedBuildCapable {
		t.Skipf("requires native Linux embedded build: native=%v wsl=%v err=%v embedded=%v", native, wsl, err, embeddedBuildCapable)
	}
}
