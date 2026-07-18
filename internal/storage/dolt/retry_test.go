package dolt

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	mysql "github.com/go-sql-driver/mysql"
)

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "driver bad connection",
			err:      errors.New("driver: bad connection"),
			expected: true,
		},
		{
			name:     "Driver Bad Connection (case insensitive)",
			err:      errors.New("Driver: Bad Connection"),
			expected: true,
		},
		{
			name:     "invalid connection",
			err:      errors.New("invalid connection"),
			expected: true,
		},
		{
			name:     "broken pipe",
			err:      errors.New("write: broken pipe"),
			expected: true,
		},
		{
			name:     "connection reset",
			err:      errors.New("read: connection reset by peer"),
			expected: true,
		},
		{
			name:     "connection refused - retryable (server restart)",
			err:      errors.New("dial tcp: connection refused"),
			expected: true,
		},
		{
			name:     "database is read only - retryable",
			err:      errors.New("cannot update manifest: database is read only"),
			expected: true,
		},
		{
			name:     "Database Is Read Only (case insensitive)",
			err:      errors.New("Database Is Read Only"),
			expected: true,
		},
		{
			name:     "lost connection - retryable (MySQL error 2013)",
			err:      errors.New("Error 2013: Lost connection to MySQL server during query"),
			expected: true,
		},
		{
			name:     "server gone away - retryable (MySQL error 2006)",
			err:      errors.New("Error 2006: MySQL server has gone away"),
			expected: true,
		},
		{
			name:     "i/o timeout - retryable",
			err:      errors.New("read tcp 127.0.0.1:3307: i/o timeout"),
			expected: true,
		},
		{
			name:     "unknown database - retryable (catalog race GH-1851)",
			err:      errors.New("Error 1049 (42000): Unknown database 'beads_test'"),
			expected: true,
		},
		{
			name:     "Unknown Database (case insensitive)",
			err:      errors.New("Unknown Database 'beads_test'"),
			expected: true,
		},
		{
			name:     "no root value found in session",
			err:      errors.New("Error 1105 (HY000): no root value found in session"),
			expected: true,
		},
		{
			name:     "syntax error - not retryable",
			err:      errors.New("Error 1064: You have an error in your SQL syntax"),
			expected: false,
		},
		{
			name:     "table not found - not retryable",
			err:      errors.New("Error 1146: Table 'beads.foo' doesn't exist"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRetryableError(tt.err)
			if got != tt.expected {
				t.Errorf("isRetryableError(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

func TestWithRetry_Success(t *testing.T) {
	store := &DoltStore{}

	callCount := 0
	err := store.withRetry(context.Background(), func() error {
		callCount++
		return nil
	})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call on success, got %d", callCount)
	}
}

func TestWithRetry_RetryOnBadConnection(t *testing.T) {
	store := &DoltStore{}

	callCount := 0
	err := store.withRetry(context.Background(), func() error {
		callCount++
		if callCount < 3 {
			return errors.New("driver: bad connection")
		}
		return nil // Success on 3rd attempt
	})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls (2 retries + success), got %d", callCount)
	}
}

func TestWithRetry_RetryOnUnknownDatabase(t *testing.T) {
	// Simulates the GH-1851 race: "Unknown database" is transient after CREATE DATABASE
	store := &DoltStore{}

	callCount := 0
	err := store.withRetry(context.Background(), func() error {
		callCount++
		if callCount < 3 {
			return errors.New("Error 1049 (42000): Unknown database 'beads_test'")
		}
		return nil // Catalog caught up on 3rd attempt
	})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls (2 retries + success), got %d", callCount)
	}
}

func TestWithRetry_NonRetryableError(t *testing.T) {
	store := &DoltStore{}

	callCount := 0
	err := store.withRetry(context.Background(), func() error {
		callCount++
		return errors.New("syntax error in SQL")
	})

	if err == nil {
		t.Error("expected error, got nil")
	}
	if callCount != 1 {
		t.Errorf("expected 1 call for non-retryable error, got %d", callCount)
	}
}

// TestWithRetryTx_AuthErrorInvalidatesAndRetries is the write-path analogue of
// withRetry's credential recovery: a MySQL 1045 from a write transaction's dial
// (a rotating token revoked before its cached expiry) must drop the cached token
// and retry, not fail permanently. Before withRetryTx learned this, it
// classified 1045 as non-retryable and surfaced it, so hosted-credential writes
// could fail for the whole life of a stale-but-unexpired cache entry.
func TestWithRetryTx_AuthErrorInvalidatesAndRetries(t *testing.T) {
	const cmd = "rotating-helper"
	credCacheMu.Lock()
	credCache = map[string]cachedCred{cmd: {token: "revoked", expires: time.Now().Add(time.Hour)}}
	credCacheMu.Unlock()
	t.Cleanup(func() {
		credCacheMu.Lock()
		credCache = map[string]cachedCred{}
		credCacheMu.Unlock()
	})

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// First dial rejects the revoked token; the retry's dial succeeds and commits.
	mock.ExpectBegin().WillReturnError(&mysql.MySQLError{Number: 1045, Message: "Access denied for user"})
	mock.ExpectBegin()
	mock.ExpectCommit()

	store := &DoltStore{db: db, credCommand: cmd, serverMode: true}

	bodyRuns := 0
	if err := store.withRetryTx(context.Background(), func(tx *sql.Tx) error {
		bodyRuns++
		return nil
	}); err != nil {
		t.Fatalf("withRetryTx must retry past the auth rejection, got: %v", err)
	}
	if bodyRuns != 1 {
		t.Fatalf("tx body should run once (only after the successful retry), ran %d times", bodyRuns)
	}

	credCacheMu.Lock()
	_, stillCached := credCache[cmd]
	credCacheMu.Unlock()
	if stillCached {
		t.Fatal("auth rejection must invalidate the cached credential so the retry re-mints")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

// TestWithRetryTx_CommitPhaseAuthErrorNotRetried guards the double-apply
// invariant: an auth rejection observed during tx.Commit is ambiguous (the
// commit may have landed), so — exactly like a commit-phase connection loss — it
// must surface permanently rather than replay the write, even though the cached
// token is still dropped so future dials re-mint.
func TestWithRetryTx_CommitPhaseAuthErrorNotRetried(t *testing.T) {
	const cmd = "rotating-helper"
	credCacheMu.Lock()
	credCache = map[string]cachedCred{cmd: {token: "revoked", expires: time.Now().Add(time.Hour)}}
	credCacheMu.Unlock()
	t.Cleanup(func() {
		credCacheMu.Lock()
		credCache = map[string]cachedCred{}
		credCacheMu.Unlock()
	})

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectCommit().WillReturnError(&mysql.MySQLError{Number: 1045, Message: "Access denied for user"})

	store := &DoltStore{db: db, credCommand: cmd, serverMode: true}

	bodyRuns := 0
	err = store.withRetryTx(context.Background(), func(tx *sql.Tx) error {
		bodyRuns++
		return nil
	})
	if err == nil {
		t.Fatal("a commit-phase auth rejection must surface, not be silently retried")
	}
	if !errors.Is(err, errCommitPhase) {
		t.Fatalf("commit-phase failure must stay tagged errCommitPhase, got: %v", err)
	}
	if bodyRuns != 1 {
		t.Fatalf("commit-phase failure must not replay the write; body ran %d times", bodyRuns)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}
