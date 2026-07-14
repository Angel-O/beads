package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/workspacestate"
)

const (
	maxDatabaseOwnershipCandidates         = 256
	maxDatabaseOwnershipRedirectCandidates = 4
	maxDatabaseWorkspaceHints              = 32
)

var (
	errDatabaseOwnershipContradiction = errors.New("database selector contradicts workspace metadata")
	errDatabaseOwnershipAmbiguous     = errors.New("database selector has multiple workspace owners")
	errDatabaseOwnershipLimit         = errors.New("database ownership discovery limit exceeded")
)

// databaseWorkspaceHint identifies a workspace that may own a database
// selector. Authoritative hints must exist and contain metadata that owns the
// selector. Environment data-directory evidence is considered only when the
// hint explicitly permits it.
type databaseWorkspaceHint struct {
	beadsDir                string
	allowEnvironmentDataDir bool
	authoritative           bool
}

type databaseOwnershipSource uint8

const (
	databaseOwnershipPersisted databaseOwnershipSource = iota
	databaseOwnershipExplicitEnvironment
)

type databaseOwnershipScope uint8

const (
	databaseOwnershipScopeWorkspace databaseOwnershipScope = iota
	databaseOwnershipScopeExact
	databaseOwnershipScopeDescendant
)

// databaseOwnershipBinding retains the concrete evidence needed by a later
// store-open admission step. Environment permission is input-only: source says
// whether ownedPath actually came from an explicitly admitted environment
// override, so later ambient changes cannot silently redirect the open. Scope
// defines whether ownedPath matched an exact SQLite file, a Dolt descendant,
// or only the selected workspace root for a remote provider.
type databaseOwnershipBinding struct {
	beadsDir       string
	backend        string
	ownedPath      string
	source         databaseOwnershipSource
	scope          databaseOwnershipScope
	beadsResolved  *resolvedDatabasePath
	ownedResolved  *resolvedDatabasePath
	sourceResolved []*resolvedDatabasePath
}

// databaseWorkspaceCandidate is an inspection-only structural route. It is
// not an ownership binding and cannot authorize a provider effect. The source
// observation records the selector-derived .beads directory; resolved records
// its strictly routed target. A later consumer must also receive the original
// selector, then revalidate selector-to-source containment and source-to-target
// routing under the workspace lifetime fence before mutation.
type databaseWorkspaceCandidate struct {
	beadsDir       string
	resolved       *resolvedDatabasePath
	sourceResolved *resolvedDatabasePath
}

// databaseOwnershipResolution keeps persisted ownership and metadata-less
// structural discovery in separate capability lanes. Successful resolution
// returns at most one of binding or structural.
type databaseOwnershipResolution struct {
	binding    *databaseOwnershipBinding
	structural *databaseWorkspaceCandidate
}

type databaseOwnershipContradictionMode uint8

const (
	databaseOwnershipNoContradiction databaseOwnershipContradictionMode = iota
	databaseOwnershipContradictUnlessNestedOwner
	databaseOwnershipAlwaysContradict
)

type databaseOwnershipCandidate struct {
	source                               string
	resolved                             *resolvedDatabasePath
	allowAuthoritativeEnvironmentDataDir bool
	requireMetadata                      bool
	contradictionMode                    databaseOwnershipContradictionMode
	workspaceRootSelected                bool
	structural                           bool
}

type routedDatabaseOwnershipCandidate struct {
	beadsDir                             string
	resolved                             *resolvedDatabasePath
	sources                              []routedDatabaseOwnershipSource
	allowAuthoritativeEnvironmentDataDir bool
	requireMetadata                      bool
	workspaceRootSelected                bool
	structural                           bool
}

type routedDatabaseOwnershipSource struct {
	resolved          *resolvedDatabasePath
	contradictionMode databaseOwnershipContradictionMode
	structural        bool
}

type databaseOwnershipRedirectProbe func(string) (bool, error)
type databaseOwnershipRedirectFollower func(string) (string, error)

type conditionalDatabaseOwnershipContradiction struct {
	sourceResolved *resolvedDatabasePath
	err            error
}

// resolveDatabaseOwnershipStrict performs a bounded, read-only ownership
// observation. It validates every candidate's redirect and metadata without
// migrating legacy config. It does not pin path components or metadata after
// return; provider effects require the descriptor-bound lifetime fence tracked
// by bd-3u1fs.
func resolveDatabaseOwnershipStrict(dbPath string, hints ...databaseWorkspaceHint) (*databaseOwnershipBinding, error) {
	result, err := resolveDatabaseOwnershipStrictWithPolicy(dbPath, true, false, hints...)
	if err != nil {
		return nil, err
	}
	return result.binding, nil
}

// resolveDatabaseOwnershipStrictWithoutAutomaticHints is for an explicit
// database selector whose path-derived evidence must not be broadened by an
// ambient BEADS_DIR. Caller-provided hints remain available for persisted
// ownership checks, but hints never acquire structural provenance.
func resolveDatabaseOwnershipStrictWithoutAutomaticHints(dbPath string, hints ...databaseWorkspaceHint) (*databaseOwnershipBinding, error) {
	result, err := resolveDatabaseOwnershipStrictWithPolicy(dbPath, false, false, hints...)
	if err != nil {
		return nil, err
	}
	return result.binding, nil
}

func resolveDatabaseOwnershipStrictResult(dbPath string, includeAutomaticHints bool, hints ...databaseWorkspaceHint) (databaseOwnershipResolution, error) {
	return resolveDatabaseOwnershipStrictWithPolicy(dbPath, includeAutomaticHints, true, hints...)
}

func resolveDatabaseOwnershipStrictWithPolicy(dbPath string, includeAutomaticHints, includeStructural bool, hints ...databaseWorkspaceHint) (databaseOwnershipResolution, error) {
	zero := databaseOwnershipResolution{}
	selector, err := validatedDatabaseSelector(dbPath)
	if err != nil {
		return zero, err
	}

	candidates, err := databasePathOwnershipCandidates(dbPath, selector)
	if err != nil {
		return zero, err
	}
	var automaticHints []databaseWorkspaceHint
	for _, hint := range hints {
		if hint.authoritative {
			includeAutomaticHints = false
			break
		}
	}
	if includeAutomaticHints {
		automaticHints, err = databaseResolutionHintsStrict()
		if err != nil {
			return zero, err
		}
	}
	if len(hints)+len(automaticHints) > maxDatabaseWorkspaceHints {
		return zero, fmt.Errorf("%w: at most %d workspace hints are allowed", errDatabaseOwnershipLimit, maxDatabaseWorkspaceHints)
	}
	hints = append(hints, automaticHints...)
	for _, hint := range hints {
		if hint.beadsDir == "" {
			if hint.authoritative {
				return zero, fmt.Errorf("%w: authoritative workspace hint is empty", errDatabaseOwnershipContradiction)
			}
			continue
		}
		candidate := databaseOwnershipCandidate{
			source:                               hint.beadsDir,
			allowAuthoritativeEnvironmentDataDir: hint.allowEnvironmentDataDir && hint.authoritative,
			requireMetadata:                      hint.authoritative,
			contradictionMode:                    databaseOwnershipNoContradiction,
		}
		if hint.authoritative {
			candidate.contradictionMode = databaseOwnershipAlwaysContradict
		}
		updatedCandidates, appendErr := appendDatabaseOwnershipCandidate(candidates, selector, candidate)
		if appendErr != nil {
			if !hint.authoritative && errors.Is(appendErr, os.ErrNotExist) && databaseWorkspaceHintIsVerifiedMissing(hint.beadsDir) {
				continue
			}
			return zero, appendErr
		}
		candidates = updatedCandidates
	}

	routed, err := routeDatabaseOwnershipCandidates(candidates)
	if err != nil {
		return zero, err
	}

	owners := make([]databaseOwnershipBinding, 0, 1)
	structuralCandidates := make([]routedDatabaseOwnershipCandidate, 0, 1)
	var contradiction error
	var conditionalContradictions []conditionalDatabaseOwnershipContradiction
	for _, candidate := range routed {
		cfg, err := configfile.LoadReadOnly(candidate.beadsDir)
		if err != nil {
			return zero, databasePathOperationError("inspect database owner metadata", candidate.beadsDir, err)
		}
		if cfg == nil {
			if candidate.requireMetadata && contradiction == nil {
				contradiction = fmt.Errorf("%w: authoritative workspace %q has no metadata for selector %q",
					errDatabaseOwnershipContradiction, candidate.beadsDir, selector.path)
			}
			if includeStructural && candidate.structural {
				structuralCandidates = append(structuralCandidates, candidate)
			}
			continue
		}

		binding, matched, err := databaseOwnershipFromConfig(selector, candidate, cfg)
		if err != nil {
			return zero, err
		}
		if !matched {
			candidateErr := fmt.Errorf("%w: selector %q is not owned by workspace %q",
				errDatabaseOwnershipContradiction, selector.path, candidate.beadsDir)
			for _, source := range candidate.sources {
				switch source.contradictionMode {
				case databaseOwnershipAlwaysContradict:
					if contradiction == nil {
						contradiction = candidateErr
					}
				case databaseOwnershipContradictUnlessNestedOwner:
					conditionalContradictions = append(conditionalContradictions, conditionalDatabaseOwnershipContradiction{
						sourceResolved: source.resolved,
						err:            candidateErr,
					})
				}
			}
			continue
		}
		owners, err = appendDatabaseOwner(owners, *binding)
		if err != nil {
			return zero, err
		}
	}

	if contradiction != nil {
		return zero, contradiction
	}
	// Binding-only callers preserve the legacy conditional-contradiction
	// behavior even when detailed structural discovery could identify a nested
	// workspace. Detailed callers also resolve persisted owners through this
	// branch so a binding always wins over structural inspection.
	if !includeStructural || len(owners) > 0 {
		for _, candidate := range conditionalContradictions {
			hasNestedOwner, err := databaseOwnershipHasNestedResolution(owners, nil, candidate.sourceResolved)
			if err != nil {
				return zero, err
			}
			if !hasNestedOwner {
				return zero, candidate.err
			}
		}
		if len(owners) == 0 {
			return zero, nil
		}
		if len(owners) > 1 {
			paths := make([]string, 0, len(owners))
			for _, owner := range owners {
				paths = append(paths, fmt.Sprintf("%q", owner.beadsDir))
			}
			sort.Strings(paths)
			return zero, fmt.Errorf("%w: selector %q resolves to %s", errDatabaseOwnershipAmbiguous, selector.path, strings.Join(paths, ", "))
		}
		return databaseOwnershipResolution{binding: &owners[0]}, nil
	}

	structural, err := selectDatabaseStructuralWorkspace(structuralCandidates)
	if err != nil {
		return zero, err
	}
	// Detailed discovery may select a nested inspection-only candidate despite
	// a conditional ancestor mismatch. Binding-only wrappers retain the legacy
	// contradiction and never expose this structural policy.
	for _, candidate := range conditionalContradictions {
		hasNestedOwner, err := databaseOwnershipHasNestedResolution(nil, structural, candidate.sourceResolved)
		if err != nil {
			return zero, err
		}
		if !hasNestedOwner {
			return zero, candidate.err
		}
	}
	return databaseOwnershipResolution{structural: structural}, nil
}

func databasePathOwnershipCandidates(originalPath string, selector *resolvedDatabasePath) ([]databaseOwnershipCandidate, error) {
	if selector == nil {
		return nil, errors.New("database ownership selector observation is missing")
	}
	originalPath, err := absoluteCleanDatabasePath(originalPath)
	if err != nil {
		return nil, err
	}
	paths := []string{originalPath, selector.path}

	var candidates []databaseOwnershipCandidate
	add := func(path string, contradictionMode databaseOwnershipContradictionMode, structural bool) error {
		var err error
		candidates, err = appendDatabaseOwnershipCandidate(candidates, selector, databaseOwnershipCandidate{
			source:            path,
			contradictionMode: contradictionMode,
			structural:        structural,
		})
		return err
	}

	// Compatibility discovery is deliberately limited to these two selector
	// representations and their ancestors up to the volume root. The hard
	// candidate cap bounds redirect and metadata probes on unusually deep paths.
	for _, path := range paths {
		path, err = absoluteCleanDatabasePath(path)
		if err != nil {
			return nil, err
		}
		observed, err := resolveCanonicalDatabasePath(path)
		if err != nil {
			return nil, err
		}
		start := path
		if observed.exists && observed.observed.Info.IsDir() {
			// Compatibility supports workspace roots with names other than
			// .beads and selectors that name the data directory itself.
			if err := add(path, databaseOwnershipContradictUnlessNestedOwner, false); err != nil {
				return nil, err
			}
			if err := add(filepath.Dir(path), databaseOwnershipContradictUnlessNestedOwner, false); err != nil {
				return nil, err
			}
		} else {
			start = filepath.Dir(path)
			if err := add(start, databaseOwnershipContradictUnlessNestedOwner, false); err != nil {
				return nil, err
			}
		}
		for dir := start; ; dir = filepath.Dir(dir) {
			structuralBeadsDir, err := isStructuralDatabaseOwnershipBeadsDir(dir)
			if err != nil {
				return nil, err
			}
			if structuralBeadsDir {
				// A canonical path-derived .beads directory that contains the
				// canonical selector may be retained for inspection-only
				// discovery. It does not establish ownership or mutation authority.
				resolvedSource, err := resolveCanonicalDatabasePath(dir)
				if err != nil {
					return nil, err
				}
				if err := add(
					dir,
					databaseOwnershipAlwaysContradict,
					resolvedDatabasePathEqualOrDescendant(selector, resolvedSource),
				); err != nil {
					return nil, err
				}
			}
			// Sibling candidates found while walking ancestors are possible
			// owners, but a parent repository must not contradict a valid
			// nested workspace merely because its own database differs.
			if err := add(filepath.Join(dir, ".beads"), databaseOwnershipNoContradiction, false); err != nil {
				return nil, err
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}
	return candidates, nil
}

// databaseWorkspaceHintIsVerifiedMissing distinguishes a genuinely absent
// advisory hint from ENOENT caused by a dangling symlink. The check is another
// point-in-time observation: it walks to the nearest lexically existing
// ancestor and requires that ancestor to resolve canonically to a directory.
func databaseWorkspaceHintIsVerifiedMissing(path string) bool {
	current, err := absoluteCleanDatabasePath(path)
	if err != nil {
		return false
	}
	observedMissing := false
	for {
		_, err := os.Lstat(current)
		if err == nil {
			if !observedMissing {
				return false
			}
			resolved, err := resolveCanonicalDatabasePath(current)
			return err == nil && resolved.exists && resolved.observed.Info.IsDir()
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false
		}
		observedMissing = true
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
		current = parent
	}
}

func isStructuralDatabaseOwnershipBeadsDir(path string) (bool, error) {
	resolved, err := resolveCanonicalDatabasePath(path)
	if err != nil {
		return false, err
	}
	if !resolved.exists || resolved.observed == nil || resolved.observed.Info == nil || !resolved.observed.Info.IsDir() {
		return false, nil
	}
	name := filepath.Base(resolved.path)
	if name == ".beads" {
		return true, nil
	}
	if !strings.EqualFold(name, ".beads") {
		return false, nil
	}
	parent, err := resolveCanonicalDatabasePath(filepath.Dir(resolved.path))
	if err != nil {
		return false, err
	}
	return parent.exists && parent.observed != nil && parent.observed.Info != nil && parent.observed.Info.IsDir() &&
		parent.observed.CaseSensitivityKnown && !parent.observed.CaseSensitive, nil
}

func appendDatabaseOwnershipCandidate(candidates []databaseOwnershipCandidate, selector *resolvedDatabasePath, candidate databaseOwnershipCandidate) ([]databaseOwnershipCandidate, error) {
	source, err := absoluteCleanDatabasePath(candidate.source)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveCanonicalDatabasePath(source)
	if err != nil {
		return nil, err
	}
	candidate.source = resolved.path
	candidate.resolved = resolved
	candidate.workspaceRootSelected = candidate.workspaceRootSelected || resolvedDatabasePathEqual(resolved, selector)

	for index := range candidates {
		if !resolvedDatabasePathEqual(candidates[index].resolved, resolved) {
			continue
		}
		candidates[index].allowAuthoritativeEnvironmentDataDir = candidates[index].allowAuthoritativeEnvironmentDataDir || candidate.allowAuthoritativeEnvironmentDataDir
		candidates[index].requireMetadata = candidates[index].requireMetadata || candidate.requireMetadata
		if candidate.contradictionMode > candidates[index].contradictionMode {
			candidates[index].contradictionMode = candidate.contradictionMode
		}
		candidates[index].workspaceRootSelected = candidates[index].workspaceRootSelected || candidate.workspaceRootSelected
		candidates[index].structural = candidates[index].structural || candidate.structural
		return candidates, nil
	}
	if len(candidates) >= maxDatabaseOwnershipCandidates {
		return nil, fmt.Errorf("%w: at most %d candidate workspaces are allowed", errDatabaseOwnershipLimit, maxDatabaseOwnershipCandidates)
	}
	return append(candidates, candidate), nil
}

func routeDatabaseOwnershipCandidates(candidates []databaseOwnershipCandidate) ([]routedDatabaseOwnershipCandidate, error) {
	return routeDatabaseOwnershipCandidatesWithDependencies(
		candidates,
		databaseOwnershipRedirectMarkerPresent,
		beads.FollowRedirectStrict,
	)
}

func routeDatabaseOwnershipCandidatesWithDependencies(
	candidates []databaseOwnershipCandidate,
	probe databaseOwnershipRedirectProbe,
	follow databaseOwnershipRedirectFollower,
) ([]routedDatabaseOwnershipCandidate, error) {
	if probe == nil || follow == nil {
		return nil, errors.New("database ownership redirect dependencies are missing")
	}
	var routed []routedDatabaseOwnershipCandidate
	redirectCandidates := 0
	for _, candidate := range candidates {
		source := candidate.source
		if candidate.requireMetadata {
			if err := validateObservedDatabaseWorkspaceDirectory(candidate.resolved); err != nil {
				return nil, err
			}
		}

		redirectPath := filepath.Join(source, beads.RedirectFileName)
		markerPresent, err := probe(source)
		if err != nil {
			return nil, databasePathOperationError("inspect database owner redirect", redirectPath, err)
		}
		target := source
		if markerPresent {
			if redirectCandidates >= maxDatabaseOwnershipRedirectCandidates {
				return nil, fmt.Errorf("%w: at most %d strict workspace redirects are allowed",
					errDatabaseOwnershipLimit, maxDatabaseOwnershipRedirectCandidates)
			}
			redirectCandidates++
			target, err = follow(source)
			if err != nil {
				return nil, databasePathOperationError("resolve database owner redirect", source, err)
			}
			if target == "" {
				return nil, fmt.Errorf("database owner redirect returned an empty target: %q", redirectPath)
			}
		}
		if markerPresent {
			// Redirect sources cannot own the selector, but malformed source
			// metadata still makes the observation untrustworthy.
			if _, err := configfile.LoadReadOnly(source); err != nil {
				return nil, databasePathOperationError("validate redirect source metadata", source, err)
			}
		}

		resolvedTarget, err := resolveCanonicalDatabasePath(target)
		if err != nil {
			return nil, err
		}
		if markerPresent && resolvedDatabasePathEqual(candidate.resolved, resolvedTarget) {
			return nil, fmt.Errorf("database owner redirect changed during inspection: %q", redirectPath)
		}
		merged := false
		for index := range routed {
			if !resolvedDatabasePathEqual(routed[index].resolved, resolvedTarget) {
				continue
			}
			routed[index].sources, err = appendRoutedDatabaseOwnershipSource(routed[index].sources, candidate)
			if err != nil {
				return nil, err
			}
			routed[index].allowAuthoritativeEnvironmentDataDir = routed[index].allowAuthoritativeEnvironmentDataDir || candidate.allowAuthoritativeEnvironmentDataDir
			routed[index].requireMetadata = routed[index].requireMetadata || candidate.requireMetadata
			routed[index].workspaceRootSelected = routed[index].workspaceRootSelected || candidate.workspaceRootSelected
			routed[index].structural = routed[index].structural || candidate.structural
			merged = true
			break
		}
		if merged {
			continue
		}
		routed = append(routed, routedDatabaseOwnershipCandidate{
			beadsDir: resolvedTarget.path,
			resolved: resolvedTarget,
			sources: []routedDatabaseOwnershipSource{{
				resolved:          candidate.resolved,
				contradictionMode: candidate.contradictionMode,
				structural:        candidate.structural,
			}},
			allowAuthoritativeEnvironmentDataDir: candidate.allowAuthoritativeEnvironmentDataDir,
			requireMetadata:                      candidate.requireMetadata,
			workspaceRootSelected:                candidate.workspaceRootSelected,
			structural:                           candidate.structural,
		})
	}
	return routed, nil
}

// databaseOwnershipRedirectMarkerPresent is a point-in-time work-budget
// observation. An absent marker takes the same immediate source path as strict
// resolution; every present object still goes through FollowRedirectStrict.
// Provider effects must re-observe routing under the bd-3u1fs lifetime fence.
func databaseOwnershipRedirectMarkerPresent(source string) (bool, error) {
	_, err := os.Lstat(filepath.Join(source, beads.RedirectFileName))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func appendRoutedDatabaseOwnershipSource(sources []routedDatabaseOwnershipSource, candidate databaseOwnershipCandidate) ([]routedDatabaseOwnershipSource, error) {
	if candidate.resolved == nil {
		return nil, errors.New("routed database ownership source observation is missing")
	}
	for index := range sources {
		if sources[index].resolved == nil {
			return nil, errors.New("routed database ownership source observation is missing")
		}
		if !resolvedDatabasePathEqual(sources[index].resolved, candidate.resolved) {
			continue
		}
		if candidate.contradictionMode > sources[index].contradictionMode {
			sources[index].contradictionMode = candidate.contradictionMode
		}
		sources[index].structural = sources[index].structural || candidate.structural
		return sources, nil
	}
	return append(sources, routedDatabaseOwnershipSource{
		resolved:          candidate.resolved,
		contradictionMode: candidate.contradictionMode,
		structural:        candidate.structural,
	}), nil
}

func validateObservedDatabaseWorkspaceDirectory(resolved *resolvedDatabasePath) error {
	if resolved == nil {
		return errors.New("workspace directory observation is missing")
	}
	if !resolved.exists {
		return fmt.Errorf("workspace directory does not exist: %q", resolved.path)
	}
	if !resolved.observed.Info.IsDir() {
		return fmt.Errorf("workspace is not a directory: %q", resolved.path)
	}
	return nil
}

func databaseOwnershipFromConfig(selector *resolvedDatabasePath, candidate routedDatabaseOwnershipCandidate, cfg *configfile.Config) (*databaseOwnershipBinding, bool, error) {
	if selector == nil || candidate.resolved == nil {
		return nil, false, errors.New("database ownership observation is incomplete")
	}
	workspaceRootSelected := candidate.workspaceRootSelected || resolvedDatabasePathEqual(selector, candidate.resolved)
	if !workspaceRootSelected && cfg != nil && cfg.Backend == configfile.BackendSQLite && cfg.SQLitePath == "" {
		relevant, err := bareSQLiteDatabaseOwnershipRelevant(
			selector,
			candidate.beadsDir,
			cfg,
			candidate.allowAuthoritativeEnvironmentDataDir,
		)
		if err != nil {
			return nil, false, err
		}
		if !relevant {
			return nil, false, nil
		}
	}
	effectiveConfig, err := workspacestate.EffectiveConfig(candidate.beadsDir, cfg)
	if err != nil {
		return nil, false, err
	}
	cfg = effectiveConfig
	backend := cfg.GetBackend()
	source := databaseOwnershipPersisted
	scope := databaseOwnershipScopeWorkspace

	var ownedPath string
	var ownedResolved *resolvedDatabasePath
	switch backend {
	case configfile.BackendDolt:
		if candidate.allowAuthoritativeEnvironmentDataDir {
			if environmentPath := os.Getenv("BEADS_DOLT_DATA_DIR"); environmentPath != "" {
				ownedPath = databasePathRelativeToWorkspace(environmentPath, candidate.beadsDir)
				source = databaseOwnershipExplicitEnvironment
			}
		}
		if ownedPath == "" {
			ownedPath = cfg.PersistedDoltDataPath(candidate.beadsDir)
		}
		ownedResolved, err = resolveCanonicalDatabasePath(ownedPath)
		if err != nil {
			return nil, false, err
		}
		ownedPath = ownedResolved.path
		contained := resolvedDatabasePathEqualOrDescendant(selector, ownedResolved)
		if !workspaceRootSelected && !contained {
			return nil, false, nil
		}
		if err := validateDatabaseProviderOwnedPath(ownedResolved, backend); err != nil {
			return nil, false, err
		}
		if !workspaceRootSelected {
			scope = databaseOwnershipScopeDescendant
		}
	case configfile.BackendSQLite:
		ownedPath = cfg.SQLitePath
		if ownedPath == "" {
			ownedPath = "beads.db"
		}
		ownedResolved, err = resolveCanonicalDatabasePath(databasePathRelativeToWorkspace(ownedPath, candidate.beadsDir))
		if err != nil {
			return nil, false, err
		}
		ownedPath = ownedResolved.path
		exact := resolvedDatabasePathEqual(selector, ownedResolved)
		relevant := workspaceRootSelected || resolvedDatabasePathEqualOrDescendant(selector, ownedResolved)
		if !relevant {
			return nil, false, nil
		}
		if err := validateDatabaseProviderOwnedPath(ownedResolved, backend); err != nil {
			return nil, false, err
		}
		if !workspaceRootSelected && !exact {
			return nil, false, nil
		}
		if !workspaceRootSelected {
			scope = databaseOwnershipScopeExact
		}
	case configfile.BackendPostgres, configfile.BackendMySQL:
		if !workspaceRootSelected {
			return nil, false, nil
		}
		ownedPath = candidate.beadsDir
		ownedResolved = candidate.resolved
	default:
		return nil, false, nil
	}
	sourceResolved, err := databaseOwnershipSourceObservations(candidate.sources)
	if err != nil {
		return nil, false, err
	}

	return &databaseOwnershipBinding{
		beadsDir:       candidate.beadsDir,
		backend:        backend,
		ownedPath:      ownedPath,
		source:         source,
		scope:          scope,
		beadsResolved:  candidate.resolved,
		ownedResolved:  ownedResolved,
		sourceResolved: sourceResolved,
	}, true, nil
}

// bareSQLiteDatabaseOwnershipRelevant checks both possible path scopes before
// provider evidence resolves legacy backend="sqlite" metadata without a
// sqlite_path. Irrelevant candidates must not probe provider state merely
// because an ancestor carries the ambiguous legacy marker.
func bareSQLiteDatabaseOwnershipRelevant(
	selector *resolvedDatabasePath,
	beadsDir string,
	cfg *configfile.Config,
	allowAuthoritativeEnvironmentDataDir bool,
) (bool, error) {
	sqlitePath, err := resolveCanonicalDatabasePath(filepath.Join(beadsDir, "beads.db"))
	if err != nil {
		return false, err
	}
	if resolvedDatabasePathEqual(selector, sqlitePath) {
		return true, nil
	}

	doltConfig := *cfg
	doltConfig.Backend = configfile.BackendDolt
	doltOwnedPath := ""
	if allowAuthoritativeEnvironmentDataDir {
		if environmentPath := os.Getenv("BEADS_DOLT_DATA_DIR"); environmentPath != "" {
			doltOwnedPath = databasePathRelativeToWorkspace(environmentPath, beadsDir)
		}
	}
	if doltOwnedPath == "" {
		doltOwnedPath = doltConfig.PersistedDoltDataPath(beadsDir)
	}
	doltPath, err := resolveCanonicalDatabasePath(doltOwnedPath)
	if err != nil {
		return false, err
	}
	return resolvedDatabasePathEqualOrDescendant(selector, doltPath), nil
}

func databaseOwnershipSourceObservations(sources []routedDatabaseOwnershipSource) ([]*resolvedDatabasePath, error) {
	if len(sources) == 0 {
		return nil, errors.New("database ownership source observations are missing")
	}
	observations := make([]*resolvedDatabasePath, 0, len(sources))
	for _, source := range sources {
		var err error
		observations, err = appendDatabaseOwnershipSourceObservation(observations, source.resolved)
		if err != nil {
			return nil, err
		}
	}
	return observations, nil
}

func validateDatabaseProviderOwnedPath(resolved *resolvedDatabasePath, backend string) error {
	if resolved == nil || resolved.observed == nil || resolved.observed.Info == nil {
		return errors.New("database provider path observation is incomplete")
	}
	if !resolved.exists {
		if !resolved.observed.Info.IsDir() {
			return fmt.Errorf("database provider path parent is not a directory: %q", filepath.Dir(resolved.path))
		}
		return nil
	}
	switch backend {
	case configfile.BackendDolt:
		if !resolved.observed.Info.IsDir() {
			return fmt.Errorf("Dolt data path is not a directory: %q", resolved.path)
		}
	case configfile.BackendSQLite:
		if !resolved.observed.Info.Mode().IsRegular() {
			return fmt.Errorf("SQLite database path is not a regular file: %q", resolved.path)
		}
		if err := validateRegularDatabaseFileLinkCount("SQLite database", resolved.path, resolved.observed); err != nil {
			return err
		}
	}
	return nil
}

func appendDatabaseOwner(owners []databaseOwnershipBinding, candidate databaseOwnershipBinding) ([]databaseOwnershipBinding, error) {
	if candidate.beadsResolved == nil {
		return nil, errors.New("database owner workspace observation is missing")
	}
	if len(candidate.sourceResolved) == 0 {
		return nil, errors.New("database owner source observations are missing")
	}
	for index := range owners {
		if owners[index].beadsResolved == nil {
			return nil, errors.New("database owner workspace observation is missing")
		}
		if resolvedDatabasePathEqual(owners[index].beadsResolved, candidate.beadsResolved) {
			for _, source := range candidate.sourceResolved {
				var err error
				owners[index].sourceResolved, err = appendDatabaseOwnershipSourceObservation(owners[index].sourceResolved, source)
				if err != nil {
					return nil, err
				}
			}
			return owners, nil
		}
	}
	return append(owners, candidate), nil
}

func appendDatabaseOwnershipSourceObservation(observations []*resolvedDatabasePath, candidate *resolvedDatabasePath) ([]*resolvedDatabasePath, error) {
	if candidate == nil {
		return nil, errors.New("database ownership source observation is missing")
	}
	for _, observation := range observations {
		if observation == nil {
			return nil, errors.New("database ownership source observation is missing")
		}
		if resolvedDatabasePathEqual(observation, candidate) {
			return observations, nil
		}
	}
	return append(observations, candidate), nil
}

// selectDatabaseStructuralWorkspace chooses by selector-derived source
// hierarchy, never by routed-target spelling. It is intentionally limited to
// metadata-less candidates gathered only after all metadata and redirect
// validation has completed.
func selectDatabaseStructuralWorkspace(candidates []routedDatabaseOwnershipCandidate) (*databaseWorkspaceCandidate, error) {
	var selected *databaseWorkspaceCandidate
	for _, candidate := range candidates {
		if !candidate.structural {
			continue
		}
		if err := validateStructuralDatabaseWorkspaceObservation(candidate.resolved, candidate.beadsDir, "target"); err != nil {
			return nil, err
		}

		foundStructuralSource := false
		for _, source := range candidate.sources {
			if !source.structural {
				continue
			}
			foundStructuralSource = true
			if err := validateStructuralDatabaseWorkspaceObservation(source.resolved, "", "source"); err != nil {
				return nil, err
			}
			current := &databaseWorkspaceCandidate{
				beadsDir:       candidate.beadsDir,
				resolved:       candidate.resolved,
				sourceResolved: source.resolved,
			}
			if selected == nil {
				selected = current
				continue
			}
			if resolvedDatabasePathEqual(current.sourceResolved, selected.sourceResolved) {
				if !resolvedDatabasePathEqual(current.resolved, selected.resolved) {
					paths := []string{current.beadsDir, selected.beadsDir}
					sort.Strings(paths)
					return nil, fmt.Errorf("%w: structural source %q routes to %q and %q",
						errDatabaseOwnershipAmbiguous, current.sourceResolved.path, paths[0], paths[1])
				}
				continue
			}
			if resolvedDatabasePathEqualOrDescendant(current.sourceResolved, selected.sourceResolved) {
				selected = current
				continue
			}
			if resolvedDatabasePathEqualOrDescendant(selected.sourceResolved, current.sourceResolved) {
				continue
			}
			paths := []string{current.sourceResolved.path, selected.sourceResolved.path}
			sort.Strings(paths)
			return nil, fmt.Errorf("%w: structural sources %q and %q are incomparable",
				errDatabaseOwnershipAmbiguous, paths[0], paths[1])
		}
		if !foundStructuralSource {
			return nil, errors.New("structural database workspace source provenance is missing")
		}
	}
	return selected, nil
}

func validateStructuralDatabaseWorkspaceObservation(resolved *resolvedDatabasePath, path, description string) error {
	if resolved == nil {
		return fmt.Errorf("structural database workspace %s observation is incomplete", description)
	}
	if path == "" {
		path = resolved.path
	}
	if path == "" || resolved.path != path || !resolved.exists ||
		resolved.observed == nil || resolved.observed.Info == nil || !resolved.observed.Info.IsDir() {
		return fmt.Errorf("structural database workspace %s observation is incomplete", description)
	}
	return nil
}

func databaseOwnershipHasNestedResolution(owners []databaseOwnershipBinding, structural *databaseWorkspaceCandidate, candidateRoot *resolvedDatabasePath) (bool, error) {
	if candidateRoot == nil {
		return false, errors.New("conditional database ownership source observation is missing")
	}
	for _, owner := range owners {
		if len(owner.sourceResolved) == 0 {
			return false, errors.New("database owner source observations are missing")
		}
		for _, source := range owner.sourceResolved {
			if source == nil {
				return false, errors.New("database owner source observation is missing")
			}
			if !resolvedDatabasePathEqual(source, candidateRoot) &&
				resolvedDatabasePathEqualOrDescendant(source, candidateRoot) {
				return true, nil
			}
		}
	}
	if structural != nil {
		if structural.sourceResolved == nil {
			return false, errors.New("structural database workspace source observation is missing")
		}
		if !resolvedDatabasePathEqual(structural.sourceResolved, candidateRoot) &&
			resolvedDatabasePathEqualOrDescendant(structural.sourceResolved, candidateRoot) {
			return true, nil
		}
	}
	return false, nil
}

// databaseResolutionHintsStrict admits only an explicit BEADS_DIR. Automatic
// CWD/worktree/JJ discovery remains the command selector's responsibility: a
// strict ownership check must not partially clone compatibility discovery or
// silently broaden its evidence set. Callers with an already-selected
// workspace pass it as a databaseWorkspaceHint; every resulting source is
// still observed through FollowRedirectStrict by the resolver.
func databaseResolutionHintsStrict() ([]databaseWorkspaceHint, error) {
	if configured := os.Getenv("BEADS_DIR"); configured != "" {
		original := configured
		var err error
		configured, err = absoluteCleanDatabasePath(configured)
		if err != nil {
			return nil, databasePathOperationError("validate explicit BEADS_DIR", original, err)
		}
		if err := validateDatabaseWorkspaceDirectory(configured); err != nil {
			return nil, databasePathOperationError("validate explicit BEADS_DIR", configured, err)
		}
		return []databaseWorkspaceHint{{
			beadsDir:                configured,
			allowEnvironmentDataDir: true,
			authoritative:           true,
		}}, nil
	}
	return nil, nil
}
