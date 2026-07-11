package sqlite

// The modernc.org/sqlite + modernc.org/libc version pins are LOAD-BEARING for
// multiprocess safety. Do not weaken or delete these guards.
//
//   - modernc.org/sqlite < v1.46.2 ships a WAL-reset bug that corrupts the
//     database under multiprocess write contention (fixed upstream in SQLite
//     3.51.3, first bundled in modernc v1.46.2). The maintainer-city infra
//     store runs many concurrent bd processes against one beads.db, so a
//     silent downgrade below that version reintroduces real data corruption.
//   - modernc.org/sqlite transpiles against ONE exact modernc.org/libc
//     version; mixing a different libc than the one sqlite's own go.mod
//     requires is an unsupported pairing (upstream treats it as UB). The two
//     pins must move in lockstep.
//
// Context: the 2026-07 bd sqlite concurrency audit proved the current pairing
// safe across ~5,900 real cross-process ops; these tests keep that safety case
// from silently regressing via a routine dependency bump or downgrade.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// moderncSqliteMinVersion is the oldest modernc.org/sqlite allowed in go.mod:
// the first release bundling SQLite's WAL-reset multiprocess corruption fix.
const moderncSqliteMinVersion = "v1.46.2"

// checkModerncPins validates the load-bearing dependency pairing:
// sqliteVer >= moderncSqliteMinVersion, and the repo's libc pin (repoLibcVer)
// exactly matches the libc version sqlite's own go.mod requires
// (sqliteRequiredLibcVer).
func checkModerncPins(sqliteVer, repoLibcVer, sqliteRequiredLibcVer string) error {
	if !semver.IsValid(sqliteVer) {
		return fmt.Errorf("modernc.org/sqlite version %q is not valid semver", sqliteVer)
	}
	if semver.Compare(sqliteVer, moderncSqliteMinVersion) < 0 {
		return fmt.Errorf("modernc.org/sqlite %s is older than %s, the first release with the WAL-reset multiprocess corruption fix (SQLite 3.51.3); downgrading reintroduces database corruption under concurrent bd processes", sqliteVer, moderncSqliteMinVersion)
	}
	if repoLibcVer != sqliteRequiredLibcVer {
		return fmt.Errorf("modernc.org/libc %s does not match the %s that modernc.org/sqlite %s requires; sqlite is transpiled against exactly one libc version and the pair must move in lockstep", repoLibcVer, sqliteRequiredLibcVer, sqliteVer)
	}
	return nil
}

// TestCheckModerncPinsRejectsViolations proves the guard actually bites: a
// too-old sqlite, an invalid version, and a libc/sqlite mismatch must each be
// rejected, and the known-safe audited pairing must pass.
func TestCheckModerncPinsRejectsViolations(t *testing.T) {
	cases := []struct {
		name                          string
		sqliteVer, libcVer, wantsLibc string
		wantErr                       bool
	}{
		{"audited pairing passes", "v1.53.0", "v1.73.4", "v1.73.4", false},
		{"minimum version itself passes", moderncSqliteMinVersion, "v1.61.0", "v1.61.0", false},
		{"pre-WAL-fix sqlite rejected", "v1.46.1", "v1.61.0", "v1.61.0", true},
		{"ancient sqlite rejected", "v1.29.0", "v1.41.0", "v1.41.0", true},
		{"invalid semver rejected", "1.53.0", "v1.73.4", "v1.73.4", true},
		{"libc out of lockstep rejected", "v1.53.0", "v1.73.3", "v1.73.4", true},
		{"libc ahead of lockstep rejected", "v1.53.0", "v1.74.0", "v1.73.4", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkModerncPins(tc.sqliteVer, tc.libcVer, tc.wantsLibc)
			if tc.wantErr && err == nil {
				t.Errorf("checkModerncPins(%q, %q, %q) = nil, want error", tc.sqliteVer, tc.libcVer, tc.wantsLibc)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("checkModerncPins(%q, %q, %q) = %v, want nil", tc.sqliteVer, tc.libcVer, tc.wantsLibc, err)
			}
		})
	}
}

// TestModerncVersionPins enforces the pins against the REAL go.mod: it fails
// any change that drops modernc.org/sqlite below the WAL-fix floor or lets
// modernc.org/libc drift out of lockstep with the version sqlite requires
// (read from sqlite's own go.mod in the module cache).
func TestModerncVersionPins(t *testing.T) {
	repoMod := parseGoMod(t, filepath.Join(repoRootDir(), "go.mod"))
	sqliteVer := requireVersion(t, repoMod, "modernc.org/sqlite")
	libcVer := requireVersion(t, repoMod, "modernc.org/libc")

	sqliteGoMod := parseGoMod(t, sqliteModuleGoModPath(t))
	requiredLibc := requireVersion(t, sqliteGoMod, "modernc.org/libc")

	if err := checkModerncPins(sqliteVer, libcVer, requiredLibc); err != nil {
		t.Errorf("go.mod modernc pins violate the multiprocess-safety floor: %v", err)
	}
}

// repoRootDir locates the repository root from this file's position
// (<root>/internal/storage/sqlite).
func repoRootDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
}

func parseGoMod(t *testing.T, path string) *modfile.File {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	f, err := modfile.Parse(path, data, nil)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return f
}

func requireVersion(t *testing.T, f *modfile.File, module string) string {
	t.Helper()
	for _, r := range f.Require {
		if r.Mod.Path == module {
			return r.Mod.Version
		}
	}
	t.Fatalf("%s: no require line for %s", f.Syntax.Name, module)
	return ""
}

// sqliteModuleGoModPath resolves the on-disk go.mod of the modernc.org/sqlite
// version the main module selects, via `go mod download -json` (cache-first;
// in any environment that can build bd the module is already downloaded).
func sqliteModuleGoModPath(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("go", "mod", "download", "-json", "modernc.org/sqlite")
	cmd.Dir = repoRootDir()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go mod download -json modernc.org/sqlite: %v", err)
	}
	var info struct {
		GoMod string `json:"GoMod"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		t.Fatalf("parse go mod download output: %v\n%s", err, out)
	}
	if info.GoMod == "" {
		t.Fatalf("go mod download reported no GoMod path:\n%s", out)
	}
	return info.GoMod
}
