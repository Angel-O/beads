//go:build android || darwin || ios || linux || windows

package main

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/workspacestate"
)

func TestInspectBackendWorkspaceSnapshotStableStates(t *testing.T) {
	t.Run("unambiguous metadata skips unrelated evidence", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".beads")
		writeOwnershipMetadata(t, dir, configfile.Config{Backend: configfile.BackendPostgres})
		if err := os.Mkdir(filepath.Join(dir, "dolt"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "beads.db"), []byte("invalid"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := inspectBackendWorkspaceSnapshot(dir)
		if err != nil || got == nil || got.state.backend != configfile.BackendPostgres || !got.state.initialized || got.state.localInspected {
			t.Fatalf("snapshot=%#v err=%v, want unprobed persisted Postgres", got, err)
		}
	})

	for _, test := range []struct {
		name      string
		setup     func(*testing.T, string)
		wantLocal workspacestate.LocalState
		want      string
	}{
		{name: "bare SQLite absent", setup: func(*testing.T, string) {}, want: configfile.BackendSQLite},
		{name: "bare SQLite normalized to Dolt", setup: func(t *testing.T, dir string) {
			writeBackendObserverDolt(t, dir)
		}, wantLocal: workspacestate.LocalState{Backend: configfile.BackendDolt, Initialized: true}, want: configfile.BackendDolt},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), ".beads")
			writeOwnershipMetadata(t, dir, configfile.Config{Backend: configfile.BackendSQLite})
			test.setup(t, dir)
			got, err := inspectBackendWorkspaceSnapshot(dir)
			if err != nil || got == nil || !got.state.localInspected || got.state.backend != test.want || got.state.local != test.wantLocal {
				t.Fatalf("snapshot=%#v err=%v, want backend=%q local=%#v", got, err, test.want, test.wantLocal)
			}
		})
	}
	t.Run("redirected structural custom SQLite", func(t *testing.T) {
		root := t.TempDir()
		source, target := filepath.Join(root, "source", ".beads"), filepath.Join(root, "target")
		selector := filepath.Join(source, "custom", "issues.db")
		makeOwnershipDirectory(t, filepath.Dir(selector))
		makeOwnershipDirectory(t, target)
		writeOwnershipRedirect(t, source, target)
		writeBackendObserverSQLite(t, filepath.Join(target, "custom", "issues.db"))
		got, err := inspectBackendWorkspaceSnapshot(selector)
		want := workspacestate.LocalState{Backend: configfile.BackendSQLite, Initialized: true}
		if err != nil || got == nil || got.route.lane != backendWorkspaceLaneStructural || got.route.source.path != source ||
			got.route.target.path != target || !got.state.localInspected || got.state.local != want {
			t.Fatalf("snapshot=%#v err=%v, want routed structural SQLite", got, err)
		}
	})

	t.Run("redirected missing nested SQLite path is verified absent", func(t *testing.T) {
		root := t.TempDir()
		source, target := filepath.Join(root, "source", ".beads"), filepath.Join(root, "target")
		selector := filepath.Join(source, "custom", "nested", "issues.db")
		makeOwnershipDirectory(t, filepath.Dir(selector))
		makeOwnershipDirectory(t, target)
		writeOwnershipRedirect(t, source, target)
		got, err := inspectBackendWorkspaceSnapshot(selector)
		if err != nil || got == nil || !got.state.localInspected || got.state.local != (workspacestate.LocalState{}) ||
			got.route.mappedSQLite != filepath.Join(target, "custom", "nested", "issues.db") {
			t.Fatalf("snapshot=%#v err=%v, want verified absent nested SQLite path", got, err)
		}
	})

	t.Run("automatic hints stay disabled", func(t *testing.T) {
		selector := filepath.Join(t.TempDir(), "external", "data")
		makeOwnershipDirectory(t, selector)
		ambient := filepath.Join(t.TempDir(), ".beads")
		writeOwnershipMetadata(t, ambient, configfile.Config{Backend: configfile.BackendDolt, DoltDataDir: filepath.Dir(selector)})
		t.Setenv("BEADS_DIR", ambient)
		if got, err := inspectBackendWorkspaceSnapshot(selector); err != nil || got != nil {
			t.Fatalf("snapshot=%#v err=%v, want ambient owner ignored", got, err)
		}
	})

}

func TestObserveBackendWorkspaceSnapshotDetectsDrift(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T) (string, func())
	}{
		{name: "SQLite path", setup: func(t *testing.T) (string, func()) {
			dir := filepath.Join(t.TempDir(), ".beads")
			writeOwnershipMetadata(t, dir, configfile.Config{Backend: configfile.BackendSQLite, SQLitePath: "a.db"})
			return dir, func() {
				writeOwnershipMetadata(t, dir, configfile.Config{Backend: configfile.BackendSQLite, SQLitePath: "b.db"})
			}
		}},
		{name: "Dolt data path", setup: func(t *testing.T) (string, func()) {
			dir := filepath.Join(t.TempDir(), ".beads")
			makeOwnershipDirectory(t, filepath.Join(dir, "a"))
			makeOwnershipDirectory(t, filepath.Join(dir, "b"))
			writeOwnershipMetadata(t, dir, configfile.Config{Backend: configfile.BackendDolt, DoltDataDir: "a"})
			return dir, func() {
				writeOwnershipMetadata(t, dir, configfile.Config{Backend: configfile.BackendDolt, DoltDataDir: "b"})
			}
		}},
		{name: "same-byte metadata replacement", setup: func(t *testing.T) (string, func()) {
			dir := filepath.Join(t.TempDir(), ".beads")
			writeOwnershipMetadata(t, dir, configfile.Config{Backend: configfile.BackendDolt})
			return dir, func() { replaceBackendObserverFile(t, configfile.ConfigPath(dir)) }
		}},
		{name: "redirect retarget", setup: redirectBackendObserverDrift(func(t *testing.T, source, _, second string) {
			writeOwnershipRedirect(t, source, second)
		})},
		{name: "redirect removal", setup: redirectBackendObserverDrift(func(t *testing.T, source, _, _ string) {
			if err := os.Remove(filepath.Join(source, "redirect")); err != nil {
				t.Fatal(err)
			}
		})},
		{name: "redirect addition", setup: func(t *testing.T) (string, func()) {
			root := t.TempDir()
			source, target := filepath.Join(root, ".beads"), filepath.Join(root, "target")
			makeOwnershipDirectory(t, source)
			makeOwnershipDirectory(t, target)
			return source, func() { writeOwnershipRedirect(t, source, target) }
		}},
		{name: "evidence absent to SQLite", setup: func(t *testing.T) (string, func()) {
			dir := filepath.Join(t.TempDir(), ".beads")
			writeOwnershipMetadata(t, dir, configfile.Config{Backend: configfile.BackendSQLite})
			return dir, func() { writeBackendObserverSQLite(t, filepath.Join(dir, "beads.db")) }
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			selector, mutate := test.setup(t)
			calls := 0
			got, err := stabilizeBackendWorkspaceSnapshot(func() (*backendWorkspaceSnapshot, error) {
				calls++
				if calls == 2 {
					mutate()
				}
				return observeBackendWorkspaceOnce(selector)
			})
			if got != nil || !errors.Is(err, errBackendWorkspaceChangedDuringInspection) || calls != 2 {
				t.Fatalf("snapshot=%#v err=%v calls=%d, want drift error", got, err, calls)
			}
		})
	}
}

func TestInspectBackendWorkspaceSnapshotRejectsUnsafeState(t *testing.T) {
	t.Run("empty selector cannot become CWD", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), ".beads")
		writeOwnershipMetadata(t, dir, configfile.Config{Backend: configfile.BackendDolt})
		t.Chdir(dir)
		if got, err := inspectBackendWorkspaceSnapshot(""); got != nil || err == nil {
			t.Fatalf("snapshot=%#v err=%v, want empty-selector error", got, err)
		}
	})

	for _, test := range []struct {
		name  string
		setup func(*testing.T, string, string)
	}{
		{name: "mapped ancestor symlink escapes", setup: func(t *testing.T, target, outside string) {
			writeBackendObserverSQLite(t, filepath.Join(outside, "issues.db"))
			if err := os.Symlink(outside, filepath.Join(target, "custom")); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
		{name: "mapped final symlink stays inside", setup: func(t *testing.T, target, _ string) {
			real := filepath.Join(target, "custom", "real.db")
			writeBackendObserverSQLite(t, real)
			if err := os.Symlink(real, filepath.Join(target, "custom", "issues.db")); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			source, target, outside := filepath.Join(root, "source", ".beads"), filepath.Join(root, "target"), filepath.Join(root, "outside")
			selector := filepath.Join(source, "custom", "issues.db")
			makeOwnershipDirectory(t, filepath.Dir(selector))
			makeOwnershipDirectory(t, target)
			writeOwnershipRedirect(t, source, target)
			test.setup(t, target, outside)
			if got, err := inspectBackendWorkspaceSnapshot(selector); got != nil || err == nil {
				t.Fatalf("snapshot=%#v err=%v, want mapped-symlink error", got, err)
			}
		})
	}

	selector := filepath.Join(t.TempDir(), "data")
	makeOwnershipDirectory(t, selector)
	if got, err := inspectBackendWorkspaceSnapshot(selector); err != nil || got != nil {
		t.Fatalf("snapshot=%#v err=%v, want nil, nil", got, err)
	}
}

func redirectBackendObserverDrift(mutate func(*testing.T, string, string, string)) func(*testing.T) (string, func()) {
	return func(t *testing.T) (string, func()) {
		root := t.TempDir()
		source, first, second := filepath.Join(root, ".beads"), filepath.Join(root, "first"), filepath.Join(root, "second")
		makeOwnershipDirectory(t, source)
		makeOwnershipDirectory(t, first)
		makeOwnershipDirectory(t, second)
		writeOwnershipRedirect(t, source, first)
		return source, func() { mutate(t, source, first, second) }
	}
}

func replaceBackendObserverFile(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(filepath.Dir(path), "replacement.json")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func writeBackendObserverDolt(t *testing.T, dir string) {
	makeOwnershipDirectory(t, filepath.Join(dir, "embeddeddolt", "beads", ".dolt"))
}

func writeBackendObserverSQLite(t *testing.T, path string) {
	t.Helper()
	makeOwnershipDirectory(t, filepath.Dir(path))
	data := make([]byte, 512)
	copy(data, "SQLite format 3\x00")
	binary.BigEndian.PutUint16(data[16:18], 512)
	data[18], data[19], data[21], data[22], data[23] = 1, 1, 64, 32, 32
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
