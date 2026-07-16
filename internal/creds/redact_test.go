package creds

import (
	"context"
	"strings"
	"testing"
)

// RedactKnownTokens replaces every live token the process currently holds in the
// credential cache with the sentinel, so a server-side auth rejection that echoes the
// presented token as the wire username cannot leak it into an error string.
func TestRedactKnownTokens(t *testing.T) {
	resetCache(t)

	const token = "eyJhbGciOiJSUzI1NiJ9.LIVE-EIA-secret-unique-value.sig-abc123"
	credRunner = func(_ context.Context, _, _ string) ([]byte, error) {
		return []byte(token), nil
	}
	src := CommandSource{Command: "gettoken", Kind: KindIdentity, DialHost: "gw.example"}
	if _, _, err := src.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve to warm the cache: %v", err)
	}

	msg := "Error 1045: Access denied for user '" + token + "'@'10.0.0.1' (using password: YES)"
	got := RedactKnownTokens(msg)
	if strings.Contains(got, token) {
		t.Fatalf("redaction left the token in the string: %q", got)
	}
	if !strings.Contains(got, RedactedTokenSentinel) {
		t.Fatalf("redaction did not insert the sentinel %q: %q", RedactedTokenSentinel, got)
	}
	if !strings.Contains(got, "Access denied for user") {
		t.Fatalf("redaction mangled the non-token text: %q", got)
	}

	if RedactKnownTokens("") != "" {
		t.Fatal("empty input must stay empty")
	}
	if RedactKnownTokens("nothing to redact here") != "nothing to redact here" {
		t.Fatal("a string with no cached token must be returned unchanged")
	}
}

// An empty cached token value is never used as a replacement needle (that would
// replace between every character); redaction is a no-op when the cache holds nothing.
func TestRedactKnownTokensEmptyCacheNoOp(t *testing.T) {
	resetCache(t)
	const s = "Access denied for user 'root'@'localhost'"
	if got := RedactKnownTokens(s); got != s {
		t.Fatalf("empty cache must not alter the string, got %q", got)
	}
}
