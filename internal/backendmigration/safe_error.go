package backendmigration

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
)

type ErrorCode string

const (
	ErrorCodeInvalidRequest  ErrorCode = "backend_migration_invalid_request"
	ErrorCodeUnsafeWorkspace ErrorCode = "backend_migration_unsafe_workspace"
	ErrorCodeUnsupported     ErrorCode = "backend_migration_unsupported_source"
	ErrorCodeTargetExists    ErrorCode = "backend_migration_target_unavailable"
	ErrorCodeBusy            ErrorCode = "backend_migration_busy"
	ErrorCodePending         ErrorCode = "backend_migration_pending"
	ErrorCodeRecoveryBlocked ErrorCode = "backend_migration_recovery_blocked"
	ErrorCodeFailed          ErrorCode = "backend_migration_failed"
)

const AuthorityUnknown = "unknown"

// SafeError is the deliberately small, allowlisted error contract exposed by
// the CLI. Its private fields prevent low-level filesystem or driver errors
// from being serialized accidentally.
type SafeError struct {
	code            ErrorCode
	message         string
	authority       string
	sourcePreserved bool
	retryCommand    string
}

func (e *SafeError) Error() string {
	if e == nil {
		return "backend migration failed"
	}
	return e.message
}

func (e *SafeError) Code() string          { return string(e.code) }
func (e *SafeError) Authority() string     { return e.authority }
func (e *SafeError) SourcePreserved() bool { return e.sourcePreserved }
func (e *SafeError) RetryCommand() string  { return e.retryCommand }

func (e *SafeError) MarshalJSON() ([]byte, error) {
	payload := struct {
		Error           string `json:"error"`
		Code            string `json:"code"`
		Authority       string `json:"authority"`
		SourcePreserved bool   `json:"source_preserved"`
		RetryCommand    string `json:"retry_command,omitempty"`
	}{
		Error: e.Error(), Code: e.Code(), Authority: e.Authority(),
		SourcePreserved: e.SourcePreserved(), RetryCommand: e.RetryCommand(),
	}
	return json.Marshal(payload)
}

func newSafeError(code ErrorCode, message, authority, retry string) *SafeError {
	switch code {
	case ErrorCodeInvalidRequest, ErrorCodeUnsafeWorkspace, ErrorCodeUnsupported,
		ErrorCodeTargetExists, ErrorCodeBusy, ErrorCodePending,
		ErrorCodeRecoveryBlocked, ErrorCodeFailed:
	default:
		code = ErrorCodeFailed
		message = "backend migration did not complete because a safety check failed"
	}
	switch authority {
	case AuthorityDoltSource, AuthoritySQLite, AuthorityUnknown:
	default:
		authority = AuthorityUnknown
	}
	return &SafeError{
		code: code, message: message, authority: authority,
		sourcePreserved: true, retryCommand: retry,
	}
}

// PendingError describes the gate presented by ordinary commands while a
// durable backend migration attempt requires recovery.
func PendingError(beadsDir string) *SafeError {
	return newSafeError(
		ErrorCodePending,
		"backend migration recovery is required before other commands can use this workspace",
		currentAuthority(beadsDir),
		defaultRecoveryCommand,
	)
}

// PendingTreeError is used when a recursive operation found a pending
// migration somewhere below its scan root. The affected workspace is not the
// command's selected workspace, so an unscoped migration retry would be unsafe.
func PendingTreeError() *SafeError {
	return newSafeError(
		ErrorCodePending,
		"backend migration recovery is required in a workspace within the scanned tree; run the migration command from the affected workspace before retrying",
		AuthorityUnknown,
		"",
	)
}

// SafeFailure converts any internal migration failure into the public CLI
// contract. The cause is used for classification only and is never retained or
// serialized. A pending attempt always infers its durable SQLite basename, so
// its recovery command intentionally omits --sqlite-path.
func SafeFailure(beadsDir string, cause error, requestedPath string) *SafeError {
	var safe *SafeError
	if errors.As(cause, &safe) {
		copy := *safe
		if copy.authority == AuthorityUnknown {
			copy.authority = currentAuthority(beadsDir)
		}
		return &copy
	}

	authority := currentAuthority(beadsDir)
	pending, pendingErr := pendingAttempt(beadsDir)
	if pendingErr == nil && pending {
		return newSafeError(
			ErrorCodeRecoveryBlocked,
			"backend migration recovery stopped because a safety check did not pass",
			authority,
			defaultRecoveryCommand,
		)
	}
	if errors.Is(cause, embeddeddolt.ErrBackendMigrationBusy) {
		return newSafeError(
			ErrorCodeBusy,
			"embedded Dolt is active in another process; close it and retry the migration",
			AuthorityDoltSource,
			applyCommand(requestedPath),
		)
	}
	return newSafeError(
		ErrorCodeFailed,
		"backend migration did not complete because a safety check failed",
		authority,
		"",
	)
}

const defaultRecoveryCommand = "bd migrate backend --to=sqlite --apply"

func applyCommand(sqlitePath string) string {
	validated, err := ValidateSQLiteBasename(sqlitePath)
	if err != nil || validated == "beads.db" {
		return defaultRecoveryCommand
	}
	return "bd migrate backend --to=sqlite --sqlite-path=" + validated + " --apply"
}

func currentAuthority(beadsDir string) string {
	current, cfg, err := configfile.ReadForBackendMigration(beadsDir)
	if err != nil || cfg == nil {
		return AuthorityUnknown
	}
	if state, stateErr := readAttempt(beadsDir); stateErr == nil {
		switch {
		case bytes.Equal(current, state.OriginalMetadata):
			return AuthorityDoltSource
		case bytes.Equal(current, state.TargetMetadata):
			return AuthoritySQLite
		}
	}
	switch cfg.GetBackend() {
	case configfile.BackendDolt:
		return AuthorityDoltSource
	case configfile.BackendSQLite:
		return AuthoritySQLite
	default:
		return AuthorityUnknown
	}
}

func safeInvalidRequest(message string) error {
	return newSafeError(ErrorCodeInvalidRequest, strings.TrimSpace(message), AuthorityUnknown, "")
}
