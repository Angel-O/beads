package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage/dolt"
)

// Gateway mode: a configured credential command resolves its token into the
// connection username, marks the config as targeting a gateway server, and
// disables local auto-start (the gateway is externally managed). This is what
// makes bd init connect as the token — never as "root" — and what makes the
// store skip the SHOW/CREATE DATABASE probe (openServerConnection keys that on
// cfg.Gateway). ServerMode is set because gateway init always targets a server.
func TestApplyInitGatewayCredentialAdoptsToken(t *testing.T) {
	t.Setenv("BEADS_DOLT_CREDENTIAL_COMMAND", "printf tok-init")
	doltCfg := &dolt.Config{ServerMode: true, AutoStart: true}
	if err := applyInitGatewayCredential(context.Background(), t.TempDir(), doltCfg); err != nil {
		t.Fatalf("applyInitGatewayCredential: %v", err)
	}
	if doltCfg.ServerUser != "tok-init" {
		t.Fatalf("ServerUser = %q, want tok-init (never root)", doltCfg.ServerUser)
	}
	if !doltCfg.Gateway {
		t.Fatal("Gateway must be true so the store skips SHOW/CREATE DATABASE")
	}
	if doltCfg.AutoStart {
		t.Fatal("AutoStart must be disabled in gateway mode (server is externally managed)")
	}
}

// Embedded init (no ServerMode) must never run the credential command, even when
// BEADS_DOLT_CREDENTIAL_COMMAND is ambient on the host. This is the FIX-1
// regression guard: the canonical open path gates the command on server mode
// ("a command exported in the environment must not run (or fail) an embedded
// open"), so init must too. The command here (`false`) would error if it ran;
// the helper returning nil with the config untouched proves it did not.
func TestApplyInitGatewayCredentialSkipsEmbeddedMode(t *testing.T) {
	t.Setenv("BEADS_DOLT_CREDENTIAL_COMMAND", "false")
	doltCfg := &dolt.Config{AutoStart: true} // ServerMode defaults to false
	if err := applyInitGatewayCredential(context.Background(), t.TempDir(), doltCfg); err != nil {
		t.Fatalf("embedded init must not run the credential command: %v", err)
	}
	if doltCfg.Gateway || doltCfg.ServerUser != "" || !doltCfg.AutoStart {
		t.Fatalf("embedded config must be left untouched: %+v", doltCfg)
	}
}

// Server mode, but no command configured: a strict no-op. The hand-built config is
// left exactly as the caller built it.
func TestApplyInitGatewayCredentialNoopWithoutCommand(t *testing.T) {
	t.Setenv("BEADS_DOLT_CREDENTIAL_COMMAND", "")
	doltCfg := &dolt.Config{ServerMode: true, AutoStart: true}
	if err := applyInitGatewayCredential(context.Background(), t.TempDir(), doltCfg); err != nil {
		t.Fatalf("applyInitGatewayCredential: %v", err)
	}
	if doltCfg.ServerUser != "" || doltCfg.Gateway || !doltCfg.AutoStart {
		t.Fatalf("config must be untouched without a command: %+v", doltCfg)
	}
}

// Fail-closed: in server mode a configured-but-failing command aborts init and
// never leaves a fallback (root) user behind.
func TestApplyInitGatewayCredentialFailsClosed(t *testing.T) {
	t.Setenv("BEADS_DOLT_CREDENTIAL_COMMAND", "false")
	doltCfg := &dolt.Config{ServerMode: true, AutoStart: true}
	err := applyInitGatewayCredential(context.Background(), t.TempDir(), doltCfg)
	if err == nil {
		t.Fatal("expected an error when the credential command fails")
	}
	if doltCfg.ServerUser != "" || doltCfg.Gateway {
		t.Fatalf("config must be untouched on failure: %+v", doltCfg)
	}
}

// A caller/flag-preset --server-user wins over the credential command (the
// command is not run). Mirrors ApplyGatewayCredential's preset short-circuit.
func TestApplyInitGatewayCredentialPresetWins(t *testing.T) {
	t.Setenv("BEADS_DOLT_CREDENTIAL_COMMAND", "false")
	doltCfg := &dolt.Config{ServerMode: true, ServerUser: "preset", AutoStart: true}
	if err := applyInitGatewayCredential(context.Background(), t.TempDir(), doltCfg); err != nil {
		t.Fatalf("preset should short-circuit before running the command: %v", err)
	}
	if doltCfg.ServerUser != "preset" || doltCfg.Gateway || !doltCfg.AutoStart {
		t.Fatalf("preset user must be preserved untouched: %+v", doltCfg)
	}
}

// issue_prefix resolution.

// Gateway with no server-provisioned issue_prefix is a provisioning-contract
// violation: bd refuses to choose one for a hosted database.
func TestResolveInitIssuePrefixGatewayMissing(t *testing.T) {
	value, write, err := resolveInitIssuePrefix(true, "", "myhosteddb", "fallback", nil)
	if err == nil {
		t.Fatal("expected a provisioning-contract error for a hosted db with no issue_prefix")
	}
	if !strings.Contains(err.Error(), "provisioning-contract violation") ||
		!strings.Contains(err.Error(), "myhosteddb") {
		t.Fatalf("error should name the db and the contract violation, got: %v", err)
	}
	if write || value != "" {
		t.Fatalf("nothing must be written on violation: value=%q write=%v", value, write)
	}
}

// FIX 3: a transient read error in gateway mode is surfaced as that error, NOT as
// a false provisioning-contract violation — the prefix may well be provisioned; we
// simply failed to read it.
func TestResolveInitIssuePrefixGatewayReadError(t *testing.T) {
	readErr := errors.New("dial tcp: connection refused")
	value, write, err := resolveInitIssuePrefix(true, "", "myhosteddb", "fallback", readErr)
	if err == nil {
		t.Fatal("expected the read error to be surfaced")
	}
	if !errors.Is(err, readErr) {
		t.Fatalf("returned error must wrap the read error, got: %v", err)
	}
	if strings.Contains(err.Error(), "provisioning-contract violation") {
		t.Fatalf("a transient read error must not be reported as a contract violation, got: %v", err)
	}
	if write || value != "" {
		t.Fatalf("nothing must be written on a read error: value=%q write=%v", value, write)
	}
}

// Gateway with an already-provisioned issue_prefix: adopt it (no write).
func TestResolveInitIssuePrefixGatewayAdopts(t *testing.T) {
	value, write, err := resolveInitIssuePrefix(true, "hq", "myhosteddb", "fallback", nil)
	if err != nil {
		t.Fatalf("adoption must not error: %v", err)
	}
	if write || value != "" {
		t.Fatalf("adoption must not write: value=%q write=%v", value, write)
	}
}

// Non-gateway with no existing prefix: set the sanitized prefix (dots -> underscores).
// This is the byte-identical legacy behavior — and a read error is ignored here,
// exactly as legacy init ignored it (the guard is gateway-only).
func TestResolveInitIssuePrefixNonGatewaySets(t *testing.T) {
	value, write, err := resolveInitIssuePrefix(false, "", "mydb", "GPUPolynomials.jl", errors.New("ignored"))
	if err != nil {
		t.Fatalf("non-gateway set must not error even with a read error: %v", err)
	}
	if !write || value != "GPUPolynomials_jl" {
		t.Fatalf("value=%q write=%v, want (GPUPolynomials_jl, true)", value, write)
	}
}

// Non-gateway with an existing prefix: no-op (do not clobber a shared db).
func TestResolveInitIssuePrefixNonGatewayExisting(t *testing.T) {
	value, write, err := resolveInitIssuePrefix(false, "existing", "mydb", "prefix", nil)
	if err != nil {
		t.Fatalf("non-gateway existing must not error: %v", err)
	}
	if write || value != "" {
		t.Fatalf("existing prefix must be preserved: value=%q write=%v", value, write)
	}
}

// project identity resolution.

// Gateway with no server-provisioned _project_id is a provisioning-contract
// violation: bd will not mint an identity for a hosted database.
func TestResolveInitProjectIDGatewayMissing(t *testing.T) {
	value, err := resolveInitProjectID(true, "", "myhosteddb", nil)
	if err == nil {
		t.Fatal("expected a provisioning-contract error for a hosted db with no _project_id")
	}
	if !strings.Contains(err.Error(), "provisioning-contract violation") ||
		!strings.Contains(err.Error(), "_project_id") ||
		!strings.Contains(err.Error(), "myhosteddb") {
		t.Fatalf("error should name the db, _project_id, and the contract, got: %v", err)
	}
	if value != "" {
		t.Fatalf("no identity must be produced: %q", value)
	}
}

// FIX 3: a transient read error in gateway mode is surfaced as that error, NOT as
// a false provisioning-contract violation.
func TestResolveInitProjectIDGatewayReadError(t *testing.T) {
	readErr := errors.New("i/o timeout")
	value, err := resolveInitProjectID(true, "", "myhosteddb", readErr)
	if err == nil {
		t.Fatal("expected the read error to be surfaced")
	}
	if !errors.Is(err, readErr) {
		t.Fatalf("returned error must wrap the read error, got: %v", err)
	}
	if strings.Contains(err.Error(), "provisioning-contract violation") {
		t.Fatalf("a transient read error must not be reported as a contract violation, got: %v", err)
	}
	if value != "" {
		t.Fatalf("no identity must be produced on a read error: %q", value)
	}
}

// Gateway with a server-provisioned _project_id: adopt it verbatim.
func TestResolveInitProjectIDGatewayAdopts(t *testing.T) {
	value, err := resolveInitProjectID(true, "proj-xyz", "myhosteddb", nil)
	if err != nil {
		t.Fatalf("adoption must not error: %v", err)
	}
	if value != "proj-xyz" {
		t.Fatalf("value = %q, want adopted proj-xyz", value)
	}
}

// Non-gateway with no adopted id: generate a fresh identity (legacy behavior).
// A read error is ignored here, exactly as legacy init ignored it.
func TestResolveInitProjectIDNonGatewayGenerates(t *testing.T) {
	value, err := resolveInitProjectID(false, "", "mydb", errors.New("ignored"))
	if err != nil {
		t.Fatalf("non-gateway generation must not error even with a read error: %v", err)
	}
	if value == "" {
		t.Fatal("non-gateway must generate a non-empty project id")
	}
}

// Non-gateway with an adopted id (existing shared/bootstrapped db): use it.
func TestResolveInitProjectIDNonGatewayAdopts(t *testing.T) {
	value, err := resolveInitProjectID(false, "adopted-id", "mydb", nil)
	if err != nil {
		t.Fatalf("non-gateway adoption must not error: %v", err)
	}
	if value != "adopted-id" {
		t.Fatalf("value = %q, want adopted-id", value)
	}
}

// The project identity is server-authoritative in gateway mode: bd must not
// write _project_id back to the (possibly read-only) hosted database. Non-gateway
// keeps writing it for cross-project verification.
func TestShouldWriteProjectIDLocally(t *testing.T) {
	if shouldWriteProjectIDLocally(true, "proj-xyz") {
		t.Fatal("gateway mode must not write _project_id back (server-authoritative)")
	}
	if !shouldWriteProjectIDLocally(false, "proj-xyz") {
		t.Fatal("non-gateway must write _project_id for cross-project verification")
	}
	if shouldWriteProjectIDLocally(false, "") {
		t.Fatal("no id means nothing to write")
	}
}

// FIX 2: gateway init must not write clone-local tracking state (bd_version,
// repo_id, clone_id, last_import_time) or issue the initial-state DOLT_COMMIT into
// the shared, server-owned database. Non-gateway keeps doing all of it —
// byte-identical legacy behavior.
func TestShouldWriteInitStateToDB(t *testing.T) {
	if shouldWriteInitStateToDB(true) {
		t.Fatal("gateway mode must not write tracking metadata or commit initial state to the shared db")
	}
	if !shouldWriteInitStateToDB(false) {
		t.Fatal("non-gateway init must write tracking metadata and commit initial state (byte-identical)")
	}
}
