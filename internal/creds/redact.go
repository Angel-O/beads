package creds

import "strings"

// RedactedTokenSentinel is the placeholder substituted for live token material in any
// string that may reach stderr, a log, or telemetry. It matches the sentinel the dolt
// backend bakes into a redacted DSN, so a scrubbed credential reads the same wherever
// it surfaces.
const RedactedTokenSentinel = "token-per-dial" //nolint:gosec // G101: a redaction placeholder that replaces token material, not a credential

// RedactKnownTokens replaces every token this process currently holds in the
// credential cache with RedactedTokenSentinel.
//
// A minted identity token is presented as the SQL wire username, so a server-side
// auth rejection (MySQL 1045) echoes it verbatim — "Access denied for user
// '<token>'". Callers on the credential path scrub error strings through this before
// they can surface, so token material never rides a printable string.
//
// Tokens are long, unique values, so exact-substring replacement is safe and
// locale-independent: it does not parse any particular server message format. It is
// concurrency-safe and a no-op when the input is empty or no cached token matches. An
// empty cached value is skipped so it is never used as a replacement needle.
func RedactKnownTokens(s string) string {
	if s == "" {
		return s
	}
	credCacheMu.Lock()
	tokens := make([]string, 0, len(credCache))
	for _, c := range credCache {
		if c.token != "" {
			tokens = append(tokens, c.token)
		}
	}
	credCacheMu.Unlock()

	for _, tok := range tokens {
		s = strings.ReplaceAll(s, tok, RedactedTokenSentinel)
	}
	return s
}
