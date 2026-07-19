package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/steveyegge/beads/cmd/bd/doctor"
	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/debug"
	"github.com/steveyegge/beads/internal/doltserver"
	"github.com/steveyegge/beads/internal/safefile"
	"github.com/steveyegge/beads/internal/storage/dolt"
)

// localVersionFile is the gitignored file that stores the last bd version used locally.
// This prevents the upgrade notification from firing repeatedly when git operations
// reset the tracked metadata.json file.
const localVersionFile = ".local_version"

// refuseLegacyDoltServerWorkspace recognizes the durable on-disk contract of a
// pre-project-identity Dolt server workspace: canonical metadata with an
// explicit persisted dolt_mode=server, no project identity, and an existing
// persisted Dolt server data root. This is the shape written by the v0.57.0-era
// external SQL server, the release gated by the migration-test harness. It keys
// on the persisted metadata mode, so a workspace whose server mode is only
// implicit (empty dolt_mode, supplied by config.yaml or the environment) is not
// classified here; strict metadata loading and normal store open handle those.
// The gitignored local version and the compatibility marker are deliberately
// not discriminators: an earlier failed upgrade can rewrite both before timing
// out. This check is store-free and has no migration side effects.
func refuseLegacyDoltServerWorkspace(beadsDir string, cfg *configfile.Config) error {
	if cfg == nil || cfg.GetBackend() != configfile.BackendDolt ||
		!strings.EqualFold(cfg.DoltMode, configfile.DoltModeServer) {
		return nil
	}
	if cfg.ProjectID != "" {
		return nil
	}
	doltDir := cfg.PersistedDoltDataPath(beadsDir)
	if doltDir == "" {
		return nil
	}
	info, err := os.Lstat(doltDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("cannot safely classify possible legacy Dolt-server workspace at %s: %w (refusing to open or modify it)", doltDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("possible legacy Dolt-server workspace has an ambiguous non-directory data root at %s; preserve it and run a version-specific explicit migration (refusing to open or modify it)", doltDir)
	}

	// Validate the effective database name only after confirming an existing
	// persisted Dolt data root. An empty dolt_database is a valid default that
	// resolves to the default database, so validate GetDoltDatabase() rather
	// than the raw field to avoid misdiagnosing a defaulted name as invalid.
	database := cfg.GetDoltDatabase()
	if err := dolt.ValidateDatabaseName(database); err != nil {
		return fmt.Errorf("possible legacy Dolt-server workspace at %s has an invalid database name %q: %v; preserve a byte-for-byte backup of .beads and use the matching historical bd binary for explicit migration (refusing to open or modify it)", doltDir, database, err)
	}

	source := safeLegacyDoltVersionLabel(beadsDir)
	return fmt.Errorf("legacy %s Dolt-server workspace requires explicit migration before bd %s can open it; first preserve a byte-for-byte backup of .beads and keep/use the matching historical bd binary for a qualified version-specific migration bridge; this build refuses automatic modification of %s",
		source, Version, beadsDir)
}

const maxLegacyVersionWitnessBytes = 64

// legacyDoltEraLabel is the human-readable provenance used in the refusal
// message when no trustworthy .local_version witness is available. It names the
// population the guard actually classifies (pre-project-identity Dolt server
// workspaces) without asserting a specific unqualified release range.
const legacyDoltEraLabel = "pre-project-identity"

func safeLegacyDoltVersionLabel(beadsDir string) string {
	path := filepath.Join(beadsDir, localVersionFile)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxLegacyVersionWitnessBytes {
		return legacyDoltEraLabel
	}
	file, err := safefile.OpenReadOnlyNoFollow(path)
	if err != nil {
		return legacyDoltEraLabel
	}
	defer func() { _ = file.Close() }()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || openedInfo.Size() != info.Size() {
		return legacyDoltEraLabel
	}
	data, err := io.ReadAll(io.LimitReader(file, maxLegacyVersionWitnessBytes+1))
	if err != nil || len(data) > maxLegacyVersionWitnessBytes {
		return legacyDoltEraLabel
	}
	version := strings.TrimSpace(string(data))
	if !isLegacyDoltVersionWitness(version) {
		return legacyDoltEraLabel
	}
	return "bd " + version
}

func isLegacyDoltVersionWitness(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return false
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	patch, patchErr := strconv.Atoi(parts[2])
	return majorErr == nil && minorErr == nil && patchErr == nil &&
		major == 0 && minor >= 50 && minor <= 58 && patch >= 0
}

// trackBdVersion checks if bd version has changed since last run and updates the local version file.
// This function is best-effort - failures are silent to avoid disrupting commands.
// Sets global variables versionUpgradeDetected and previousVersion if upgrade detected.
func trackBdVersion() {
	// Find the beads directory
	beadsDir := beads.FindBeadsDir()
	if beadsDir == "" {
		// No .beads directory found - this is fine (e.g., bd init, bd version, etc.)
		return
	}

	// Read last version from local (gitignored) file
	localVersionPath := filepath.Join(beadsDir, localVersionFile)
	lastVersion := readLocalVersion(localVersionPath)

	// Check if version changed (only flag actual upgrades, not downgrades)
	if lastVersion != "" && lastVersion != Version {
		if doctor.CompareVersions(Version, lastVersion) > 0 {
			// Version upgrade detected!
			versionUpgradeDetected = true
			previousVersion = lastVersion
		}
	}

	// Update local version file (best effort)
	// Only write if version actually changed to minimize I/O
	if lastVersion != Version {
		_ = writeLocalVersion(localVersionPath, Version) // Best effort: version tracking is advisory
	}

}

// readLocalVersion reads the last bd version from the local version file.
// Returns empty string if file doesn't exist or can't be read.
func readLocalVersion(path string) string {
	// #nosec G304 - path is constructed from beadsDir + constant
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// writeLocalVersion writes the current version to the local version file.
func writeLocalVersion(path, version string) error {
	return os.WriteFile(path, []byte(version+"\n"), 0600)
}

// getVersionsSince returns all version changes since the given version.
// If sinceVersion is empty, returns all known versions.
// Returns changes in chronological order (oldest first).
//
// Note: versionChanges array is in reverse chronological order (newest first),
// so we return elements before the found index and reverse the slice.
func getVersionsSince(sinceVersion string) []VersionChange {
	if sinceVersion == "" {
		// Return all versions (already in reverse chronological, but kept for compatibility)
		return versionChanges
	}

	// Find the index of sinceVersion
	// versionChanges is ordered newest-first: [0.23.0, 0.22.1, 0.22.0, 0.21.0]
	startIdx := -1
	for i, vc := range versionChanges {
		if vc.Version == sinceVersion {
			startIdx = i
			break
		}
	}

	if startIdx == -1 {
		// sinceVersion not found in our changelog - return all versions
		// (user might be upgrading from a very old version)
		return versionChanges
	}

	if startIdx == 0 {
		// Already on the newest version
		return []VersionChange{}
	}

	// Return versions before sinceVersion (those are newer)
	// Then reverse to get chronological order (oldest first)
	newerVersions := versionChanges[:startIdx]

	// Reverse the slice to get chronological order
	result := make([]VersionChange, len(newerVersions))
	for i := range newerVersions {
		result[i] = newerVersions[len(newerVersions)-1-i]
	}

	return result
}

// maybeShowUpgradeNotification displays a one-time upgrade notification if version changed.
// This is called by commands like 'bd ready' and 'bd list' to inform users of upgrades.
func maybeShowUpgradeNotification() {
	// Only show if upgrade detected and not yet acknowledged
	if !versionUpgradeDetected || upgradeAcknowledged {
		return
	}

	// Mark as acknowledged so we only show once per session
	upgradeAcknowledged = true

	// Display notification
	fmt.Printf("🔄 bd upgraded from v%s to v%s since last use\n", previousVersion, Version)
	fmt.Println("💡 Run 'bd upgrade review' to see what changed")
	if usesSQLServer() {
		fmt.Println("💊 Run 'bd doctor' to verify upgrade completed cleanly")
	}

	fmt.Println()
}

// autoMigrateOnVersionBump automatically migrates the database when CLI version changes.
// This function is best-effort - failures are silent to avoid disrupting commands.
// Called from PersistentPreRun before opening DB for main operation.
//
// IMPORTANT: This must be called BEFORE opening the database to avoid opening DB twice.
//
// beadsDir is the path to the .beads directory.
func autoMigrateOnVersionBump(beadsDir string) {
	// Only migrate if version upgrade was detected
	if !versionUpgradeDetected {
		return
	}

	// Validate beadsDir
	if beadsDir == "" {
		debug.Logf("auto-migrate: skipping migration, no beads directory")
		return
	}

	// Load config to determine the correct database path for this backend
	cfg, err := configfile.Load(beadsDir)
	if err != nil {
		debug.Logf("auto-migrate: failed to load config: %v", err)
		return
	}
	if cfg == nil {
		cfg = configfile.DefaultConfig()
	}

	if cfg.IsDoltProxiedServerMode() {
		debug.Logf("auto-migrate: skipping embedded migration, proxied-server handled after UOW provider init")
		return
	}

	// Check if database exists at the backend-appropriate path
	dbPath := cfg.DatabasePath(beadsDir)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		// No database - nothing to migrate
		debug.Logf("auto-migrate: skipping migration, database does not exist: %s", dbPath)
		return
	}

	// GH#2137: If upgrading from pre-0.56, the dolt database may have been
	// created by the old embedded Dolt mode. Recover by reinitializing.
	if previousVersion != "" && doctor.CompareVersions(previousVersion, "0.56.0") < 0 {
		recovered, recErr := doltserver.RecoverPreV56DoltDir(dbPath)
		if recErr != nil {
			debug.Logf("auto-migrate: pre-v56 recovery failed: %v", recErr)
		}
		if recovered {
			debug.Logf("auto-migrate: rebuilt pre-v56 dolt database at %s", dbPath)
		}
	}

	// Open database using factory (respects backend config from metadata.json)
	// Use rootCtx if available and not canceled, otherwise use Background
	ctx := rootCtx
	if ctx == nil || ctx.Err() != nil {
		// rootCtx is nil or canceled - use fresh background context
		ctx = context.Background()
	}

	store, err := dolt.NewFromConfig(ctx, beadsDir)
	if err != nil {
		// Failed to open database - skip migration
		debug.Logf("auto-migrate: failed to open database: %v", err)
		return
	}

	// Get current database version (clone-local, dolt-ignored)
	dbVersion, err := store.GetLocalMetadata(ctx, "bd_version")
	if err != nil {
		// Failed to read version - skip migration
		debug.Logf("auto-migrate: failed to read database version: %v", err)
		_ = store.Close() // Best effort cleanup on error path
		return
	}

	// Check if migration is needed
	if dbVersion == Version {
		// Database is already at current version
		debug.Logf("auto-migrate: database already at version %s", Version)
		_ = store.Close() // Best effort cleanup on error path
		return
	}

	// Check for downgrade: refuse to overwrite a newer version with an older one (gt-e3uiy)
	maxVersion, _ := store.GetLocalMetadata(ctx, "bd_version_max")
	if dbVersion != "" && doctor.CompareVersions(Version, dbVersion) < 0 {
		debug.Logf("auto-migrate: refusing downgrade from %s to %s", dbVersion, Version)
		_ = store.Close() // Best effort cleanup on error path
		return
	}
	if maxVersion != "" && doctor.CompareVersions(Version, maxVersion) < 0 {
		debug.Logf("auto-migrate: refusing downgrade (max version %s > current %s)", maxVersion, Version)
		_ = store.Close() // Best effort cleanup on error path
		return
	}

	// Perform migration: update database version
	debug.Logf("auto-migrate: migrating database from %s to %s", dbVersion, Version)
	if err := store.SetLocalMetadata(ctx, "bd_version", Version); err != nil {
		// Migration failed - log and continue
		debug.Logf("auto-migrate: failed to update database version: %v", err)
		_ = store.Close() // Best effort cleanup on error path
		return
	}

	// Update max version tracking
	if maxVersion == "" || doctor.CompareVersions(Version, maxVersion) > 0 {
		if err := store.SetLocalMetadata(ctx, "bd_version_max", Version); err != nil {
			debug.Logf("auto-migrate: failed to update max version: %v", err)
		}
	}

	// No Dolt commit needed — local_metadata is dolt-ignored and persists
	// in the working set for the lifetime of the server session.

	// Close database
	if err := store.Close(); err != nil {
		debug.Logf("auto-migrate: warning: failed to close database: %v", err)
	}

	debug.Logf("auto-migrate: successfully migrated database to version %s", Version)
}
