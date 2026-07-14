package workspacestate

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

func TestInspectLocalClassifiesProviderEvidence(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, beadsDir string)
		want  LocalState
	}{
		{
			name:  "no evidence",
			setup: func(*testing.T, string) {},
			want:  LocalState{},
		},
		{
			name: "Dolt evidence",
			setup: func(t *testing.T, beadsDir string) {
				writeDoltEvidence(t, beadsDir)
			},
			want: LocalState{Backend: configfile.BackendDolt, Initialized: true},
		},
		{
			name: "SQLite evidence",
			setup: func(t *testing.T, beadsDir string) {
				writeSQLiteEvidence(t, filepath.Join(beadsDir, "beads.db"))
			},
			want: LocalState{Backend: configfile.BackendSQLite, Initialized: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beadsDir := t.TempDir()
			tt.setup(t, beadsDir)

			got, err := InspectLocal(beadsDir, "")
			if err != nil {
				t.Fatalf("InspectLocal: %v", err)
			}
			if got != tt.want {
				t.Fatalf("InspectLocal = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestInspectLocalUsesConfiguredSQLitePath(t *testing.T) {
	beadsDir := t.TempDir()
	configuredPath := filepath.Join("custom", "issues.db")
	writeSQLiteEvidence(t, filepath.Join(beadsDir, configuredPath))

	got, err := InspectLocal(beadsDir, configuredPath)
	if err != nil {
		t.Fatalf("InspectLocal: %v", err)
	}
	want := LocalState{Backend: configfile.BackendSQLite, Initialized: true}
	if got != want {
		t.Fatalf("InspectLocal = %#v, want %#v", got, want)
	}
}

func TestInspectLocalRejectsConflictingProviderEvidence(t *testing.T) {
	beadsDir := t.TempDir()
	writeDoltEvidence(t, beadsDir)
	writeSQLiteEvidence(t, filepath.Join(beadsDir, "beads.db"))

	got, err := InspectLocal(beadsDir, "")
	if !errors.Is(err, ErrConflictingEvidence) {
		t.Fatalf("InspectLocal error = %v, want ErrConflictingEvidence", err)
	}
	if got != (LocalState{}) {
		t.Fatalf("InspectLocal state = %#v on conflict, want zero value", got)
	}
}

func TestInspectLocalPropagatesMalformedProviderEvidence(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(t *testing.T, beadsDir string)
		wantProvider string
	}{
		{
			name: "malformed Dolt layout",
			setup: func(t *testing.T, beadsDir string) {
				if err := os.Mkdir(filepath.Join(beadsDir, "dolt"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			wantProvider: "Dolt",
		},
		{
			name: "malformed SQLite file",
			setup: func(t *testing.T, beadsDir string) {
				if err := os.WriteFile(filepath.Join(beadsDir, "beads.db"), []byte("not sqlite"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantProvider: "SQLite",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beadsDir := t.TempDir()
			tt.setup(t, beadsDir)

			got, err := InspectLocal(beadsDir, "")
			if err == nil {
				t.Fatalf("InspectLocal = %#v, want malformed-evidence error", got)
			}
			if errors.Is(err, ErrConflictingEvidence) {
				t.Fatalf("malformed evidence reported as conflict: %v", err)
			}
			if !strings.Contains(err.Error(), tt.wantProvider) {
				t.Fatalf("InspectLocal error = %q, want provider context %q", err, tt.wantProvider)
			}
			if got != (LocalState{}) {
				t.Fatalf("InspectLocal state = %#v on error, want zero value", got)
			}
		})
	}
}

func TestInspectEffectiveConfigRetainsLocalEvidenceDecision(t *testing.T) {
	t.Run("unambiguous metadata skips unrelated evidence", func(t *testing.T) {
		beadsDir := t.TempDir()
		if err := os.Mkdir(filepath.Join(beadsDir, "dolt"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(beadsDir, "beads.db"), []byte("not sqlite"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := &configfile.Config{
			Backend:        configfile.BackendPostgres,
			PostgresDSN:    "postgres://example.invalid/beads",
			PostgresSchema: "workspace",
		}
		before := *cfg

		inspection, err := InspectEffectiveConfig(beadsDir, cfg)
		if err != nil {
			t.Fatalf("InspectEffectiveConfig: %v", err)
		}
		if inspection.Local != nil {
			t.Fatalf("Local = %#v, want nil for deliberately uninspected evidence", inspection.Local)
		}
		if !reflect.DeepEqual(inspection.Config, before) {
			t.Fatalf("Config = %#v, want %#v", inspection.Config, before)
		}
		if !reflect.DeepEqual(*cfg, before) {
			t.Fatalf("InspectEffectiveConfig mutated input: got %#v, want %#v", *cfg, before)
		}
	})

	for _, test := range []struct {
		name        string
		setup       func(*testing.T, string)
		wantLocal   LocalState
		wantBackend string
	}{
		{
			name:        "verified absence",
			setup:       func(*testing.T, string) {},
			wantLocal:   LocalState{},
			wantBackend: configfile.BackendSQLite,
		},
		{
			name: "sole Dolt evidence",
			setup: func(t *testing.T, beadsDir string) {
				writeDoltEvidence(t, beadsDir)
			},
			wantLocal:   LocalState{Backend: configfile.BackendDolt, Initialized: true},
			wantBackend: configfile.BackendDolt,
		},
		{
			name: "sole SQLite evidence",
			setup: func(t *testing.T, beadsDir string) {
				writeSQLiteEvidence(t, filepath.Join(beadsDir, "beads.db"))
			},
			wantLocal:   LocalState{Backend: configfile.BackendSQLite, Initialized: true},
			wantBackend: configfile.BackendSQLite,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			beadsDir := t.TempDir()
			test.setup(t, beadsDir)
			cfg := &configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite}
			before := *cfg

			inspection, err := InspectEffectiveConfig(beadsDir, cfg)
			if err != nil {
				t.Fatalf("InspectEffectiveConfig: %v", err)
			}
			if inspection.Local == nil || *inspection.Local != test.wantLocal {
				t.Fatalf("Local = %#v, want %#v", inspection.Local, test.wantLocal)
			}
			if got := inspection.Config.GetBackend(); got != test.wantBackend {
				t.Fatalf("effective backend = %q, want %q", got, test.wantBackend)
			}
			if !reflect.DeepEqual(*cfg, before) {
				t.Fatalf("InspectEffectiveConfig mutated input: got %#v, want %#v", *cfg, before)
			}
		})
	}
}

func TestInspectEffectiveConfigFailsClosedWithZeroResult(t *testing.T) {
	t.Run("missing metadata", func(t *testing.T) {
		got, err := InspectEffectiveConfig(t.TempDir(), nil)
		if err == nil || got != (EffectiveConfigInspection{}) {
			t.Fatalf("InspectEffectiveConfig = %#v, %v, want zero result and error", got, err)
		}
	})

	for _, test := range []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{
			name: "conflicting evidence",
			setup: func(t *testing.T, beadsDir string) {
				writeDoltEvidence(t, beadsDir)
				writeSQLiteEvidence(t, filepath.Join(beadsDir, "beads.db"))
			},
		},
		{
			name: "malformed evidence",
			setup: func(t *testing.T, beadsDir string) {
				if err := os.WriteFile(filepath.Join(beadsDir, "beads.db"), []byte("not sqlite"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			beadsDir := t.TempDir()
			test.setup(t, beadsDir)
			got, err := InspectEffectiveConfig(beadsDir, &configfile.Config{Backend: configfile.BackendSQLite})
			if err == nil || got != (EffectiveConfigInspection{}) {
				t.Fatalf("InspectEffectiveConfig = %#v, %v, want zero result and error", got, err)
			}
		})
	}
}

func TestEffectiveConfigNormalizesBareSQLiteMetadataWithLiveDolt(t *testing.T) {
	beadsDir := t.TempDir()
	writeDoltEvidence(t, beadsDir)
	cfg := &configfile.Config{
		Database:  "beads.db",
		Backend:   configfile.BackendSQLite,
		ProjectID: "project-1",
	}
	before := *cfg

	effective, err := EffectiveConfig(beadsDir, cfg)
	if err != nil {
		t.Fatalf("EffectiveConfig: %v", err)
	}
	if effective == nil {
		t.Fatal("EffectiveConfig returned nil without an error")
	}
	if effective == cfg {
		t.Fatal("EffectiveConfig returned the input pointer")
	}
	want := before
	want.Backend = configfile.BackendDolt
	if !reflect.DeepEqual(*effective, want) {
		t.Fatalf("effective config = %#v, want %#v", *effective, want)
	}
	if !reflect.DeepEqual(*cfg, before) {
		t.Fatalf("EffectiveConfig mutated input: got %#v, want %#v", *cfg, before)
	}
}

func TestEffectiveConfigKeepsBareSQLiteMetadataWithoutDoltEvidence(t *testing.T) {
	cfg := &configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite}

	effective, err := EffectiveConfig(t.TempDir(), cfg)
	if err != nil {
		t.Fatalf("EffectiveConfig: %v", err)
	}
	if effective == cfg {
		t.Fatal("EffectiveConfig returned the input pointer")
	}
	if got := effective.GetBackend(); got != configfile.BackendSQLite {
		t.Fatalf("effective backend = %q, want %q", got, configfile.BackendSQLite)
	}
}

func TestEffectiveConfigKeepsBareSQLiteMetadataWithSoleSQLiteEvidence(t *testing.T) {
	beadsDir := t.TempDir()
	writeSQLiteEvidence(t, filepath.Join(beadsDir, "beads.db"))
	cfg := &configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite}

	effective, err := EffectiveConfig(beadsDir, cfg)
	if err != nil {
		t.Fatalf("EffectiveConfig: %v", err)
	}
	if effective == nil {
		t.Fatal("EffectiveConfig returned nil without an error")
	}
	if effective == cfg {
		t.Fatal("EffectiveConfig returned the input pointer")
	}
	if !reflect.DeepEqual(*effective, *cfg) {
		t.Fatalf("effective config = %#v, want %#v", *effective, *cfg)
	}
}

func TestEffectiveConfigRejectsConflictingBareSQLiteEvidence(t *testing.T) {
	beadsDir := t.TempDir()
	writeDoltEvidence(t, beadsDir)
	writeSQLiteEvidence(t, filepath.Join(beadsDir, "beads.db"))
	cfg := &configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite}

	effective, err := EffectiveConfig(beadsDir, cfg)
	if !errors.Is(err, ErrConflictingEvidence) {
		t.Fatalf("EffectiveConfig error = %v, want ErrConflictingEvidence", err)
	}
	if effective != nil {
		t.Fatalf("EffectiveConfig = %#v on conflict, want nil", effective)
	}
}

func TestEffectiveConfigRejectsMalformedSQLiteEvidenceAlongsideDolt(t *testing.T) {
	beadsDir := t.TempDir()
	writeDoltEvidence(t, beadsDir)
	if err := os.WriteFile(filepath.Join(beadsDir, "beads.db"), []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite}

	effective, err := EffectiveConfig(beadsDir, cfg)
	if err == nil || !strings.Contains(err.Error(), "SQLite") {
		t.Fatalf("EffectiveConfig error = %v, want malformed SQLite evidence error", err)
	}
	if effective != nil {
		t.Fatalf("EffectiveConfig = %#v on malformed evidence, want nil", effective)
	}
}

func TestEffectiveConfigDoesNotProbeUnambiguousMetadata(t *testing.T) {
	tests := []struct {
		name string
		cfg  *configfile.Config
		want string
	}{
		{
			name: "SQLite with explicit path",
			cfg: &configfile.Config{
				Backend:    configfile.BackendSQLite,
				SQLitePath: "beads.db",
			},
			want: configfile.BackendSQLite,
		},
		{
			name: "Postgres",
			cfg:  &configfile.Config{Backend: configfile.BackendPostgres},
			want: configfile.BackendPostgres,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beadsDir := t.TempDir()
			if err := os.Mkdir(filepath.Join(beadsDir, "dolt"), 0o700); err != nil {
				t.Fatal(err)
			}
			before := *tt.cfg

			effective, err := EffectiveConfig(beadsDir, tt.cfg)
			if err != nil {
				t.Fatalf("EffectiveConfig probed malformed Dolt evidence: %v", err)
			}
			if effective == tt.cfg {
				t.Fatal("EffectiveConfig returned the input pointer")
			}
			if got := effective.GetBackend(); got != tt.want {
				t.Fatalf("effective backend = %q, want %q", got, tt.want)
			}
			if !reflect.DeepEqual(*tt.cfg, before) {
				t.Fatalf("EffectiveConfig mutated input: got %#v, want %#v", *tt.cfg, before)
			}
		})
	}
}

func TestEffectiveConfigRejectsMissingMetadataAndMalformedDoltEvidence(t *testing.T) {
	if effective, err := EffectiveConfig(t.TempDir(), nil); err == nil || effective != nil {
		t.Fatalf("EffectiveConfig(nil) = %#v, %v, want error", effective, err)
	}

	beadsDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(beadsDir, "dolt"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := &configfile.Config{Backend: configfile.BackendSQLite}
	if effective, err := EffectiveConfig(beadsDir, cfg); err == nil || effective != nil {
		t.Fatalf("EffectiveConfig(malformed Dolt) = %#v, %v, want error", effective, err)
	}
}

func writeDoltEvidence(t *testing.T, beadsDir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt", "beads", ".dolt"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func writeSQLiteEvidence(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 512)
	copy(data, "SQLite format 3\x00")
	binary.BigEndian.PutUint16(data[16:18], 512)
	data[18] = 1
	data[19] = 1
	data[21] = 64
	data[22] = 32
	data[23] = 32
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
