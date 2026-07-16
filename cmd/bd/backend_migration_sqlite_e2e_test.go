//go:build cgo

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/steveyegge/beads/cmd/bd/doctor"
	backendmigrationcontrol "github.com/steveyegge/beads/internal/backendmigration/control"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
)

func TestBackendMigrationEmbeddedDoltToSQLiteUserJourney(t *testing.T) {
	for _, service := range strings.Split(os.Getenv("BEADS_TEST_SKIP"), ",") {
		if strings.TrimSpace(service) == "dolt" {
			t.Skip("skipping: Dolt tests skipped (BEADS_TEST_SKIP=dolt)")
		}
	}
	bd := buildEmbeddedBD(t)
	repoDir := t.TempDir()
	initGitRepoAt(t, repoDir)

	runSQLiteMigrationBD(t, bd, repoDir,
		"init", "--quiet", "--backend=dolt", "--prefix=mig", "--skip-hooks")
	runSQLiteMigrationBD(t, bd, repoDir,
		"create", "Root issue", "--id=mig-root", "--type=feature",
		"--description=root description", "--labels=alpha,beta", "--json")
	runSQLiteMigrationBD(t, bd, repoDir,
		"create", "Blocking issue", "--id=mig-blocker", "--json")
	runSQLiteMigrationBD(t, bd, repoDir,
		"dep", "add", "mig-root", "mig-blocker", "--type=blocks")
	runSQLiteMigrationBD(t, bd, repoDir,
		"comments", "add", "mig-root", "migration comment")
	runSQLiteMigrationBD(t, bd, repoDir,
		"config", "set", "custom.migration-sentinel", "kept")
	firstChild := runSQLiteMigrationBD(t, bd, repoDir,
		"create", "First child", "--parent=mig-root", "--json")
	secondChild := runSQLiteMigrationBD(t, bd, repoDir,
		"create", "Second child", "--parent=mig-root", "--json")
	for payload, wantID := range map[string]string{string(firstChild): "mig-root.1", string(secondChild): "mig-root.2"} {
		if !strings.Contains(payload, wantID) {
			t.Fatalf("pre-migration child counter did not produce %s:\n%s", wantID, payload)
		}
	}

	beadsDir := filepath.Join(repoDir, ".beads")
	metadataPath := configfile.ConfigPath(beadsDir)
	beforeMetadata, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("read source metadata: %v", err)
	}
	targetPath := filepath.Join(beadsDir, "beads.db")

	ambientDirCmd := exec.Command(bd, "migrate", "backend", "--to=sqlite", "--json")
	ambientDirCmd.Dir = repoDir
	ambientDirCmd.Env = append(bdEnv(repoDir), "BEADS_DIR="+beadsDir)
	ambientDirOutput, ambientDirErr := ambientDirCmd.CombinedOutput()
	if ambientDirErr == nil || !bytes.Contains(ambientDirOutput, []byte("BEADS_DIR")) {
		t.Fatalf("ambient BEADS_DIR selection was not rejected clearly: err=%v\n%s", ambientDirErr, ambientDirOutput)
	}
	assertSQLiteMigrationPreviewHadNoEffect(t, metadataPath, targetPath, beforeMetadata)

	configYAMLPath := filepath.Join(beadsDir, "config.yaml")
	beforeConfigYAML, configYAMLErr := os.ReadFile(configYAMLPath)
	if configYAMLErr != nil && !os.IsNotExist(configYAMLErr) {
		t.Fatalf("read source config.yaml: %v", configYAMLErr)
	}
	restoreConfigYAML := func() {
		t.Helper()
		if os.IsNotExist(configYAMLErr) {
			if err := os.Remove(configYAMLPath); err != nil && !os.IsNotExist(err) {
				t.Fatalf("remove test config.yaml: %v", err)
			}
			return
		}
		if err := os.WriteFile(configYAMLPath, beforeConfigYAML, 0o600); err != nil {
			t.Fatalf("restore source config.yaml: %v", err)
		}
	}

	sourceConfig, err := configfile.ParseForBackendMigration(beforeMetadata)
	if err != nil {
		t.Fatalf("parse source metadata: %v", err)
	}
	emptyModeConfig := *sourceConfig
	emptyModeConfig.DoltMode = ""
	emptyModeMetadata, err := configfile.MarshalForBackendMigration(&emptyModeConfig)
	if err != nil {
		t.Fatalf("marshal empty-mode source metadata: %v", err)
	}
	if err := os.WriteFile(metadataPath, emptyModeMetadata, 0o600); err != nil {
		t.Fatalf("write empty-mode source metadata: %v", err)
	}
	if err := os.WriteFile(configYAMLPath, []byte("dolt:\n  mode: server\n"), 0o600); err != nil {
		t.Fatalf("write server-mode config.yaml: %v", err)
	}
	serverModeFailure := runSQLiteMigrationBDFailure(t, bd, repoDir,
		"migrate", "backend", "--to=sqlite", "--json")
	if !bytes.Contains(bytes.ToLower(serverModeFailure), []byte("server")) {
		t.Fatalf("effective dolt.mode=server was not rejected clearly:\n%s", serverModeFailure)
	}
	assertSQLiteMigrationPreviewHadNoEffect(t, metadataPath, targetPath, emptyModeMetadata)
	if err := os.WriteFile(metadataPath, beforeMetadata, 0o600); err != nil {
		t.Fatalf("restore source metadata after server-mode test: %v", err)
	}
	restoreConfigYAML()

	if err := os.WriteFile(configYAMLPath, []byte("dolt:\n  shared-server: true\n"), 0o600); err != nil {
		t.Fatalf("write shared-server config.yaml: %v", err)
	}
	sharedServerFailure := runSQLiteMigrationBDFailure(t, bd, repoDir,
		"migrate", "backend", "--to=sqlite", "--json")
	if !bytes.Contains(bytes.ToLower(sharedServerFailure), []byte("server")) {
		t.Fatalf("effective dolt.shared-server=true was not rejected clearly:\n%s", sharedServerFailure)
	}
	assertSQLiteMigrationPreviewHadNoEffect(t, metadataPath, targetPath, beforeMetadata)
	restoreConfigYAML()

	overrideCmd := exec.Command(bd, "migrate", "backend", "--to=sqlite", "--json")
	overrideCmd.Dir = repoDir
	overrideCmd.Env = append(bdEnv(repoDir), "BEADS_DOLT_SERVER_DATABASE=wrong_database")
	overrideOutput, overrideErr := overrideCmd.CombinedOutput()
	if overrideErr == nil || !bytes.Contains(overrideOutput, []byte("BEADS_DOLT_SERVER_DATABASE")) {
		t.Fatalf("database-selection override was not rejected clearly: err=%v\n%s", overrideErr, overrideOutput)
	}
	assertSQLiteMigrationPreviewHadNoEffect(t, metadataPath, targetPath, beforeMetadata)

	redirectRepo := t.TempDir()
	initGitRepoAt(t, redirectRepo)
	redirectBeadsDir := filepath.Join(redirectRepo, ".beads")
	if err := os.Mkdir(redirectBeadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(redirectBeadsDir, "redirect"), []byte(beadsDir+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	redirectCmd := exec.Command(bd, "migrate", "backend", "--to=sqlite", "--json")
	redirectCmd.Dir = redirectRepo
	redirectCmd.Env = bdEnv(redirectRepo)
	redirectOutput, redirectErr := redirectCmd.CombinedOutput()
	if redirectErr == nil || !bytes.Contains(bytes.ToLower(redirectOutput), []byte("redirect")) {
		t.Fatalf("redirected workspace migration was not rejected clearly: err=%v\n%s", redirectErr, redirectOutput)
	}
	assertSQLiteMigrationPreviewHadNoEffect(t, metadataPath, targetPath, beforeMetadata)

	preview := runSQLiteMigrationBD(t, bd, repoDir,
		"migrate", "backend", "--to=sqlite", "--json")
	var plan map[string]any
	if err := json.Unmarshal(preview, &plan); err != nil {
		t.Fatalf("preview returned invalid JSON: %v\n%s", err, preview)
	}
	for key, want := range map[string]any{
		"status":           "planned",
		"effect":           "none",
		"source_backend":   "dolt",
		"target_backend":   "sqlite",
		"apply_required":   true,
		"source_preserved": true,
	} {
		if got := plan[key]; got != want {
			t.Errorf("preview %s = %#v, want %#v\n%s", key, got, want, preview)
		}
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("preview created SQLite target: %v", err)
	}
	humanPreview := runSQLiteMigrationBD(t, bd, repoDir,
		"migrate", "backend", "--to=sqlite", "--json=false")
	if !bytes.Contains(humanPreview, []byte("Backend migration plan")) || json.Valid(humanPreview) {
		t.Fatalf("explicit --json=false did not retain human output:\n%s", humanPreview)
	}
	afterPreviewMetadata, err := os.ReadFile(metadataPath)
	if err != nil || !bytes.Equal(afterPreviewMetadata, beforeMetadata) {
		t.Fatalf("preview changed metadata: equal=%v err=%v", bytes.Equal(afterPreviewMetadata, beforeMetadata), err)
	}

	failed := runSQLiteMigrationBDFailure(t, bd, repoDir,
		"migrate", "backend", "--to=sqlite", "--apply", "--json")
	if !strings.Contains(string(failed), "--yes") {
		t.Fatalf("non-interactive apply should require --yes:\n%s", failed)
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("unconfirmed apply created SQLite target: %v", err)
	}
	misplacedYes := runSQLiteMigrationBDFailure(t, bd, repoDir,
		"migrate", "backend", "--to=sqlite", "--yes", "--json")
	if !strings.Contains(string(misplacedYes), "--apply") {
		t.Fatalf("--yes without --apply should be rejected:\n%s", misplacedYes)
	}

	if err := os.WriteFile(configYAMLPath, []byte("dolt:\n  auto-commit: definitely-invalid\n"), 0o600); err != nil {
		t.Fatalf("write unrelated invalid auto-commit fixture: %v", err)
	}
	markerPath := filepath.Join(beadsDir, configfile.BackendMigrationStateFileName)
	if err := os.WriteFile(markerPath, []byte("invalid recovery fixture"), 0o600); err != nil {
		t.Fatalf("write pending migration fixture: %v", err)
	}
	pendingCmd := exec.Command(bd, "migrate", "backend", "--to=sqlite", "--apply", "--yes", "--json")
	pendingCmd.Dir = repoDir
	pendingCmd.Env = bdEnv(repoDir)
	pendingStdout, pendingStderr, pendingErr := runCommandBuffers(t, pendingCmd)
	if pendingErr == nil {
		t.Fatal("invalid pending migration fixture unexpectedly recovered")
	}
	if got := pendingStderr.String(); got != "" {
		t.Fatalf("pending migration recovery leaked a startup warning or path on stderr:\n%s", got)
	}
	assertBackendMigrationErrorPayload(t, pendingStdout.Bytes(), map[string]any{
		"code":             "backend_migration_recovery_blocked",
		"authority":        "dolt_source",
		"source_preserved": true,
		"retry_command":    "bd migrate backend --to=sqlite --apply",
	}, repoDir, beadsDir)

	blockedStatus := exec.Command(bd, "status", "--json")
	blockedStatus.Dir = repoDir
	blockedStatus.Env = bdEnv(repoDir)
	blockedStatusStdout, blockedStatusStderr, blockedStatusErr := runCommandBuffers(t, blockedStatus)
	if blockedStatusErr == nil {
		t.Fatal("ordinary status unexpectedly bypassed pending migration recovery")
	}
	if got := blockedStatusStderr.String(); got != "" {
		t.Fatalf("ordinary JSON command leaked pending recovery details on stderr:\n%s", got)
	}
	assertBackendMigrationErrorPayload(t, blockedStatusStdout.Bytes(), map[string]any{
		"code":             "backend_migration_pending",
		"authority":        "dolt_source",
		"source_preserved": true,
		"retry_command":    "bd migrate backend --to=sqlite --apply",
	}, repoDir, beadsDir)

	blockedHuman := exec.Command(bd, "status")
	blockedHuman.Dir = repoDir
	blockedHuman.Env = bdEnv(repoDir)
	blockedHumanOutput, blockedHumanErr := blockedHuman.CombinedOutput()
	if blockedHumanErr == nil {
		t.Fatal("ordinary human command unexpectedly bypassed pending migration recovery")
	}
	for _, want := range []string{
		"backend migration recovery is required",
		"Authority: dolt_source",
		"Source preserved: true",
		"Retry: bd migrate backend --to=sqlite --apply",
	} {
		if !bytes.Contains(blockedHumanOutput, []byte(want)) {
			t.Fatalf("ordinary human recovery error missing %q:\n%s", want, blockedHumanOutput)
		}
	}
	for _, leaked := range []string{repoDir, beadsDir, "metadata.json"} {
		if bytes.Contains(blockedHumanOutput, []byte(leaked)) {
			t.Fatalf("ordinary human recovery error leaked %q:\n%s", leaked, blockedHumanOutput)
		}
	}
	if err := os.Remove(markerPath); err != nil {
		t.Fatalf("remove pending migration fixture: %v", err)
	}
	restoreConfigYAML()

	resultJSON := runSQLiteMigrationBD(t, bd, repoDir,
		"migrate", "backend", "--to=sqlite", "--apply", "--yes", "--json")
	var result map[string]any
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("apply returned invalid JSON: %v\n%s", err, resultJSON)
	}
	for key, want := range map[string]any{
		"status":           "migrated",
		"source_backend":   "dolt",
		"target_backend":   "sqlite",
		"verified":         true,
		"cutover_applied":  true,
		"source_preserved": true,
	} {
		if got := result[key]; got != want {
			t.Errorf("result %s = %#v, want %#v\n%s", key, got, want, resultJSON)
		}
	}

	cfg, err := configfile.Load(beadsDir)
	if err != nil {
		t.Fatalf("load cutover metadata: %v", err)
	}
	if got := cfg.GetBackend(); got != configfile.BackendSQLite {
		t.Fatalf("cutover backend = %q, want sqlite", got)
	}
	if got := cfg.GetSQLitePath(); got != "beads.db" {
		t.Fatalf("cutover sqlite path = %q, want beads.db", got)
	}
	if _, err := os.Stat(targetPath); err != nil {
		t.Fatalf("SQLite target missing after cutover: %v", err)
	}
	if _, err := os.Stat(filepath.Join(beadsDir, "embeddeddolt")); err != nil {
		t.Fatalf("Dolt source was not preserved: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(beadsDir, configfile.BackendMigrationStateFileName)); !os.IsNotExist(err) {
		t.Fatalf("successful migration left a recovery marker: %v", err)
	}
	if leftovers, err := filepath.Glob(filepath.Join(beadsDir, ".backend-migration-*.db*")); err != nil || len(leftovers) != 0 {
		t.Fatalf("successful migration left staging files: %v err=%v", leftovers, err)
	}
	if info, err := os.Stat(targetPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("SQLite target permissions = %v err=%v, want 0600", info, err)
	}

	sourceDatabase := sourceConfig.DoltDatabase
	if sourceDatabase == "" {
		sourceDatabase = configfile.DefaultDoltDatabase
	}
	preservedDolt, closePreservedDolt, err := embeddeddolt.OpenSQL(
		t.Context(), filepath.Join(beadsDir, "embeddeddolt"), sourceDatabase, "main",
	)
	if err != nil {
		t.Fatalf("open preserved Dolt source directly: %v", err)
	}
	defer closePreservedDolt() //nolint:errcheck // test cleanup
	var preservedTitle string
	if err := preservedDolt.QueryRowContext(t.Context(), "SELECT title FROM issues WHERE id = ?", "mig-root").Scan(&preservedTitle); err != nil {
		t.Fatalf("read issue from preserved Dolt source: %v", err)
	}
	if preservedTitle != "Root issue" {
		t.Fatalf("preserved Dolt issue title = %q, want Root issue", preservedTitle)
	}
	var preservedCommitCount int
	if err := preservedDolt.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM dolt_log").Scan(&preservedCommitCount); err != nil {
		t.Fatalf("read preserved Dolt history: %v", err)
	}
	if preservedCommitCount == 0 {
		t.Fatal("preserved Dolt source has no history")
	}

	status := runSQLiteMigrationBD(t, bd, repoDir, "status", "--json")
	if !bytes.Contains(status, []byte(`"total_issues": 4`)) {
		t.Fatalf("first SQLite-backed status lost migrated issues:\n%s", status)
	}
	show := runSQLiteMigrationBD(t, bd, repoDir, "show", "mig-root", "--json")
	for _, want := range []string{"mig-root", "root description", "alpha", "beta", "mig-blocker"} {
		if !bytes.Contains(show, []byte(want)) {
			t.Fatalf("first SQLite-backed show missing %q:\n%s", want, show)
		}
	}
	blocked := runSQLiteMigrationBD(t, bd, repoDir, "blocked", "--json")
	if !bytes.Contains(blocked, []byte("mig-root")) {
		t.Fatalf("SQLite-backed blocked view lost derived blocked state:\n%s", blocked)
	}
	comments := runSQLiteMigrationBD(t, bd, repoDir, "comments", "mig-root", "--json")
	if !bytes.Contains(comments, []byte("migration comment")) {
		t.Fatalf("SQLite-backed comments lost migrated comment:\n%s", comments)
	}
	configValue := runSQLiteMigrationBD(t, bd, repoDir,
		"config", "get", "custom.migration-sentinel", "--json")
	if !bytes.Contains(configValue, []byte("kept")) {
		t.Fatalf("SQLite-backed config lost migrated value:\n%s", configValue)
	}
	afterCreate := runSQLiteMigrationBD(t, bd, repoDir,
		"create", "Created after cutover", "--parent=mig-root", "--json")
	if !bytes.Contains(afterCreate, []byte("mig-root.3")) {
		t.Fatalf("SQLite-backed child counter did not continue at mig-root.3:\n%s", afterCreate)
	}
	after := runSQLiteMigrationBD(t, bd, repoDir, "show", "mig-root.3", "--json")
	if !bytes.Contains(after, []byte("Created after cutover")) {
		t.Fatalf("SQLite-backed create was not readable:\n%s", after)
	}

	rerun := runSQLiteMigrationBDFailure(t, bd, repoDir,
		"migrate", "backend", "--to=sqlite", "--json")
	assertBackendMigrationErrorPayload(t, rerun, map[string]any{
		"code":             "backend_migration_unsupported_source",
		"authority":        "sqlite",
		"source_preserved": true,
	}, repoDir, beadsDir)
	if bytes.Contains(rerun, []byte("target_unavailable")) || bytes.Contains(rerun, []byte("retry_command")) {
		t.Fatalf("already-migrated rerun was mislabeled or suggested a retry:\n%s", rerun)
	}
}

func TestBackendMigrationSQLiteBootstrapRefusesBeforeDoltEffects(t *testing.T) {
	for _, service := range strings.Split(os.Getenv("BEADS_TEST_SKIP"), ",") {
		if strings.TrimSpace(service) == "dolt" {
			t.Skip("skipping: Dolt tests skipped (BEADS_TEST_SKIP=dolt)")
		}
	}

	var remoteConnections atomic.Int64
	var remoteRequests atomic.Int64
	remote := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		remoteRequests.Add(1)
		http.Error(w, "provider sentinel must not be reached", http.StatusServiceUnavailable)
	}))
	remote.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			remoteConnections.Add(1)
		}
	}
	remote.Start()
	defer remote.Close()

	bd := buildEmbeddedBD(t)
	repoDir := t.TempDir()
	initGitRepoAt(t, repoDir)
	remoteURL := remote.URL + "/org/beads"
	runSQLiteMigrationBD(t, bd, repoDir,
		"init", "--quiet", "--backend=dolt", "--prefix=bootstrap", "--skip-hooks")
	runSQLiteMigrationBD(t, bd, repoDir,
		"create", "Bootstrap migration sentinel", "--id=bootstrap-sentinel", "--json")
	runSQLiteMigrationBD(t, bd, repoDir,
		"config", "set", "sync.remote", remoteURL, "--json")
	runSQLiteMigrationBD(t, bd, repoDir,
		"migrate", "backend", "--to=sqlite", "--apply", "--yes", "--json")

	beadsDir := filepath.Join(repoDir, ".beads")
	metadataPath := configfile.ConfigPath(beadsDir)
	sqlitePath := filepath.Join(beadsDir, "beads.db")
	configYAMLPath := filepath.Join(beadsDir, "config.yaml")
	sourcePath := filepath.Join(beadsDir, "embeddeddolt")

	beforeMetadata, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("read migrated metadata: %v", err)
	}
	beforeSQLite, err := os.ReadFile(sqlitePath)
	if err != nil {
		t.Fatalf("read migrated SQLite database: %v", err)
	}
	beforeConfigYAML, err := os.ReadFile(configYAMLPath)
	if err != nil {
		t.Fatalf("read migrated config.yaml: %v", err)
	}
	beforeSource := backendMigrationTreeDigest(t, sourcePath)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bd, "bootstrap", "--yes", "--json")
	cmd.Dir = repoDir
	cmd.Env = backendMigrationCleanEnv(repoDir)
	stdout, stderr, runErr := runCommandBuffers(t, cmd)
	if ctx.Err() != nil {
		t.Fatalf("bootstrap contacted or waited on the retained Dolt provider instead of refusing: %v", ctx.Err())
	}
	if runErr == nil {
		t.Fatalf("bootstrap unexpectedly accepted a migrated SQLite workspace:\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("bootstrap JSON refusal wrote stderr:\n%s", stderr.String())
	}
	payload := decodeSingleJSONObject(t, stdout.Bytes())
	errorText, _ := payload["error"].(string)
	hintText, _ := payload["hint"].(string)
	for _, want := range []string{"bootstrap", "Dolt", "sqlite"} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("bootstrap refusal error missing %q: %#v", want, payload)
		}
	}
	for _, want := range []string{"bd status", "bd doctor"} {
		if !strings.Contains(hintText, want) {
			t.Fatalf("bootstrap refusal hint missing %q: %#v", want, payload)
		}
	}
	for _, forbidden := range []string{repoDir, beadsDir, remoteURL} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("bootstrap refusal leaked %q:\n%s", forbidden, stdout.String())
		}
	}
	if got := remoteConnections.Load(); got != 0 {
		t.Fatalf("bootstrap opened %d provider connection(s) after SQLite cutover", got)
	}
	if got := remoteRequests.Load(); got != 0 {
		t.Fatalf("bootstrap sent %d provider request(s) after SQLite cutover", got)
	}

	for path, want := range map[string][]byte{
		metadataPath:   beforeMetadata,
		sqlitePath:     beforeSQLite,
		configYAMLPath: beforeConfigYAML,
	} {
		after, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(after, want) {
			t.Fatalf("bootstrap changed %s after SQLite cutover: equal=%v err=%v", filepath.Base(path), bytes.Equal(after, want), readErr)
		}
	}
	if afterSource := backendMigrationTreeDigest(t, sourcePath); afterSource != beforeSource {
		t.Fatal("bootstrap changed the retained embedded-Dolt source after SQLite cutover")
	}

	sharedDoctorStore := doctor.NewSharedStore(repoDir)
	doctorCheck := doctor.CheckMigrationContentSkew(sharedDoctorStore)
	sharedDoctorStore.Close()
	if doctorCheck.Status != doctor.StatusOK || !strings.Contains(strings.ToLower(doctorCheck.Message), "n/a (backend sqlite)") {
		t.Fatalf("doctor migration-skew check did not classify retained Dolt as non-authoritative after SQLite cutover: %#v", doctorCheck)
	}
	for path, want := range map[string][]byte{
		metadataPath:   beforeMetadata,
		sqlitePath:     beforeSQLite,
		configYAMLPath: beforeConfigYAML,
	} {
		after, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(after, want) {
			t.Fatalf("doctor changed %s after SQLite cutover: equal=%v err=%v", filepath.Base(path), bytes.Equal(after, want), readErr)
		}
	}
	if afterSource := backendMigrationTreeDigest(t, sourcePath); afterSource != beforeSource {
		t.Fatal("doctor changed the retained embedded-Dolt source after SQLite cutover")
	}
	show := runSQLiteMigrationBD(t, bd, repoDir, "show", "bootstrap-sentinel", "--json")
	if !bytes.Contains(show, []byte("Bootstrap migration sentinel")) {
		t.Fatalf("bootstrap refusal damaged migrated SQLite state:\n%s", show)
	}
}

func TestBackendMigrationBootstrapBusyEmitsOnePathFreeJSONError(t *testing.T) {
	bd := buildEmbeddedBD(t)
	repoDir := t.TempDir()
	initGitRepoAt(t, repoDir)
	runSQLiteMigrationBD(t, bd, repoDir,
		"init", "--quiet", "--backend=dolt", "--prefix=bootstrap", "--skip-hooks")
	runSQLiteMigrationBD(t, bd, repoDir,
		"config", "set", "sync.remote", "https://provider.invalid/org/board", "--json")

	beadsDir := filepath.Join(repoDir, ".beads")
	guard, err := backendmigrationcontrol.TryAcquire(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close() //nolint:errcheck // test cleanup

	cmd := exec.Command(bd, "bootstrap", "--yes", "--json")
	cmd.Dir = repoDir
	cmd.Env = backendMigrationCleanEnv(repoDir)
	stdout, stderr, runErr := runCommandBuffers(t, cmd)
	if runErr == nil {
		t.Fatalf("bootstrap unexpectedly ignored busy migration control:\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("busy bootstrap JSON refusal wrote stderr:\n%s", stderr.String())
	}
	payload := decodeSingleJSONObject(t, stdout.Bytes())
	errorText, _ := payload["error"].(string)
	hintText, _ := payload["hint"].(string)
	if !strings.Contains(strings.ToLower(errorText), "backend") || !strings.Contains(strings.ToLower(errorText), "changing") {
		t.Fatalf("busy bootstrap error is not actionable: %#v", payload)
	}
	if !strings.Contains(strings.ToLower(hintText), "retry") {
		t.Fatalf("busy bootstrap hint is not actionable: %#v", payload)
	}
	for _, forbidden := range []string{repoDir, beadsDir, "provider.invalid", "action", "sync_remote"} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("busy bootstrap JSON leaked %q or emitted a plan:\n%s", forbidden, stdout.String())
		}
	}
}

func TestBackendMigrationBootstrapDryRunDoesNotPublishControl(t *testing.T) {
	bd := buildEmbeddedBD(t)
	repoDir := t.TempDir()
	initGitRepoAt(t, repoDir)
	beadsDir := filepath.Join(repoDir, ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	metadataPath := configfile.ConfigPath(beadsDir)
	metadata := []byte(`{"database":"dolt","backend":"dolt","dolt_mode":"embedded","dolt_database":"dry_run"}`)
	if err := os.WriteFile(metadataPath, metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	controlPath := filepath.Join(beadsDir, backendmigrationcontrol.FileName)

	cmd := exec.Command(bd, "bootstrap", "--dry-run", "--json")
	cmd.Dir = repoDir
	cmd.Env = backendMigrationCleanEnv(repoDir)
	stdout, stderr, runErr := runCommandBuffers(t, cmd)
	if runErr != nil {
		t.Fatalf("bootstrap dry-run failed: %v\nstdout:\n%s\nstderr:\n%s", runErr, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("bootstrap dry-run JSON wrote stderr:\n%s", stderr.String())
	}
	payload := decodeSingleJSONObject(t, stdout.Bytes())
	if payload["action"] != "init" {
		t.Fatalf("bootstrap dry-run plan = %#v, want init", payload)
	}
	if _, err := os.Lstat(controlPath); !os.IsNotExist(err) {
		t.Fatalf("bootstrap dry-run published migration control: %v", err)
	}
	after, err := os.ReadFile(metadataPath)
	if err != nil || !bytes.Equal(after, metadata) {
		t.Fatalf("bootstrap dry-run changed metadata: equal=%v err=%v", bytes.Equal(after, metadata), err)
	}
}

func TestBackendMigrationBootstrapDryRunDefersFreshCloneProviderProbe(t *testing.T) {
	var remoteConnections atomic.Int64
	remote := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "dry-run must not reach provider", http.StatusServiceUnavailable)
	}))
	remote.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			remoteConnections.Add(1)
		}
	}
	remote.Start()
	defer remote.Close()

	bd := buildEmbeddedBD(t)
	repoDir := t.TempDir()
	initGitRepoAt(t, repoDir)
	runGitForBootstrapTest(t, repoDir, "remote", "add", "origin", remote.URL+"/org/beads")

	cmd := exec.Command(bd, "bootstrap", "--dry-run", "--json")
	cmd.Dir = repoDir
	cmd.Env = backendMigrationCleanEnv(repoDir)
	stdout, stderr, runErr := runCommandBuffers(t, cmd)
	if runErr != nil {
		t.Fatalf("fresh-clone bootstrap dry-run failed: %v\nstdout:\n%s\nstderr:\n%s", runErr, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("fresh-clone bootstrap dry-run JSON wrote stderr:\n%s", stderr.String())
	}
	payload := decodeSingleJSONObject(t, stdout.Bytes())
	if payload["action"] != "deferred" || !strings.Contains(strings.ToLower(fmt.Sprint(payload["reason"])), "defer") {
		t.Fatalf("fresh-clone dry-run guessed instead of deferring provider detection: %#v", payload)
	}
	if got := remoteConnections.Load(); got != 0 {
		t.Fatalf("fresh-clone dry-run opened %d provider connection(s)", got)
	}
	if _, err := os.Lstat(filepath.Join(repoDir, ".beads")); !os.IsNotExist(err) {
		t.Fatalf("fresh-clone dry-run created .beads: %v", err)
	}
}

func TestBackendMigrationControlBlocksCaseVariantConfigSelectors(t *testing.T) {
	bd := buildEmbeddedBD(t)
	tests := []struct {
		name string
		args []string
	}{
		{name: "set", args: []string{"config", "set", "dolt.Mode", "server"}},
		{name: "unset", args: []string{"config", "unset", "dolt.Shared-Server"}},
		{name: "set-many", args: []string{"config", "set-many", "dolt.Mode=server"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repoDir := t.TempDir()
			initGitRepoAt(t, repoDir)
			runSQLiteMigrationBD(t, bd, repoDir,
				"init", "--quiet", "--backend=dolt", "--prefix=caseguard", "--skip-hooks")
			beadsDir := filepath.Join(repoDir, ".beads")
			configPath := filepath.Join(beadsDir, "config.yaml")
			configYAML, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			configYAML = append(configYAML, []byte("\ndolt.Shared-Server: false\n")...)
			if err := os.WriteFile(configPath, configYAML, 0o600); err != nil {
				t.Fatal(err)
			}

			guard, err := backendmigrationcontrol.TryAcquire(beadsDir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if closeErr := guard.Close(); closeErr != nil {
					t.Errorf("close migration control: %v", closeErr)
				}
			}()

			cmd := exec.Command(bd, test.args...)
			cmd.Dir = repoDir
			cmd.Env = backendMigrationCleanEnv(repoDir)
			output, runErr := cmd.CombinedOutput()
			if runErr == nil {
				t.Fatalf("bd %s bypassed migration control:\n%s", strings.Join(test.args, " "), output)
			}
			if !bytes.Contains(bytes.ToLower(output), []byte("control")) {
				t.Fatalf("bd %s returned unclear migration-control refusal:\n%s", strings.Join(test.args, " "), output)
			}
			afterConfigYAML, err := os.ReadFile(configPath)
			if err != nil || !bytes.Equal(afterConfigYAML, configYAML) {
				t.Fatalf("bd %s changed config during migration control: equal=%v err=%v", strings.Join(test.args, " "), bytes.Equal(afterConfigYAML, configYAML), err)
			}
		})
	}
}

func TestBackendMigrationLegacyMetadataPrerequisiteIsEffectFree(t *testing.T) {
	bd := buildEmbeddedBD(t)
	repoDir := t.TempDir()
	initGitRepoAt(t, repoDir)
	beadsDir := filepath.Join(repoDir, ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(beadsDir, "config.json")
	legacy := []byte(`{"backend":"dolt","dolt_mode":"embedded"}`)
	if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bd, "migrate", "backend", "--to=sqlite", "--json")
	cmd.Dir = repoDir
	cmd.Env = backendMigrationCleanEnv(repoDir)
	stdout, stderr, runErr := runCommandBuffers(t, cmd)
	if runErr == nil {
		t.Fatalf("legacy-only migration preview unexpectedly succeeded:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("legacy-only migration wrote stderr in JSON mode:\n%s", stderr.String())
	}
	payload := decodeSingleJSONObject(t, stdout.Bytes())
	if payload["code"] != "backend_migration_unsupported_source" || !strings.Contains(payload["error"].(string), "bd doctor") {
		t.Fatalf("legacy-only prerequisite was not actionable:\n%s", stdout.String())
	}
	if retry, present := payload["retry_command"]; present {
		t.Fatalf("legacy-only prerequisite suggested impossible retry %#v", retry)
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil || !bytes.Equal(after, legacy) {
		t.Fatalf("legacy-only preview changed config.json: equal=%v err=%v", bytes.Equal(after, legacy), err)
	}
	for _, path := range []string{
		configfile.ConfigPath(beadsDir),
		filepath.Join(beadsDir, "beads.db"),
		filepath.Join(beadsDir, "backend-migration-control.lock"),
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("legacy-only preview created %s: %v", path, err)
		}
	}
}

func TestBackendMigrationJSONProtocolAdmissionFailures(t *testing.T) {
	bd := buildEmbeddedBD(t)

	t.Run("fresh home invalid target", func(t *testing.T) {
		home := t.TempDir()
		cmd := exec.Command(bd, "migrate", "backend", "--to=not-a-backend", "--json")
		cmd.Dir = t.TempDir()
		cmd.Env = backendMigrationMetricsEnabledEnv(home)
		stdout, stderr, runErr := runCommandBuffers(t, cmd)
		if runErr == nil {
			t.Fatalf("invalid target unexpectedly succeeded:\n%s", stdout.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("fresh-home JSON failure wrote stderr:\n%s", stderr.String())
		}
		payload := decodeSingleJSONObject(t, stdout.Bytes())
		if !strings.Contains(payload["error"].(string), "--to=sqlite") {
			t.Fatalf("invalid-target JSON was not actionable:\n%s", stdout.String())
		}
	})

	repoDir := t.TempDir()
	initGitRepoAt(t, repoDir)
	runSQLiteMigrationBD(t, bd, repoDir,
		"init", "--quiet", "--backend=dolt", "--prefix=protocol", "--skip-hooks")
	beadsDir := filepath.Join(repoDir, ".beads")
	metadataPath := configfile.ConfigPath(beadsDir)
	beforeMetadata, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(beadsDir, "beads.db")
	configPath := filepath.Join(beadsDir, "config.yaml")
	beforeConfig, beforeConfigErr := os.ReadFile(configPath)
	if beforeConfigErr != nil && !os.IsNotExist(beforeConfigErr) {
		t.Fatal(beforeConfigErr)
	}
	restoreConfig := func() {
		if os.IsNotExist(beforeConfigErr) {
			_ = os.Remove(configPath)
			return
		}
		if err := os.WriteFile(configPath, beforeConfig, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	run := func(t *testing.T, dir string, env []string, args ...string) (map[string]any, error) {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = env
		stdout, stderr, runErr := runCommandBuffers(t, cmd)
		if stderr.Len() != 0 {
			t.Fatalf("bd %s wrote stderr in JSON mode:\n%s", strings.Join(args, " "), stderr.String())
		}
		return decodeSingleJSONObject(t, stdout.Bytes()), runErr
	}

	t.Run("0755 preview is clean", func(t *testing.T) {
		if err := os.Chmod(beadsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		payload, runErr := run(t, repoDir, backendMigrationCleanEnv(repoDir),
			"migrate", "backend", "--to=sqlite", "--json")
		if runErr != nil || payload["status"] != "planned" {
			t.Fatalf("0755 preview = %#v err=%v", payload, runErr)
		}
	})

	t.Run("world-writable workspace refuses safely", func(t *testing.T) {
		if err := os.Chmod(beadsDir, 0o777); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(beadsDir, 0o700) //nolint:errcheck // test cleanup
		payload, runErr := run(t, repoDir, backendMigrationCleanEnv(repoDir),
			"migrate", "backend", "--to=sqlite", "--json")
		if runErr == nil || payload["code"] != "backend_migration_unsafe_workspace" {
			t.Fatalf("unsafe-workspace preview = %#v err=%v", payload, runErr)
		}
		if strings.Contains(fmt.Sprint(payload), repoDir) {
			t.Fatalf("unsafe-workspace JSON leaked a path: %#v", payload)
		}
	})
	if err := os.Chmod(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	t.Run("malformed config refuses safely", func(t *testing.T) {
		if err := os.WriteFile(configPath, []byte("dolt: ["), 0o600); err != nil {
			t.Fatal(err)
		}
		defer restoreConfig()
		payload, runErr := run(t, repoDir, backendMigrationCleanEnv(repoDir),
			"migrate", "backend", "--to=sqlite", "--json")
		if runErr == nil || !strings.Contains(fmt.Sprint(payload["error"]), "safely read") {
			t.Fatalf("malformed-config preview = %#v err=%v", payload, runErr)
		}
		if strings.Contains(fmt.Sprint(payload), repoDir) {
			t.Fatalf("malformed-config JSON leaked a path: %#v", payload)
		}
	})
	restoreConfig()

	t.Run("readonly apply refuses safely", func(t *testing.T) {
		payload, runErr := run(t, repoDir, backendMigrationCleanEnv(repoDir),
			"--readonly", "migrate", "backend", "--to=sqlite", "--apply", "--yes", "--json")
		if runErr == nil || !strings.Contains(fmt.Sprint(payload["error"]), "read-only") {
			t.Fatalf("readonly apply = %#v err=%v", payload, runErr)
		}
	})

	t.Run("blocked backend environment refuses safely", func(t *testing.T) {
		env := append(backendMigrationCleanEnv(repoDir), "BD_BACKEND=sqlite")
		payload, runErr := run(t, repoDir, env,
			"migrate", "backend", "--to=sqlite", "--json")
		if runErr == nil || !strings.Contains(fmt.Sprint(payload["error"]), "BD_BACKEND") {
			t.Fatalf("blocked-env preview = %#v err=%v", payload, runErr)
		}
	})

	t.Run("format alias preserves JSON for early environment refusal", func(t *testing.T) {
		env := append(backendMigrationCleanEnv(repoDir), "BD_BACKEND=sqlite")
		payload, runErr := run(t, repoDir, env,
			"migrate", "backend", "--to=sqlite", "--format=json")
		if runErr == nil || !strings.Contains(fmt.Sprint(payload["error"]), "BD_BACKEND") {
			t.Fatalf("format-alias blocked-env preview = %#v err=%v", payload, runErr)
		}
	})

	t.Run("invalid change directory refuses safely", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing")
		payload, runErr := run(t, t.TempDir(), backendMigrationCleanEnv(repoDir),
			"-C", missing, "migrate", "backend", "--to=sqlite", "--json")
		if runErr == nil || !strings.Contains(fmt.Sprint(payload["error"]), "requested -C workspace") {
			t.Fatalf("invalid -C preview = %#v err=%v", payload, runErr)
		}
		if strings.Contains(fmt.Sprint(payload), missing) {
			t.Fatalf("invalid -C JSON leaked requested path: %#v", payload)
		}
	})

	t.Run("format alias preserves JSON for early change-directory refusal", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing")
		payload, runErr := run(t, t.TempDir(), backendMigrationCleanEnv(repoDir),
			"-C", missing, "migrate", "backend", "--to=sqlite", "--format=json")
		if runErr == nil || !strings.Contains(fmt.Sprint(payload["error"]), "requested -C workspace") {
			t.Fatalf("format-alias invalid -C preview = %#v err=%v", payload, runErr)
		}
		if strings.Contains(fmt.Sprint(payload), missing) {
			t.Fatalf("format-alias invalid -C JSON leaked requested path: %#v", payload)
		}
	})

	t.Run("redirected change directory refuses safely", func(t *testing.T) {
		redirectRepo := t.TempDir()
		redirectBeadsDir := filepath.Join(redirectRepo, ".beads")
		if err := os.Mkdir(redirectBeadsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(redirectBeadsDir, "redirect"), []byte(beadsDir+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		payload, runErr := run(t, t.TempDir(), backendMigrationCleanEnv(repoDir),
			"-C", redirectRepo, "migrate", "backend", "--to=sqlite", "--json")
		if runErr == nil || !strings.Contains(strings.ToLower(fmt.Sprint(payload["error"])), "physical") {
			t.Fatalf("redirected -C preview = %#v err=%v", payload, runErr)
		}
	})

	afterMetadata, err := os.ReadFile(metadataPath)
	if err != nil || !bytes.Equal(afterMetadata, beforeMetadata) {
		t.Fatalf("protocol refusals changed metadata: equal=%v err=%v", bytes.Equal(afterMetadata, beforeMetadata), err)
	}
	if _, err := os.Lstat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("protocol refusals created SQLite target: %v", err)
	}
}

func TestBackendMigrationEnvDecoysAndChangeDirSelection(t *testing.T) {
	bd := buildEmbeddedBD(t)
	repoA := t.TempDir()
	repoB := t.TempDir()
	for _, repo := range []string{repoA, repoB} {
		initGitRepoAt(t, repo)
		runSQLiteMigrationBD(t, bd, repo,
			"init", "--quiet", "--backend=dolt", "--prefix=selector", "--skip-hooks")
	}
	beadsA := filepath.Join(repoA, ".beads")
	beadsB := filepath.Join(repoB, ".beads")
	metadataA := configfile.ConfigPath(beadsA)
	metadataB := configfile.ConfigPath(beadsB)
	beforeA, err := os.ReadFile(metadataA)
	if err != nil {
		t.Fatal(err)
	}
	beforeB, err := os.ReadFile(metadataB)
	if err != nil {
		t.Fatal(err)
	}

	for key, value := range map[string]string{
		"BEADS_DIR": beadsB,
		"BEADS_DB":  filepath.Join(beadsB, "beads.db"),
		"BD_DB":     filepath.Join(beadsB, "legacy.db"),
	} {
		t.Run("env file "+key, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(beadsA, ".env"), []byte(key+"="+value+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			defer os.Remove(filepath.Join(beadsA, ".env")) //nolint:errcheck // test cleanup
			cmd := exec.Command(bd, "migrate", "backend", "--to=sqlite", "--json")
			cmd.Dir = repoA
			cmd.Env = backendMigrationCleanEnv(repoA)
			stdout, stderr, runErr := runCommandBuffers(t, cmd)
			if runErr == nil || stderr.Len() != 0 {
				t.Fatalf(".env %s refusal err=%v stdout=%s stderr=%s", key, runErr, stdout.String(), stderr.String())
			}
			payload := decodeSingleJSONObject(t, stdout.Bytes())
			if !strings.Contains(fmt.Sprint(payload["error"]), key) {
				t.Fatalf(".env %s refusal was not actionable: %#v", key, payload)
			}
			assertMetadataPairUnchanged(t, metadataA, beforeA, metadataB, beforeB)
		})
	}

	env := append(backendMigrationCleanEnv(repoA),
		"BEADS_DIR="+beadsB,
		"BEADS_DB="+filepath.Join(beadsB, "beads.db"),
		"BD_DB="+filepath.Join(beadsB, "legacy.db"),
	)
	cmd := exec.Command(bd, "-C", repoA, "migrate", "backend", "--to=sqlite", "--json")
	cmd.Dir = t.TempDir()
	cmd.Env = env
	stdout, stderr, runErr := runCommandBuffers(t, cmd)
	if runErr != nil || stderr.Len() != 0 {
		t.Fatalf("physical -C preview failed: %v\nstdout:\n%s\nstderr:\n%s", runErr, stdout.String(), stderr.String())
	}
	payload := decodeSingleJSONObject(t, stdout.Bytes())
	if payload["status"] != "planned" {
		t.Fatalf("physical -C preview = %#v, want planned", payload)
	}
	assertMetadataPairUnchanged(t, metadataA, beforeA, metadataB, beforeB)
	for _, path := range []string{filepath.Join(beadsA, "beads.db"), filepath.Join(beadsB, "beads.db")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("selector preview created %s: %v", path, err)
		}
	}
}

func TestBackendMigrationRejectsSharedGitWorktreeFallback(t *testing.T) {
	bd := buildEmbeddedBD(t)
	root := t.TempDir()
	mainRepo := filepath.Join(root, "main")
	worktreeRepo := filepath.Join(root, "secondary")
	if err := os.Mkdir(mainRepo, 0o700); err != nil {
		t.Fatal(err)
	}
	initGitRepoAt(t, mainRepo)
	runSQLiteMigrationBD(t, bd, mainRepo,
		"init", "--quiet", "--backend=dolt", "--prefix=physical", "--skip-hooks")
	if err := os.WriteFile(filepath.Join(mainRepo, "README.md"), []byte("physical workspace fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "physical workspace fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = mainRepo
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	add := exec.Command("git", "worktree", "add", "--detach", worktreeRepo, "HEAD")
	add.Dir = mainRepo
	if output, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add failed: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		remove := exec.Command("git", "worktree", "remove", "--force", worktreeRepo)
		remove.Dir = mainRepo
		_ = remove.Run()
	})

	mainBeadsDir := filepath.Join(mainRepo, ".beads")
	worktreeBeadsDir := filepath.Join(worktreeRepo, ".beads")
	mainMetadataPath := configfile.ConfigPath(mainBeadsDir)
	worktreeMetadataPath := configfile.ConfigPath(worktreeBeadsDir)
	mainMetadata, err := os.ReadFile(mainMetadataPath)
	if err != nil {
		t.Fatal(err)
	}
	worktreeMetadata, err := os.ReadFile(worktreeMetadataPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(worktreeBeadsDir, "embeddeddolt")); !os.IsNotExist(err) {
		t.Fatalf("secondary worktree unexpectedly owns embedded Dolt: %v", err)
	}

	for _, test := range []struct {
		name string
		dir  string
		args []string
	}{
		{name: "implicit", dir: worktreeRepo, args: []string{"migrate", "backend", "--to=sqlite", "--json"}},
		{name: "change directory", dir: t.TempDir(), args: []string{"-C", worktreeRepo, "migrate", "backend", "--to=sqlite", "--json"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command(bd, test.args...)
			cmd.Dir = test.dir
			cmd.Env = backendMigrationCleanEnv(worktreeRepo)
			stdout, stderr, runErr := runCommandBuffers(t, cmd)
			if runErr == nil {
				t.Fatalf("shared worktree migration unexpectedly succeeded:\n%s", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("shared worktree JSON refusal wrote stderr:\n%s", stderr.String())
			}
			payload := decodeSingleJSONObject(t, stdout.Bytes())
			if !strings.Contains(strings.ToLower(fmt.Sprint(payload["error"])), "physical") {
				t.Fatalf("shared worktree refusal was not actionable: %#v", payload)
			}
			assertMetadataPairUnchanged(t, mainMetadataPath, mainMetadata, worktreeMetadataPath, worktreeMetadata)
			for _, path := range []string{filepath.Join(mainBeadsDir, "beads.db"), filepath.Join(worktreeBeadsDir, "beads.db")} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("shared worktree refusal created %s: %v", path, err)
				}
			}
		})
	}
}

func TestBackendReinitializationExplicitlyPublishesProvisionedSQLite(t *testing.T) {
	bd := buildEmbeddedBD(t)
	repoDir := t.TempDir()
	initGitRepoAt(t, repoDir)
	runSQLiteMigrationBD(t, bd, repoDir,
		"init", "--quiet", "--backend=dolt", "--prefix=reinit", "--skip-hooks")
	beadsDir := filepath.Join(repoDir, ".beads")

	cmd := exec.Command(bd, "init", "--reinit-local", "--backend=sqlite", "--quiet", "--skip-hooks", "--skip-agents", "--non-interactive", "--json")
	cmd.Dir = repoDir
	cmd.Env = backendMigrationCleanEnv(repoDir)
	stdout, stderr, runErr := runCommandBuffers(t, cmd)
	if runErr != nil {
		t.Fatalf("authorized backend reinitialization failed: %v\nstdout:\n%s\nstderr:\n%s", runErr, stdout.String(), stderr.String())
	}
	decodeSingleJSONObject(t, stdout.Bytes())
	cfg, err := configfile.LoadReadOnly(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GetBackend() != configfile.BackendSQLite {
		t.Fatalf("authorized reinit backend = %q, want sqlite", cfg.GetBackend())
	}
	if _, err := os.Stat(filepath.Join(beadsDir, "beads.db")); err != nil {
		t.Fatalf("authorized reinit orphaned provisioned SQLite target: %v", err)
	}
	status := runSQLiteMigrationBD(t, bd, repoDir, "status", "--json")
	if !json.Valid(status) {
		t.Fatalf("reinitialized SQLite store was not immediately usable:\n%s", status)
	}
}

func TestArtifactCleanupJSONSuccessIsSingleDocument(t *testing.T) {
	bd := buildEmbeddedBD(t)
	root := t.TempDir()
	beadsDir := filepath.Join(root, ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded}).Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	walPath := filepath.Join(beadsDir, "beads.db-wal")
	if err := os.WriteFile(walPath, []byte("removable artifact"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bd, "doctor", root, "--check=artifacts", "--clean", "--yes", "--json")
	cmd.Dir = t.TempDir()
	cmd.Env = backendMigrationCleanEnv(root)
	stdout, stderr, runErr := runCommandBuffers(t, cmd)
	if runErr != nil {
		t.Fatalf("JSON artifact cleanup failed: %v\nstdout:\n%s\nstderr:\n%s", runErr, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("JSON artifact cleanup wrote stderr:\n%s", stderr.String())
	}
	payload := decodeSingleJSONObject(t, stdout.Bytes())
	if payload["total_count"] != float64(0) {
		t.Fatalf("post-cleanup JSON = %#v, want no remaining artifacts", payload)
	}
	if _, err := os.Lstat(walPath); !os.IsNotExist(err) {
		t.Fatalf("JSON artifact cleanup did not remove WAL: %v", err)
	}
}

func TestBackendMigrationPendingBlocksNoStoreCommandsBeforeEffects(t *testing.T) {
	for _, service := range strings.Split(os.Getenv("BEADS_TEST_SKIP"), ",") {
		if strings.TrimSpace(service) == "dolt" {
			t.Skip("skipping: Dolt tests skipped (BEADS_TEST_SKIP=dolt)")
		}
	}
	bd := buildEmbeddedBD(t)
	tests := []struct {
		name             string
		args             []string
		explicitTarget   bool
		artifactSentinel bool
		profile          bool
	}{
		{
			name: "init reinit-local",
			args: []string{"init", "--reinit-local", "--backend=sqlite", "--quiet", "--skip-hooks", "--skip-agents", "--non-interactive", "--json"},
		},
		{
			name: "bootstrap apply",
			args: []string{"bootstrap", "--yes", "--json"},
		},
		{
			name: "doctor fix",
			args: []string{"doctor", "--fix", "--yes", "--json"},
		},
		{
			name:           "doctor explicit workspace fix",
			args:           []string{"doctor", "--fix", "--yes", "--json"},
			explicitTarget: true,
		},
		{
			name:             "doctor explicit workspace artifact clean",
			args:             []string{"doctor", "--check=artifacts", "--clean", "--yes", "--json"},
			explicitTarget:   true,
			artifactSentinel: true,
		},
		{
			name: "config storage selector",
			args: []string{"config", "set", "dolt.mode", "server", "--json"},
		},
		{
			name:    "store command profiling",
			args:    []string{"status", "--profile", "--json"},
			profile: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoDir := t.TempDir()
			initGitRepoAt(t, repoDir)
			runSQLiteMigrationBD(t, bd, repoDir,
				"init", "--quiet", "--backend=dolt", "--prefix=guard", "--skip-hooks")
			runSQLiteMigrationBD(t, bd, repoDir,
				"create", "Migration guard sentinel", "--id=guard-sentinel", "--json")

			beadsDir := filepath.Join(repoDir, ".beads")
			metadataPath := configfile.ConfigPath(beadsDir)
			beforeMetadata, err := os.ReadFile(metadataPath)
			if err != nil {
				t.Fatalf("read source metadata: %v", err)
			}
			markerPath := filepath.Join(beadsDir, configfile.BackendMigrationStateFileName)
			marker := []byte("pending migration fixture")
			if err := os.WriteFile(markerPath, marker, 0o600); err != nil {
				t.Fatalf("write pending migration fixture: %v", err)
			}
			localVersionPath := filepath.Join(beadsDir, ".local_version")
			if err := os.Remove(localVersionPath); err != nil && !os.IsNotExist(err) {
				t.Fatalf("remove local version fixture: %v", err)
			}
			artifactPath := filepath.Join(beadsDir, "beads.db-wal")
			artifact := []byte("recovery artifact sentinel")
			if tt.artifactSentinel {
				if err := os.WriteFile(artifactPath, artifact, 0o600); err != nil {
					t.Fatalf("write recovery artifact sentinel: %v", err)
				}
			}
			configYAMLPath := filepath.Join(beadsDir, "config.yaml")
			beforeConfigYAML, beforeConfigYAMLErr := os.ReadFile(configYAMLPath)
			if beforeConfigYAMLErr != nil && !os.IsNotExist(beforeConfigYAMLErr) {
				t.Fatalf("read source config.yaml: %v", beforeConfigYAMLErr)
			}

			args := append([]string(nil), tt.args...)
			runDir := repoDir
			if tt.explicitTarget {
				args = append([]string{"doctor", repoDir}, tt.args[1:]...)
				runDir = t.TempDir()
			}
			cmd := exec.Command(bd, args...)
			cmd.Dir = runDir
			cmd.Env = bdEnv(repoDir)
			stdout, stderr, runErr := runCommandBuffers(t, cmd)
			if runErr == nil {
				t.Fatalf("bd %s unexpectedly bypassed pending migration recovery\nstdout:\n%s\nstderr:\n%s",
					strings.Join(args, " "), stdout.String(), stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("bd %s leaked recovery output on stderr in JSON mode:\n%s",
					strings.Join(args, " "), stderr.String())
			}
			assertBackendMigrationErrorPayload(t, stdout.Bytes(), map[string]any{
				"code":             "backend_migration_pending",
				"authority":        "dolt_source",
				"source_preserved": true,
				"retry_command":    "bd migrate backend --to=sqlite --apply",
			}, repoDir, beadsDir)

			afterMetadata, err := os.ReadFile(metadataPath)
			if err != nil || !bytes.Equal(afterMetadata, beforeMetadata) {
				t.Fatalf("bd %s changed source metadata while recovery was pending: equal=%v err=%v",
					strings.Join(args, " "), bytes.Equal(afterMetadata, beforeMetadata), err)
			}
			afterMarker, err := os.ReadFile(markerPath)
			if err != nil || !bytes.Equal(afterMarker, marker) {
				t.Fatalf("bd %s changed the recovery marker: equal=%v err=%v",
					strings.Join(args, " "), bytes.Equal(afterMarker, marker), err)
			}
			if _, err := os.Stat(filepath.Join(beadsDir, "embeddeddolt")); err != nil {
				t.Fatalf("bd %s changed the embedded Dolt source: %v", strings.Join(args, " "), err)
			}
			afterConfigYAML, afterConfigYAMLErr := os.ReadFile(configYAMLPath)
			if !sameOptionalFile(beforeConfigYAML, beforeConfigYAMLErr, afterConfigYAML, afterConfigYAMLErr) {
				t.Fatalf("bd %s changed config.yaml while recovery was pending: before_err=%v after_err=%v",
					strings.Join(args, " "), beforeConfigYAMLErr, afterConfigYAMLErr)
			}
			if _, err := os.Lstat(filepath.Join(beadsDir, "beads.db")); !os.IsNotExist(err) {
				t.Fatalf("bd %s created the SQLite migration target: %v", strings.Join(args, " "), err)
			}
			if _, err := os.Lstat(localVersionPath); !os.IsNotExist(err) {
				t.Fatalf("bd %s wrote .local_version before refusing: %v", strings.Join(args, " "), err)
			}
			if tt.artifactSentinel {
				afterArtifact, err := os.ReadFile(artifactPath)
				if err != nil || !bytes.Equal(afterArtifact, artifact) {
					t.Fatalf("bd %s changed the recovery artifact: equal=%v err=%v",
						strings.Join(args, " "), bytes.Equal(afterArtifact, artifact), err)
				}
			}
			if tt.profile {
				profiles, profileErr := filepath.Glob(filepath.Join(runDir, "bd-profile-*"))
				traces, traceErr := filepath.Glob(filepath.Join(runDir, "bd-trace-*"))
				if profileErr != nil || traceErr != nil || len(profiles) != 0 || len(traces) != 0 {
					t.Fatalf("bd %s created profiling effects before refusing: profiles=%v traces=%v errs=%v/%v",
						strings.Join(args, " "), profiles, traces, profileErr, traceErr)
				}
			}

			if err := os.Remove(markerPath); err != nil {
				t.Fatalf("remove pending migration fixture: %v", err)
			}
			show := runSQLiteMigrationBD(t, bd, repoDir, "show", "guard-sentinel", "--json")
			if !bytes.Contains(show, []byte("Migration guard sentinel")) {
				t.Fatalf("bd %s damaged source state before refusing:\n%s", strings.Join(args, " "), show)
			}
		})
	}
}

func TestBackendMigrationPendingBlocksMarkerOnlyNoStoreMutations(t *testing.T) {
	bd := buildEmbeddedBD(t)
	tests := []struct {
		name             string
		command          string
		markerName       string
		configYAML       bool
		artifactSentinel bool
		changeDir        bool
	}{
		{
			name:       "config selector with primary marker",
			command:    "config",
			markerName: configfile.BackendMigrationStateFileName,
			configYAML: true,
		},
		{
			name:       "config selector with cleanup marker",
			command:    "config",
			markerName: ".backend-migration-test.cleanup.lock",
			configYAML: true,
		},
		{
			name:       "init reinit with marker only",
			command:    "init",
			markerName: configfile.BackendMigrationStateFileName,
		},
		{
			name:       "bootstrap with cleanup marker only",
			command:    "bootstrap",
			markerName: ".backend-migration-test.cleanup.lock",
		},
		{
			name:       "init change-dir overrides ambient database",
			command:    "init",
			markerName: configfile.BackendMigrationStateFileName,
			changeDir:  true,
		},
		{
			name:             "explicit doctor artifact cleanup",
			command:          "doctor",
			markerName:       configfile.BackendMigrationStateFileName,
			artifactSentinel: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoDir := t.TempDir()
			initGitRepoAt(t, repoDir)
			beadsDir := filepath.Join(repoDir, ".beads")
			if err := os.MkdirAll(beadsDir, 0o700); err != nil {
				t.Fatalf("create marker-only beads directory: %v", err)
			}
			configYAMLPath := filepath.Join(beadsDir, "config.yaml")
			configYAML := []byte("# recovery config sentinel\n")
			if tt.configYAML {
				if err := os.WriteFile(configYAMLPath, configYAML, 0o600); err != nil {
					t.Fatalf("write config.yaml sentinel: %v", err)
				}
			}
			markerPath := filepath.Join(beadsDir, tt.markerName)
			marker := []byte("pending migration fixture")
			if err := os.WriteFile(markerPath, marker, 0o600); err != nil {
				t.Fatalf("write migration marker: %v", err)
			}
			artifactPath := filepath.Join(beadsDir, "beads.db-wal")
			artifact := []byte("recovery artifact sentinel")
			if tt.artifactSentinel {
				if err := os.WriteFile(artifactPath, artifact, 0o600); err != nil {
					t.Fatalf("write artifact sentinel: %v", err)
				}
			}

			var args []string
			runDir := repoDir
			switch tt.command {
			case "init":
				args = []string{"init", "--reinit-local", "--backend=sqlite", "--quiet", "--skip-hooks", "--skip-agents", "--non-interactive", "--json"}
			case "bootstrap":
				args = []string{"bootstrap", "--yes", "--json"}
			case "doctor":
				args = []string{"doctor", repoDir, "--check=artifacts", "--clean", "--yes", "--json"}
				runDir = t.TempDir()
			default:
				args = []string{"config", "set", "dolt.mode", "server", "--json"}
			}
			env := bdEnv(repoDir)
			if tt.changeDir {
				args = append([]string{"-C", repoDir}, args...)
				runDir = t.TempDir()
				env = envWithout(envWithout(env, "BEADS_DB"), "BD_DB")
				env = append(env,
					"BEADS_DB="+filepath.Join(t.TempDir(), "ambient.db"),
					"BD_DB="+filepath.Join(t.TempDir(), "ambient-legacy.db"),
				)
			}
			cmd := exec.Command(bd, args...)
			cmd.Dir = runDir
			cmd.Env = env
			stdout, stderr, runErr := runCommandBuffers(t, cmd)
			if runErr == nil {
				t.Fatalf("bd %s unexpectedly bypassed marker-only recovery\nstdout:\n%s\nstderr:\n%s",
					strings.Join(args, " "), stdout.String(), stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("bd %s leaked recovery output on stderr in JSON mode:\n%s",
					strings.Join(args, " "), stderr.String())
			}
			assertBackendMigrationErrorPayload(t, stdout.Bytes(), map[string]any{
				"code":             "backend_migration_pending",
				"authority":        "unknown",
				"source_preserved": true,
				"retry_command":    "bd migrate backend --to=sqlite --apply",
			}, repoDir, beadsDir)

			for path, want := range map[string][]byte{markerPath: marker} {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("bd %s changed %s: equal=%v err=%v",
						strings.Join(args, " "), filepath.Base(path), bytes.Equal(got, want), err)
				}
			}
			if tt.configYAML {
				got, err := os.ReadFile(configYAMLPath)
				if err != nil || !bytes.Equal(got, configYAML) {
					t.Fatalf("bd %s changed config.yaml: equal=%v err=%v",
						strings.Join(args, " "), bytes.Equal(got, configYAML), err)
				}
			}
			if tt.artifactSentinel {
				got, err := os.ReadFile(artifactPath)
				if err != nil || !bytes.Equal(got, artifact) {
					t.Fatalf("bd %s changed recovery WAL: equal=%v err=%v",
						strings.Join(args, " "), bytes.Equal(got, artifact), err)
				}
			}
			for _, path := range []string{
				filepath.Join(repoDir, ".gitignore"),
				filepath.Join(beadsDir, ".gitignore"),
				filepath.Join(beadsDir, ".local_version"),
				configfile.ConfigPath(beadsDir),
				filepath.Join(beadsDir, "beads.db"),
			} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("bd %s created %s before refusing: %v", strings.Join(args, " "), path, err)
				}
			}
		})
	}
}

func TestBackendMigrationPendingArtifactCleanupPreflightsBeforeJSONOutput(t *testing.T) {
	bd := buildEmbeddedBD(t)
	root := t.TempDir()
	beforeBeadsDir := filepath.Join(root, "a", ".beads")
	markedBeadsDir := filepath.Join(root, "z", ".beads")
	wals := make(map[string][]byte)
	for _, beadsDir := range []string{beforeBeadsDir, markedBeadsDir} {
		if err := os.MkdirAll(beadsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		walPath := filepath.Join(beadsDir, "beads.db-wal")
		wals[walPath] = []byte("recovery state at " + filepath.Base(filepath.Dir(beadsDir)))
		if err := os.WriteFile(walPath, wals[walPath], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	markerPath := filepath.Join(markedBeadsDir, configfile.BackendMigrationStateFileName)
	marker := []byte("pending migration fixture")
	if err := os.WriteFile(markerPath, marker, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, clean := range []bool{false, true} {
		args := []string{"doctor", root, "--check=artifacts", "--json"}
		operation := "scan"
		if clean {
			args = append(args, "--clean", "--yes")
			operation = "cleanup"
		}
		cmd := exec.Command(bd, args...)
		cmd.Dir = t.TempDir()
		cmd.Env = bdEnv(root)
		stdout, stderr, runErr := runCommandBuffers(t, cmd)
		if runErr == nil {
			t.Fatalf("recursive artifact %s unexpectedly bypassed pending migration\nstdout:\n%s\nstderr:\n%s", operation, stdout.String(), stderr.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("recursive artifact %s wrote JSON recovery output to stderr:\n%s", operation, stderr.String())
		}
		assertBackendMigrationErrorPayload(t, stdout.Bytes(), map[string]any{
			"code":             "backend_migration_pending",
			"authority":        "unknown",
			"source_preserved": true,
		}, root, beforeBeadsDir, markedBeadsDir)
		var errorPayload map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &errorPayload); err != nil {
			t.Fatalf("decode recursive pending-migration payload: %v", err)
		}
		if retry, ok := errorPayload["retry_command"]; ok {
			t.Fatalf("recursive artifact %s payload exposed unsafe unscoped retry %#v", operation, retry)
		}
	}
	for path, want := range wals {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("recursive preflight changed %s: equal=%v err=%v", path, bytes.Equal(got, want), err)
		}
	}
	gotMarker, err := os.ReadFile(markerPath)
	if err != nil || !bytes.Equal(gotMarker, marker) {
		t.Fatalf("recursive preflight changed marker: equal=%v err=%v", bytes.Equal(gotMarker, marker), err)
	}
}

func sameOptionalFile(before []byte, beforeErr error, after []byte, afterErr error) bool {
	if os.IsNotExist(beforeErr) || os.IsNotExist(afterErr) {
		return os.IsNotExist(beforeErr) && os.IsNotExist(afterErr)
	}
	return beforeErr == nil && afterErr == nil && bytes.Equal(before, after)
}

func backendMigrationTreeDigest(t *testing.T, root string) string {
	t.Helper()
	digest := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(digest, "%s\x00%d\x00%d\x00%d\x00", filepath.ToSlash(relative), info.Mode(), info.Size(), info.ModTime().UnixNano()); err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_, err = io.WriteString(digest, target)
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		file, err := os.Open(path) // #nosec G304 -- test snapshots a t.TempDir workspace
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(digest, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		t.Fatalf("snapshot backend tree %s: %v", filepath.Base(root), err)
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}

func assertBackendMigrationErrorPayload(t *testing.T, payload []byte, want map[string]any, forbidden ...string) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("backend migration error returned invalid JSON: %v\n%s", err, payload)
	}
	for key, wantValue := range want {
		if gotValue := got[key]; gotValue != wantValue {
			t.Errorf("backend migration error %s = %#v, want %#v\n%s", key, gotValue, wantValue, payload)
		}
	}
	for _, value := range forbidden {
		if value != "" && bytes.Contains(payload, []byte(value)) {
			t.Fatalf("backend migration error leaked %q:\n%s", value, payload)
		}
	}
}

func assertSQLiteMigrationPreviewHadNoEffect(t *testing.T, metadataPath, targetPath string, wantMetadata []byte) {
	t.Helper()
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("refused migration created SQLite target: %v", err)
	}
	metadata, err := os.ReadFile(metadataPath)
	if err != nil || !bytes.Equal(metadata, wantMetadata) {
		t.Fatalf("refused migration changed metadata: equal=%v err=%v", bytes.Equal(metadata, wantMetadata), err)
	}
}

func backendMigrationCleanEnv(home string) []string {
	env := bdEnv(home)
	for _, key := range []string{
		"BEADS_DIR", "BEADS_DB", "BD_DB", "BEADS_DOLT_SERVER_DATABASE", "BEADS_DOLT_DATA_DIR",
		"BEADS_DOLT_SERVER_MODE", "BEADS_DOLT_SHARED_SERVER", "BD_IGNORE_SCHEMA_SKEW",
		"BD_BACKEND", "BD_DATABASE_BACKEND", "BD_JSON_ENVELOPE",
	} {
		env = envWithout(env, key)
	}
	return append(env, "BD_JSON_ENVELOPE=0")
}

func backendMigrationMetricsEnabledEnv(home string) []string {
	env := envWithout(backendMigrationCleanEnv(home), "BD_DISABLE_METRICS")
	return append(env, "BD_DISABLE_EVENT_FLUSH=1")
}

func decodeSingleJSONObject(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("expected exactly one JSON object: %v\npayload:\n%s", err, payload)
	}
	if decoded == nil {
		t.Fatalf("expected JSON object, got null:\n%s", payload)
	}
	return decoded
}

func assertMetadataPairUnchanged(t *testing.T, pathA string, wantA []byte, pathB string, wantB []byte) {
	t.Helper()
	for path, want := range map[string][]byte{pathA: wantA, pathB: wantB} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("selector test changed %s: equal=%v err=%v", path, bytes.Equal(got, want), err)
		}
	}
}

func runSQLiteMigrationBD(t *testing.T, bd, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(bd, args...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.Bytes()
}

func runSQLiteMigrationBDFailure(t *testing.T, bd, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(bd, args...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("bd %s unexpectedly succeeded:\n%s", strings.Join(args, " "), out)
	}
	return out
}
