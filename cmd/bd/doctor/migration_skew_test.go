package doctor

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	backendmigrationcontrol "github.com/steveyegge/beads/internal/backendmigration/control"
	"github.com/steveyegge/beads/internal/configfile"
)

func TestCheckMigrationContentSkew_NoDatabase(t *testing.T) {
	got := CheckMigrationContentSkew(&SharedStore{}) // Store() == nil
	if got.Status != StatusOK {
		t.Errorf("status = %q, want %q", got.Status, StatusOK)
	}
}

func TestCheckMigrationContentSkew_SQLiteAuthorityNeverCallsEmbeddedOpener(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&configfile.Config{Backend: configfile.BackendSQLite, SQLitePath: "beads.db"}).Save(beadsDir); err != nil {
		t.Fatal(err)
	}

	openCalls := 0
	got, ok := checkMigrationContentSkewEmbeddedWithOpener(t.Context(), beadsDir,
		func(context.Context, string, string, string) (*sql.DB, func() error, error) {
			openCalls++
			return nil, nil, errors.New("retained provider must not be opened")
		})
	if !ok {
		t.Fatal("SQLite authority did not return an explicit non-applicable check")
	}
	if got.Status != StatusOK || !strings.Contains(strings.ToLower(got.Message), "n/a (backend sqlite)") {
		t.Fatalf("SQLite skew check = %q (%s), want OK non-applicable", got.Status, got.Message)
	}
	if openCalls != 0 {
		t.Fatalf("SQLite skew check called retained provider %d time(s)", openCalls)
	}
}

func TestCheckMigrationContentSkew_MetadataErrorsFailClosed(t *testing.T) {
	for name, metadata := range map[string]string{
		"malformed":       `{"backend":`,
		"duplicate":       `{"backend":"dolt","backend":"sqlite"}`,
		"case variant":    `{"backend":"dolt","Backend":"sqlite"}`,
		"unsupported":     `{"backend":"future-provider"}`,
		"unknown field":   `{"backend":"dolt","surprise":true}`,
		"trailing object": `{"backend":"dolt"} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			beadsDir := filepath.Join(t.TempDir(), ".beads")
			if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configfile.ConfigPath(beadsDir), []byte(metadata), 0o600); err != nil {
				t.Fatal(err)
			}
			openCalls := 0
			got, ok := checkMigrationContentSkewEmbeddedWithOpener(t.Context(), beadsDir,
				func(context.Context, string, string, string) (*sql.DB, func() error, error) {
					openCalls++
					return nil, nil, errors.New("retained provider must not be opened")
				})
			if !ok || got.Status != StatusWarning || !strings.Contains(strings.ToLower(got.Message), "backend") {
				t.Fatalf("invalid metadata skew check = %#v, %v; want backend-classification warning", got, ok)
			}
			if openCalls != 0 {
				t.Fatalf("invalid metadata called retained provider %d time(s)", openCalls)
			}
		})
	}
}

func TestCheckMigrationContentSkew_PendingMigrationFailsClosed(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&configfile.Config{Backend: configfile.BackendDolt}).Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, configfile.BackendMigrationStateFileName), []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}
	openCalls := 0
	got, ok := checkMigrationContentSkewEmbeddedWithOpener(t.Context(), beadsDir,
		func(context.Context, string, string, string) (*sql.DB, func() error, error) {
			openCalls++
			return nil, nil, errors.New("retained provider must not be opened")
		})
	if !ok || got.Status != StatusWarning {
		t.Fatalf("pending migration skew check = %#v, %v; want warning", got, ok)
	}
	if openCalls != 0 {
		t.Fatalf("pending migration called retained provider %d time(s)", openCalls)
	}
}

func TestCheckMigrationContentSkew_LinkedMetadataFailsClosed(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt"), 0o700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(t.TempDir(), "foreign.json")
	foreignBytes := []byte(`{"backend":"dolt"}`)
	if err := os.WriteFile(foreign, foreignBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, configfile.ConfigPath(beadsDir)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	openCalls := 0
	got, ok := checkMigrationContentSkewEmbeddedWithOpener(t.Context(), beadsDir,
		func(context.Context, string, string, string) (*sql.DB, func() error, error) {
			openCalls++
			return nil, nil, errors.New("retained provider must not be opened")
		})
	if !ok || got.Status != StatusWarning || openCalls != 0 {
		t.Fatalf("linked metadata skew check = %#v, %v calls=%d; want warning without open", got, ok, openCalls)
	}
	after, err := os.ReadFile(foreign)
	if err != nil || string(after) != string(foreignBytes) {
		t.Fatalf("linked metadata check changed foreign target: %q, %v", after, err)
	}
}

func TestCheckMigrationContentSkew_HoldsMigrationControlThroughOpenQueryAndClose(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&configfile.Config{Backend: configfile.BackendDolt, DoltDatabase: "beads"}).Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM dolt_remotes WHERE name = \?`).
		WithArgs("origin").
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
	cleaned := false
	got, ok := checkMigrationContentSkewEmbeddedWithOpener(t.Context(), beadsDir,
		func(context.Context, string, string, string) (*sql.DB, func() error, error) {
			contending, contentionErr := backendmigrationcontrol.TryAcquire(beadsDir)
			if contending != nil {
				_ = contending.Close()
			}
			if !errors.Is(contentionErr, backendmigrationcontrol.ErrBusy) {
				return nil, nil, errors.New("doctor did not control workspace while opening retained Dolt")
			}
			return db, func() error {
				cleaned = true
				contending, contentionErr := backendmigrationcontrol.TryAcquire(beadsDir)
				if contending != nil {
					_ = contending.Close()
				}
				if !errors.Is(contentionErr, backendmigrationcontrol.ErrBusy) {
					return errors.New("doctor released workspace control before closing retained Dolt")
				}
				return db.Close()
			}, nil
		})
	if !ok || got.Status != StatusOK || !strings.Contains(strings.ToLower(got.Message), "not configured") {
		t.Fatalf("controlled skew check = %#v, %v; want successful diagnostic", got, ok)
	}
	if !cleaned {
		t.Fatal("controlled skew check did not close the opened provider")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCheckMigrationContentSkew_ControlBusyFailsClosedBeforeOpen(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&configfile.Config{Backend: configfile.BackendDolt}).Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	guard, err := backendmigrationcontrol.TryAcquire(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close() //nolint:errcheck // test cleanup

	openCalls := 0
	got, ok := checkMigrationContentSkewEmbeddedWithOpener(t.Context(), beadsDir,
		func(context.Context, string, string, string) (*sql.DB, func() error, error) {
			openCalls++
			return nil, nil, errors.New("retained provider must not be opened")
		})
	if !ok || got.Status != StatusWarning || !strings.Contains(strings.ToLower(got.Message), "changing") {
		t.Fatalf("busy migration skew check = %#v, %v; want explicit warning", got, ok)
	}
	if openCalls != 0 {
		t.Fatalf("busy migration skew check opened retained provider %d time(s)", openCalls)
	}
}

func TestCheckMigrationContentSkew_MissingMetadataFailsClosed(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(filepath.Join(beadsDir, "embeddeddolt"), 0o700); err != nil {
		t.Fatal(err)
	}
	openCalls := 0
	got, ok := checkMigrationContentSkewEmbeddedWithOpener(t.Context(), beadsDir,
		func(context.Context, string, string, string) (*sql.DB, func() error, error) {
			openCalls++
			return nil, nil, errors.New("retained provider must not be opened")
		})
	if !ok || got.Status != StatusWarning || !strings.Contains(strings.ToLower(got.Message), "classify") {
		t.Fatalf("missing metadata skew check = %#v, %v; want classification warning", got, ok)
	}
	if openCalls != 0 {
		t.Fatalf("missing metadata skew check opened retained provider %d time(s)", openCalls)
	}
}

func expectRemoteAndBranch(mock sqlmock.Sqlmock, branch string) {
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM dolt_remotes WHERE name = \?`).
		WithArgs("origin").
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(1))
	mock.ExpectQuery(`SELECT active_branch\(\)`).
		WillReturnRows(sqlmock.NewRows([]string{"b"}).AddRow(branch))
}

func TestCheckMigrationContentSkew_NoRemote(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// The configured sync remote is absent from dolt_remotes -> skip.
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM dolt_remotes WHERE name = \?`).
		WithArgs("origin").
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))

	got := checkMigrationContentSkew(context.Background(), db, "origin")
	if got.Status != StatusOK {
		t.Errorf("status = %q, want %q (%s)", got.Status, StatusOK, got.Message)
	}
	if !strings.Contains(got.Message, "not configured") {
		t.Errorf("message = %q, want a 'not configured' skip", got.Message)
	}
}

// The check must compare against the CONFIGURED sync remote, not whichever
// remote sorts first in dolt_remotes (bd-6dnrw.27).
func TestCheckMigrationContentSkew_UsesConfiguredRemote(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM dolt_remotes WHERE name = \?`).
		WithArgs("upstream").
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(1))
	mock.ExpectQuery(`SELECT active_branch\(\)`).
		WillReturnRows(sqlmock.NewRows([]string{"b"}).AddRow("main"))
	mock.ExpectQuery(`SELECT version, content_hash FROM schema_migrations$`).
		WillReturnRows(sqlmock.NewRows([]string{"version", "content_hash"}).AddRow(1, "a"))
	mock.ExpectQuery(`AS OF 'remotes/upstream/main'`).
		WillReturnRows(sqlmock.NewRows([]string{"version", "content_hash"}).AddRow(1, "a"))

	got := checkMigrationContentSkew(context.Background(), db, "upstream")
	if got.Status != StatusOK {
		t.Errorf("status = %q, want %q (%s)", got.Status, StatusOK, got.Message)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestCheckMigrationContentSkew_NoCachedRemoteRef(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	expectRemoteAndBranch(mock, "main")
	mock.ExpectQuery(`SELECT version, content_hash FROM schema_migrations$`).
		WillReturnRows(sqlmock.NewRows([]string{"version", "content_hash"}).AddRow(1, "a"))
	// Real Dolt phrasing for an AS OF ref that is not cached locally.
	mock.ExpectQuery(`AS OF 'remotes/origin/main'`).
		WillReturnError(errors.New("branch not found: remotes/origin/main"))

	got := checkMigrationContentSkew(context.Background(), db, "origin")
	if got.Status != StatusOK {
		t.Errorf("status = %q, want %q (%s)", got.Status, StatusOK, got.Message)
	}
}

// An UNEXPECTED failure of the remote-side read must surface as a warning, not
// be swallowed as OK — the original #4270 bug hid `unbound variable "v1" in
// query` (Dolt rejecting bind params in AS OF) behind a green check forever.
func TestCheckMigrationContentSkew_UnexpectedErrorWarns(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	expectRemoteAndBranch(mock, "main")
	mock.ExpectQuery(`SELECT version, content_hash FROM schema_migrations$`).
		WillReturnRows(sqlmock.NewRows([]string{"version", "content_hash"}).AddRow(1, "a"))
	mock.ExpectQuery(`AS OF 'remotes/origin/main'`).
		WillReturnError(errors.New(`unbound variable "v1" in query`))

	got := checkMigrationContentSkew(context.Background(), db, "origin")
	if got.Status != StatusWarning {
		t.Fatalf("status = %q, want %q (%s)", got.Status, StatusWarning, got.Message)
	}
	if !strings.Contains(got.Message, "Could not check") {
		t.Errorf("message = %q, want a 'Could not check' diagnostic", got.Message)
	}
}

func TestCheckMigrationContentSkew_NoLocalHashes(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	expectRemoteAndBranch(mock, "main")
	// Old database: rows exist but content_hash is all NULL.
	mock.ExpectQuery(`SELECT version, content_hash FROM schema_migrations$`).
		WillReturnRows(sqlmock.NewRows([]string{"version", "content_hash"}).AddRow(1, nil))

	got := checkMigrationContentSkew(context.Background(), db, "origin")
	if got.Status != StatusOK {
		t.Errorf("status = %q, want %q (%s)", got.Status, StatusOK, got.Message)
	}
	if !strings.Contains(got.Message, "No local migration content hashes") {
		t.Errorf("message = %q, want a no-local-hashes skip", got.Message)
	}
}

func TestCheckMigrationContentSkew_Matches(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	expectRemoteAndBranch(mock, "main")
	mock.ExpectQuery(`SELECT version, content_hash FROM schema_migrations$`).
		WillReturnRows(sqlmock.NewRows([]string{"version", "content_hash"}).AddRow(1, "a").AddRow(2, "b"))
	mock.ExpectQuery(`AS OF 'remotes/origin/main'`).
		WillReturnRows(sqlmock.NewRows([]string{"version", "content_hash"}).AddRow(1, "a").AddRow(2, "b"))

	got := checkMigrationContentSkew(context.Background(), db, "origin")
	if got.Status != StatusOK {
		t.Errorf("status = %q, want %q (%s)", got.Status, StatusOK, got.Message)
	}
}

func TestCheckMigrationContentSkew_Diverges(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	expectRemoteAndBranch(mock, "main")
	mock.ExpectQuery(`SELECT version, content_hash FROM schema_migrations$`).
		WillReturnRows(sqlmock.NewRows([]string{"version", "content_hash"}).AddRow(1, "a").AddRow(2, "b"))
	mock.ExpectQuery(`AS OF 'remotes/origin/main'`).
		WillReturnRows(sqlmock.NewRows([]string{"version", "content_hash"}).AddRow(1, "a").AddRow(2, "DIFFERENT"))

	got := checkMigrationContentSkew(context.Background(), db, "origin")
	if got.Status != StatusWarning {
		t.Fatalf("status = %q, want %q (%s)", got.Status, StatusWarning, got.Message)
	}
	if !strings.Contains(got.Message, "0002") {
		t.Errorf("message = %q, want it to name migration 0002", got.Message)
	}
}
