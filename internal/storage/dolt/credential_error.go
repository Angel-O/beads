package dolt

import "github.com/steveyegge/beads/internal/creds"

// redactedError wraps a credential-path error whose message may carry live token
// material. Error() returns the scrubbed text (cached tokens replaced by the
// sentinel), while Unwrap() preserves the original chain so errors.As/errors.Is still
// reach the inner *mysql.MySQLError. Classification therefore keys on the preserved
// 1045 number, not on the scrubbed string: isAuthError keeps matching and the circuit
// breaker keeps seeing the true error type.
type redactedError struct {
	scrubbed string
	err      error
}

func (e *redactedError) Error() string { return e.scrubbed }

func (e *redactedError) Unwrap() error { return e.err }

// redactCredentialError scrubs live token material out of err's message before it can
// surface to stderr, logs, or telemetry. The gateway reads the minted identity token
// as the wire username, so a 1045 echoes it verbatim ("Access denied for user
// '<token>'"); this replaces every currently-cached token with a sentinel.
//
// If nothing is redacted the original error is returned unchanged, preserving its
// identity; otherwise the redactedError wrapper keeps errors.As reaching the inner
// error so isAuthError and the circuit breaker are unaffected. Safe on a nil error.
func redactCredentialError(err error) error {
	if err == nil {
		return nil
	}
	scrubbed := creds.RedactKnownTokens(err.Error())
	if scrubbed == err.Error() {
		return err
	}
	return &redactedError{scrubbed: scrubbed, err: err}
}
