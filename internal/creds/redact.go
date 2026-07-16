package creds

// RedactedTokenSentinel is the placeholder substituted for live token material in any
// string that may reach stderr, a log, or telemetry. It matches the sentinel the dolt
// backend bakes into a redacted DSN, so a scrubbed credential reads the same wherever it
// surfaces.
//
// A minted identity token is presented as the SQL wire username, so a server-side auth
// rejection (MySQL 1045) echoes it verbatim — "Access denied for user '<token>'". The
// dolt credential connector scrubs the exact per-dial token out of such an error before
// it can surface, replacing it with this sentinel.
const RedactedTokenSentinel = "token-per-dial" //nolint:gosec // G101: a redaction placeholder that replaces token material, not a credential
