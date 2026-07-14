package configfile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/safefile"
)

func TestLoadReadOnlyDoesNotMigrateLegacyConfig(t *testing.T) {
	beadsDir := t.TempDir()
	legacyPath := filepath.Join(beadsDir, "config.json")
	legacyBytes := []byte("{\n  \"database\": \"dolt\",\n  \"backend\": \"dolt\"\n}\n")
	if err := os.WriteFile(legacyPath, legacyBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadReadOnly(beadsDir)
	if err != nil {
		t.Fatalf("LoadReadOnly: %v", err)
	}
	if cfg == nil || cfg.GetBackend() != BackendDolt {
		t.Fatalf("loaded config = %#v, want legacy Dolt", cfg)
	}
	if _, err := os.Stat(ConfigPath(beadsDir)); !os.IsNotExist(err) {
		t.Fatalf("LoadReadOnly created metadata.json: %v", err)
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, legacyBytes) {
		t.Fatalf("LoadReadOnly changed legacy bytes: got %q want %q", after, legacyBytes)
	}
}

func TestLoadReadOnlyPrefersCurrentMetadataWithoutMutation(t *testing.T) {
	beadsDir := t.TempDir()
	currentPath := ConfigPath(beadsDir)
	legacyPath := filepath.Join(beadsDir, "config.json")
	currentBytes := []byte("{\n  \"database\": \"beads.db\",\n  \"backend\": \"sqlite\",\n  \"sqlite_path\": \"beads.db\"\n}\n")
	legacyBytes := []byte("{\n  \"database\": \"dolt\",\n  \"backend\": \"dolt\"\n}\n")
	if err := os.WriteFile(currentPath, currentBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, legacyBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadReadOnly(beadsDir)
	if err != nil {
		t.Fatalf("LoadReadOnly: %v", err)
	}
	if cfg == nil || cfg.GetBackend() != BackendSQLite {
		t.Fatalf("loaded config = %#v, want current SQLite metadata", cfg)
	}
	for path, want := range map[string][]byte{currentPath: currentBytes, legacyPath: legacyBytes} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("LoadReadOnly changed %s: got %q err=%v want %q", path, got, err, want)
		}
	}
}

func TestReadOnlySnapshotEqualWhenMetadataIsUnchanged(t *testing.T) {
	beadsDir := t.TempDir()
	if err := os.WriteFile(ConfigPath(beadsDir), []byte(`{"database":"dolt","backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	firstConfig, first, err := LoadReadOnlySnapshot(beadsDir)
	if err != nil {
		t.Fatalf("first LoadReadOnlySnapshot: %v", err)
	}
	secondConfig, second, err := LoadReadOnlySnapshot(beadsDir)
	if err != nil {
		t.Fatalf("second LoadReadOnlySnapshot: %v", err)
	}
	if firstConfig == nil || secondConfig == nil {
		t.Fatalf("loaded configs = %#v, %#v, want present metadata", firstConfig, secondConfig)
	}
	if !first.Present() || !second.Present() {
		t.Fatalf("snapshot presence = %v, %v, want both present", first.Present(), second.Present())
	}
	if !first.Equal(second) || !second.Equal(first) {
		t.Fatal("unchanged metadata snapshots are not equal in both directions")
	}
}

func TestReadOnlySnapshotDetectsByteChangeOnSameFile(t *testing.T) {
	beadsDir := t.TempDir()
	configPath := ConfigPath(beadsDir)
	initial := []byte(`{"database":"dolt","backend":"dolt","project_id":"first1"}`)
	changed := []byte(`{"database":"dolt","backend":"dolt","project_id":"second"}`)
	if len(initial) != len(changed) {
		t.Fatalf("test metadata lengths differ: %d != %d", len(initial), len(changed))
	}
	if err := os.WriteFile(configPath, initial, 0o600); err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Lstat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	_, before, err := LoadReadOnlySnapshot(beadsDir)
	if err != nil {
		t.Fatalf("LoadReadOnlySnapshot before byte change: %v", err)
	}

	file, err := os.OpenFile(configPath, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(changed); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Lstat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(beforeInfo, afterInfo) {
		t.Fatal("test setup replaced metadata identity instead of changing bytes in place")
	}

	_, after, err := LoadReadOnlySnapshot(beadsDir)
	if err != nil {
		t.Fatalf("LoadReadOnlySnapshot after byte change: %v", err)
	}
	if before.Equal(after) || after.Equal(before) {
		t.Fatal("same-file byte change produced equal snapshots")
	}
}

func TestReadOnlySnapshotDetectsSameByteFileReplacement(t *testing.T) {
	beadsDir := t.TempDir()
	configPath := ConfigPath(beadsDir)
	replacementPath := filepath.Join(beadsDir, "replacement.json")
	metadata := []byte(`{"database":"dolt","backend":"dolt"}`)
	if err := os.WriteFile(configPath, metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Lstat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	_, before, err := LoadReadOnlySnapshot(beadsDir)
	if err != nil {
		t.Fatalf("LoadReadOnlySnapshot before replacement: %v", err)
	}

	if err := os.WriteFile(replacementPath, metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacementPath, configPath); err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Lstat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(beforeInfo, afterInfo) {
		t.Fatal("test setup did not replace metadata identity")
	}

	_, after, err := LoadReadOnlySnapshot(beadsDir)
	if err != nil {
		t.Fatalf("LoadReadOnlySnapshot after replacement: %v", err)
	}
	if before.Equal(after) || after.Equal(before) {
		t.Fatal("same-byte metadata replacement produced equal snapshots")
	}
}

func TestReadOnlySnapshotDetectsCurrentLegacyPrecedenceChange(t *testing.T) {
	beadsDir := t.TempDir()
	legacyPath := filepath.Join(beadsDir, "config.json")
	currentPath := ConfigPath(beadsDir)
	metadata := []byte(`{"database":"dolt","backend":"dolt"}`)
	if err := os.WriteFile(legacyPath, metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Lstat(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	_, legacy, err := LoadReadOnlySnapshot(beadsDir)
	if err != nil {
		t.Fatalf("LoadReadOnlySnapshot for legacy metadata: %v", err)
	}

	if err := os.Rename(legacyPath, currentPath); err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Lstat(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(beforeInfo, afterInfo) {
		t.Fatal("test setup changed identity while changing metadata precedence")
	}
	_, current, err := LoadReadOnlySnapshot(beadsDir)
	if err != nil {
		t.Fatalf("LoadReadOnlySnapshot for current metadata: %v", err)
	}
	if legacy.Equal(current) || current.Equal(legacy) {
		t.Fatal("current-versus-legacy precedence change produced equal snapshots")
	}
}

func TestReadOnlySnapshotAbsentValuesAreEqual(t *testing.T) {
	firstConfig, first, err := LoadReadOnlySnapshot(t.TempDir())
	if err != nil {
		t.Fatalf("first LoadReadOnlySnapshot: %v", err)
	}
	secondConfig, second, err := LoadReadOnlySnapshot(t.TempDir())
	if err != nil {
		t.Fatalf("second LoadReadOnlySnapshot: %v", err)
	}
	if firstConfig != nil || secondConfig != nil {
		t.Fatalf("absent configs = %#v, %#v, want nil", firstConfig, secondConfig)
	}
	if first.Present() || second.Present() {
		t.Fatalf("absent snapshot presence = %v, %v, want false", first.Present(), second.Present())
	}
	if !first.Equal(second) || !second.Equal(first) {
		t.Fatal("absent snapshots are not equal in both directions")
	}
}

func TestLoadReadOnlyDoesNotBypassMalformedCurrentMetadata(t *testing.T) {
	beadsDir := t.TempDir()
	if err := os.WriteFile(ConfigPath(beadsDir), []byte(`{"database":"dolt"} {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "config.json"), []byte(`{"database":"dolt","backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if cfg, err := LoadReadOnly(beadsDir); err == nil || cfg != nil {
		t.Fatalf("malformed current metadata got config=%#v err=%v, want error without legacy fallback", cfg, err)
	}
}

func TestLoadReadOnlyRejectsCurrentMetadataReplacementDuringOpen(t *testing.T) {
	beadsDir := t.TempDir()
	currentPath := ConfigPath(beadsDir)
	legacyPath := filepath.Join(beadsDir, "config.json")
	replacementPath := filepath.Join(beadsDir, "replacement.json")
	if err := os.WriteFile(currentPath, []byte(`{"database":"beads.db","backend":"sqlite","sqlite_path":"beads.db"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"database":"dolt","backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacementPath, []byte(`{"database":"dolt","backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	readFile := func(path string) ([]byte, error) {
		opener := safefile.OpenReadOnlyNoFollow
		if path == currentPath {
			opener = func(path string) (*os.File, error) {
				if err := os.Remove(path); err != nil {
					return nil, err
				}
				if err := os.Rename(replacementPath, path); err != nil {
					return nil, err
				}
				return safefile.OpenReadOnlyNoFollow(path)
			}
		}
		return readStableConfigFileWithOpener(path, opener)
	}
	cfg, _, err := loadConfig(beadsDir, readFile, decodeConfigStrict, isStrictConfigAbsent)
	if err == nil || cfg != nil {
		t.Fatalf("replacement load got config=%#v err=%v, want changed-file error", cfg, err)
	}
	if !errors.Is(err, errConfigChanged) {
		t.Fatalf("replacement error = %v, want errConfigChanged", err)
	}
}

func TestLoadReadOnlyDoesNotFallbackWhenCurrentDisappearsDuringOpen(t *testing.T) {
	beadsDir := t.TempDir()
	currentPath := ConfigPath(beadsDir)
	legacyPath := filepath.Join(beadsDir, "config.json")
	if err := os.WriteFile(currentPath, []byte(`{"database":"beads.db","backend":"sqlite","sqlite_path":"beads.db"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"database":"dolt","backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	legacyRead := false
	readFile := func(path string) ([]byte, error) {
		opener := safefile.OpenReadOnlyNoFollow
		if path == currentPath {
			opener = func(path string) (*os.File, error) {
				if err := os.Remove(path); err != nil {
					return nil, err
				}
				return safefile.OpenReadOnlyNoFollow(path)
			}
		} else if path == legacyPath {
			legacyRead = true
		}
		return readStableConfigFileWithOpener(path, opener)
	}
	cfg, _, err := loadConfig(beadsDir, readFile, decodeConfigStrict, isStrictConfigAbsent)
	if err == nil || cfg != nil {
		t.Fatalf("disappearing current config got config=%#v err=%v, want error without legacy fallback", cfg, err)
	}
	if !errors.Is(err, errConfigChanged) || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("disappearance error = %v, want only errConfigChanged", err)
	}
	if legacyRead {
		t.Fatal("legacy metadata was read after current metadata disappeared")
	}
}

func TestLoadReadOnlyDoesNotFallbackWhenCurrentDisappearsAfterRead(t *testing.T) {
	beadsDir := t.TempDir()
	currentPath := ConfigPath(beadsDir)
	legacyPath := filepath.Join(beadsDir, "config.json")
	if err := os.WriteFile(currentPath, []byte(`{"database":"beads.db","backend":"sqlite","sqlite_path":"beads.db"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"database":"dolt","backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	legacyRead := false
	readFile := func(path string) ([]byte, error) {
		opener := safefile.OpenReadOnlyNoFollow
		if path == currentPath {
			opener = func(path string) (*os.File, error) {
				file, err := safefile.OpenReadOnlyNoFollow(path)
				if err != nil {
					return nil, err
				}
				if err := os.Remove(path); err != nil {
					_ = file.Close()
					return nil, err
				}
				return file, nil
			}
		} else if path == legacyPath {
			legacyRead = true
		}
		return readStableConfigFileWithOpener(path, opener)
	}
	cfg, _, err := loadConfig(beadsDir, readFile, decodeConfigStrict, isStrictConfigAbsent)
	if err == nil || cfg != nil {
		t.Fatalf("unlinked current config got config=%#v err=%v, want error without legacy fallback", cfg, err)
	}
	if !errors.Is(err, errConfigChanged) || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("post-open disappearance error = %v, want only errConfigChanged", err)
	}
	if legacyRead {
		t.Fatal("legacy metadata was read after current metadata disappeared")
	}
}

func TestLoadReadOnlyDoesNotTreatLegacyDisappearanceAsAbsence(t *testing.T) {
	beadsDir := t.TempDir()
	legacyPath := filepath.Join(beadsDir, "config.json")
	if err := os.WriteFile(legacyPath, []byte(`{"database":"dolt","backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	readFile := func(path string) ([]byte, error) {
		opener := safefile.OpenReadOnlyNoFollow
		if path == legacyPath {
			opener = func(path string) (*os.File, error) {
				if err := os.Remove(path); err != nil {
					return nil, err
				}
				return safefile.OpenReadOnlyNoFollow(path)
			}
		}
		return readStableConfigFileWithOpener(path, opener)
	}
	cfg, _, err := loadConfig(beadsDir, readFile, decodeConfigStrict, isStrictConfigAbsent)
	if err == nil || cfg != nil {
		t.Fatalf("disappearing legacy config got config=%#v err=%v, want operational error", cfg, err)
	}
	if !errors.Is(err, errConfigChanged) || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy disappearance error = %v, want only errConfigChanged", err)
	}
}

func TestLoadReadOnlyRejectsSymlinkedConfig(t *testing.T) {
	for _, name := range []string{ConfigFileName, "config.json"} {
		t.Run(name, func(t *testing.T) {
			beadsDir := t.TempDir()
			target := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(target, []byte(`{"database":"dolt"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(beadsDir, name)); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			if cfg, err := LoadReadOnly(beadsDir); err == nil || cfg != nil {
				t.Fatalf("got config=%#v err=%v, want non-regular-path error", cfg, err)
			}
		})
	}
}

func TestLoadReadOnlyRejectsOversizedConfig(t *testing.T) {
	for _, name := range []string{ConfigFileName, "config.json"} {
		t.Run(name, func(t *testing.T) {
			beadsDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(beadsDir, name), bytes.Repeat([]byte("x"), maxReadOnlyConfigFileBytes+1), 0o600); err != nil {
				t.Fatal(err)
			}
			if cfg, err := LoadReadOnly(beadsDir); err == nil || cfg != nil || !strings.Contains(err.Error(), "exceeds") {
				t.Fatalf("got config=%#v err=%v, want bounded-read error", cfg, err)
			}
		})
	}
}

func TestLoadReadOnlyAcceptsExactSizeLimit(t *testing.T) {
	beadsDir := t.TempDir()
	prefix := []byte(`{"database":"dolt","backend":"dolt"}`)
	data := append(prefix, bytes.Repeat([]byte(" "), maxReadOnlyConfigFileBytes-len(prefix))...)
	if err := os.WriteFile(ConfigPath(beadsDir), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err := LoadReadOnly(beadsDir); err != nil || cfg == nil || cfg.GetBackend() != BackendDolt {
		t.Fatalf("exact-limit load got config=%#v err=%v", cfg, err)
	}
}

func TestLoadReadOnlyRejectsAmbiguousMetadata(t *testing.T) {
	for _, data := range []string{
		`{"database":"dolt","database":"other"}`,
		`{"database":"dolt","Database":"other"}`,
		`{"database":"dolt","unknown_field":true}`,
		`{"database":"dolt","backend":"mystery"}`,
		`{"database":"dolt","dolt_mode":"mystery"}`,
	} {
		beadsDir := t.TempDir()
		if err := os.WriteFile(ConfigPath(beadsDir), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if cfg, err := LoadReadOnly(beadsDir); err == nil || cfg != nil {
			t.Fatalf("LoadReadOnly(%s) got config=%#v err=%v, want strict error", data, cfg, err)
		}
	}
}

func TestLoadReadOnlyRejectsNoncanonicalValuesAndTrailingData(t *testing.T) {
	for _, data := range []string{
		`{"database":null}`,
		`{"database":"dolt","backend":1}`,
		`{"database":"dolt"} {"database":"other"}`,
	} {
		beadsDir := t.TempDir()
		if err := os.WriteFile(ConfigPath(beadsDir), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if cfg, err := LoadReadOnly(beadsDir); err == nil || cfg != nil {
			t.Fatalf("LoadReadOnly(%s) got config=%#v err=%v, want strict error", data, cfg, err)
		}
	}

	beadsDir := t.TempDir()
	data := []byte(`{"database":"dolt"} "\u001b[31m\n"`)
	if err := os.WriteFile(ConfigPath(beadsDir), data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadReadOnly(beadsDir)
	if err == nil {
		t.Fatal("trailing JSON string was accepted")
	}
	if strings.ContainsAny(err.Error(), "\n\x1b") {
		t.Fatalf("trailing-token error contains raw terminal controls: %q", err)
	}
}

func TestLoadReadOnlyRejectsAmbiguousLegacyJSON(t *testing.T) {
	beadsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(beadsDir, "config.json"), []byte(`{"database":"dolt","database":"other"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err := LoadReadOnly(beadsDir); err == nil || cfg != nil {
		t.Fatalf("legacy strict load got config=%#v err=%v, want duplicate-field error", cfg, err)
	}
}

func TestLoadReadOnlyDistinguishesMissingAndDanglingWorkspaceRoots(t *testing.T) {
	parent := t.TempDir()
	missing := filepath.Join(parent, "missing")
	if cfg, err := LoadReadOnly(missing); err != nil || cfg != nil {
		t.Fatalf("missing workspace got config=%#v err=%v, want absence", cfg, err)
	}

	dangling := filepath.Join(parent, "dangling")
	if err := os.Symlink(filepath.Join(parent, "missing-target"), dangling); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if cfg, err := LoadReadOnly(dangling); err == nil || cfg != nil {
		t.Fatalf("dangling workspace got config=%#v err=%v, want operational error", cfg, err)
	}
}

func TestLoadReadOnlyRejectsSymlinkedWorkspaceRoots(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(target), []byte(`{"database":"dolt","backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rootLink := filepath.Join(parent, "root-link")
	if err := os.Symlink(target, rootLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if cfg, err := LoadReadOnly(rootLink); err == nil || cfg != nil {
		t.Fatalf("symlinked workspace root got config=%#v err=%v, want operational error", cfg, err)
	}

	danglingParent := filepath.Join(parent, "dangling-parent")
	if err := os.Symlink(filepath.Join(parent, "missing-target"), danglingParent); err != nil {
		t.Fatal(err)
	}
	missingRoot := filepath.Join(danglingParent, ".beads")
	if cfg, err := LoadReadOnly(missingRoot); err == nil || cfg != nil {
		t.Fatalf("workspace below dangling parent got config=%#v err=%v, want operational error", cfg, err)
	}
}

func TestLoadCompatibilityStillIgnoresUnknownMetadata(t *testing.T) {
	beadsDir := t.TempDir()
	if err := os.WriteFile(ConfigPath(beadsDir), []byte(`{"database":"dolt","future_field":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err := Load(beadsDir); err != nil || cfg == nil {
		t.Fatalf("compatibility Load got config=%#v err=%v", cfg, err)
	}
}

func TestPersistedDoltDataPathIgnoresEnvironment(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	t.Setenv("BEADS_DOLT_DATA_DIR", filepath.Join(t.TempDir(), "ambient"))
	for _, tt := range []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "embedded default",
			cfg:  Config{Database: "dolt", Backend: BackendDolt},
			want: filepath.Join(beadsDir, "embeddeddolt"),
		},
		{
			name: "server",
			cfg:  Config{Database: "dolt", Backend: BackendDolt, DoltMode: DoltModeServer},
			want: filepath.Join(beadsDir, "dolt"),
		},
		{
			name: "proxied server",
			cfg:  Config{Database: "dolt", Backend: BackendDolt, DoltMode: DoltModeProxiedServer},
			want: filepath.Join(beadsDir, "proxieddb"),
		},
		{
			name: "relative custom",
			cfg:  Config{Database: "dolt", Backend: BackendDolt, DoltDataDir: "custom-dolt"},
			want: filepath.Join(beadsDir, "custom-dolt"),
		},
		{
			name: "absolute legacy database",
			cfg:  Config{Database: filepath.Join(t.TempDir(), "legacy-dolt"), Backend: BackendDolt},
			want: "",
		},
		{
			name: "non dolt backend",
			cfg:  Config{Database: "beads.db", Backend: BackendSQLite, SQLitePath: "beads.db"},
			want: "none",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			want := tt.want
			if want == "" {
				want = tt.cfg.Database
			}
			if want == "none" {
				want = ""
			}
			if got := tt.cfg.PersistedDoltDataPath(beadsDir); got != want {
				t.Fatalf("PersistedDoltDataPath() = %q, want %q", got, want)
			}
		})
	}
}

func TestLoadReadOnlyQuotesControlCharactersInPaths(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), "unsafe\n\x1b[31m")
	if err := os.MkdirAll(ConfigPath(beadsDir), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := LoadReadOnly(beadsDir)
	if err == nil {
		t.Fatal("directory metadata path was accepted")
	}
	if strings.ContainsAny(err.Error(), "\n\x1b") {
		t.Fatalf("error contains raw terminal-control characters: %q", err)
	}
}

func TestLoadStillMigratesLegacyConfig(t *testing.T) {
	beadsDir := t.TempDir()
	legacyPath := filepath.Join(beadsDir, "config.json")
	if err := os.WriteFile(legacyPath, []byte(`{"database":"dolt","backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(beadsDir)
	if err != nil || cfg == nil || cfg.GetBackend() != BackendDolt {
		t.Fatalf("Load migration got config=%#v err=%v", cfg, err)
	}
	if _, err := os.Stat(ConfigPath(beadsDir)); err != nil {
		t.Fatalf("Load did not create metadata.json: %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("Load did not retire legacy config: %v", err)
	}
}
