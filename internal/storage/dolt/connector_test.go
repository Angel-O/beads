package dolt

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mysql "github.com/go-sql-driver/mysql"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/creds"
)

// canarySource returns a KindIdentity command source that records the BEADS_EXEC_INFO
// it was handed to outFile and prints the given bare token. The command string is
// unique per outFile so it never collides with another test's process-global cache.
func canarySource(t *testing.T, token string) (src creds.CommandSource, outFile string) {
	t.Helper()
	outFile = filepath.Join(t.TempDir(), "execinfo.json")
	cmd := "printf '%s' \"$BEADS_EXEC_INFO\" > " + outFile + "; printf %s " + token
	return creds.CommandSource{Command: cmd, Kind: creds.KindIdentity, Label: "BEADS_DOLT_CREDENTIAL_COMMAND"}, outFile
}

func readExecInfo(t *testing.T, outFile string) (dialHost string, dialPort int, database string, present bool) {
	t.Helper()
	raw, err := os.ReadFile(outFile)
	if err != nil {
		return "", 0, "", false
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return "", 0, "", false
	}
	var p struct {
		APIVersion string `json:"apiVersion"`
		Origin     string `json:"origin"`
		Spec       struct {
			DialHost string `json:"dialHost"`
			DialPort int    `json:"dialPort"`
			Database string `json:"database"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("exec-info not valid JSON (%q): %v", raw, err)
	}
	if p.APIVersion != "beads.dev/credential-exec/v1" || p.Origin != "bd" {
		t.Fatalf("exec-info missing apiVersion/origin: %q", raw)
	}
	return p.Spec.DialHost, p.Spec.DialPort, p.Spec.Database, true
}

// The BeforeConnect hook derives the reported dial host from the driver Config (the
// literal dial target), so the host in BEADS_EXEC_INFO is the host bd dials — report
// equals dial, structurally, on the per-dial path.
func TestConnectorHookReportEqualsDial(t *testing.T) {
	src, out := canarySource(t, "tok-perdial")
	hook := credentialBeforeConnect(src)

	c := &mysql.Config{Net: "tcp", Addr: "GW.Example.COM:3306", DBName: "bd_prj_x"}
	if err := hook(context.Background(), c); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if c.User != "tok-perdial" {
		t.Fatalf("c.User = %q, want tok-perdial", c.User)
	}
	host, port, db, present := readExecInfo(t, out)
	if !present {
		t.Fatal("helper saw no BEADS_EXEC_INFO")
	}
	if host != "gw.example.com" {
		t.Fatalf("reported dialHost = %q, want gw.example.com (canonical of the dialed Addr)", host)
	}
	if port != 3306 || db != "bd_prj_x" {
		t.Fatalf("reported port/db = %d/%q, want 3306/bd_prj_x", port, db)
	}
}

// Loopback-lookalike hosts are still reported faithfully — nothing suppresses exec-info
// injection, so an attacker-owned name that merely looks like loopback cannot dodge the
// helper's destination gate.
func TestConnectorHookNoLoopbackExemption(t *testing.T) {
	cases := []struct{ addr, wantHost string }{
		{"127.0.0.1.evil.example:3306", "127.0.0.1.evil.example"},
		{"0.0.0.0:3306", "0.0.0.0"},
		{"[::ffff:127.0.0.1]:3306", "127.0.0.1"},
		{"localhost.evil.example:3306", "localhost.evil.example"},
		{"localhost:3306", "localhost"},
	}
	for _, c := range cases {
		t.Run(c.addr, func(t *testing.T) {
			src, out := canarySource(t, "tok")
			hook := credentialBeforeConnect(src)
			cfg := &mysql.Config{Net: "tcp", Addr: c.addr, DBName: "bd_prj_x"}
			if err := hook(context.Background(), cfg); err != nil {
				t.Fatalf("hook: %v", err)
			}
			host, _, _, present := readExecInfo(t, out)
			if !present {
				t.Fatal("exec-info was suppressed for a loopback-lookalike host")
			}
			if host != c.wantHost {
				t.Fatalf("reported dialHost = %q, want %q", host, c.wantHost)
			}
		})
	}
}

// The hook fails closed: a failing helper aborts the dial and leaves the username unset.
func TestConnectorHookFailsClosed(t *testing.T) {
	src := creds.CommandSource{Command: "exit 7", Kind: creds.KindIdentity, Label: "X"}
	hook := credentialBeforeConnect(src)
	c := &mysql.Config{Net: "tcp", Addr: "gw.example:3306"}
	if err := hook(context.Background(), c); err == nil {
		t.Fatal("expected the dial to abort on helper failure")
	}
	if c.User != "" {
		t.Fatalf("username must be untouched on failure, got %q", c.User)
	}
}

// A non-identity credential is refused per dial (defense in depth: the token lands in
// the username slot).
func TestConnectorHookRejectsNonIdentity(t *testing.T) {
	src := creds.CommandSource{Command: "printf tok", Kind: creds.KindSecret, Label: "X"}
	hook := credentialBeforeConnect(src)
	c := &mysql.Config{Net: "tcp", Addr: "gw.example:3306"}
	if err := hook(context.Background(), c); err == nil || !strings.Contains(err.Error(), "not an identity") {
		t.Fatalf("expected a non-identity rejection, got %v", err)
	}
}

// A token containing a DSN-breaking character is refused per dial.
func TestConnectorHookRejectsBadCharToken(t *testing.T) {
	src := creds.CommandSource{Command: "printf tok@host", Kind: creds.KindIdentity, Label: "X"}
	hook := credentialBeforeConnect(src)
	c := &mysql.Config{Net: "tcp", Addr: "gw.example:3306"}
	if err := hook(context.Background(), c); err == nil {
		t.Fatal("expected a DSN-breaking-character rejection")
	}
}

// openSQLDB with a nil source is exactly sql.Open (the static/local path is untouched);
// with a source it returns a connector-backed pool. A malformed DSN on the credential
// path is a construction error.
func TestOpenSQLDBSourceGating(t *testing.T) {
	db, err := openSQLDB("root@tcp(127.0.0.1:1)/never_dialed", nil)
	if err != nil {
		t.Fatalf("nil source open: %v", err)
	}
	_ = db.Close()

	src := creds.CommandSource{Command: "printf tok", Kind: creds.KindIdentity}
	db, err = openSQLDB("token-per-dial@tcp(gw.example:3306)/bd_prj_x?tls=false", src)
	if err != nil {
		t.Fatalf("credential connector open: %v", err)
	}
	_ = db.Close()

	if _, err := openSQLDB("::: not a dsn :::", src); err == nil {
		t.Fatal("expected a parse error for a malformed DSN on the credential path")
	}
}

func TestDialTarget(t *testing.T) {
	if h, p := dialTarget(&mysql.Config{Net: "tcp", Addr: "gw.example:3306"}); h != "gw.example" || p != 3306 {
		t.Fatalf("tcp: got %q/%d", h, p)
	}
	if h, p := dialTarget(&mysql.Config{Net: "unix", Addr: "/tmp/dolt.sock"}); h != "/tmp/dolt.sock" || p != 0 {
		t.Fatalf("unix: got %q/%d", h, p)
	}
	if h, p := dialTarget(&mysql.Config{Net: "tcp", Addr: "hostonly"}); h != "hostonly" || p != 0 {
		t.Fatalf("no-port: got %q/%d", h, p)
	}
}

// The retained connStr redacts the token to a sentinel when a CredentialSource is
// present — nothing that logs or re-parses the DSN can leak token material. Without a
// source the real username rides the DSN unchanged.
func TestBuildServerDSNRedactsToken(t *testing.T) {
	const secret = "SUPER-SECRET-EIA-TOKEN"
	withSrc := &Config{
		ServerHost:       "gw.example.com",
		ServerPort:       3306,
		ServerUser:       secret,
		ServerTLS:        true,
		CredentialSource: creds.CommandSource{Command: "gettoken", Kind: creds.KindIdentity},
	}
	dsn := buildServerDSN(withSrc, "bd_prj_x")
	if strings.Contains(dsn, secret) {
		t.Fatalf("DSN leaked the token: %q", dsn)
	}
	if !strings.Contains(dsn, credSentinelUser) {
		t.Fatalf("DSN missing the redaction sentinel %q: %q", credSentinelUser, dsn)
	}

	noSrc := &Config{ServerHost: "127.0.0.1", ServerPort: 3307, ServerUser: "root"}
	if got := buildServerDSN(noSrc, "beads"); !strings.Contains(got, "root@") {
		t.Fatalf("static path should bake the real user: %q", got)
	}
}

// Eager mint: ApplyGatewayCredential reads cfg.ServerHost at the mint choke and reports
// that exact destination, canonicalized, to the helper — for ordinary and
// loopback-lookalike hosts alike (no suppression) — and retains the source for per-dial
// re-mint. This is the report==dial guarantee on the eager path; env, metadata, and
// committed config.yaml host shapes all funnel into cfg.ServerHost upstream
// (open.go GetDoltServerHost), so exercising cfg.ServerHost covers all three.
func TestApplyGatewayCredentialReportsDialHost(t *testing.T) {
	cases := []struct{ serverHost, wantHost string }{
		{"GW.Example.COM", "gw.example.com"},
		{"127.0.0.1.evil.example", "127.0.0.1.evil.example"},
		{"0.0.0.0", "0.0.0.0"},
		{"::ffff:127.0.0.1", "127.0.0.1"},
		{"localhost", "localhost"},
	}
	for _, c := range cases {
		t.Run(c.serverHost, func(t *testing.T) {
			src, out := canarySource(t, "tok-eager")
			t.Setenv("BEADS_DOLT_CREDENTIAL_COMMAND", src.Command)
			cfg := &Config{ServerHost: c.serverHost, ServerPort: 3306, Database: "bd_prj_x"}
			applied, err := ApplyGatewayCredential(context.Background(), &configfile.Config{}, cfg)
			if err != nil || !applied {
				t.Fatalf("applied=%v err=%v", applied, err)
			}
			if cfg.CredentialSource == nil {
				t.Fatal("CredentialSource was not retained for per-dial re-mint")
			}
			host, _, db, present := readExecInfo(t, out)
			if !present {
				t.Fatal("eager mint injected no BEADS_EXEC_INFO")
			}
			if host != c.wantHost {
				t.Fatalf("reported dialHost = %q, want %q", host, c.wantHost)
			}
			if db != "bd_prj_x" {
				t.Fatalf("reported database = %q, want bd_prj_x", db)
			}
		})
	}
}
