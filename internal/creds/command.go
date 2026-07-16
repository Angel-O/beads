package creds

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// CommandSource runs a configured command and reads a credential from its stdout —
// the external credential-process idiom (kubectl ExecCredential / AWS
// credential_process / git credential helper). bd knows nothing of the issuer: the
// operator points Command at whatever mints the credential — a token-issuing CLI for
// an identity, or `vault read ...` / `aws rds generate-db-auth-token ...` for a secret.
// Kind is fixed at construction from the config slot, so the resolved value's role is
// never ambiguous.
//
// When DialHost is set (the authenticating-gateway path), the source tells the helper
// where bd is about to send the credential by exporting BEADS_EXEC_INFO into the
// helper's environment, and the (canonical) host becomes part of the cache key so a
// token minted for one destination is never served for a dial to another. DialHost is
// left empty by the password-slot backends, which present no destination and inherit
// the parent environment unchanged.
//
// The result is cached per (command, canonical dial host) until near expiry so
// repeated opens don't re-spawn the helper; the cache lives for the process and dies
// with it.
type CommandSource struct {
	Command string
	Kind    Kind
	Label   string // provenance slug (the config-slot name); defaults to "credential-command"

	// DialHost/DialPort/Database describe the destination bd is about to dial.
	// When DialHost is non-empty they are surfaced to the helper via BEADS_EXEC_INFO
	// and the canonical host joins the cache key. All empty (the password-command
	// backends) means "no destination context": no BEADS_EXEC_INFO is injected and
	// the helper inherits the parent environment unchanged.
	DialHost string
	DialPort int
	Database string
}

// Name returns the provenance slug.
func (s CommandSource) Name() string {
	if s.Label != "" {
		return s.Label
	}
	return "credential-command"
}

// Resolve runs the command (or a cached result) and returns the credential. An
// empty Command means "not configured"; a helper failure is a configured error and
// aborts the ladder.
func (s CommandSource) Resolve(ctx context.Context) (Credential, bool, error) {
	if s.Command == "" {
		return Credential{}, false, nil
	}
	host, execInfo, err := s.dialContext()
	if err != nil {
		return Credential{}, true, err
	}
	tok, user, expiry, err := resolveCredentialToken(ctx, s.Command, host, execInfo)
	if err != nil {
		return Credential{}, true, err
	}
	return Credential{Value: tok, Username: user, Kind: s.Kind, Expiry: expiry, Source: s.Name()}, true, nil
}

// dialContext returns the canonical cache-key host and the BEADS_EXEC_INFO value for
// this source's destination. An empty DialHost (the password-command backends) yields
// ("", "", nil): no destination context, so the helper inherits the parent environment
// unchanged and the cache key carries no host dimension.
func (s CommandSource) dialContext() (canonHost, execInfo string, err error) {
	if s.DialHost == "" {
		return "", "", nil
	}
	canonHost, err = CanonicalHost(s.DialHost)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", s.Name(), err)
	}
	execInfo, err = buildExecInfo(canonHost, s.DialPort, s.Database)
	if err != nil {
		return "", "", err
	}
	return canonHost, execInfo, nil
}

// ResolveForDial re-resolves src for a specific dial destination, honoring the
// per-(command, host) cache. The connector calls it inside the driver's BeforeConnect
// hook with the host/port/database derived from the literal dial target, so the value
// reported to the helper is structurally the value bd dials. Only a CommandSource can
// be re-resolved per dial; any other source is a programming error on this path.
func ResolveForDial(ctx context.Context, src Source, host string, port int, database string) (Credential, error) {
	cs, ok := src.(CommandSource)
	if !ok {
		return Credential{}, fmt.Errorf("credential source %s does not support per-dial resolution", src.Name())
	}
	cs.DialHost = host
	cs.DialPort = port
	cs.Database = database
	cred, _, err := cs.Resolve(ctx)
	return cred, err
}

// Invalidate drops any cached token for src's (command, canonical dial host) so the
// next resolution re-runs the helper. Called when a server rejects a presented token
// before its recorded expiry (a rotating credential revoked mid-life). A non-command
// source has nothing to invalidate.
func Invalidate(src Source) {
	cs, ok := src.(CommandSource)
	if !ok {
		return
	}
	host := ""
	if cs.DialHost != "" {
		if h, err := CanonicalHost(cs.DialHost); err == nil {
			host = h
		}
	}
	invalidateCredentialToken(cs.Command, host)
}

const (
	credCommandTimeout = 30 * time.Second // a helper that hangs must not wedge an open
	credDefaultTTL     = 60 * time.Second // cache window when the helper reports no expiry
	credExpirySkew     = 10 * time.Second // refresh this long before the reported expiry
)

// execCredential is the union of stdout envelopes accepted: the kubectl
// ExecCredential subset {token, expirationTimestamp}, the OAuth-style
// {access_token, expires_in} shape, and an optional username for dynamic user/password
// pairs (e.g. Vault). A helper may instead print a bare token — see parseCredential.
type execCredential struct {
	Token               string `json:"token"`        //nolint:gosec // G117: ExecCredential/OAuth envelope field name (wire format), not an embedded secret
	AccessToken         string `json:"access_token"` //nolint:gosec // G117: ExecCredential/OAuth envelope field name (wire format), not an embedded secret
	Username            string `json:"username"`
	ExpirationTimestamp string `json:"expirationTimestamp"`
	ExpiresIn           int64  `json:"expires_in"`
}

type cachedCred struct {
	token    string
	username string
	expires  time.Time
}

// credCacheKey keys the process-level token cache. The host dimension is a SECURITY
// control, not an optimization: the cache is consulted before the helper runs, so
// keying by command alone would let a token minted for a trusted destination be served
// for a later dial to a different host with the same command — the helper's
// destination gate would never execute. host is the canonical dial host (see
// CanonicalHost), empty for password-command sources that present no destination.
type credCacheKey struct {
	command string
	host    string
}

var (
	credCacheMu sync.Mutex
	credCache   = map[credCacheKey]cachedCred{}

	// credRunner runs the helper; a package var so tests can stub it without a shell.
	// execInfo, when non-empty, is exported as BEADS_EXEC_INFO into the helper's
	// environment (see runHelper); an empty execInfo leaves the environment untouched.
	credRunner = func(ctx context.Context, command, execInfo string) ([]byte, error) {
		// POSIX shells parse the helper command; native Windows has no `sh`, so
		// dispatch through cmd.exe there so a bare Windows bd does not hard-fail
		// every *_PASSWORD_COMMAND / CREDENTIAL_COMMAND in the fail-closed ladder.
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(ctx, "cmd.exe", "/C", command)
		} else {
			cmd = exec.CommandContext(ctx, "sh", "-c", command)
		}
		// Only when we have a destination to report do we set cmd.Env, and then we
		// preserve the full parent environment (the helper needs its own inputs) and
		// replace only any inherited BEADS_EXEC_INFO with bd's own — a narrow,
		// exact-name filter, never a broad BEADS_*/"sensitive-key" filter. With no
		// destination the child inherits the parent environment unchanged.
		if execInfo != "" {
			cmd.Env = append(strippedEnv(), ExecInfoEnvVar+"="+execInfo)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return nil, fmt.Errorf("%w: %s", err, msg)
			}
			return nil, err
		}
		return stdout.Bytes(), nil
	}
)

// strippedEnv returns the parent environment with any pre-existing BEADS_EXEC_INFO
// removed, so bd's own value (appended by the caller) is the only one the helper sees.
// A hostile parent must not be able to pre-seed a destination bd never dials.
func strippedEnv() []string {
	parent := os.Environ()
	out := make([]string, 0, len(parent))
	for _, kv := range parent {
		if strings.HasPrefix(kv, ExecInfoEnvVar+"=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// resolveCredentialToken returns the token (and any username/expiry) for the given
// helper command dialing host, using a process-level cache keyed by (command, host) so
// repeated opens don't re-spawn the helper until the token is near expiry. execInfo, if
// set, is exported to the helper as BEADS_EXEC_INFO. It is concurrency-safe.
func resolveCredentialToken(ctx context.Context, command, host, execInfo string) (token, username string, expiry time.Time, err error) {
	now := time.Now()
	key := credCacheKey{command: command, host: host}

	credCacheMu.Lock()
	if c, ok := credCache[key]; ok && now.Before(c.expires.Add(-credExpirySkew)) {
		tok, user, exp := c.token, c.username, c.expires
		credCacheMu.Unlock()
		return tok, user, exp, nil
	}
	credCacheMu.Unlock()

	runCtx, cancel := context.WithTimeout(ctx, credCommandTimeout)
	defer cancel()
	raw, err := credRunner(runCtx, command, execInfo)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("credential command failed: %w", err)
	}
	token, username, expiry, err = parseCredential(raw)
	if err != nil {
		return "", "", time.Time{}, err
	}
	if expiry.IsZero() {
		expiry = now.Add(credDefaultTTL)
	}

	credCacheMu.Lock()
	credCache[key] = cachedCred{token: token, username: username, expires: expiry}
	credCacheMu.Unlock()
	return token, username, expiry, nil
}

// invalidateCredentialToken drops any cached token for (command, host). No-op if
// nothing is cached.
func invalidateCredentialToken(command, host string) {
	credCacheMu.Lock()
	delete(credCache, credCacheKey{command: command, host: host})
	credCacheMu.Unlock()
}

// parseCredential extracts the token (and any username/expiry) from a helper's
// stdout. A JSON object is read as the ExecCredential/getToken envelope; otherwise
// the trimmed output is taken as a bare token. A bare value containing whitespace is
// rejected — it is almost always an error message, and using it as a credential
// would only fail confusingly downstream.
func parseCredential(raw []byte) (token, username string, expiry time.Time, err error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", "", time.Time{}, fmt.Errorf("credential command produced no output")
	}

	if trimmed[0] == '{' {
		var c execCredential
		if jerr := json.Unmarshal(trimmed, &c); jerr != nil {
			return "", "", time.Time{}, fmt.Errorf("credential command returned unparseable JSON: %w", jerr)
		}
		token = c.Token
		if token == "" {
			token = c.AccessToken
		}
		if token == "" {
			return "", "", time.Time{}, fmt.Errorf("credential command JSON has no token/access_token field")
		}
		switch {
		case c.ExpirationTimestamp != "":
			if t, perr := time.Parse(time.RFC3339, c.ExpirationTimestamp); perr == nil {
				expiry = t
			}
		case c.ExpiresIn > 0:
			expiry = time.Now().Add(time.Duration(c.ExpiresIn) * time.Second)
		}
		return token, c.Username, expiry, nil
	}

	bare := string(trimmed)
	if strings.ContainsAny(bare, " \t\r\n") {
		return "", "", time.Time{}, fmt.Errorf("credential command output is not a bare token (contains whitespace); expected a token or a JSON {token,expirationTimestamp} envelope")
	}
	return bare, "", time.Time{}, nil
}
