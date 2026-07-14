package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/workspacestate"
	"github.com/steveyegge/beads/internal/utils"
	"github.com/steveyegge/beads/internal/workspaceidentity"
	"github.com/subosito/gotenv"
)

var (
	errInitBackendPreflightMissing = errors.New("init backend admission preflight is missing")
	errInitBackendPreflightChanged = errors.New("init backend admission changed before initialization")
	errInitBackendSelectorUnowned  = errors.New("init database selector has no verified workspace")
)

type initBackendSelectionSource uint8

const (
	initBackendSelectionExplicitDB initBackendSelectionSource = iota + 1
	initBackendSelectionBeadsDB
	initBackendSelectionLegacyBDDB
	initBackendSelectionBeadsDir
	initBackendSelectionConfiguredDB
	initBackendSelectionDotEnvBeadsDB
	initBackendSelectionDotEnvLegacyBDDB
	initBackendSelectionDotEnvBeadsDir
	initBackendSelectionDiscovered
	initBackendSelectionWorktree
	initBackendSelectionCWD
)

func (source initBackendSelectionSource) isDatabase() bool {
	switch source {
	case initBackendSelectionExplicitDB, initBackendSelectionBeadsDB,
		initBackendSelectionLegacyBDDB, initBackendSelectionConfiguredDB,
		initBackendSelectionDotEnvBeadsDB, initBackendSelectionDotEnvLegacyBDDB:
		return true
	default:
		return false
	}
}

func (source initBackendSelectionSource) valid() bool {
	return source >= initBackendSelectionExplicitDB && source <= initBackendSelectionCWD
}

// initBackendSelection retains both the selected path and why it won. The
// creation target is derived only from that authority, never from ambient
// workspace discovery after admission.
type initBackendSelection struct {
	source           initBackendSelectionSource
	selector         string
	creationBeadsDir string
}

type initBackendFreshTargetSnapshot struct {
	root     backendPathFact
	metadata configfile.ReadOnlySnapshot
	local    workspacestate.LocalState
}

type initBackendPreflight struct {
	selection      initBackendSelection
	requested      string
	snapshot       *backendWorkspaceSnapshot
	freshTarget    *initBackendFreshTargetSnapshot
	sourceDatabase []initBackendSourceDatabaseSnapshot
	witness        initBackendWorkspaceWitness
}

type initBackendWorkspaceWitness interface {
	Revalidate() error
	Close() error
}

type initBackendSourceDatabaseSnapshot struct {
	path     string
	metadata configfile.ReadOnlySnapshot
	database string
}

type initBackendAdmission struct {
	backend          string
	selection        initBackendSelection
	beadsDir         string
	databasePath     string
	providerPath     string
	initialized      bool
	redirectDatabase string
}

type initBackendSelectorCandidates struct {
	explicitDBSet  bool
	explicitDB     string
	beadsDB        string
	bdDB           string
	beadsDir       string
	configuredDB   string
	dotEnvBeadsDB  string
	dotEnvBDDB     string
	dotEnvBeadsDir string
	discovered     string
	worktree       string
	cwd            string
}

type initBackendPreflightDependencies struct {
	resolveSelection     func(*cobra.Command) (initBackendSelection, error)
	inspectWorkspace     func(string) (*backendWorkspaceSnapshot, error)
	inspectFreshTarget   func(string) (*initBackendFreshTargetSnapshot, error)
	admit                func(string, *backendWorkspaceSnapshot) (string, error)
	witnessSupported     func() bool
	bindWorkspaceWitness func(string, int64) (initBackendWorkspaceWitness, error)
}

type initBackendPreflightContextKey struct{}

type initBackendPreflightContextState struct {
	preflight *initBackendPreflight
}

func selectInitBackendSelection(candidates initBackendSelectorCandidates) (initBackendSelection, error) {
	var source initBackendSelectionSource
	selector := ""
	switch {
	case candidates.explicitDBSet:
		if candidates.explicitDB == "" {
			return initBackendSelection{}, errors.New("explicit --db selector is empty")
		}
		source, selector = initBackendSelectionExplicitDB, candidates.explicitDB
	case candidates.beadsDB != "":
		source, selector = initBackendSelectionBeadsDB, candidates.beadsDB
	case candidates.bdDB != "":
		source, selector = initBackendSelectionLegacyBDDB, candidates.bdDB
	case candidates.beadsDir != "":
		source, selector = initBackendSelectionBeadsDir, candidates.beadsDir
	case candidates.dotEnvBeadsDB != "":
		source, selector = initBackendSelectionDotEnvBeadsDB, candidates.dotEnvBeadsDB
	case candidates.dotEnvBDDB != "":
		source, selector = initBackendSelectionDotEnvLegacyBDDB, candidates.dotEnvBDDB
	case candidates.dotEnvBeadsDir != "":
		source, selector = initBackendSelectionDotEnvBeadsDir, candidates.dotEnvBeadsDir
	case candidates.configuredDB != "":
		source, selector = initBackendSelectionConfiguredDB, candidates.configuredDB
	case candidates.discovered != "":
		source, selector = initBackendSelectionDiscovered, candidates.discovered
	case candidates.worktree != "":
		source, selector = initBackendSelectionWorktree, candidates.worktree
	case candidates.cwd != "":
		source, selector = initBackendSelectionCWD, filepath.Join(candidates.cwd, ".beads")
	default:
		return initBackendSelection{}, errors.New("init backend selector has no working directory")
	}

	selector, err := absoluteCleanDatabasePath(selector)
	if err != nil {
		return initBackendSelection{}, err
	}
	creationBeadsDir := selector
	if source.isDatabase() {
		creationBeadsDir = filepath.Dir(selector)
	}
	creationBeadsDir, err = absoluteCleanDatabasePath(creationBeadsDir)
	if err != nil {
		return initBackendSelection{}, err
	}
	return initBackendSelection{source: source, selector: selector, creationBeadsDir: creationBeadsDir}, nil
}

func resolveInitBackendSelection(cmd *cobra.Command) (initBackendSelection, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return initBackendSelection{}, fmt.Errorf("resolve init backend selector: %w", err)
	}
	candidates := initBackendSelectorCandidates{cwd: cwd}
	if cmd != nil && cmd.Root() != nil {
		if flag := cmd.Root().PersistentFlags().Lookup("db"); flag != nil && flag.Changed {
			candidates.explicitDBSet = true
			candidates.explicitDB = flag.Value.String()
			return selectInitBackendSelection(candidates)
		}
	}

	candidates.beadsDB = os.Getenv("BEADS_DB")
	candidates.bdDB = os.Getenv("BD_DB")
	if candidates.beadsDB != "" || candidates.bdDB != "" {
		return selectInitBackendSelection(candidates)
	}

	if strings.TrimSpace(changeDir) != "" {
		candidates.beadsDir, err = resolveChangeDirBeadsDir(changeDir)
		if err != nil {
			return initBackendSelection{}, err
		}
	} else {
		candidates.beadsDir = os.Getenv("BEADS_DIR")
	}
	if candidates.beadsDir != "" {
		return selectInitBackendSelection(candidates)
	}

	candidates.dotEnvBeadsDB, candidates.dotEnvBDDB, candidates.dotEnvBeadsDir, err = readInitBackendSelectionEnv()
	if err != nil {
		return initBackendSelection{}, err
	}
	if candidates.dotEnvBeadsDB != "" || candidates.dotEnvBDDB != "" || candidates.dotEnvBeadsDir != "" {
		return selectInitBackendSelection(candidates)
	}
	candidates.configuredDB = config.GetString("db")
	if candidates.configuredDB != "" {
		return selectInitBackendSelection(candidates)
	}
	candidates.discovered = discoverInitBackendBeadsDir()
	candidates.worktree = beads.GetWorktreeFallbackBeadsDir()
	return selectInitBackendSelection(candidates)
}

func readInitBackendSelectionEnv() (beadsDB, bdDB, beadsDir string, err error) {
	discovered := beads.FindBeadsDir()
	if discovered == "" {
		return "", "", "", nil
	}
	pairs, err := gotenv.Read(filepath.Join(discovered, ".env"))
	if err != nil {
		return "", "", "", nil
	}
	value := func(want string) (string, error) {
		selected := ""
		for key, candidate := range pairs {
			if strings.EqualFold(key, want) && strings.TrimSpace(candidate) != "" {
				if selected != "" && candidate != selected {
					return "", fmt.Errorf("conflicting case-insensitive %s entries in workspace .env", want)
				}
				selected = candidate
			}
		}
		return selected, nil
	}
	if beadsDB, err = value("BEADS_DB"); err != nil {
		return "", "", "", err
	}
	if bdDB, err = value("BD_DB"); err != nil {
		return "", "", "", err
	}
	if beadsDir, err = value("BEADS_DIR"); err != nil {
		return "", "", "", err
	}
	return beadsDB, bdDB, beadsDir, nil
}

func discoverInitBackendBeadsDir() string {
	discovered := beads.FindBeadsDir()
	if discovered == "" {
		return ""
	}
	redirect := beads.GetRedirectInfo()
	if redirect.LocalDir == "" {
		return discovered
	}
	if redirect.IsRedirected && utils.PathsEqual(redirect.TargetDir, discovered) {
		return redirect.LocalDir
	}
	if !redirect.IsRedirected && utils.PathsEqual(redirect.LocalDir, discovered) {
		return redirect.LocalDir
	}
	return discovered
}

func inspectInitBackendFreshTarget(beadsDir string) (*initBackendFreshTargetSnapshot, error) {
	first, err := observeInitBackendFreshTarget(beadsDir)
	if err != nil {
		return nil, err
	}
	second, err := observeInitBackendFreshTarget(beadsDir)
	if err != nil {
		return nil, err
	}
	if !equalInitBackendFreshTargetSnapshots(first, second) {
		return nil, errBackendWorkspaceChangedDuringInspection
	}
	return second, nil
}

func observeInitBackendFreshTarget(beadsDir string) (*initBackendFreshTargetSnapshot, error) {
	resolved, err := resolveCanonicalDatabasePath(beadsDir)
	if err != nil {
		return nil, err
	}
	root, err := backendSnapshotPathFact(resolved)
	if err != nil {
		return nil, err
	}
	if root.exists && !root.mode.IsDir() {
		return nil, fmt.Errorf("fresh init target is not a directory: %q", root.path)
	}
	redirectPath := filepath.Join(root.path, "redirect")
	if _, err := os.Lstat(redirectPath); err == nil {
		return nil, fmt.Errorf("fresh init target acquired a redirect: %q", root.path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, databasePathOperationError("inspect fresh init target redirect", redirectPath, err)
	}
	cfg, metadata, err := configfile.LoadReadOnlySnapshot(root.path)
	if err != nil {
		return nil, err
	}
	if cfg != nil || metadata.Present() {
		return nil, fmt.Errorf("fresh init target acquired metadata: %q", root.path)
	}
	local, err := workspacestate.InspectLocal(root.path, "")
	if err != nil {
		return nil, err
	}
	if local.Initialized || local.Backend != "" {
		return nil, fmt.Errorf("fresh init target acquired provider state: %q", root.path)
	}
	return &initBackendFreshTargetSnapshot{root: root, metadata: metadata, local: local}, nil
}

func equalInitBackendFreshTargetSnapshots(left, right *initBackendFreshTargetSnapshot) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return equalBackendPathFact(left.root, right.root) && left.metadata.Equal(right.metadata) && left.local == right.local
}

func inspectInitBackendSourceDatabases(snapshot *backendWorkspaceSnapshot) ([]initBackendSourceDatabaseSnapshot, error) {
	first, err := observeInitBackendSourceDatabases(snapshot)
	if err != nil {
		return nil, err
	}
	second, err := observeInitBackendSourceDatabases(snapshot)
	if err != nil {
		return nil, err
	}
	if !equalInitBackendSourceDatabaseSnapshots(first, second) {
		return nil, errBackendWorkspaceChangedDuringInspection
	}
	return second, nil
}

func observeInitBackendSourceDatabases(snapshot *backendWorkspaceSnapshot) ([]initBackendSourceDatabaseSnapshot, error) {
	if snapshot == nil {
		return nil, nil
	}
	var sources []backendPathFact
	switch snapshot.route.lane {
	case backendWorkspaceLaneBinding:
		sources = snapshot.route.bindingSources
	case backendWorkspaceLaneStructural:
		sources = []backendPathFact{snapshot.route.source}
	default:
		return nil, errors.New("backend workspace route is invalid while inspecting redirect databases")
	}
	observed := make([]initBackendSourceDatabaseSnapshot, 0, len(sources))
	for _, source := range sources {
		if source.path == "" || utils.PathsEqual(source.path, snapshot.route.target.path) {
			continue
		}
		cfg, metadata, err := configfile.LoadReadOnlySnapshot(source.path)
		if err != nil {
			return nil, err
		}
		database := ""
		if cfg != nil {
			database = cfg.DoltDatabase
		}
		observed = append(observed, initBackendSourceDatabaseSnapshot{path: source.path, metadata: metadata, database: database})
	}
	sort.Slice(observed, func(i, j int) bool { return observed[i].path < observed[j].path })
	return observed, nil
}

func equalInitBackendSourceDatabaseSnapshots(left, right []initBackendSourceDatabaseSnapshot) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].path != right[index].path || left[index].database != right[index].database ||
			!left[index].metadata.Equal(right[index].metadata) {
			return false
		}
	}
	return true
}

func admittedInitRedirectDatabase(source []initBackendSourceDatabaseSnapshot) (string, error) {
	database := ""
	for _, observed := range source {
		if observed.database == "" {
			continue
		}
		if database != "" && observed.database != database {
			return "", errors.New("redirect sources disagree on Dolt database")
		}
		database = observed.database
	}
	return database, nil
}

func prepareInitBackendPreflight(cmd *cobra.Command) error {
	return prepareInitBackendPreflightWith(cmd, initBackendPreflightDependencies{
		resolveSelection:   resolveInitBackendSelection,
		inspectWorkspace:   inspectBackendWorkspaceSnapshot,
		inspectFreshTarget: inspectInitBackendFreshTarget,
		admit:              admitInitBackend,
	})
}

func prepareInitBackendPreflightWith(cmd *cobra.Command, deps initBackendPreflightDependencies) (returnErr error) {
	if err := clearInitBackendPreflight(cmd); err != nil {
		return err
	}
	if cmd == nil {
		return errInitBackendPreflightMissing
	}
	if deps.resolveSelection == nil || deps.inspectWorkspace == nil || deps.inspectFreshTarget == nil || deps.admit == nil {
		return errors.New("init backend admission dependencies are incomplete")
	}
	requested, err := initBackendFlag(cmd)
	if err != nil {
		return err
	}
	requested, err = normalizeInitBackend(requested)
	if err != nil {
		return err
	}
	selection, err := deps.resolveSelection(cmd)
	if err != nil {
		return err
	}
	// The existing observation remains the compatibility-preserving eligibility
	// probe. Only a direct initialized physical workspace receives the stronger
	// retained-identity path below.
	snapshot, err := deps.inspectWorkspace(selection.selector)
	if err != nil {
		return err
	}
	witnessSupported := workspaceidentity.Supported
	if deps.witnessSupported != nil {
		witnessSupported = deps.witnessSupported
	}
	bindWitness := func(path string, limit int64) (initBackendWorkspaceWitness, error) {
		witness, _, bindErr := workspaceidentity.BindExisting(path, limit)
		return witness, bindErr
	}
	if deps.bindWorkspaceWitness != nil {
		bindWitness = deps.bindWorkspaceWitness
	}
	var witness initBackendWorkspaceWitness
	defer func() {
		if returnErr != nil && witness != nil {
			if closeErr := witness.Close(); closeErr != nil {
				returnErr = errors.Join(returnErr, closeErr)
			}
		}
	}()
	witnessEligible := witnessSupported() && initBackendWitnessEligible(selection, snapshot)
	if witnessEligible {
		witnessEligible, err = initBackendCurrentMetadataEligible(snapshot)
		if err != nil {
			return err
		}
	}
	if witnessEligible {
		witness, err = bindWitness(snapshot.route.target.path, workspaceidentity.MaxMetadataBytes)
		if err != nil {
			return classifyInitBackendWitnessError(err)
		}
		if err := witness.Revalidate(); err != nil {
			return classifyInitBackendWitnessError(err)
		}
		witnessed, err := deps.inspectWorkspace(selection.selector)
		if err != nil {
			return err
		}
		if err := witness.Revalidate(); err != nil {
			return classifyInitBackendWitnessError(err)
		}
		if !equalBackendWorkspaceSnapshots(snapshot, witnessed) {
			return fmt.Errorf("%w: workspace snapshot changed", errInitBackendPreflightChanged)
		}
		snapshot = witnessed
	}
	var freshTarget *initBackendFreshTargetSnapshot
	if snapshot == nil {
		if selection.source.isDatabase() {
			return unownedInitBackendSelectorError(selection)
		}
		freshTarget, err = deps.inspectFreshTarget(selection.creationBeadsDir)
		if err != nil {
			return err
		}
	}
	sourceDatabase, err := inspectInitBackendSourceDatabases(snapshot)
	if err != nil {
		return err
	}
	requested, err = deps.admit(requested, snapshot)
	if err != nil {
		return err
	}
	setInitBackendPreflight(cmd, &initBackendPreflight{
		selection:      selection,
		requested:      requested,
		snapshot:       cloneBackendWorkspaceSnapshot(snapshot),
		freshTarget:    cloneInitBackendFreshTargetSnapshot(freshTarget),
		sourceDatabase: append([]initBackendSourceDatabaseSnapshot(nil), sourceDatabase...),
		witness:        witness,
	})
	witness = nil // Ownership moved into the command-scoped preflight.
	return nil
}

func initBackendWitnessEligible(selection initBackendSelection, snapshot *backendWorkspaceSnapshot) bool {
	return !selection.source.isDatabase() && snapshot != nil && snapshot.state.initialized &&
		snapshot.route.lane == backendWorkspaceLaneBinding && len(snapshot.route.bindingSources) == 1 &&
		equalBackendPathFact(snapshot.route.bindingSources[0], snapshot.route.target) &&
		utils.PathsEqual(selection.creationBeadsDir, snapshot.route.target.path)
}

func initBackendCurrentMetadataEligible(snapshot *backendWorkspaceSnapshot) (bool, error) {
	if snapshot == nil || snapshot.route.target.path == "" {
		return false, nil
	}
	currentPath := configfile.ConfigPath(snapshot.route.target.path)
	if _, err := os.Lstat(currentPath); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, databasePathOperationError("inspect current metadata eligibility", currentPath, err)
	}

	// Read the metadata selection again only when the current file is absent.
	// A stable legacy-only workspace compares equal and keeps the Slice 1 path;
	// a current file that disappeared after the probe compares different and is
	// treated as drift rather than being mistaken for legacy compatibility.
	_, observed, err := configfile.LoadReadOnlySnapshot(snapshot.route.target.path)
	if err != nil {
		return false, err
	}
	if !snapshot.metadata.Equal(observed) {
		return false, classifyInitBackendWitnessError(workspaceidentity.ErrChanged)
	}
	if _, err := os.Lstat(currentPath); err == nil {
		return true, nil
	} else if errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else {
		return false, databasePathOperationError("reinspect current metadata eligibility", currentPath, err)
	}
}

func classifyInitBackendWitnessError(err error) error {
	if errors.Is(err, workspaceidentity.ErrIneligible) {
		err = errors.Join(workspaceidentity.ErrChanged, err)
	}
	if errors.Is(err, workspaceidentity.ErrChanged) {
		return fmt.Errorf("%w: workspace identity changed: %w", errInitBackendPreflightChanged, err)
	}
	return err
}

func consumeInitBackendPreflight(cmd *cobra.Command) (initBackendAdmission, error) {
	return consumeInitBackendPreflightWith(cmd, initBackendPreflightDependencies{
		resolveSelection:   resolveInitBackendSelection,
		inspectWorkspace:   inspectBackendWorkspaceSnapshot,
		inspectFreshTarget: inspectInitBackendFreshTarget,
		admit:              admitInitBackend,
	})
}

func consumeInitBackendPreflightWith(cmd *cobra.Command, deps initBackendPreflightDependencies) (result initBackendAdmission, returnErr error) {
	preflight := takeInitBackendPreflight(cmd)
	if preflight == nil {
		return initBackendAdmission{}, errInitBackendPreflightMissing
	}
	defer func() {
		if preflight.witness == nil {
			return
		}
		closeErr := preflight.witness.Close()
		preflight.witness = nil
		if closeErr != nil {
			result = initBackendAdmission{}
			returnErr = errors.Join(returnErr, closeErr)
		}
	}()
	if deps.resolveSelection == nil || deps.inspectWorkspace == nil || deps.inspectFreshTarget == nil || deps.admit == nil {
		return initBackendAdmission{}, errors.New("init backend admission dependencies are incomplete")
	}
	requested, err := initBackendFlag(cmd)
	if err != nil {
		return initBackendAdmission{}, err
	}
	selection, err := deps.resolveSelection(cmd)
	if err != nil {
		return initBackendAdmission{}, err
	}
	if preflight.witness != nil {
		if err := preflight.witness.Revalidate(); err != nil {
			return initBackendAdmission{}, classifyInitBackendWitnessError(err)
		}
	}
	snapshot, err := deps.inspectWorkspace(selection.selector)
	if err != nil {
		return initBackendAdmission{}, err
	}
	if preflight.witness != nil {
		if err := preflight.witness.Revalidate(); err != nil {
			return initBackendAdmission{}, classifyInitBackendWitnessError(err)
		}
	}
	// Re-run admission on the second workspace observation before any drift
	// branch below. Fresh-target validation is additional authority checking;
	// it must not make the RunE admission rerun conditional.
	admitted, admissionErr := deps.admit(requested, snapshot)
	var freshTarget *initBackendFreshTargetSnapshot
	if snapshot == nil && !selection.source.isDatabase() {
		freshTarget, err = deps.inspectFreshTarget(selection.creationBeadsDir)
		if err != nil {
			return initBackendAdmission{}, err
		}
	}
	sourceDatabase, err := inspectInitBackendSourceDatabases(snapshot)
	if err != nil {
		return initBackendAdmission{}, err
	}

	if selection != preflight.selection {
		return initBackendAdmission{}, fmt.Errorf("%w: database selection changed", errInitBackendPreflightChanged)
	}
	if !equalBackendWorkspaceSnapshots(preflight.snapshot, snapshot) {
		return initBackendAdmission{}, fmt.Errorf("%w: workspace snapshot changed", errInitBackendPreflightChanged)
	}
	if !equalInitBackendFreshTargetSnapshots(preflight.freshTarget, freshTarget) {
		return initBackendAdmission{}, fmt.Errorf("%w: fresh target changed", errInitBackendPreflightChanged)
	}
	if !equalInitBackendSourceDatabaseSnapshots(preflight.sourceDatabase, sourceDatabase) {
		return initBackendAdmission{}, fmt.Errorf("%w: redirect source database changed", errInitBackendPreflightChanged)
	}
	if snapshot == nil && selection.source.isDatabase() {
		return initBackendAdmission{}, unownedInitBackendSelectorError(selection)
	}
	normalizedRequested, normalizeErr := normalizeInitBackend(requested)
	if normalizeErr != nil || normalizedRequested != preflight.requested {
		return initBackendAdmission{}, fmt.Errorf("%w: requested backend changed", errInitBackendPreflightChanged)
	}
	if admissionErr != nil {
		return initBackendAdmission{}, admissionErr
	}
	if admitted != preflight.requested {
		return initBackendAdmission{}, fmt.Errorf("%w: requested backend changed", errInitBackendPreflightChanged)
	}
	result, err = buildInitBackendAdmission(admitted, selection, snapshot)
	if err != nil {
		return initBackendAdmission{}, err
	}
	if admitted == configfile.BackendDolt {
		result.redirectDatabase, err = admittedInitRedirectDatabase(sourceDatabase)
		if err != nil {
			return initBackendAdmission{}, err
		}
	}
	return result, nil
}

func buildInitBackendAdmission(backend string, selection initBackendSelection, snapshot *backendWorkspaceSnapshot) (initBackendAdmission, error) {
	if !isInitBackend(backend) {
		return initBackendAdmission{}, errors.New("admitted init backend is invalid")
	}
	if !selection.source.valid() || selection.selector == "" || selection.creationBeadsDir == "" {
		return initBackendAdmission{}, errors.New("init backend selection is incomplete")
	}
	result := initBackendAdmission{backend: backend, selection: selection, beadsDir: selection.creationBeadsDir}
	if snapshot == nil {
		if selection.source.isDatabase() {
			return initBackendAdmission{}, unownedInitBackendSelectorError(selection)
		}
		return result, nil
	}
	if snapshot.route.target.path == "" {
		return initBackendAdmission{}, errors.New("admitted backend workspace target is empty")
	}
	result.beadsDir = snapshot.route.target.path
	result.initialized = snapshot.state.initialized
	switch snapshot.route.lane {
	case backendWorkspaceLaneBinding:
		if snapshot.route.owned.path == "" {
			return initBackendAdmission{}, errors.New("admitted backend workspace owned path is empty")
		}
		if backend == configfile.BackendDolt || backend == configfile.BackendSQLite {
			result.providerPath = snapshot.route.owned.path
		}
		if selection.source.isDatabase() {
			result.databasePath = snapshot.route.owned.path
		}
	case backendWorkspaceLaneStructural:
		if !selection.source.isDatabase() {
			if backend == configfile.BackendSQLite && snapshot.state.initialized {
				result.providerPath = filepath.Join(snapshot.route.target.path, "beads.db")
			}
			break
		}
		if snapshot.route.mappedSQLite != "" {
			result.databasePath = snapshot.route.mappedSQLite
		} else {
			relative, err := filepath.Rel(snapshot.route.source.path, selection.selector)
			if err != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return initBackendAdmission{}, errors.New("admitted structural database selector cannot be mapped to target")
			}
			result.databasePath = filepath.Clean(filepath.Join(snapshot.route.target.path, relative))
		}
		if backend == configfile.BackendDolt || backend == configfile.BackendSQLite {
			result.providerPath = result.databasePath
		}
	default:
		return initBackendAdmission{}, errors.New("admitted backend workspace route is invalid")
	}
	return result, nil
}

func unownedInitBackendSelectorError(selection initBackendSelection) error {
	action := "remove the database selector"
	switch selection.source {
	case initBackendSelectionExplicitDB:
		action = "remove --db"
	case initBackendSelectionBeadsDB:
		action = "unset BEADS_DB"
	case initBackendSelectionLegacyBDDB:
		action = "unset BD_DB"
	case initBackendSelectionConfiguredDB:
		action = "clear the configured db value"
	case initBackendSelectionDotEnvBeadsDB:
		action = "remove BEADS_DB from the workspace .env"
	case initBackendSelectionDotEnvLegacyBDDB:
		action = "remove BD_DB from the workspace .env"
	}
	return fmt.Errorf("%w: %q; %s, then rerun with BEADS_DIR pointing to the intended .beads workspace",
		errInitBackendSelectorUnowned, selection.selector, action)
}

func resolveAdmittedInitSQLitePath(admission initBackendAdmission, flagValue string, flagChanged bool) (string, error) {
	if admission.backend != configfile.BackendSQLite || admission.providerPath == "" {
		return flagValue, nil
	}
	if flagChanged {
		candidate := flagValue
		if candidate == "" {
			candidate = "beads.db"
		}
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(admission.beadsDir, candidate)
		}
		candidate, err := absoluteCleanDatabasePath(candidate)
		if err != nil {
			return "", err
		}
		if !utils.PathsEqual(candidate, admission.providerPath) {
			return "", fmt.Errorf("--sqlite-path %q conflicts with admitted database path %q", flagValue, admission.providerPath)
		}
	}
	return admission.providerPath, nil
}

func resolveAdmittedInitDoltPath(admission initBackendAdmission, fallback string) string {
	if admission.backend == configfile.BackendDolt && admission.providerPath != "" {
		return admission.providerPath
	}
	return fallback
}

func prepareInitContextAfterBackendPreflight(cmd *cobra.Command, admission initBackendAdmission) error {
	if !isInitBackend(admission.backend) || !admission.selection.source.valid() || admission.selection.selector == "" || admission.beadsDir == "" {
		return errors.New("init backend admission result is incomplete")
	}
	setDBPath(admission.databasePath)
	if err := os.Setenv("BEADS_DIR", admission.beadsDir); err != nil {
		return fmt.Errorf("bind admitted init workspace: %w", err)
	}
	if err := loadInitBackendRuntimeEnvFile(admission.beadsDir); err != nil {
		return err
	}
	if admission.redirectDatabase != "" && os.Getenv("BEADS_DOLT_SERVER_DATABASE") == "" {
		if err := os.Setenv("BEADS_DOLT_SERVER_DATABASE", admission.redirectDatabase); err != nil {
			return fmt.Errorf("bind admitted redirect database: %w", err)
		}
	}
	prepareSelectedCommandContext(admission.beadsDir, false)
	refreshBoundCommandConfig(cmd)
	if _, err := getDoltAutoCommitMode(); err != nil {
		return HandleError("%v", err)
	}
	return nil
}

func loadInitBackendRuntimeEnvFile(beadsDir string) error {
	pairs, err := gotenv.Read(filepath.Join(beadsDir, ".env"))
	if err != nil {
		return nil
	}
	selected := make(map[string]string)
	for key, value := range pairs {
		canonical, allowed := canonicalInitBackendRuntimeEnvKey(key)
		if !allowed {
			continue
		}
		if previous, present := selected[canonical]; present && previous != value {
			return fmt.Errorf("conflicting case-insensitive %s entries in workspace .env", canonical)
		}
		selected[canonical] = value
	}
	keys := make([]string, 0, len(selected))
	for key := range selected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, present := os.LookupEnv(key); !present {
			if err := os.Setenv(key, selected[key]); err != nil {
				return fmt.Errorf("load admitted runtime credential %s: %w", key, err)
			}
		}
	}
	return nil
}

func canonicalInitBackendRuntimeEnvKey(key string) (string, bool) {
	canonical := strings.ToUpper(key)
	switch canonical {
	case "BEADS_DOLT_PASSWORD",
		"BEADS_DOLT_CREDENTIAL_COMMAND",
		"BEADS_PG_PASSWORD",
		"BEADS_PG_PASSWORD_COMMAND",
		"BEADS_MYSQL_PASSWORD",
		"BEADS_MYSQL_PASSWORD_COMMAND",
		"BEADS_PROXIED_SERVER_EXTERNAL_PASSWORD":
		return canonical, true
	default:
		return "", false
	}
}

func cloneInitBackendFreshTargetSnapshot(source *initBackendFreshTargetSnapshot) *initBackendFreshTargetSnapshot {
	if source == nil {
		return nil
	}
	clone := *source
	return &clone
}

func setInitBackendPreflight(cmd *cobra.Command, preflight *initBackendPreflight) {
	base := cmd.Context()
	if base == nil {
		base = context.Background()
	}
	cmd.SetContext(context.WithValue(base, initBackendPreflightContextKey{}, &initBackendPreflightContextState{preflight: preflight}))
}

func takeInitBackendPreflight(cmd *cobra.Command) *initBackendPreflight {
	if cmd == nil || cmd.Context() == nil {
		return nil
	}
	state, _ := cmd.Context().Value(initBackendPreflightContextKey{}).(*initBackendPreflightContextState)
	if state == nil {
		return nil
	}
	preflight := state.preflight
	state.preflight = nil
	return preflight
}

func clearInitBackendPreflight(cmd *cobra.Command) error {
	if cmd == nil || cmd.Context() == nil {
		return nil
	}
	if state, _ := cmd.Context().Value(initBackendPreflightContextKey{}).(*initBackendPreflightContextState); state != nil {
		preflight := state.preflight
		state.preflight = nil
		if preflight != nil && preflight.witness != nil {
			err := preflight.witness.Close()
			preflight.witness = nil
			return err
		}
	}
	return nil
}

func clearInitBackendPreflightAfterError(cmd *cobra.Command, returnErr *error) {
	if returnErr == nil || *returnErr == nil {
		return
	}
	if clearErr := clearInitBackendPreflight(cmd); clearErr != nil {
		*returnErr = errors.Join(*returnErr, clearErr)
	}
}

func initBackendFlag(cmd *cobra.Command) (string, error) {
	if cmd == nil || cmd.Flags().Lookup("backend") == nil {
		return "", errors.New("init backend flag is unavailable")
	}
	backend, err := cmd.Flags().GetString("backend")
	if err != nil {
		return "", fmt.Errorf("read init backend flag: %w", err)
	}
	return backend, nil
}
