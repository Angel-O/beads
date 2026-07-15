package backendmigration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	workspaceEffectCensusFormat       = "backend-migration-workspace-effect-census/1"
	workspaceEffectSourceBase         = "d62c36f4ce9e716cec8fee35b5983f652c304556"
	workspaceEffectManifestPath       = "testdata/workspace_participation_census_v1.json"
	workspaceEffectModulePath         = "github.com/steveyegge/beads"
	workspaceEffectGotypesAlias       = "gotypesalias=1"
	workspaceEffectSafeErrorText      = "backend migration effect census mismatch"
	workspaceEffectClassificationBase = "032fd955ece885a7fe21fc38f32a218992875cc5603e14d7a6cc4db440160ef0"
	workspaceEffectSignatureBase      = "3f137a82cf8f020b258ddbc690c5b47316bef83a5e0cfce8ee0472e7e957b8aa"
)

type workspaceEffectScanRoot struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type workspaceEffectBuildProfile struct {
	Name       string   `json:"name"`
	GOOS       string   `json:"goos"`
	GOARCH     string   `json:"goarch"`
	CGOEnabled bool     `json:"cgo_enabled"`
	Tags       []string `json:"tags"`
}

type workspaceEffectWatchedSink struct {
	Symbol        string `json:"symbol"`
	Signature     string `json:"signature"`
	EvidenceLayer string `json:"evidence_layer"`
	Scope         string `json:"scope"`
}

type workspaceEffectFamily struct {
	Name              string `json:"name"`
	FutureDisposition string `json:"future_disposition"`
	ObservationState  string `json:"observation_state"`
	SiteCount         int    `json:"site_count"`
}

type workspaceEffectDeferredSurface struct {
	ID      string   `json:"id"`
	State   string   `json:"state"`
	Paths   []string `json:"paths"`
	Symbols []string `json:"symbols"`
}

type workspaceEffectSite struct {
	ID                string   `json:"id"`
	Path              string   `json:"path"`
	EnclosingSymbol   string   `json:"enclosing_symbol"`
	Callee            string   `json:"callee"`
	EvidenceLayer     string   `json:"evidence_layer"`
	InvocationKind    string   `json:"invocation_kind"`
	CallShapeSHA256   string   `json:"call_shape_sha256"`
	Ordinal           int      `json:"ordinal"`
	BuildProfiles     []string `json:"build_profiles"`
	Family            string   `json:"family"`
	FutureDisposition string   `json:"future_disposition"`
}

type workspaceEffectObservedExclusion struct {
	Kind            string   `json:"kind"`
	ID              string   `json:"id"`
	Path            string   `json:"path"`
	EnclosingSymbol string   `json:"enclosing_symbol"`
	Callee          string   `json:"callee"`
	EvidenceLayer   string   `json:"evidence_layer"`
	InvocationKind  string   `json:"invocation_kind"`
	CallShapeSHA256 string   `json:"call_shape_sha256"`
	Ordinal         int      `json:"ordinal"`
	BuildProfiles   []string `json:"build_profiles"`
	Reason          string   `json:"reason"`
}

type workspaceEffectUnselectedExclusion struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type workspaceEffectExclusion struct {
	Kind            string
	ID              string
	Path            string
	EnclosingSymbol string
	Callee          string
	EvidenceLayer   string
	InvocationKind  string
	CallShapeSHA256 string
	Ordinal         int
	BuildProfiles   []string
	Reason          string
}

type workspaceEffectManifest struct {
	Format                string                           `json:"format"`
	SourceBaseline        string                           `json:"source_baseline"`
	RuntimeEnforced       bool                             `json:"runtime_enforced"`
	ScanRoots             []workspaceEffectScanRoot        `json:"scan_roots"`
	BuildProfiles         []workspaceEffectBuildProfile    `json:"build_profiles"`
	SensitiveFilePatterns []string                         `json:"sensitive_file_patterns"`
	WatchedSinks          []workspaceEffectWatchedSink     `json:"watched_sinks"`
	Families              []workspaceEffectFamily          `json:"families"`
	DeferredSurfaces      []workspaceEffectDeferredSurface `json:"deferred_surfaces"`
	Sites                 []workspaceEffectSite            `json:"sites"`
	Exclusions            []workspaceEffectExclusion       `json:"exclusions"`
}

type workspaceEffectRegistrySpec struct {
	Symbol        string
	EvidenceLayer string
	Scope         string
}

type workspaceEffectMismatch struct {
	Kind            string
	Path            string
	EnclosingSymbol string
	Ordinal         int
	Callee          string
}

func (e *workspaceEffectMismatch) Error() string {
	return fmt.Sprintf("%s: %s: %s:%s#%d -> %s", workspaceEffectSafeErrorText, e.Kind, e.Path, e.EnclosingSymbol, e.Ordinal, e.Callee)
}

func workspaceEffectManifestMismatch(kind string) error {
	return &workspaceEffectMismatch{
		Kind:            kind,
		Path:            "<manifest>",
		EnclosingSymbol: "<manifest>",
		Callee:          "<manifest>",
	}
}

func workspaceEffectAnalysisMismatch() error {
	return &workspaceEffectMismatch{
		Kind:            "source_analysis_failed",
		Path:            "<analysis>",
		EnclosingSymbol: "<analysis>",
		Callee:          "<analysis>",
	}
}

func (e workspaceEffectExclusion) MarshalJSON() ([]byte, error) {
	switch e.Kind {
	case "observed_site":
		return json.Marshal(workspaceEffectObservedExclusion{
			Kind:            e.Kind,
			ID:              e.ID,
			Path:            e.Path,
			EnclosingSymbol: e.EnclosingSymbol,
			Callee:          e.Callee,
			EvidenceLayer:   e.EvidenceLayer,
			InvocationKind:  e.InvocationKind,
			CallShapeSHA256: e.CallShapeSHA256,
			Ordinal:         e.Ordinal,
			BuildProfiles:   e.BuildProfiles,
			Reason:          e.Reason,
		})
	case "build_unselected_file":
		return json.Marshal(workspaceEffectUnselectedExclusion{
			Kind:   e.Kind,
			ID:     e.ID,
			Path:   e.Path,
			Reason: e.Reason,
		})
	default:
		return nil, workspaceEffectManifestMismatch("unknown_schema")
	}
}

func (e *workspaceEffectExclusion) UnmarshalJSON(data []byte) error {
	var discriminator struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return workspaceEffectManifestMismatch("unknown_schema")
	}
	switch discriminator.Kind {
	case "observed_site":
		var value workspaceEffectObservedExclusion
		if err := workspaceEffectStrictDecode(data, &value); err != nil {
			return err
		}
		*e = workspaceEffectExclusion{
			Kind:            value.Kind,
			ID:              value.ID,
			Path:            value.Path,
			EnclosingSymbol: value.EnclosingSymbol,
			Callee:          value.Callee,
			EvidenceLayer:   value.EvidenceLayer,
			InvocationKind:  value.InvocationKind,
			CallShapeSHA256: value.CallShapeSHA256,
			Ordinal:         value.Ordinal,
			BuildProfiles:   value.BuildProfiles,
			Reason:          value.Reason,
		}
		return nil
	case "build_unselected_file":
		var value workspaceEffectUnselectedExclusion
		if err := workspaceEffectStrictDecode(data, &value); err != nil {
			return err
		}
		*e = workspaceEffectExclusion{
			Kind:   value.Kind,
			ID:     value.ID,
			Path:   value.Path,
			Reason: value.Reason,
		}
		return nil
	default:
		return workspaceEffectManifestMismatch("unknown_schema")
	}
}

func workspaceEffectScanRoots() []workspaceEffectScanRoot {
	return []workspaceEffectScanRoot{
		{Path: "beads.go", Kind: "file"},
		{Path: "beads_cgo.go", Kind: "file"},
		{Path: "beads_nocgo.go", Kind: "file"},
		{Path: "cmd/bd", Kind: "tree"},
		{Path: "internal/beads", Kind: "tree"},
		{Path: "internal/configfile", Kind: "tree"},
		{Path: "internal/doltserver", Kind: "tree"},
		{Path: "internal/storage", Kind: "tree"},
	}
}

func workspaceEffectBuildProfiles() []workspaceEffectBuildProfile {
	return []workspaceEffectBuildProfile{
		{Name: "darwin_amd64_nocgo", GOOS: "darwin", GOARCH: "amd64", Tags: []string{"gms_pure_go"}},
		{Name: "linux_amd64_cgo", GOOS: "linux", GOARCH: "amd64", CGOEnabled: true, Tags: []string{"gms_pure_go"}},
		{Name: "linux_amd64_nocgo", GOOS: "linux", GOARCH: "amd64", Tags: []string{"gms_pure_go"}},
		{Name: "linux_arm64_nocgo", GOOS: "linux", GOARCH: "arm64", Tags: []string{"gms_pure_go"}},
		{Name: "windows_amd64_nocgo", GOOS: "windows", GOARCH: "amd64", Tags: []string{"gms_pure_go"}},
	}
}

func workspaceEffectSensitivePatterns() []string {
	return []string{
		"cmd/bd/backup*.go",
		"cmd/bd/bootstrap.go",
		"cmd/bd/create.go",
		"cmd/bd/doctor*.go",
		"cmd/bd/doctor/*.go",
		"cmd/bd/doctor/fix/*.go",
		"cmd/bd/init*.go",
		"cmd/bd/migrate_hooks_apply.go",
		"cmd/bd/reset.go",
		"cmd/bd/store_factory.go",
		"cmd/bd/store_factory_nocgo.go",
		"cmd/bd/version_tracking.go",
		"cmd/bd/worktree_cmd.go",
		"internal/beads/beads.go",
		"internal/configfile/*.go",
		"internal/doltserver/*.go",
	}
}

func workspaceEffectFamilies() []workspaceEffectFamily {
	return []workspaceEffectFamily{
		{Name: "backup_restore", FutureDisposition: "pre_effect_refusal_required"},
		{Name: "compatibility_migration", FutureDisposition: "shared_participation_required"},
		{Name: "current_metadata_save", FutureDisposition: "shared_participation_required"},
		{Name: "direct_configured_open", FutureDisposition: "shared_participation_required"},
		{Name: "doctor_fix", FutureDisposition: "pre_effect_refusal_required"},
		{Name: "init_bootstrap", FutureDisposition: "pre_effect_refusal_required"},
		{Name: "provider_state_rename_restore", FutureDisposition: "pre_effect_refusal_required"},
		{Name: "redirect_mutation", FutureDisposition: "pre_effect_refusal_required"},
	}
}

func workspaceEffectDeferredSurfaces() []workspaceEffectDeferredSurface {
	return []workspaceEffectDeferredSurface{{
		ID:    "pr-4561",
		State: "absent_unresolved",
		Paths: []string{
			"backend/plugin",
			"cmd/bd/backend.go",
			"internal/backend",
			"internal/backend/pluginprocess",
			"internal/configfile/backend_plugin_trust.go",
		},
		Symbols: []string{
			"github.com/steveyegge/beads.OpenConfigured",
			"github.com/steveyegge/beads/internal/backend.Lookup",
			"github.com/steveyegge/beads/internal/backend.MustLookup",
			"github.com/steveyegge/beads/internal/backend.OpenConfigured",
			"github.com/steveyegge/beads/internal/backend.Register",
			"github.com/steveyegge/beads/internal/backend/pluginprocess.Start",
			"github.com/steveyegge/beads/internal/configfile.ResolveBackendPluginConfig",
		},
	}}
}

func workspaceEffectRegistry() []workspaceEffectRegistrySpec {
	semanticSymbols := []string{
		"database/sql.Open",
		"database/sql.OpenDB",
		"github.com/steveyegge/beads/cmd/bd.applyFixList",
		"github.com/steveyegge/beads/cmd/bd.applyHookMigrationExecution",
		"github.com/steveyegge/beads/cmd/bd.applyNoCOW",
		"github.com/steveyegge/beads/cmd/bd.atomicWriteFile",
		"github.com/steveyegge/beads/cmd/bd/doctor.FixBtrfsNoCOW",
		"github.com/steveyegge/beads/internal/atomicfile.WriteFile",
		"github.com/steveyegge/beads/internal/configfile.(*Config).Save",
		"github.com/steveyegge/beads/internal/configfile.Load",
		"github.com/steveyegge/beads/internal/configfile.writeFileAtomic",
		"github.com/steveyegge/beads/internal/doltserver.EnsureRunning",
		"github.com/steveyegge/beads/internal/doltserver.EnsureRunningDetailed",
		"github.com/steveyegge/beads/internal/doltserver.IsRunning",
		"github.com/steveyegge/beads/internal/doltserver.KillStaleServers",
		"github.com/steveyegge/beads/internal/doltserver.MarkDoltDirCompatible",
		"github.com/steveyegge/beads/internal/doltserver.RecoverCorruptManifest",
		"github.com/steveyegge/beads/internal/doltserver.RecoverPreV56DoltDir",
		"github.com/steveyegge/beads/internal/doltserver.Start",
		"github.com/steveyegge/beads/internal/doltserver.Stop",
		"github.com/steveyegge/beads/internal/doltserver.StopWithForce",
		"github.com/steveyegge/beads/internal/doltserver.ensureDoltInit",
		"github.com/steveyegge/beads/internal/storage/domain.(*beadsDirFSUseCaseImpl).InitializeBeadsDir",
		"github.com/steveyegge/beads/internal/storage/domain.(BeadsDirFSUseCase).InitializeBeadsDir",
		"github.com/steveyegge/beads/internal/storage.(BackupStore).BackupAdd",
		"github.com/steveyegge/beads/internal/storage.(BackupStore).BackupDatabase",
		"github.com/steveyegge/beads/internal/storage.(BackupStore).BackupRemove",
		"github.com/steveyegge/beads/internal/storage.(BackupStore).BackupSync",
		"github.com/steveyegge/beads/internal/storage.(BackupStore).RestoreDatabase",
		"github.com/steveyegge/beads/internal/storage/dolt.(*DoltStore).BackupAdd",
		"github.com/steveyegge/beads/internal/storage/dolt.(*DoltStore).BackupDatabase",
		"github.com/steveyegge/beads/internal/storage/dolt.(*DoltStore).BackupRemove",
		"github.com/steveyegge/beads/internal/storage/dolt.(*DoltStore).BackupSync",
		"github.com/steveyegge/beads/internal/storage/dolt.(*DoltStore).RestoreDatabase",
		"github.com/steveyegge/beads/internal/storage/dolt.CleanStaleCircuitBreakerFiles",
		"github.com/steveyegge/beads/internal/storage/dolt.New",
		"github.com/steveyegge/beads/internal/storage/dolt.NewFromConfig",
		"github.com/steveyegge/beads/internal/storage/dolt.NewFromConfigWithCLIOptions",
		"github.com/steveyegge/beads/internal/storage/dolt.NewFromConfigWithOptions",
		"github.com/steveyegge/beads/internal/storage/embeddeddolt.(*EmbeddedDoltStore).BackupAdd",
		"github.com/steveyegge/beads/internal/storage/embeddeddolt.(*EmbeddedDoltStore).BackupDatabase",
		"github.com/steveyegge/beads/internal/storage/embeddeddolt.(*EmbeddedDoltStore).BackupRemove",
		"github.com/steveyegge/beads/internal/storage/embeddeddolt.(*EmbeddedDoltStore).BackupSync",
		"github.com/steveyegge/beads/internal/storage/embeddeddolt.(*EmbeddedDoltStore).RestoreDatabase",
		"github.com/steveyegge/beads/internal/storage/embeddeddolt.Open",
		"github.com/steveyegge/beads/internal/storage/embeddeddolt.OpenForReadOnlyCommand",
		"github.com/steveyegge/beads/internal/storage/embeddeddolt.OpenForWorkingSetReconcile",
		"github.com/steveyegge/beads/internal/storage/embeddeddolt.OpenReadOnly",
		"github.com/steveyegge/beads/internal/storage/embeddeddolt.OpenSQL",
		"github.com/steveyegge/beads/internal/storage/mysql.New",
		"github.com/steveyegge/beads/internal/storage/mysql.NewFromConfig",
		"github.com/steveyegge/beads/internal/storage/mysql.Provision",
		"github.com/steveyegge/beads/internal/storage/postgres.New",
		"github.com/steveyegge/beads/internal/storage/postgres.NewFromConfig",
		"github.com/steveyegge/beads/internal/storage/postgres.Provision",
		"github.com/steveyegge/beads/internal/storage/sqlite.New",
		"github.com/steveyegge/beads/internal/storage/sqlite.NewFromConfig",
		"github.com/steveyegge/beads/internal/storage/sqlite.Provision",
		"github.com/steveyegge/beads/internal/storage/uow.NewDoltServerUOWProvider",
		"github.com/steveyegge/beads/internal/storage/uow.NewExternalDoltServerUOWProvider",
		"github.com/steveyegge/beads/internal/storage/versioncontrolops.BackupAdd",
		"github.com/steveyegge/beads/internal/storage/versioncontrolops.BackupRemove",
		"github.com/steveyegge/beads/internal/storage/versioncontrolops.BackupRestore",
		"github.com/steveyegge/beads/internal/storage/versioncontrolops.BackupSync",
	}
	leafSymbols := []string{
		"os.(*File).Chmod",
		"os.(*File).Chown",
		"os.(*File).Sync",
		"os.(*File).Truncate",
		"os.(*File).Write",
		"os.(*File).WriteAt",
		"os.(*File).WriteString",
		"os.(*Process).Kill",
		"os.(*Process).Signal",
		"os.Chmod",
		"os.Chown",
		"os.Chtimes",
		"os.Create",
		"os.CreateTemp",
		"os.Lchown",
		"os.Link",
		"os.Mkdir",
		"os.MkdirAll",
		"os.MkdirTemp",
		"os.OpenFile",
		"os.Remove",
		"os.RemoveAll",
		"os.Rename",
		"os.Symlink",
		"os.Truncate",
		"os.WriteFile",
		"os/exec.(*Cmd).CombinedOutput",
		"os/exec.(*Cmd).Output",
		"os/exec.(*Cmd).Run",
		"os/exec.(*Cmd).Start",
		"os/exec.(*Cmd).Wait",
		"syscall.Kill",
	}
	registry := make([]workspaceEffectRegistrySpec, 0, len(semanticSymbols)+len(leafSymbols))
	for _, symbol := range semanticSymbols {
		registry = append(registry, workspaceEffectRegistrySpec{Symbol: symbol, EvidenceLayer: "semantic_boundary", Scope: "global"})
	}
	for _, symbol := range leafSymbols {
		registry = append(registry, workspaceEffectRegistrySpec{Symbol: symbol, EvidenceLayer: "leaf", Scope: "sensitive_files"})
	}
	sort.Slice(registry, func(i, j int) bool {
		if registry[i].Symbol != registry[j].Symbol {
			return registry[i].Symbol < registry[j].Symbol
		}
		if registry[i].EvidenceLayer != registry[j].EvidenceLayer {
			return registry[i].EvidenceLayer < registry[j].EvidenceLayer
		}
		return registry[i].Scope < registry[j].Scope
	})
	return registry
}

type workspaceEffectGoListPackage struct {
	Dir             string
	ImportPath      string
	Name            string
	Export          string
	CompiledGoFiles []string
	Standard        bool
	Error           *struct{}
	DepsErrors      []struct{}
}

type workspaceEffectLoadedPackage struct {
	listed           workspaceEffectGoListPackage
	relativeFiles    map[*ast.File]string
	files            []*ast.File
	fset             *token.FileSet
	typesPackage     *types.Package
	info             *types.Info
	interfaceOwners  map[*types.Func]string
	interfaceMethods map[*types.Func]struct{}
}

type workspaceEffectRawSite struct {
	Path            string
	EnclosingSymbol string
	Callee          string
	EvidenceLayer   string
	InvocationKind  string
	CallShapeSHA256 string
	Position        token.Pos
	SourceOffset    int
	SourceEndOffset int
}

type workspaceEffectDetectedSite struct {
	ID              string
	Path            string
	EnclosingSymbol string
	Callee          string
	EvidenceLayer   string
	InvocationKind  string
	CallShapeSHA256 string
	Ordinal         int
	BuildProfiles   []string
	SourceOffset    int
	SourceEndOffset int
}

type workspaceEffectCallAnchor struct {
	Watched bool
	Site    workspaceEffectDetectedSite
}

func workspaceEffectMergeCallAnchors(destination map[string]workspaceEffectCallAnchor, source map[string]workspaceEffectCallAnchor) error {
	for key, anchor := range source {
		existing, exists := destination[key]
		if !exists {
			destination[key] = anchor
			continue
		}
		if existing.Watched != anchor.Watched || (anchor.Watched && !workspaceEffectSameSite(existing.Site, anchor.Site)) {
			return workspaceEffectAnalysisMismatch()
		}
	}
	return nil
}

type workspaceEffectProfileResult struct {
	Sites                      []workspaceEffectDetectedSite
	SelectedFiles              map[string]struct{}
	Signatures                 map[string]string
	Declarations               map[string]struct{}
	CallAnchors                map[string]workspaceEffectCallAnchor
	PendingInterfaceReferences []workspaceEffectPendingInterfaceReference
}

type workspaceEffectPendingInterfaceReference struct {
	Path            string
	EnclosingSymbol string
	Callee          string
	SignatureBody   string
}

type workspaceEffectRepositoryResult struct {
	Sites           []workspaceEffectDetectedSite
	UnselectedFiles []string
	WatchedSinks    []workspaceEffectWatchedSink
	Declarations    map[string]struct{}
}

type workspaceEffectAliasSpec struct {
	PackagePath string
	Name        string
	File        string
	Root        string
}

func workspaceEffectAliases() []workspaceEffectAliasSpec {
	const packagePath = "github.com/steveyegge/beads/cmd/bd/doctor/fix"
	const file = "cmd/bd/doctor/fix/fs.go"
	return []workspaceEffectAliasSpec{
		{PackagePath: packagePath, Name: "openFileRW", File: file, Root: "os.OpenFile"},
		{PackagePath: packagePath, Name: "removeFile", File: file, Root: "os.Remove"},
		{PackagePath: packagePath, Name: "renameFile", File: file, Root: "os.Rename"},
	}
}

func workspaceEffectCheckDeferredPaths(repoRoot string) error {
	for _, sentinel := range workspaceEffectDeferredSurfaces()[0].Paths {
		_, err := os.Lstat(filepath.Join(repoRoot, filepath.FromSlash(sentinel)))
		if err == nil {
			return workspaceEffectManifestMismatch("deferred_surface_present")
		}
		if !os.IsNotExist(err) {
			return workspaceEffectAnalysisMismatch()
		}
	}
	return nil
}

func workspaceEffectCheckDeferredSymbols(declarations map[string]struct{}) error {
	for _, sentinel := range workspaceEffectDeferredSurfaces()[0].Symbols {
		if _, exists := declarations[sentinel]; exists {
			return workspaceEffectManifestMismatch("deferred_surface_present")
		}
	}
	return nil
}

func workspaceEffectAliasMismatch(alias workspaceEffectAliasSpec) error {
	return &workspaceEffectMismatch{
		Kind:            "unresolved_watched_reference",
		Path:            alias.File,
		EnclosingSymbol: alias.PackagePath + ".package_init@" + alias.File,
		Callee:          alias.Root,
	}
}

func workspaceEffectRegistryMap() map[string]workspaceEffectRegistrySpec {
	registry := workspaceEffectRegistry()
	result := make(map[string]workspaceEffectRegistrySpec, len(registry))
	for _, spec := range registry {
		result[spec.Symbol] = spec
	}
	return result
}

func workspaceEffectSHA256(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func workspaceEffectCallShape(callee, kind string, argumentCount int, ellipsis bool, typeArgumentCount int) string {
	ellipsisValue := 0
	if ellipsis {
		ellipsisValue = 1
	}
	value := "callee=" + callee + "\x00" +
		"kind=" + kind + "\x00" +
		"args=" + strconv.Itoa(argumentCount) + "\x00" +
		"ellipsis=" + strconv.Itoa(ellipsisValue) + "\x00" +
		"type_args=" + strconv.Itoa(typeArgumentCount) + "\x00"
	return workspaceEffectSHA256(value)
}

func workspaceEffectSiteID(site workspaceEffectDetectedSite) string {
	value := "path=" + site.Path + "\x00" +
		"enclosing=" + site.EnclosingSymbol + "\x00" +
		"callee=" + site.Callee + "\x00" +
		"kind=" + site.InvocationKind + "\x00" +
		"shape_sha256=" + site.CallShapeSHA256 + "\x00" +
		"ordinal=" + strconv.Itoa(site.Ordinal) + "\x00"
	return workspaceEffectSHA256(value)
}

func workspaceEffectUnselectedID(file string) string {
	return workspaceEffectSHA256("build_unselected_file\x00" + file)
}

func workspaceEffectNameFreeSignature(symbol string, signature *types.Signature) (string, bool) {
	body, ok := workspaceEffectNameFreeSignatureBody(signature, false)
	if !ok {
		return "", false
	}
	return symbol + " " + body, true
}

func workspaceEffectNameFreeSignatureBody(signature *types.Signature, allowReceiverTypeParameters bool) (string, bool) {
	if signature == nil || signature.TypeParams().Len() != 0 || (!allowReceiverTypeParameters && signature.RecvTypeParams().Len() != 0) {
		return "", false
	}
	qualifier := func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return pkg.Path()
	}
	parameters := make([]string, signature.Params().Len())
	for i := 0; i < signature.Params().Len(); i++ {
		parameterType := signature.Params().At(i).Type()
		if signature.Variadic() && i == signature.Params().Len()-1 {
			slice, ok := parameterType.(*types.Slice)
			if !ok {
				return "", false
			}
			parameters[i] = "..." + types.TypeString(slice.Elem(), qualifier)
			continue
		}
		parameters[i] = types.TypeString(parameterType, qualifier)
	}
	results := make([]string, signature.Results().Len())
	for i := 0; i < signature.Results().Len(); i++ {
		results[i] = types.TypeString(signature.Results().At(i).Type(), qualifier)
	}
	return "func(" + strings.Join(parameters, ", ") + ")(" + strings.Join(results, ", ") + ")", true
}

func workspaceEffectInterfaceOwners(pkg *types.Package) map[*types.Func]string {
	owners := make(map[*types.Func]string)
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		typeName, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		named, ok := types.Unalias(typeName.Type()).(*types.Named)
		if !ok || named.Obj() != typeName {
			continue
		}
		iface, ok := named.Underlying().(*types.Interface)
		if !ok {
			continue
		}
		iface.Complete()
		owner := pkg.Path() + ".(" + typeName.Name() + ")"
		for i := 0; i < iface.NumExplicitMethods(); i++ {
			owners[iface.ExplicitMethod(i)] = owner
		}
	}
	return owners
}

func workspaceEffectInterfaceMethods(info *types.Info, owners map[*types.Func]string) map[*types.Func]struct{} {
	methods := make(map[*types.Func]struct{}, len(owners))
	for method := range owners {
		methods[method] = struct{}{}
	}
	if info == nil {
		return methods
	}
	seen := make(map[types.Type]struct{})
	var collect func(types.Type)
	collect = func(value types.Type) {
		if value == nil {
			return
		}
		value = types.Unalias(value)
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		switch typed := value.(type) {
		case *types.Named:
			collect(typed.Underlying())
		case *types.Pointer:
			collect(typed.Elem())
		case *types.Struct:
			for index := 0; index < typed.NumFields(); index++ {
				collect(typed.Field(index).Type())
			}
		case *types.Interface:
			typed.Complete()
			for index := 0; index < typed.NumMethods(); index++ {
				methods[typed.Method(index)] = struct{}{}
			}
		case *types.TypeParam:
			collect(typed.Constraint())
		}
	}
	for _, typeAndValue := range info.Types {
		collect(typeAndValue.Type)
	}
	for _, selection := range info.Selections {
		if selection == nil || selection.Recv() == nil {
			continue
		}
		if _, ok := types.Unalias(selection.Recv()).Underlying().(*types.Interface); !ok {
			continue
		}
		if method, ok := selection.Obj().(*types.Func); ok {
			methods[method] = struct{}{}
		}
	}
	return methods
}

func workspaceEffectReceiverName(receiver types.Type) (string, string, bool) {
	pointer := false
	receiver = types.Unalias(receiver)
	if value, ok := receiver.(*types.Pointer); ok {
		pointer = true
		receiver = types.Unalias(value.Elem())
	}
	named, ok := receiver.(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return "", "", false
	}
	name := named.Obj().Name()
	if pointer {
		name = "*" + name
	}
	return named.Obj().Pkg().Path(), name, true
}

func workspaceEffectCanonicalFunction(object *types.Func, interfaceOwners map[*types.Func]string) (string, *types.Signature, bool) {
	if object == nil {
		return "", nil, false
	}
	signature, ok := object.Type().(*types.Signature)
	if !ok {
		return "", nil, false
	}
	if owner, exists := interfaceOwners[object]; exists {
		return owner + "." + object.Name(), signature, true
	}
	if signature.Recv() != nil {
		packagePath, receiver, ok := workspaceEffectReceiverName(signature.Recv().Type())
		if !ok {
			return "", nil, false
		}
		return packagePath + ".(" + receiver + ")." + object.Name(), signature, true
	}
	if object.Pkg() == nil {
		return "", nil, false
	}
	return object.Pkg().Path() + "." + object.Name(), signature, true
}

func workspaceEffectIndexPackageDeclarations(loaded *workspaceEffectLoadedPackage, registry map[string]workspaceEffectRegistrySpec, result *workspaceEffectProfileResult) error {
	pkg := loaded.typesPackage
	indexFunction := func(function *types.Func) error {
		symbol, signature, ok := workspaceEffectCanonicalFunction(function, loaded.interfaceOwners)
		if !ok {
			return nil
		}
		result.Declarations[symbol] = struct{}{}
		if _, watched := registry[symbol]; !watched {
			return nil
		}
		canonical, ok := workspaceEffectNameFreeSignature(symbol, signature)
		if !ok {
			return workspaceEffectAnalysisMismatch()
		}
		if existing, exists := result.Signatures[symbol]; exists && existing != canonical {
			return workspaceEffectAnalysisMismatch()
		}
		result.Signatures[symbol] = canonical
		return nil
	}
	for _, name := range pkg.Scope().Names() {
		object := pkg.Scope().Lookup(name)
		result.Declarations[pkg.Path()+"."+name] = struct{}{}
		if function, ok := object.(*types.Func); ok {
			if err := indexFunction(function); err != nil {
				return err
			}
		}
		typeName, ok := object.(*types.TypeName)
		if !ok {
			continue
		}
		named, ok := types.Unalias(typeName.Type()).(*types.Named)
		if !ok {
			continue
		}
		if iface, ok := named.Underlying().(*types.Interface); ok {
			iface.Complete()
			if named.TypeParams().Len() != 0 {
				for i := 0; i < iface.NumExplicitMethods(); i++ {
					symbol, _, canonical := workspaceEffectCanonicalFunction(iface.ExplicitMethod(i), loaded.interfaceOwners)
					if canonical {
						if _, watched := registry[symbol]; watched {
							return workspaceEffectAnalysisMismatch()
						}
					}
				}
			}
			for i := 0; i < iface.NumMethods(); i++ {
				if err := indexFunction(iface.Method(i)); err != nil {
					return err
				}
			}
		}
		methodSets := []*types.MethodSet{types.NewMethodSet(named), types.NewMethodSet(types.NewPointer(named))}
		seen := make(map[*types.Func]struct{})
		for _, methodSet := range methodSets {
			for i := 0; i < methodSet.Len(); i++ {
				function, ok := methodSet.At(i).Obj().(*types.Func)
				if !ok {
					continue
				}
				if _, exists := seen[function]; exists {
					continue
				}
				seen[function] = struct{}{}
				if err := indexFunction(function); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func workspaceEffectRepositoryRoot() (string, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", workspaceEffectAnalysisMismatch()
	}
	current, err := filepath.Abs(workingDirectory)
	if err != nil {
		return "", workspaceEffectAnalysisMismatch()
	}
	for {
		if info, statErr := os.Stat(filepath.Join(current, "go.mod")); statErr == nil && info.Mode().IsRegular() {
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil {
				return "", workspaceEffectAnalysisMismatch()
			}
			return filepath.Clean(resolved), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", workspaceEffectAnalysisMismatch()
		}
		current = parent
	}
}

func workspaceEffectResolvedRoot(value string, requireAbsolute bool) (string, error) {
	if value == "" || (requireAbsolute && !filepath.IsAbs(value)) {
		return "", workspaceEffectAnalysisMismatch()
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", workspaceEffectAnalysisMismatch()
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", workspaceEffectAnalysisMismatch()
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", workspaceEffectAnalysisMismatch()
	}
	return filepath.Clean(resolved), nil
}

func workspaceEffectPathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." {
		return relative == "." && err == nil
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func workspaceEffectRequireExternal(repoRoot, candidate string) error {
	if workspaceEffectPathWithin(repoRoot, candidate) {
		return workspaceEffectAnalysisMismatch()
	}
	return nil
}

func workspaceEffectValidateOuterGoTypesEnvironment() error {
	if value := os.Getenv("GODEBUG"); value != "" && value != workspaceEffectGotypesAlias {
		return workspaceEffectAnalysisMismatch()
	}
	return nil
}

func workspaceEffectGoBootstrap(repoRoot, privateRoot string) (string, string, error) {
	if err := workspaceEffectValidateOuterGoTypesEnvironment(); err != nil {
		return "", "", workspaceEffectAnalysisMismatch()
	}
	tool := filepath.Join(runtime.GOROOT(), "bin", "go")
	if !filepath.IsAbs(tool) {
		return "", "", workspaceEffectAnalysisMismatch()
	}
	privateHome := filepath.Join(privateRoot, "home")
	privateTemp := filepath.Join(privateRoot, "tmp")
	privateCache := filepath.Join(privateRoot, "gocache")
	privateGOPATH := filepath.Join(privateRoot, "gopath")
	for _, directory := range []string{privateHome, privateTemp, privateCache, privateGOPATH} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return "", "", workspaceEffectAnalysisMismatch()
		}
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = privateHome
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		gopath = privateGOPATH
	}
	environment := []string{
		"GOCACHE=" + privateCache,
		"GODEBUG=" + workspaceEffectGotypesAlias,
		"GOENV=off",
		"GOPATH=" + gopath,
		"GOROOT=" + runtime.GOROOT(),
		"GOTELEMETRY=off",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + privateTemp,
	}
	if moduleCache := os.Getenv("GOMODCACHE"); moduleCache != "" {
		environment = append(environment, "GOMODCACHE="+moduleCache)
	}
	command := exec.Command(tool, "env", "GOVERSION", "GOMODCACHE")
	command.Dir = repoRoot
	command.Env = environment
	output, err := command.Output()
	if err != nil {
		return "", "", workspaceEffectAnalysisMismatch()
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != 2 || strings.TrimSpace(lines[0]) != runtime.Version() {
		return "", "", workspaceEffectAnalysisMismatch()
	}
	moduleCache, err := workspaceEffectResolvedRoot(strings.TrimSpace(lines[1]), true)
	if err != nil || workspaceEffectRequireExternal(repoRoot, moduleCache) != nil {
		return "", "", workspaceEffectAnalysisMismatch()
	}
	return tool, moduleCache, nil
}

func workspaceEffectProfileEnvironment(privateRoot, moduleCache string, profile workspaceEffectBuildProfile) []string {
	cgo := "0"
	if profile.CGOEnabled {
		cgo = "1"
	}
	environment := []string{
		"CGO_ENABLED=" + cgo,
		"GOCACHE=" + filepath.Join(privateRoot, "gocache"),
		"GODEBUG=" + workspaceEffectGotypesAlias,
		"GOENV=off",
		"GOFLAGS=-mod=readonly",
		"GOOS=" + profile.GOOS,
		"GOARCH=" + profile.GOARCH,
		"GOMODCACHE=" + moduleCache,
		"GOPATH=" + filepath.Join(privateRoot, "gopath"),
		"GOPROXY=off",
		"GOROOT=" + runtime.GOROOT(),
		"GOSUMDB=off",
		"GOTELEMETRY=off",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
		"HOME=" + filepath.Join(privateRoot, "home"),
		"LANG=C",
		"LC_ALL=C",
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + filepath.Join(privateRoot, "tmp"),
	}
	if profile.GOARCH == "amd64" {
		environment = append(environment, "GOAMD64=v1")
	} else if profile.GOARCH == "arm64" {
		environment = append(environment, "GOARM64=v8.0")
	}
	sort.Strings(environment)
	return environment
}

func workspaceEffectGoList(repoRoot, privateRoot, tool, moduleCache string, profile workspaceEffectBuildProfile) ([]workspaceEffectGoListPackage, error) {
	arguments := []string{
		"list", "-deps", "-export", "-json", "-compiled", "-tags=gms_pure_go",
		".",
		"./cmd/bd/...",
		"./internal/configfile/...",
		"./internal/beads/...",
		"./internal/doltserver/...",
		"./internal/storage/...",
	}
	command := exec.Command(tool, arguments...)
	command.Dir = repoRoot
	command.Env = workspaceEffectProfileEnvironment(privateRoot, moduleCache, profile)
	output, err := command.Output()
	if err != nil {
		return nil, workspaceEffectAnalysisMismatch()
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	var packages []workspaceEffectGoListPackage
	for {
		var listed workspaceEffectGoListPackage
		err := decoder.Decode(&listed)
		if err == io.EOF {
			break
		}
		if err != nil || listed.Error != nil || len(listed.DepsErrors) != 0 || listed.ImportPath == "" {
			return nil, workspaceEffectAnalysisMismatch()
		}
		packages = append(packages, listed)
	}
	return packages, nil
}

func workspaceEffectProductionFile(file string) bool {
	if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
		return false
	}
	for _, component := range strings.Split(file, "/") {
		if component == "testdata" || component == "vendor" {
			return false
		}
	}
	return true
}

func workspaceEffectFileInScanRoots(file string) bool {
	for _, root := range workspaceEffectScanRoots() {
		if root.Kind == "file" && file == root.Path {
			return true
		}
		if root.Kind == "tree" && (file == root.Path || strings.HasPrefix(file, root.Path+"/")) {
			return true
		}
	}
	return false
}

func workspaceEffectSensitiveFile(file string) bool {
	for _, pattern := range workspaceEffectSensitivePatterns() {
		matched, err := path.Match(pattern, file)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func workspaceEffectRepoRelative(repoRoot, file string) (string, bool, error) {
	if !filepath.IsAbs(file) {
		file = filepath.Join(repoRoot, file)
	}
	absolute, err := filepath.Abs(file)
	if err != nil {
		return "", false, workspaceEffectAnalysisMismatch()
	}
	relative, err := filepath.Rel(repoRoot, absolute)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false, nil
	}
	clean := filepath.ToSlash(filepath.Clean(relative))
	if !workspaceEffectProductionFile(clean) {
		return "", false, nil
	}
	if !workspaceEffectValidPath(clean, false) {
		return "", false, workspaceEffectAnalysisMismatch()
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", false, workspaceEffectAnalysisMismatch()
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || filepath.Clean(resolved) != filepath.Clean(absolute) {
		return "", false, workspaceEffectAnalysisMismatch()
	}
	return clean, true, nil
}

func workspaceEffectLoadPackages(repoRoot string, listed []workspaceEffectGoListPackage, profile workspaceEffectBuildProfile) ([]*workspaceEffectLoadedPackage, error) {
	exports := make(map[string]string, len(listed))
	for _, item := range listed {
		if item.Export != "" {
			exports[item.ImportPath] = item.Export
		}
	}
	var retained []workspaceEffectGoListPackage
	for _, item := range listed {
		if item.Standard || (item.ImportPath != workspaceEffectModulePath && !strings.HasPrefix(item.ImportPath, workspaceEffectModulePath+"/")) {
			continue
		}
		selected := false
		for _, file := range item.CompiledGoFiles {
			file = workspaceEffectListedFile(item, file)
			relative, ok, err := workspaceEffectRepoRelative(repoRoot, file)
			if err != nil {
				return nil, err
			}
			if ok && workspaceEffectFileInScanRoots(relative) {
				selected = true
				break
			}
		}
		if selected {
			retained = append(retained, item)
		}
	}
	sort.Slice(retained, func(i, j int) bool { return retained[i].ImportPath < retained[j].ImportPath })
	loadedPackages := make([]*workspaceEffectLoadedPackage, 0, len(retained))
	for _, item := range retained {
		fset := token.NewFileSet()
		files := make([]*ast.File, 0, len(item.CompiledGoFiles))
		relativeFiles := make(map[*ast.File]string)
		for _, fileName := range item.CompiledGoFiles {
			fileName = workspaceEffectListedFile(item, fileName)
			file, err := parser.ParseFile(fset, fileName, nil, 0)
			if err != nil {
				return nil, workspaceEffectAnalysisMismatch()
			}
			files = append(files, file)
			relative, ok, relativeErr := workspaceEffectRepoRelative(repoRoot, fileName)
			if relativeErr != nil {
				return nil, relativeErr
			}
			if ok && workspaceEffectFileInScanRoots(relative) {
				relativeFiles[file] = relative
			}
		}
		if len(files) == 0 || len(relativeFiles) == 0 {
			return nil, workspaceEffectAnalysisMismatch()
		}
		lookup := func(importPath string) (io.ReadCloser, error) {
			exportFile, ok := exports[importPath]
			if !ok || exportFile == "" {
				return nil, os.ErrNotExist
			}
			return os.Open(exportFile)
		}
		information := &types.Info{
			Types:      make(map[ast.Expr]types.TypeAndValue),
			Defs:       make(map[*ast.Ident]types.Object),
			Uses:       make(map[*ast.Ident]types.Object),
			Instances:  make(map[*ast.Ident]types.Instance),
			Selections: make(map[*ast.SelectorExpr]*types.Selection),
		}
		sizes := types.SizesFor("gc", profile.GOARCH)
		if sizes == nil {
			return nil, workspaceEffectAnalysisMismatch()
		}
		configuration := &types.Config{
			Importer: importer.ForCompiler(fset, "gc", lookup),
			Sizes:    sizes,
			Error:    func(error) {},
		}
		typedPackage, err := configuration.Check(item.ImportPath, fset, files, information)
		if err != nil || typedPackage == nil {
			return nil, workspaceEffectAnalysisMismatch()
		}
		interfaceOwners := workspaceEffectInterfaceOwners(typedPackage)
		loaded := &workspaceEffectLoadedPackage{
			listed:           item,
			relativeFiles:    relativeFiles,
			files:            files,
			fset:             fset,
			typesPackage:     typedPackage,
			info:             information,
			interfaceOwners:  interfaceOwners,
			interfaceMethods: workspaceEffectInterfaceMethods(information, interfaceOwners),
		}
		loadedPackages = append(loadedPackages, loaded)
	}
	return loadedPackages, nil
}

func workspaceEffectListedFile(listed workspaceEffectGoListPackage, file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(listed.Dir, file)
}

func workspaceEffectInvocationKinds(files []*ast.File) map[*ast.CallExpr]string {
	kinds := make(map[*ast.CallExpr]string)
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.DeferStmt:
				kinds[value.Call] = "defer"
			case *ast.GoStmt:
				kinds[value.Call] = "go"
			}
			return true
		})
	}
	return kinds
}

func workspaceEffectPeelCallee(expression ast.Expr, info *types.Info) (ast.Expr, int) {
	typeArguments := 0
	for {
		switch value := expression.(type) {
		case *ast.ParenExpr:
			expression = value.X
		case *ast.IndexExpr:
			identifier := workspaceEffectCalleeIdentifier(value.X)
			if identifier == nil {
				return expression, typeArguments
			}
			if _, instantiated := info.Instances[identifier]; !instantiated {
				return expression, typeArguments
			}
			typeArguments++
			expression = value.X
		case *ast.IndexListExpr:
			identifier := workspaceEffectCalleeIdentifier(value.X)
			if identifier == nil {
				return expression, typeArguments
			}
			if _, instantiated := info.Instances[identifier]; !instantiated {
				return expression, typeArguments
			}
			typeArguments += len(value.Indices)
			expression = value.X
		default:
			return expression, typeArguments
		}
	}
}

func workspaceEffectCalleeIdentifier(expression ast.Expr) *ast.Ident {
	for {
		switch value := expression.(type) {
		case *ast.ParenExpr:
			expression = value.X
		case *ast.Ident:
			return value
		case *ast.SelectorExpr:
			return value.Sel
		default:
			return nil
		}
	}
}

func workspaceEffectCalledObject(expression ast.Expr, info *types.Info) (types.Object, *ast.Ident) {
	switch value := expression.(type) {
	case *ast.Ident:
		return info.Uses[value], value
	case *ast.SelectorExpr:
		if selection := info.Selections[value]; selection != nil {
			return selection.Obj(), value.Sel
		}
		return info.Uses[value.Sel], value.Sel
	default:
		return nil, nil
	}
}

type workspaceEffectRelativeFile struct {
	File *ast.File
	Path string
}

func workspaceEffectSortedRelativeFiles(loaded *workspaceEffectLoadedPackage) []workspaceEffectRelativeFile {
	files := make([]workspaceEffectRelativeFile, 0, len(loaded.relativeFiles))
	for file, relative := range loaded.relativeFiles {
		files = append(files, workspaceEffectRelativeFile{File: file, Path: relative})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

func workspaceEffectFileForPosition(loaded *workspaceEffectLoadedPackage, position token.Pos) string {
	for _, source := range workspaceEffectSortedRelativeFiles(loaded) {
		if position >= source.File.Pos() && position <= source.File.End() {
			return source.Path
		}
	}
	return ""
}

func workspaceEffectAliasObjects(loaded *workspaceEffectLoadedPackage, registry map[string]workspaceEffectRegistrySpec, signatures map[string]string) (map[*types.Var]string, map[*ast.Ident]struct{}, error) {
	aliases := workspaceEffectAliases()
	relevant := make(map[string]workspaceEffectAliasSpec)
	for _, alias := range aliases {
		if alias.PackagePath == loaded.listed.ImportPath {
			relevant[alias.Name] = alias
		}
	}
	objects := make(map[*types.Var]string)
	allowedInitializers := make(map[*ast.Ident]struct{})
	if len(relevant) == 0 {
		return objects, allowedInitializers, nil
	}
	counts := make(map[string]int)
	for _, source := range workspaceEffectSortedRelativeFiles(loaded) {
		file, relative := source.File, source.Path
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok {
				if alias, watched := relevant[function.Name.Name]; watched {
					return nil, nil, workspaceEffectAliasMismatch(alias)
				}
				continue
			}
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.VAR {
				continue
			}
			for _, raw := range general.Specs {
				value, ok := raw.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for index, name := range value.Names {
					alias, watched := relevant[name.Name]
					if !watched {
						continue
					}
					counts[name.Name]++
					if relative != alias.File || index >= len(value.Values) || len(value.Names) != len(value.Values) {
						return nil, nil, workspaceEffectAliasMismatch(alias)
					}
					initializer := value.Values[index]
					switch initializer.(type) {
					case *ast.Ident, *ast.SelectorExpr:
					default:
						return nil, nil, workspaceEffectAliasMismatch(alias)
					}
					rootObject, rootIdentifier := workspaceEffectCalledObject(initializer, loaded.info)
					rootFunction, ok := rootObject.(*types.Func)
					if !ok || rootIdentifier == nil {
						return nil, nil, workspaceEffectAliasMismatch(alias)
					}
					rootSymbol, rootSignature, ok := workspaceEffectCanonicalFunction(rootFunction, loaded.interfaceOwners)
					if !ok || rootSymbol != alias.Root {
						return nil, nil, workspaceEffectAliasMismatch(alias)
					}
					if _, watched := registry[rootSymbol]; !watched {
						return nil, nil, workspaceEffectAliasMismatch(alias)
					}
					canonicalRoot, ok := workspaceEffectNameFreeSignature(rootSymbol, rootSignature)
					if !ok {
						return nil, nil, workspaceEffectAliasMismatch(alias)
					}
					if existing, exists := signatures[rootSymbol]; exists && existing != canonicalRoot {
						return nil, nil, workspaceEffectAliasMismatch(alias)
					}
					signatures[rootSymbol] = canonicalRoot
					variable, ok := loaded.info.Defs[name].(*types.Var)
					if !ok || variable.Parent() != loaded.typesPackage.Scope() || !types.Identical(variable.Type(), rootFunction.Type()) {
						return nil, nil, workspaceEffectAliasMismatch(alias)
					}
					objects[variable] = rootSymbol
					allowedInitializers[rootIdentifier] = struct{}{}
				}
			}
		}
	}
	for _, alias := range aliases {
		if alias.PackagePath == loaded.listed.ImportPath && counts[alias.Name] != 1 {
			return nil, nil, workspaceEffectAliasMismatch(alias)
		}
	}
	return objects, allowedInitializers, nil
}

func workspaceEffectWalkCallable(root ast.Node, parent string, literalCounters map[string]int, visit func(ast.Node, string)) {
	if literal, ok := root.(*ast.FuncLit); ok {
		literalCounters[parent]++
		child := parent + "$func#" + strconv.Itoa(literalCounters[parent])
		workspaceEffectWalkCallable(literal.Body, child, literalCounters, visit)
		return
	}
	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			return true
		}
		if literal, ok := node.(*ast.FuncLit); ok {
			literalCounters[parent]++
			child := parent + "$func#" + strconv.Itoa(literalCounters[parent])
			workspaceEffectWalkCallable(literal.Body, child, literalCounters, visit)
			return false
		}
		visit(node, parent)
		return true
	})
}

func workspaceEffectPackageRawSites(loaded *workspaceEffectLoadedPackage, registry map[string]workspaceEffectRegistrySpec, result *workspaceEffectProfileResult, bindingAllowed map[*ast.Ident]struct{}) ([]workspaceEffectRawSite, error) {
	if result.CallAnchors == nil {
		result.CallAnchors = make(map[string]workspaceEffectCallAnchor)
	}
	aliasObjects, allowedReferences, err := workspaceEffectAliasObjects(loaded, registry, result.Signatures)
	if err != nil {
		return nil, err
	}
	for identifier := range bindingAllowed {
		allowedReferences[identifier] = struct{}{}
	}
	invocationKinds := workspaceEffectInvocationKinds(loaded.files)
	referenceEnclosing := make(map[*ast.Ident]string)
	var rawSites []workspaceEffectRawSite

	recordNode := func(relative string, node ast.Node, enclosing string) error {
		if identifier, ok := node.(*ast.Ident); ok {
			if loaded.info.Uses[identifier] != nil {
				referenceEnclosing[identifier] = enclosing
			}
			return nil
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return nil
		}
		tokenFile := loaded.fset.File(call.Pos())
		if tokenFile == nil {
			return workspaceEffectAnalysisMismatch()
		}
		sourceOffset := tokenFile.Offset(call.Pos())
		sourceEndOffset := tokenFile.Offset(call.End())
		anchorKey := workspaceEffectCallAnchorKey(relative, sourceOffset, sourceEndOffset)
		if _, duplicate := result.CallAnchors[anchorKey]; duplicate {
			return workspaceEffectAnalysisMismatch()
		}
		result.CallAnchors[anchorKey] = workspaceEffectCallAnchor{}
		calleeExpression, typeArgumentCount := workspaceEffectPeelCallee(call.Fun, loaded.info)
		object, identifier := workspaceEffectCalledObject(calleeExpression, loaded.info)
		if object == nil || identifier == nil {
			return nil
		}
		callee := ""
		var signature *types.Signature
		if alias, ok := object.(*types.Var); ok {
			mapped, watched := aliasObjects[alias]
			if !watched {
				return nil
			}
			callee = mapped
			rootSignature := result.Signatures[callee]
			if rootSignature == "" {
				return workspaceEffectAnalysisMismatch()
			}
		} else if function, ok := object.(*types.Func); ok {
			ambiguous := workspaceEffectAmbiguousInterfaceMethod(function, loaded.interfaceOwners, loaded.interfaceMethods, result.Signatures)
			canonical, functionSignature, resolved := workspaceEffectCanonicalFunction(function, loaded.interfaceOwners)
			if !resolved {
				if ambiguous != "" {
					return &workspaceEffectMismatch{Kind: "unresolved_watched_reference", Path: relative, EnclosingSymbol: enclosing, Callee: ambiguous}
				}
				return nil
			}
			callee = canonical
			signature = functionSignature
			if _, watched := registry[callee]; !watched && ambiguous != "" {
				return &workspaceEffectMismatch{Kind: "unresolved_watched_reference", Path: relative, EnclosingSymbol: enclosing, Callee: ambiguous}
			}
		} else {
			return nil
		}
		spec, watched := registry[callee]
		if !watched {
			return nil
		}
		if spec.Scope == "sensitive_files" && !workspaceEffectSensitiveFile(relative) {
			return nil
		}
		if signature != nil {
			canonicalSignature, ok := workspaceEffectNameFreeSignature(callee, signature)
			if !ok {
				return workspaceEffectAnalysisMismatch()
			}
			if existing, exists := result.Signatures[callee]; exists && existing != canonicalSignature {
				return workspaceEffectAnalysisMismatch()
			}
			result.Signatures[callee] = canonicalSignature
		}
		allowedReferences[identifier] = struct{}{}
		kind := invocationKinds[call]
		if kind == "" {
			kind = "call"
		}
		rawSites = append(rawSites, workspaceEffectRawSite{
			Path:            relative,
			EnclosingSymbol: enclosing,
			Callee:          callee,
			EvidenceLayer:   spec.EvidenceLayer,
			InvocationKind:  kind,
			CallShapeSHA256: workspaceEffectCallShape(callee, kind, len(call.Args), call.Ellipsis.IsValid(), typeArgumentCount),
			Position:        call.Pos(),
			SourceOffset:    sourceOffset,
			SourceEndOffset: sourceEndOffset,
		})
		return nil
	}

	for _, source := range workspaceEffectSortedRelativeFiles(loaded) {
		file, relative := source.File, source.Path
		literalCounters := make(map[string]int)
		initOrdinal := 0
		for _, declaration := range file.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				function, ok := loaded.info.Defs[value.Name].(*types.Func)
				if !ok {
					return nil, workspaceEffectAnalysisMismatch()
				}
				enclosing, _, ok := workspaceEffectCanonicalFunction(function, loaded.interfaceOwners)
				if !ok {
					return nil, workspaceEffectAnalysisMismatch()
				}
				if value.Recv == nil && value.Name.Name == "init" {
					initOrdinal++
					enclosing = loaded.listed.ImportPath + ".init@" + relative + "#" + strconv.Itoa(initOrdinal)
				}
				if value.Body != nil {
					var walkErr error
					workspaceEffectWalkCallable(value.Body, enclosing, literalCounters, func(node ast.Node, parent string) {
						if walkErr == nil {
							walkErr = recordNode(relative, node, parent)
						}
					})
					if walkErr != nil {
						return nil, walkErr
					}
				}
			case *ast.GenDecl:
				if value.Tok != token.VAR {
					continue
				}
				enclosing := loaded.listed.ImportPath + ".package_init@" + relative
				for _, raw := range value.Specs {
					valueSpec, ok := raw.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, expression := range valueSpec.Values {
						var walkErr error
						workspaceEffectWalkCallable(expression, enclosing, literalCounters, func(node ast.Node, parent string) {
							if walkErr == nil {
								walkErr = recordNode(relative, node, parent)
							}
						})
						if walkErr != nil {
							return nil, walkErr
						}
					}
				}
			}
		}
	}

	for _, source := range workspaceEffectSortedRelativeFiles(loaded) {
		var references []*ast.Ident
		ast.Inspect(source.File, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if ok && loaded.info.Uses[identifier] != nil {
				references = append(references, identifier)
			}
			return true
		})
		for _, identifier := range references {
			object := loaded.info.Uses[identifier]
			if _, allowed := allowedReferences[identifier]; allowed {
				continue
			}
			if alias, ok := object.(*types.Var); ok {
				if callee, watched := aliasObjects[alias]; watched {
					return nil, &workspaceEffectMismatch{Kind: "unresolved_watched_reference", Path: source.Path, EnclosingSymbol: referenceEnclosing[identifier], Callee: callee}
				}
				continue
			}
			function, ok := object.(*types.Func)
			if !ok {
				continue
			}
			candidateCallee, candidateBody, candidate := workspaceEffectInterfaceMethodCandidate(function, loaded.interfaceOwners, loaded.interfaceMethods)
			ambiguous := workspaceEffectAmbiguousInterfaceMethod(function, loaded.interfaceOwners, loaded.interfaceMethods, result.Signatures)
			if ambiguous != "" {
				enclosing := referenceEnclosing[identifier]
				if enclosing == "" {
					enclosing = loaded.listed.ImportPath + ".package_init@" + source.Path
				}
				return nil, &workspaceEffectMismatch{Kind: "unresolved_watched_reference", Path: source.Path, EnclosingSymbol: enclosing, Callee: ambiguous}
			}
			if candidate && result.Signatures[candidateCallee] == "" {
				enclosing := referenceEnclosing[identifier]
				if enclosing == "" {
					enclosing = loaded.listed.ImportPath + ".package_init@" + source.Path
				}
				result.PendingInterfaceReferences = append(result.PendingInterfaceReferences, workspaceEffectPendingInterfaceReference{
					Path:            source.Path,
					EnclosingSymbol: enclosing,
					Callee:          candidateCallee,
					SignatureBody:   candidateBody,
				})
			}
			callee, _, resolved := workspaceEffectCanonicalFunction(function, loaded.interfaceOwners)
			if !resolved {
				continue
			}
			spec, watched := registry[callee]
			if !watched || (spec.Scope == "sensitive_files" && !workspaceEffectSensitiveFile(source.Path)) {
				continue
			}
			enclosing := referenceEnclosing[identifier]
			if enclosing == "" {
				enclosing = loaded.listed.ImportPath + ".package_init@" + source.Path
			}
			return nil, &workspaceEffectMismatch{Kind: "unresolved_watched_reference", Path: source.Path, EnclosingSymbol: enclosing, Callee: callee}
		}
	}
	return rawSites, nil
}

func workspaceEffectInterfaceMethodCandidate(function *types.Func, owners map[*types.Func]string, interfaceMethods map[*types.Func]struct{}) (string, string, bool) {
	if _, isInterfaceMethod := interfaceMethods[function]; !isInterfaceMethod {
		return "", "", false
	}
	signature, ok := function.Type().(*types.Signature)
	if !ok {
		return "", "", false
	}
	candidateBody, ok := workspaceEffectNameFreeSignatureBody(signature, true)
	if !ok {
		return "", "", false
	}
	canonical, _, canonicalOK := workspaceEffectCanonicalFunction(function, owners)
	for _, symbol := range []string{
		workspaceEffectModulePath + "/internal/storage.(BackupStore).BackupAdd",
		workspaceEffectModulePath + "/internal/storage.(BackupStore).BackupDatabase",
		workspaceEffectModulePath + "/internal/storage.(BackupStore).BackupRemove",
		workspaceEffectModulePath + "/internal/storage.(BackupStore).BackupSync",
		workspaceEffectModulePath + "/internal/storage.(BackupStore).RestoreDatabase",
		workspaceEffectModulePath + "/internal/storage/domain.(BeadsDirFSUseCase).InitializeBeadsDir",
	} {
		method := symbol[strings.LastIndex(symbol, ".")+1:]
		if function.Name() != method {
			continue
		}
		if canonicalOK && canonical == symbol {
			return "", "", false
		}
		return symbol, candidateBody, true
	}
	return "", "", false
}

func workspaceEffectAmbiguousInterfaceMethod(function *types.Func, owners map[*types.Func]string, interfaceMethods map[*types.Func]struct{}, signatures map[string]string) string {
	symbol, candidateBody, candidate := workspaceEffectInterfaceMethodCandidate(function, owners, interfaceMethods)
	if !candidate {
		return ""
	}
	expected := signatures[symbol]
	if expected != "" && strings.TrimPrefix(expected, symbol+" ") == candidateBody {
		return symbol
	}
	return ""
}

func workspaceEffectValidatePendingInterfaceReferences(references []workspaceEffectPendingInterfaceReference, signatures map[string]string) error {
	ordered := append([]workspaceEffectPendingInterfaceReference(nil), references...)
	sort.Slice(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.EnclosingSymbol != right.EnclosingSymbol {
			return left.EnclosingSymbol < right.EnclosingSymbol
		}
		if left.Callee != right.Callee {
			return left.Callee < right.Callee
		}
		return left.SignatureBody < right.SignatureBody
	})
	for _, reference := range ordered {
		expected := signatures[reference.Callee]
		if expected == "" {
			continue
		}
		prefix := reference.Callee + " "
		if !strings.HasPrefix(expected, prefix) {
			return workspaceEffectAnalysisMismatch()
		}
		if strings.TrimPrefix(expected, prefix) == reference.SignatureBody {
			return &workspaceEffectMismatch{
				Kind:            "unresolved_watched_reference",
				Path:            reference.Path,
				EnclosingSymbol: reference.EnclosingSymbol,
				Callee:          reference.Callee,
			}
		}
	}
	return nil
}

func workspaceEffectNamedTypeIdentity(value types.Type) (string, string, bool) {
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	named, ok := value.(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return "", "", false
	}
	return named.Obj().Pkg().Path(), named.Obj().Name(), true
}

func workspaceEffectIsAdapterType(value types.Type) bool {
	packagePath, name, ok := workspaceEffectNamedTypeIdentity(value)
	return ok && packagePath == workspaceEffectModulePath+"/internal/storage/domain" && name == "BeadsDirFSAdapters"
}

func workspaceEffectAdapterField(pkg *types.Package) *types.Var {
	if pkg == nil || pkg.Path() != workspaceEffectModulePath+"/internal/storage/domain" {
		return nil
	}
	typeName, ok := pkg.Scope().Lookup("BeadsDirFSAdapters").(*types.TypeName)
	if !ok {
		return nil
	}
	named, ok := types.Unalias(typeName.Type()).(*types.Named)
	if !ok {
		return nil
	}
	structure, ok := named.Underlying().(*types.Struct)
	if !ok {
		return nil
	}
	for index := 0; index < structure.NumFields(); index++ {
		field := structure.Field(index)
		if field.Name() == "ApplyNoCOW" {
			return field
		}
	}
	return nil
}

func workspaceEffectParentMap(file *ast.File) map[ast.Node]ast.Node {
	parents := make(map[ast.Node]ast.Node)
	stack := make([]ast.Node, 0, 32)
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if len(stack) > 0 {
			parents[node] = stack[len(stack)-1]
		}
		stack = append(stack, node)
		return true
	})
	return parents
}

func workspaceEffectNearestIf(parents map[ast.Node]ast.Node, node ast.Node) *ast.IfStmt {
	for current := node; current != nil; current = parents[current] {
		if statement, ok := current.(*ast.IfStmt); ok {
			return statement
		}
	}
	return nil
}

func workspaceEffectNamedEnclosing(loaded *workspaceEffectLoadedPackage, parents map[ast.Node]ast.Node, node ast.Node) (string, bool) {
	for current := node; current != nil; current = parents[current] {
		switch callable := current.(type) {
		case *ast.FuncLit:
			return "", false
		case *ast.FuncDecl:
			object, ok := loaded.info.Defs[callable.Name].(*types.Func)
			if !ok {
				return "", false
			}
			symbol, _, ok := workspaceEffectCanonicalFunction(object, loaded.interfaceOwners)
			return symbol, ok
		}
	}
	return "", false
}

func workspaceEffectNodeWithin(root, node ast.Node) bool {
	return root != nil && node != nil && node.Pos() >= root.Pos() && node.End() <= root.End()
}

func workspaceEffectPositiveConjunct(root ast.Expr, target ast.Expr) bool {
	if root == target {
		return true
	}
	switch expression := root.(type) {
	case *ast.ParenExpr:
		return workspaceEffectPositiveConjunct(expression.X, target)
	case *ast.BinaryExpr:
		return expression.Op == token.LAND &&
			(workspaceEffectPositiveConjunct(expression.X, target) || workspaceEffectPositiveConjunct(expression.Y, target))
	default:
		return false
	}
}

func workspaceEffectSameObjectPath(left, right ast.Expr, info *types.Info) bool {
	for {
		parenthesized, ok := left.(*ast.ParenExpr)
		if !ok {
			break
		}
		left = parenthesized.X
	}
	for {
		parenthesized, ok := right.(*ast.ParenExpr)
		if !ok {
			break
		}
		right = parenthesized.X
	}
	switch leftValue := left.(type) {
	case *ast.Ident:
		rightValue, ok := right.(*ast.Ident)
		return ok && info.ObjectOf(leftValue) != nil && info.ObjectOf(leftValue) == info.ObjectOf(rightValue)
	case *ast.SelectorExpr:
		rightValue, ok := right.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		leftSelection := info.Selections[leftValue]
		rightSelection := info.Selections[rightValue]
		return leftSelection != nil && rightSelection != nil &&
			leftSelection.Obj() == rightSelection.Obj() &&
			workspaceEffectSameObjectPath(leftValue.X, rightValue.X, info)
	default:
		return false
	}
}

func workspaceEffectValidateNoCOWBinding(loadedPackages []*workspaceEffectLoadedPackage) (map[*workspaceEffectLoadedPackage]map[*ast.Ident]struct{}, error) {
	const domainPackage = "github.com/steveyegge/beads/internal/storage/domain"
	const concreteMethod = domainPackage + ".(*beadsDirFSUseCaseImpl).InitializeBeadsDir"
	const bindingFunction = "github.com/steveyegge/beads/cmd/bd.newFileSystemAdapters"
	const bindingPath = "cmd/bd/fs_adapters.go"
	const root = "github.com/steveyegge/beads/cmd/bd.applyNoCOW"

	allowed := make(map[*workspaceEffectLoadedPackage]map[*ast.Ident]struct{})
	typeDeclarations := 0
	fieldDeclarations := 0
	fieldReads := 0
	nilGuards := 0
	fieldCalls := 0
	adapterLiterals := 0
	bindings := 0
	invalid := false
	var nilGuardIf *ast.IfStmt
	var fieldCallIf *ast.IfStmt
	var nilGuardReceiver ast.Expr
	var fieldCallReceiver ast.Expr
	var nilGuardPackage *workspaceEffectLoadedPackage
	var fieldCallPackage *workspaceEffectLoadedPackage
	var domainField *types.Var

	for _, loaded := range loadedPackages {
		if loaded.listed.ImportPath != domainPackage {
			continue
		}
		for file, relative := range loaded.relativeFiles {
			for _, declaration := range file.Decls {
				general, ok := declaration.(*ast.GenDecl)
				if !ok || general.Tok != token.TYPE {
					continue
				}
				for _, raw := range general.Specs {
					typeSpec, ok := raw.(*ast.TypeSpec)
					if !ok || typeSpec.Name.Name != "BeadsDirFSAdapters" {
						continue
					}
					typeDeclarations++
					typeName, ok := loaded.info.Defs[typeSpec.Name].(*types.TypeName)
					if !ok {
						invalid = true
						continue
					}
					named, namedOK := types.Unalias(typeName.Type()).(*types.Named)
					if !namedOK || named.Obj() != typeName || named.TypeParams().Len() != 0 || relative != "internal/storage/domain/beads.go" {
						invalid = true
						continue
					}
					structure, ok := named.Underlying().(*types.Struct)
					if !ok {
						invalid = true
						continue
					}
					for index := 0; index < structure.NumFields(); index++ {
						field := structure.Field(index)
						if field.Name() != "ApplyNoCOW" {
							continue
						}
						fieldDeclarations++
						signature, ok := field.Type().(*types.Signature)
						canonical, valid := workspaceEffectNameFreeSignature("binding", signature)
						if !ok || !valid || canonical != "binding func(string)(error)" {
							invalid = true
							continue
						}
						domainField = field
					}
				}
			}
		}
	}

	for _, loaded := range loadedPackages {
		allowed[loaded] = make(map[*ast.Ident]struct{})
		for file, relative := range loaded.relativeFiles {
			parents := workspaceEffectParentMap(file)
			ast.Inspect(file, func(node ast.Node) bool {
				composite, ok := node.(*ast.CompositeLit)
				if !ok {
					return true
				}
				typed := loaded.info.TypeOf(composite)
				if typed == nil || !workspaceEffectIsAdapterType(typed) {
					return true
				}
				adapterLiterals++
				enclosing, validEnclosing := workspaceEffectNamedEnclosing(loaded, parents, composite)
				if relative != bindingPath || !validEnclosing || enclosing != bindingFunction {
					invalid = true
					return false
				}
				for _, element := range composite.Elts {
					keyed, ok := element.(*ast.KeyValueExpr)
					if !ok {
						invalid = true
						return false
					}
					key, ok := keyed.Key.(*ast.Ident)
					if !ok || key.Name != "ApplyNoCOW" {
						continue
					}
					field, ok := loaded.info.Uses[key].(*types.Var)
					if !ok || !field.IsField() || field.Pkg() == nil || field.Pkg().Path() != domainPackage || field.Name() != "ApplyNoCOW" {
						invalid = true
						return false
					}
					if field != workspaceEffectAdapterField(field.Pkg()) {
						invalid = true
						return false
					}
					functionExpression, ok := keyed.Value.(*ast.Ident)
					if !ok {
						invalid = true
						return false
					}
					function, ok := loaded.info.Uses[functionExpression].(*types.Func)
					if !ok {
						invalid = true
						return false
					}
					functionSymbol, functionSignature, ok := workspaceEffectCanonicalFunction(function, loaded.interfaceOwners)
					canonical, valid := workspaceEffectNameFreeSignature("binding", functionSignature)
					if !ok || !valid || functionSymbol != root || canonical != "binding func(string)(error)" || !types.Identical(field.Type(), function.Type()) {
						invalid = true
						return false
					}
					bindings++
					allowed[loaded][functionExpression] = struct{}{}
				}
				return true
			})

			ast.Inspect(file, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "ApplyNoCOW" {
					return true
				}
				selection := loaded.info.Selections[selector]
				if selection == nil {
					return true
				}
				field, ok := selection.Obj().(*types.Var)
				if !ok || !field.IsField() || field.Pkg() == nil || field.Pkg().Path() != domainPackage || field.Name() != "ApplyNoCOW" {
					return true
				}
				if field != workspaceEffectAdapterField(field.Pkg()) {
					return true
				}
				if loaded.listed.ImportPath != domainPackage || field != domainField {
					invalid = true
					return false
				}
				signature, ok := field.Type().(*types.Signature)
				canonical, valid := workspaceEffectNameFreeSignature("binding", signature)
				if !ok || !valid || canonical != "binding func(string)(error)" {
					invalid = true
					return false
				}
				fieldReads++
				enclosing, validEnclosing := workspaceEffectNamedEnclosing(loaded, parents, selector)
				if !validEnclosing || enclosing != concreteMethod {
					invalid = true
					return false
				}
				switch parent := parents[selector].(type) {
				case *ast.BinaryExpr:
					nilIdentifier, isNil := parent.Y.(*ast.Ident)
					guard := workspaceEffectNearestIf(parents, parent)
					if parent.X != selector || parent.Op != token.NEQ || !isNil || nilIdentifier.Name != "nil" ||
						loaded.info.Uses[nilIdentifier] != types.Universe.Lookup("nil") || guard == nil ||
						!workspaceEffectNodeWithin(guard.Cond, parent) || !workspaceEffectPositiveConjunct(guard.Cond, parent) {
						invalid = true
						return false
					}
					nilGuards++
					nilGuardIf = guard
					nilGuardReceiver = selector.X
					nilGuardPackage = loaded
				case *ast.CallExpr:
					if parent.Fun != selector || len(parent.Args) != 1 || parent.Ellipsis.IsValid() {
						invalid = true
						return false
					}
					if _, deferred := parents[parent].(*ast.DeferStmt); deferred {
						invalid = true
						return false
					}
					if _, concurrent := parents[parent].(*ast.GoStmt); concurrent {
						invalid = true
						return false
					}
					guard := workspaceEffectNearestIf(parents, parent)
					if guard == nil || !workspaceEffectNodeWithin(guard.Body, parent) {
						invalid = true
						return false
					}
					fieldCalls++
					fieldCallIf = guard
					fieldCallReceiver = selector.X
					fieldCallPackage = loaded
				default:
					invalid = true
					return false
				}
				return true
			})
		}
	}
	if invalid || typeDeclarations != 1 || fieldDeclarations != 1 || domainField == nil || adapterLiterals != 1 || bindings != 1 || fieldReads != 2 || nilGuards != 1 || fieldCalls != 1 || nilGuardIf == nil || fieldCallIf != nilGuardIf || nilGuardPackage == nil || nilGuardPackage != fieldCallPackage || !workspaceEffectSameObjectPath(nilGuardReceiver, fieldCallReceiver, nilGuardPackage.info) {
		return nil, &workspaceEffectMismatch{Kind: "unresolved_watched_reference", Path: bindingPath, EnclosingSymbol: bindingFunction, Callee: root}
	}
	return allowed, nil
}

func workspaceEffectRawSiteKey(site workspaceEffectRawSite) string {
	return site.Path + "\x00" + site.EnclosingSymbol + "\x00" + site.Callee + "\x00" + site.InvocationKind + "\x00" + site.CallShapeSHA256
}

func workspaceEffectCallAnchorKey(file string, sourceOffset, sourceEndOffset int) string {
	return file + "\x00" + strconv.Itoa(sourceOffset) + "\x00" + strconv.Itoa(sourceEndOffset)
}

func workspaceEffectFinalizeRawSites(raw []workspaceEffectRawSite, profile string) ([]workspaceEffectDetectedSite, error) {
	sort.Slice(raw, func(i, j int) bool {
		left := workspaceEffectRawSiteKey(raw[i])
		right := workspaceEffectRawSiteKey(raw[j])
		if left != right {
			return left < right
		}
		return raw[i].Position < raw[j].Position
	})
	result := make([]workspaceEffectDetectedSite, 0, len(raw))
	previousKey := ""
	ordinal := 0
	for _, item := range raw {
		key := workspaceEffectRawSiteKey(item)
		if key != previousKey {
			previousKey = key
			ordinal = 0
		}
		ordinal++
		site := workspaceEffectDetectedSite{
			Path:            item.Path,
			EnclosingSymbol: item.EnclosingSymbol,
			Callee:          item.Callee,
			EvidenceLayer:   item.EvidenceLayer,
			InvocationKind:  item.InvocationKind,
			CallShapeSHA256: item.CallShapeSHA256,
			Ordinal:         ordinal,
			BuildProfiles:   []string{profile},
			SourceOffset:    item.SourceOffset,
			SourceEndOffset: item.SourceEndOffset,
		}
		site.ID = workspaceEffectSiteID(site)
		result = append(result, site)
	}
	seen := make(map[string]struct{}, len(result))
	for _, site := range result {
		if _, exists := seen[site.ID]; exists {
			return nil, workspaceEffectAnalysisMismatch()
		}
		seen[site.ID] = struct{}{}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func workspaceEffectIndexStandardPackages(listed []workspaceEffectGoListPackage, registry map[string]workspaceEffectRegistrySpec, result *workspaceEffectProfileResult) error {
	exports := make(map[string]string, len(listed))
	for _, item := range listed {
		if item.Export != "" {
			exports[item.ImportPath] = item.Export
		}
	}
	fset := token.NewFileSet()
	lookup := func(importPath string) (io.ReadCloser, error) {
		exportFile, ok := exports[importPath]
		if !ok || exportFile == "" {
			return nil, os.ErrNotExist
		}
		return os.Open(exportFile)
	}
	imports := importer.ForCompiler(fset, "gc", lookup)
	for _, importPath := range []string{"database/sql", "os", "os/exec", "syscall"} {
		pkg, err := imports.Import(importPath)
		if err != nil || pkg == nil {
			return workspaceEffectAnalysisMismatch()
		}
		loaded := &workspaceEffectLoadedPackage{typesPackage: pkg, interfaceOwners: workspaceEffectInterfaceOwners(pkg)}
		if err := workspaceEffectIndexPackageDeclarations(loaded, registry, result); err != nil {
			return err
		}
	}
	return nil
}

func workspaceEffectAnalyzeProfile(repoRoot, privateRoot, tool, moduleCache string, profile workspaceEffectBuildProfile) (*workspaceEffectProfileResult, error) {
	listed, err := workspaceEffectGoList(repoRoot, privateRoot, tool, moduleCache, profile)
	if err != nil {
		return nil, err
	}
	loadedPackages, err := workspaceEffectLoadPackages(repoRoot, listed, profile)
	if err != nil {
		return nil, err
	}
	registry := workspaceEffectRegistryMap()
	result := &workspaceEffectProfileResult{
		SelectedFiles: make(map[string]struct{}),
		Signatures:    make(map[string]string),
		Declarations:  make(map[string]struct{}),
		CallAnchors:   make(map[string]workspaceEffectCallAnchor),
	}
	if err := workspaceEffectIndexStandardPackages(listed, registry, result); err != nil {
		return nil, err
	}
	for _, loaded := range loadedPackages {
		for _, relative := range loaded.relativeFiles {
			result.SelectedFiles[relative] = struct{}{}
		}
		if err := workspaceEffectIndexPackageDeclarations(loaded, registry, result); err != nil {
			return nil, err
		}
	}
	bindingAllowed, err := workspaceEffectValidateNoCOWBinding(loadedPackages)
	if err != nil {
		return nil, err
	}
	var rawSites []workspaceEffectRawSite
	for _, loaded := range loadedPackages {
		packageSites, err := workspaceEffectPackageRawSites(loaded, registry, result, bindingAllowed[loaded])
		if err != nil {
			return nil, err
		}
		rawSites = append(rawSites, packageSites...)
	}
	result.Sites, err = workspaceEffectFinalizeRawSites(rawSites, profile.Name)
	if err != nil {
		return nil, err
	}
	for _, site := range result.Sites {
		key := workspaceEffectCallAnchorKey(site.Path, site.SourceOffset, site.SourceEndOffset)
		anchor, exists := result.CallAnchors[key]
		if !exists || anchor.Watched {
			return nil, workspaceEffectAnalysisMismatch()
		}
		anchor.Watched = true
		anchor.Site = site
		result.CallAnchors[key] = anchor
	}
	return result, nil
}

func workspaceEffectProductionFiles(repoRoot string) ([]string, error) {
	files := make(map[string]struct{})
	for _, root := range workspaceEffectScanRoots() {
		if root.Kind == "file" {
			info, err := os.Lstat(filepath.Join(repoRoot, filepath.FromSlash(root.Path)))
			if err != nil || !info.Mode().IsRegular() || !workspaceEffectProductionFile(root.Path) {
				return nil, workspaceEffectAnalysisMismatch()
			}
			files[root.Path] = struct{}{}
			continue
		}
		rootPath := filepath.Join(repoRoot, filepath.FromSlash(root.Path))
		rootInfo, err := os.Lstat(rootPath)
		if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
			return nil, workspaceEffectAnalysisMismatch()
		}
		err = filepath.WalkDir(rootPath, func(filePath string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return workspaceEffectAnalysisMismatch()
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return workspaceEffectAnalysisMismatch()
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" || entry.Name() == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			relative, ok, relativeErr := workspaceEffectRepoRelative(repoRoot, filePath)
			if relativeErr != nil {
				return relativeErr
			}
			if !ok || !workspaceEffectFileInScanRoots(relative) {
				return nil
			}
			files[relative] = struct{}{}
			return nil
		})
		if err != nil {
			return nil, workspaceEffectAnalysisMismatch()
		}
	}
	result := make([]string, 0, len(files))
	for file := range files {
		result = append(result, file)
	}
	sort.Strings(result)
	return result, nil
}

func workspaceEffectValidateAliasUniverse(repoRoot string, productionFiles []string) error {
	aliases := workspaceEffectAliases()
	byName := make(map[string]workspaceEffectAliasSpec, len(aliases))
	for _, alias := range aliases {
		byName[alias.Name] = alias
	}
	counts := make(map[string]int)
	files := append([]string(nil), productionFiles...)
	sort.Strings(files)
	for _, relative := range files {
		if !strings.HasPrefix(relative, "cmd/bd/doctor/fix/") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(repoRoot, filepath.FromSlash(relative)), nil, 0)
		if err != nil {
			return workspaceEffectAnalysisMismatch()
		}
		for _, declaration := range file.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				if alias, watched := byName[value.Name.Name]; watched {
					return workspaceEffectAliasMismatch(alias)
				}
			case *ast.GenDecl:
				if value.Tok != token.VAR {
					continue
				}
				for _, raw := range value.Specs {
					valueSpec, ok := raw.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, name := range valueSpec.Names {
						if alias, watched := byName[name.Name]; watched {
							counts[name.Name]++
							if relative != alias.File {
								return workspaceEffectAliasMismatch(alias)
							}
						}
					}
				}
			}
		}
	}
	for _, alias := range aliases {
		if counts[alias.Name] != 1 {
			return workspaceEffectAliasMismatch(alias)
		}
	}
	return nil
}

func workspaceEffectImportPathForFile(file string) string {
	directory := path.Dir(file)
	if directory == "." {
		return workspaceEffectModulePath
	}
	return workspaceEffectModulePath + "/" + directory
}

func workspaceEffectValidateUnselectedFiles(repoRoot string, files []string, declarations map[string]struct{}) error {
	aliasNames := make(map[string]string)
	for _, alias := range workspaceEffectAliases() {
		aliasNames[alias.Name] = alias.Root
	}
	deferredSymbols := make(map[string]struct{})
	for _, symbol := range workspaceEffectDeferredSurfaces()[0].Symbols {
		deferredSymbols[symbol] = struct{}{}
	}
	for _, relative := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(repoRoot, filepath.FromSlash(relative)), nil, 0)
		if err != nil {
			return workspaceEffectAnalysisMismatch()
		}
		packagePath := workspaceEffectImportPathForFile(relative)
		parents := workspaceEffectParentMap(file)
		topLevelObjects := make(map[*ast.Object]struct{})
		if file.Scope != nil {
			for _, object := range file.Scope.Objects {
				topLevelObjects[object] = struct{}{}
			}
		}
		isBarePackageReference := func(identifier *ast.Ident) bool {
			if selector, ok := parents[identifier].(*ast.SelectorExpr); ok && selector.Sel == identifier {
				return false
			}
			if identifier.Obj == nil {
				return true
			}
			_, topLevel := topLevelObjects[identifier.Obj]
			return topLevel
		}
		indexName := func(name string) error {
			symbol := packagePath + "." + name
			declarations[symbol] = struct{}{}
			if _, deferred := deferredSymbols[symbol]; deferred {
				return workspaceEffectManifestMismatch("deferred_surface_present")
			}
			return nil
		}
		for _, declaration := range file.Decls {
			switch typed := declaration.(type) {
			case *ast.FuncDecl:
				if typed.Recv == nil {
					if err := indexName(typed.Name.Name); err != nil {
						return err
					}
				}
			case *ast.GenDecl:
				for _, raw := range typed.Specs {
					switch spec := raw.(type) {
					case *ast.TypeSpec:
						if err := indexName(spec.Name.Name); err != nil {
							return err
						}
					case *ast.ValueSpec:
						for _, name := range spec.Names {
							if err := indexName(name.Name); err != nil {
								return err
							}
						}
					}
				}
			}
		}
		var violation error
		ast.Inspect(file, func(node ast.Node) bool {
			if violation != nil {
				return false
			}
			if identifier, ok := node.(*ast.Ident); ok {
				callee := ""
				if packagePath == workspaceEffectModulePath+"/cmd/bd/doctor/fix" && isBarePackageReference(identifier) {
					callee = aliasNames[identifier.Name]
				}
				if packagePath == workspaceEffectModulePath+"/cmd/bd" && identifier.Name == "applyNoCOW" && isBarePackageReference(identifier) {
					callee = workspaceEffectModulePath + "/cmd/bd.applyNoCOW"
				}
				if identifier.Name == "ApplyNoCOW" || identifier.Name == "BeadsDirFSAdapters" {
					callee = workspaceEffectModulePath + "/cmd/bd.applyNoCOW"
				}
				if callee != "" {
					violation = &workspaceEffectMismatch{
						Kind:            "unresolved_watched_reference",
						Path:            relative,
						EnclosingSymbol: packagePath + ".package_init@" + relative,
						Callee:          callee,
					}
					return false
				}
			}
			return true
		})
		if violation != nil {
			return violation
		}
	}
	return nil
}

func workspaceEffectSameSite(left, right workspaceEffectDetectedSite) bool {
	return left.ID == right.ID &&
		left.Path == right.Path &&
		left.EnclosingSymbol == right.EnclosingSymbol &&
		left.Callee == right.Callee &&
		left.EvidenceLayer == right.EvidenceLayer &&
		left.InvocationKind == right.InvocationKind &&
		left.CallShapeSHA256 == right.CallShapeSHA256 &&
		left.Ordinal == right.Ordinal
}

func workspaceEffectAnalyzeRepository() (repository *workspaceEffectRepositoryResult, resultErr error) {
	repoRoot, err := workspaceEffectRepositoryRoot()
	if err != nil {
		return nil, err
	}
	if err := workspaceEffectCheckDeferredPaths(repoRoot); err != nil {
		return nil, err
	}
	tempRoot, err := workspaceEffectResolvedRoot(os.TempDir(), false)
	if err != nil || workspaceEffectRequireExternal(repoRoot, tempRoot) != nil {
		return nil, workspaceEffectAnalysisMismatch()
	}
	privateRoot, err := os.MkdirTemp(tempRoot, "backend-census.")
	if err != nil {
		return nil, workspaceEffectAnalysisMismatch()
	}
	defer func() {
		if cleanupErr := os.RemoveAll(privateRoot); cleanupErr != nil {
			repository = nil
			resultErr = workspaceEffectAnalysisMismatch()
		}
	}()
	tool, moduleCache, err := workspaceEffectGoBootstrap(repoRoot, privateRoot)
	if err != nil {
		return nil, err
	}
	productionFiles, err := workspaceEffectProductionFiles(repoRoot)
	if err != nil {
		return nil, err
	}
	if err := workspaceEffectValidateAliasUniverse(repoRoot, productionFiles); err != nil {
		return nil, err
	}

	sites := make(map[string]workspaceEffectDetectedSite)
	selectedFiles := make(map[string]struct{})
	signatures := make(map[string]string)
	declarations := make(map[string]struct{})
	callAnchors := make(map[string]workspaceEffectCallAnchor)
	var pendingInterfaceReferences []workspaceEffectPendingInterfaceReference
	for _, profile := range workspaceEffectBuildProfiles() {
		profileRoot := filepath.Join(privateRoot, profile.Name)
		for _, directory := range []string{
			filepath.Join(profileRoot, "home"),
			filepath.Join(profileRoot, "tmp"),
			filepath.Join(profileRoot, "gocache"),
			filepath.Join(profileRoot, "gopath"),
		} {
			if err := os.MkdirAll(directory, 0o700); err != nil {
				return nil, workspaceEffectAnalysisMismatch()
			}
		}
		profileResult, err := workspaceEffectAnalyzeProfile(repoRoot, profileRoot, tool, moduleCache, profile)
		if err != nil {
			return nil, err
		}
		for file := range profileResult.SelectedFiles {
			selectedFiles[file] = struct{}{}
		}
		for declaration := range profileResult.Declarations {
			declarations[declaration] = struct{}{}
		}
		pendingInterfaceReferences = append(pendingInterfaceReferences, profileResult.PendingInterfaceReferences...)
		if err := workspaceEffectMergeCallAnchors(callAnchors, profileResult.CallAnchors); err != nil {
			return nil, err
		}
		for symbol, signature := range profileResult.Signatures {
			if existing, exists := signatures[symbol]; exists && existing != signature {
				return nil, workspaceEffectAnalysisMismatch()
			}
			signatures[symbol] = signature
		}
		for _, site := range profileResult.Sites {
			existing, exists := sites[site.ID]
			if !exists {
				sites[site.ID] = site
				continue
			}
			if !workspaceEffectSameSite(existing, site) {
				return nil, workspaceEffectAnalysisMismatch()
			}
			existing.BuildProfiles = append(existing.BuildProfiles, profile.Name)
			sort.Strings(existing.BuildProfiles)
			sites[site.ID] = existing
		}
	}
	if err := workspaceEffectValidatePendingInterfaceReferences(pendingInterfaceReferences, signatures); err != nil {
		return nil, err
	}

	registry := workspaceEffectRegistry()
	watchedSinks := make([]workspaceEffectWatchedSink, 0, len(registry))
	for _, spec := range registry {
		signature := signatures[spec.Symbol]
		if signature == "" {
			return nil, workspaceEffectAnalysisMismatch()
		}
		watchedSinks = append(watchedSinks, workspaceEffectWatchedSink{
			Symbol:        spec.Symbol,
			Signature:     signature,
			EvidenceLayer: spec.EvidenceLayer,
			Scope:         spec.Scope,
		})
	}

	unselectedFiles := make([]string, 0)
	for _, file := range productionFiles {
		if _, selected := selectedFiles[file]; !selected {
			unselectedFiles = append(unselectedFiles, file)
		}
	}
	if err := workspaceEffectValidateUnselectedFiles(repoRoot, unselectedFiles, declarations); err != nil {
		return nil, err
	}
	if err := workspaceEffectCheckDeferredSymbols(declarations); err != nil {
		return nil, err
	}

	detectedSites := make([]workspaceEffectDetectedSite, 0, len(sites))
	for _, site := range sites {
		sort.Strings(site.BuildProfiles)
		detectedSites = append(detectedSites, site)
	}
	sort.Slice(detectedSites, func(i, j int) bool { return detectedSites[i].ID < detectedSites[j].ID })
	return &workspaceEffectRepositoryResult{
		Sites:           detectedSites,
		UnselectedFiles: unselectedFiles,
		WatchedSinks:    watchedSinks,
		Declarations:    declarations,
	}, nil
}

func workspaceEffectRejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var parseValue func() error
	parseValue = func() error {
		tokenValue, err := decoder.Token()
		if err != nil {
			return workspaceEffectManifestMismatch("unknown_schema")
		}
		delimiter, ok := tokenValue.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			keys := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return workspaceEffectManifestMismatch("unknown_schema")
				}
				key, ok := keyToken.(string)
				if !ok {
					return workspaceEffectManifestMismatch("unknown_schema")
				}
				if _, duplicate := keys[key]; duplicate {
					return workspaceEffectManifestMismatch("duplicate_entry")
				}
				keys[key] = struct{}{}
				if err := parseValue(); err != nil {
					return err
				}
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim('}') {
				return workspaceEffectManifestMismatch("unknown_schema")
			}
		case '[':
			for decoder.More() {
				if err := parseValue(); err != nil {
					return err
				}
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim(']') {
				return workspaceEffectManifestMismatch("unknown_schema")
			}
		default:
			return workspaceEffectManifestMismatch("unknown_schema")
		}
		return nil
	}
	if err := parseValue(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return workspaceEffectManifestMismatch("unknown_schema")
	}
	return nil
}

func workspaceEffectStrictDecode(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return workspaceEffectManifestMismatch("unknown_schema")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return workspaceEffectManifestMismatch("unknown_schema")
	}
	return nil
}

func workspaceEffectLoadManifest(data []byte) (*workspaceEffectManifest, error) {
	if err := workspaceEffectRejectDuplicateKeys(data); err != nil {
		return nil, err
	}
	var manifest workspaceEffectManifest
	if err := workspaceEffectStrictDecode(data, &manifest); err != nil {
		return nil, err
	}
	if err := workspaceEffectValidateManifest(&manifest); err != nil {
		return nil, err
	}
	canonical, err := json.MarshalIndent(&manifest, "", "  ")
	if err != nil {
		return nil, workspaceEffectManifestMismatch("unknown_schema")
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(data, canonical) {
		return nil, workspaceEffectManifestMismatch("unknown_schema")
	}
	return &manifest, nil
}

func workspaceEffectValidHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func workspaceEffectUnsafeRune(character rune) bool {
	if character <= 0x1f || (character >= 0x7f && character <= 0x9f) {
		return true
	}
	switch character {
	case '\u061c', '\u200e', '\u200f', '\u2028', '\u2029', '\u202a', '\u202b', '\u202c', '\u202d', '\u202e', '\u2066', '\u2067', '\u2068', '\u2069':
		return true
	}
	return false
}

func workspaceEffectSafeIdentity(value string) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if workspaceEffectUnsafeRune(character) {
			return false
		}
	}
	return true
}

func workspaceEffectValidPath(value string, allowPattern bool) bool {
	if !workspaceEffectSafeIdentity(value) || strings.Contains(value, "\\") || path.IsAbs(value) || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return false
	}
	components := strings.Split(value, "/")
	for _, component := range components {
		if component == "" || component == "." || component == ".." || strings.Contains(component, ":") {
			return false
		}
	}
	if path.Clean(value) != value {
		return false
	}
	hasPattern := strings.ContainsAny(value, "*?[")
	if !allowPattern && hasPattern {
		return false
	}
	if allowPattern && hasPattern {
		found := false
		for _, expected := range workspaceEffectSensitivePatterns() {
			if value == expected {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func workspaceEffectPathUnderScanRoot(value string) bool {
	if !workspaceEffectValidPath(value, false) {
		return false
	}
	for _, root := range workspaceEffectScanRoots() {
		if root.Kind == "file" && value == root.Path {
			return true
		}
		if root.Kind == "tree" && strings.HasPrefix(value, root.Path+"/") {
			return true
		}
	}
	return false
}

func workspaceEffectSortedUnique(values []string) bool {
	if values == nil {
		return false
	}
	for index, value := range values {
		if value == "" || (index > 0 && values[index-1] >= value) {
			return false
		}
	}
	return true
}

func workspaceEffectProfilesValid(values []string, known map[string]struct{}) bool {
	if len(values) == 0 || !workspaceEffectSortedUnique(values) {
		return false
	}
	for _, value := range values {
		if _, exists := known[value]; !exists {
			return false
		}
	}
	return true
}

func workspaceEffectValidInvocation(value string) bool {
	return value == "call" || value == "defer" || value == "go"
}

func workspaceEffectValidLayer(value string) bool {
	return value == "leaf" || value == "semantic_boundary"
}

func workspaceEffectValidDisposition(value string) bool {
	return value == "shared_participation_required" || value == "pre_effect_refusal_required"
}

func workspaceEffectClassificationSHA256(manifest *workspaceEffectManifest) string {
	if manifest == nil {
		return ""
	}
	records := make([]string, 0, len(manifest.Sites)+len(manifest.Exclusions))
	for _, site := range manifest.Sites {
		records = append(records, "site\x00"+site.ID+"\x00"+site.Family+"\x00"+site.FutureDisposition+"\x00")
	}
	for _, exclusion := range manifest.Exclusions {
		switch exclusion.Kind {
		case "observed_site":
			records = append(records, "observed_exclusion\x00"+exclusion.ID+"\x00"+exclusion.Reason+"\x00")
		case "build_unselected_file":
			records = append(records, "build_unselected_file\x00"+exclusion.ID+"\x00"+exclusion.Path+"\x00"+exclusion.Reason+"\x00")
		}
	}
	sort.Strings(records)
	digest := sha256.New()
	for _, record := range records {
		_, _ = io.WriteString(digest, record)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func workspaceEffectSignatureSHA256(sinks []workspaceEffectWatchedSink) string {
	digest := sha256.New()
	for _, sink := range sinks {
		_, _ = io.WriteString(digest, sink.Symbol)
		_, _ = io.WriteString(digest, "\x00")
		_, _ = io.WriteString(digest, sink.Signature)
		_, _ = io.WriteString(digest, "\x00")
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func workspaceEffectValidateManifest(manifest *workspaceEffectManifest) error {
	if manifest == nil || manifest.Format != workspaceEffectCensusFormat || manifest.SourceBaseline != workspaceEffectSourceBase || manifest.RuntimeEnforced {
		return workspaceEffectManifestMismatch("unknown_schema")
	}
	if !reflect.DeepEqual(manifest.ScanRoots, workspaceEffectScanRoots()) ||
		!reflect.DeepEqual(manifest.BuildProfiles, workspaceEffectBuildProfiles()) ||
		!reflect.DeepEqual(manifest.SensitiveFilePatterns, workspaceEffectSensitivePatterns()) ||
		!reflect.DeepEqual(manifest.DeferredSurfaces, workspaceEffectDeferredSurfaces()) {
		return workspaceEffectManifestMismatch("unknown_schema")
	}
	if manifest.WatchedSinks == nil || manifest.Families == nil || manifest.Sites == nil || manifest.Exclusions == nil {
		return workspaceEffectManifestMismatch("unknown_schema")
	}
	for _, root := range manifest.ScanRoots {
		if !workspaceEffectValidPath(root.Path, false) || (root.Kind != "file" && root.Kind != "tree") {
			return workspaceEffectManifestMismatch("unknown_schema")
		}
	}
	for _, pattern := range manifest.SensitiveFilePatterns {
		if !workspaceEffectValidPath(pattern, true) || !workspaceEffectPathPatternUnderScanRoot(pattern) {
			return workspaceEffectManifestMismatch("unknown_schema")
		}
	}

	registry := workspaceEffectRegistry()
	if len(manifest.WatchedSinks) != len(registry) {
		return workspaceEffectManifestMismatch("unknown_schema")
	}
	sinkBySymbol := make(map[string]workspaceEffectWatchedSink, len(manifest.WatchedSinks))
	for index, sink := range manifest.WatchedSinks {
		expected := registry[index]
		if sink.Symbol != expected.Symbol || sink.EvidenceLayer != expected.EvidenceLayer || sink.Scope != expected.Scope || !workspaceEffectValidLayer(sink.EvidenceLayer) {
			return workspaceEffectManifestMismatch("unknown_schema")
		}
		if sink.Signature == "" || !workspaceEffectSafeIdentity(sink.Signature) || !strings.HasPrefix(sink.Signature, sink.Symbol+" func(") || !strings.HasSuffix(sink.Signature, ")") {
			return workspaceEffectManifestMismatch("unknown_schema")
		}
		if _, duplicate := sinkBySymbol[sink.Symbol]; duplicate {
			return workspaceEffectManifestMismatch("duplicate_entry")
		}
		sinkBySymbol[sink.Symbol] = sink
	}
	if workspaceEffectSignatureSHA256(manifest.WatchedSinks) != workspaceEffectSignatureBase {
		return workspaceEffectManifestMismatch("stale_entry")
	}

	expectedFamilies := workspaceEffectFamilies()
	if len(manifest.Families) != len(expectedFamilies) {
		return workspaceEffectManifestMismatch("unknown_family")
	}
	familyByName := make(map[string]workspaceEffectFamily, len(manifest.Families))
	for index, family := range manifest.Families {
		expected := expectedFamilies[index]
		if family.Name != expected.Name {
			return workspaceEffectManifestMismatch("unknown_family")
		}
		if family.FutureDisposition != expected.FutureDisposition || !workspaceEffectValidDisposition(family.FutureDisposition) {
			return workspaceEffectManifestMismatch("unknown_disposition")
		}
		if family.SiteCount < 0 || (family.ObservationState != "sites_observed" && family.ObservationState != "no_in_process_site_observed") {
			return workspaceEffectManifestMismatch("unknown_schema")
		}
		familyByName[family.Name] = family
	}
	profileNames := make(map[string]struct{}, len(manifest.BuildProfiles))
	for _, profile := range manifest.BuildProfiles {
		profileNames[profile.Name] = struct{}{}
	}

	classifiedIDs := make(map[string]struct{}, len(manifest.Sites)+len(manifest.Exclusions))
	familyCounts := make(map[string]int)
	previousSiteID := ""
	for _, site := range manifest.Sites {
		if site.ID <= previousSiteID || !workspaceEffectValidateSite(site, sinkBySymbol, familyByName, profileNames) {
			return workspaceEffectManifestMismatch("unknown_schema")
		}
		previousSiteID = site.ID
		if _, duplicate := classifiedIDs[site.ID]; duplicate {
			return workspaceEffectManifestMismatch("duplicate_entry")
		}
		classifiedIDs[site.ID] = struct{}{}
		familyCounts[site.Family]++
	}
	previousExclusionKey := ""
	for _, exclusion := range manifest.Exclusions {
		key := exclusion.Kind + "\x00" + exclusion.Path + "\x00" + exclusion.ID
		if key <= previousExclusionKey {
			return workspaceEffectManifestMismatch("unknown_schema")
		}
		previousExclusionKey = key
		if err := workspaceEffectValidateExclusion(exclusion, sinkBySymbol, profileNames); err != nil {
			return err
		}
		if _, duplicate := classifiedIDs[exclusion.ID]; duplicate {
			return workspaceEffectManifestMismatch("duplicate_entry")
		}
		classifiedIDs[exclusion.ID] = struct{}{}
	}
	for _, family := range manifest.Families {
		count := familyCounts[family.Name]
		if family.SiteCount != count {
			return workspaceEffectManifestMismatch("unknown_schema")
		}
		if (count == 0 && family.ObservationState != "no_in_process_site_observed") || (count > 0 && family.ObservationState != "sites_observed") {
			return workspaceEffectManifestMismatch("unknown_schema")
		}
	}
	if workspaceEffectClassificationSHA256(manifest) != workspaceEffectClassificationBase {
		return workspaceEffectManifestMismatch("stale_entry")
	}
	return nil
}

func workspaceEffectPathPatternUnderScanRoot(pattern string) bool {
	for _, root := range workspaceEffectScanRoots() {
		if root.Kind == "file" && pattern == root.Path {
			return true
		}
		if root.Kind == "tree" && strings.HasPrefix(pattern, root.Path+"/") {
			return true
		}
	}
	return false
}

func workspaceEffectValidEnclosing(value string) bool {
	moduleScoped := strings.HasPrefix(value, workspaceEffectModulePath+".") || strings.HasPrefix(value, workspaceEffectModulePath+"/")
	return workspaceEffectSafeIdentity(value) && moduleScoped && !strings.ContainsAny(value, " \\\t\r\n")
}

func workspaceEffectValidateSite(site workspaceEffectSite, sinks map[string]workspaceEffectWatchedSink, families map[string]workspaceEffectFamily, profiles map[string]struct{}) bool {
	if !workspaceEffectValidHash(site.ID) || !workspaceEffectPathUnderScanRoot(site.Path) || !workspaceEffectValidEnclosing(site.EnclosingSymbol) ||
		!workspaceEffectValidInvocation(site.InvocationKind) || !workspaceEffectValidHash(site.CallShapeSHA256) || site.Ordinal <= 0 ||
		!workspaceEffectProfilesValid(site.BuildProfiles, profiles) {
		return false
	}
	sink, exists := sinks[site.Callee]
	if !exists || site.EvidenceLayer != sink.EvidenceLayer {
		return false
	}
	family, exists := families[site.Family]
	if !exists || site.FutureDisposition != family.FutureDisposition {
		return false
	}
	detected := workspaceEffectDetectedSite{
		Path:            site.Path,
		EnclosingSymbol: site.EnclosingSymbol,
		Callee:          site.Callee,
		EvidenceLayer:   site.EvidenceLayer,
		InvocationKind:  site.InvocationKind,
		CallShapeSHA256: site.CallShapeSHA256,
		Ordinal:         site.Ordinal,
	}
	return workspaceEffectSiteID(detected) == site.ID
}

func workspaceEffectValidateExclusion(exclusion workspaceEffectExclusion, sinks map[string]workspaceEffectWatchedSink, profiles map[string]struct{}) error {
	switch exclusion.Kind {
	case "observed_site":
		reasons := map[string]struct{}{
			"diagnostic_artifact":         {},
			"diagnostic_only":             {},
			"git_integration_artifact":    {},
			"non_workspace_artifact":      {},
			"temporary_candidate_cleanup": {},
		}
		if _, valid := reasons[exclusion.Reason]; !valid {
			return workspaceEffectManifestMismatch("unknown_schema")
		}
		if !workspaceEffectValidHash(exclusion.ID) || !workspaceEffectPathUnderScanRoot(exclusion.Path) ||
			!workspaceEffectValidEnclosing(exclusion.EnclosingSymbol) || !workspaceEffectValidInvocation(exclusion.InvocationKind) ||
			!workspaceEffectValidHash(exclusion.CallShapeSHA256) || exclusion.Ordinal <= 0 ||
			!workspaceEffectProfilesValid(exclusion.BuildProfiles, profiles) {
			return workspaceEffectManifestMismatch("unknown_schema")
		}
		sink, exists := sinks[exclusion.Callee]
		if !exists || exclusion.EvidenceLayer != sink.EvidenceLayer {
			return workspaceEffectManifestMismatch("unknown_schema")
		}
		detected := workspaceEffectDetectedSite{
			Path:            exclusion.Path,
			EnclosingSymbol: exclusion.EnclosingSymbol,
			Callee:          exclusion.Callee,
			EvidenceLayer:   exclusion.EvidenceLayer,
			InvocationKind:  exclusion.InvocationKind,
			CallShapeSHA256: exclusion.CallShapeSHA256,
			Ordinal:         exclusion.Ordinal,
		}
		if workspaceEffectSiteID(detected) != exclusion.ID {
			return workspaceEffectManifestMismatch("unknown_schema")
		}
		return nil
	case "build_unselected_file":
		if exclusion.Reason != "unrepresented_build_constraint" || !workspaceEffectPathUnderScanRoot(exclusion.Path) ||
			!workspaceEffectValidHash(exclusion.ID) || workspaceEffectUnselectedID(exclusion.Path) != exclusion.ID {
			return workspaceEffectManifestMismatch("unknown_schema")
		}
		return nil
	default:
		return workspaceEffectManifestMismatch("unknown_schema")
	}
}

func workspaceEffectDetectedFromSite(site workspaceEffectSite) workspaceEffectDetectedSite {
	return workspaceEffectDetectedSite{
		ID:              site.ID,
		Path:            site.Path,
		EnclosingSymbol: site.EnclosingSymbol,
		Callee:          site.Callee,
		EvidenceLayer:   site.EvidenceLayer,
		InvocationKind:  site.InvocationKind,
		CallShapeSHA256: site.CallShapeSHA256,
		Ordinal:         site.Ordinal,
		BuildProfiles:   site.BuildProfiles,
	}
}

func workspaceEffectDetectedFromExclusion(exclusion workspaceEffectExclusion) workspaceEffectDetectedSite {
	return workspaceEffectDetectedSite{
		ID:              exclusion.ID,
		Path:            exclusion.Path,
		EnclosingSymbol: exclusion.EnclosingSymbol,
		Callee:          exclusion.Callee,
		EvidenceLayer:   exclusion.EvidenceLayer,
		InvocationKind:  exclusion.InvocationKind,
		CallShapeSHA256: exclusion.CallShapeSHA256,
		Ordinal:         exclusion.Ordinal,
		BuildProfiles:   exclusion.BuildProfiles,
	}
}

func workspaceEffectCompareManifest(manifest *workspaceEffectManifest, repository *workspaceEffectRepositoryResult) error {
	if manifest == nil || repository == nil || !reflect.DeepEqual(manifest.WatchedSinks, repository.WatchedSinks) {
		return workspaceEffectManifestMismatch("stale_entry")
	}
	detected := append([]workspaceEffectDetectedSite(nil), repository.Sites...)
	sort.Slice(detected, func(i, j int) bool { return detected[i].ID < detected[j].ID })
	classified := make([]workspaceEffectDetectedSite, 0, len(manifest.Sites)+len(manifest.Exclusions))
	for _, site := range manifest.Sites {
		classified = append(classified, workspaceEffectDetectedFromSite(site))
	}
	var unselected []string
	for _, exclusion := range manifest.Exclusions {
		if exclusion.Kind == "observed_site" {
			classified = append(classified, workspaceEffectDetectedFromExclusion(exclusion))
		} else {
			unselected = append(unselected, exclusion.Path)
		}
	}
	sort.Slice(classified, func(i, j int) bool { return classified[i].ID < classified[j].ID })
	for index := 1; index < len(classified); index++ {
		if classified[index-1].ID == classified[index].ID {
			return workspaceEffectManifestMismatch("duplicate_entry")
		}
	}
	for index := 1; index < len(detected); index++ {
		if detected[index-1].ID == detected[index].ID {
			return workspaceEffectAnalysisMismatch()
		}
	}
	for detectedIndex, classifiedIndex := 0, 0; detectedIndex < len(detected) || classifiedIndex < len(classified); {
		if detectedIndex == len(detected) {
			return workspaceEffectManifestMismatch("stale_entry")
		}
		observed := detected[detectedIndex]
		if classifiedIndex == len(classified) || observed.ID < classified[classifiedIndex].ID {
			return &workspaceEffectMismatch{Kind: "uncensused_site", Path: observed.Path, EnclosingSymbol: observed.EnclosingSymbol, Ordinal: observed.Ordinal, Callee: observed.Callee}
		}
		manifestSite := classified[classifiedIndex]
		if manifestSite.ID < observed.ID {
			return workspaceEffectManifestMismatch("stale_entry")
		}
		if !workspaceEffectSameSite(observed, manifestSite) || !reflect.DeepEqual(observed.BuildProfiles, manifestSite.BuildProfiles) {
			return &workspaceEffectMismatch{Kind: "stale_entry", Path: observed.Path, EnclosingSymbol: observed.EnclosingSymbol, Ordinal: observed.Ordinal, Callee: observed.Callee}
		}
		detectedIndex++
		classifiedIndex++
	}
	sort.Strings(unselected)
	repositoryUnselected := append([]string(nil), repository.UnselectedFiles...)
	sort.Strings(repositoryUnselected)
	if !reflect.DeepEqual(unselected, repositoryUnselected) {
		return workspaceEffectManifestMismatch("build_matrix_gap")
	}
	return nil
}

func TestWorkspaceEffectCensusRejectsSeededBypass(t *testing.T) {
	seed := `package seed; import "github.com/steveyegge/beads/internal/storage/embeddeddolt"`
	err := detectWorkspaceEffectCensusSeed(seed + `; func f() { _, _, _ = embeddeddolt.OpenSQL(nil, "secret", "", "") }`)
	if err == nil {
		t.Fatal("seeded uncensused workspace effect was not rejected")
	}
	if !strings.Contains(err.Error(), "uncensused_site") || strings.Contains(err.Error(), "secret") {
		t.Fatal("seeded workspace effect returned a noncanonical or unsafe error")
	}
}

func detectWorkspaceEffectCensusSeed(source string) error {
	if err := workspaceEffectValidateOuterGoTypesEnvironment(); err != nil {
		return err
	}
	dependencySet := token.NewFileSet()
	dependencySource := `package embeddeddolt
import (
	"context"
	"database/sql"
)
func OpenSQL(context.Context, string, string, string) (*sql.DB, func() error, error) { return nil, nil, nil }
`
	dependencyFile, err := parser.ParseFile(dependencySet, "dependency.go", dependencySource, 0)
	if err != nil {
		return workspaceEffectAnalysisMismatch()
	}
	dependencyPackage, err := (&types.Config{Importer: importer.Default(), Error: func(error) {}}).Check(
		workspaceEffectModulePath+"/internal/storage/embeddeddolt",
		dependencySet,
		[]*ast.File{dependencyFile},
		nil,
	)
	if err != nil || dependencyPackage == nil {
		return workspaceEffectAnalysisMismatch()
	}

	seedSet := token.NewFileSet()
	seedFile, err := parser.ParseFile(seedSet, "fixture.go", source, 0)
	if err != nil {
		return workspaceEffectAnalysisMismatch()
	}
	information := &types.Info{
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
		Instances:  make(map[*ast.Ident]types.Instance),
	}
	configuration := &types.Config{
		Importer: workspaceEffectFixtureImporter{packages: map[string]*types.Package{
			dependencyPackage.Path(): dependencyPackage,
		}},
		Error: func(error) {},
	}
	seedPackage, err := configuration.Check("github.com/steveyegge/beads/internal/backendmigration/seed", seedSet, []*ast.File{seedFile}, information)
	if err != nil || seedPackage == nil {
		return workspaceEffectAnalysisMismatch()
	}
	owners := workspaceEffectInterfaceOwners(seedPackage)
	var detected error
	ast.Inspect(seedFile, func(node ast.Node) bool {
		if detected != nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		callee, _ := workspaceEffectPeelCallee(call.Fun, information)
		object, _ := workspaceEffectCalledObject(callee, information)
		function, ok := object.(*types.Func)
		if !ok {
			return true
		}
		symbol, _, ok := workspaceEffectCanonicalFunction(function, owners)
		if !ok || symbol != workspaceEffectModulePath+"/internal/storage/embeddeddolt.OpenSQL" {
			return true
		}
		detected = &workspaceEffectMismatch{
			Kind:            "uncensused_site",
			Path:            "internal/backendmigration/fixture.go",
			EnclosingSymbol: workspaceEffectModulePath + "/internal/backendmigration/seed.f",
			Ordinal:         1,
			Callee:          symbol,
		}
		return false
	})
	return detected
}

type workspaceEffectFixtureImporter struct {
	packages map[string]*types.Package
}

func (i workspaceEffectFixtureImporter) Import(importPath string) (*types.Package, error) {
	if pkg := i.packages[importPath]; pkg != nil {
		return pkg, nil
	}
	return importer.Default().Import(importPath)
}

func workspaceEffectCompileFixturePackage(importPath string, sources map[string]string, imports map[string]*types.Package) (*workspaceEffectLoadedPackage, error) {
	if err := workspaceEffectValidateOuterGoTypesEnvironment(); err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(sources))
	for relative := range sources {
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(paths))
	relativeFiles := make(map[*ast.File]string, len(paths))
	for _, relative := range paths {
		file, err := parser.ParseFile(fset, relative, sources[relative], 0)
		if err != nil {
			return nil, workspaceEffectAnalysisMismatch()
		}
		files = append(files, file)
		relativeFiles[file] = relative
	}
	information := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Instances:  make(map[*ast.Ident]types.Instance),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}
	configuration := &types.Config{
		Importer: workspaceEffectFixtureImporter{packages: imports},
		Sizes:    types.SizesFor("gc", "amd64"),
		Error:    func(error) {},
	}
	typedPackage, err := configuration.Check(importPath, fset, files, information)
	if err != nil || typedPackage == nil {
		return nil, workspaceEffectAnalysisMismatch()
	}
	owners := workspaceEffectInterfaceOwners(typedPackage)
	return &workspaceEffectLoadedPackage{
		listed:           workspaceEffectGoListPackage{ImportPath: importPath},
		relativeFiles:    relativeFiles,
		files:            files,
		fset:             fset,
		typesPackage:     typedPackage,
		info:             information,
		interfaceOwners:  owners,
		interfaceMethods: workspaceEffectInterfaceMethods(information, owners),
	}, nil
}

func workspaceEffectScanFixtureProfile(target *workspaceEffectLoadedPackage, indexed ...*workspaceEffectLoadedPackage) (*workspaceEffectProfileResult, error) {
	registry := workspaceEffectRegistryMap()
	result := &workspaceEffectProfileResult{
		SelectedFiles: make(map[string]struct{}),
		Signatures:    make(map[string]string),
		Declarations:  make(map[string]struct{}),
		CallAnchors:   make(map[string]workspaceEffectCallAnchor),
	}
	for _, importPath := range []string{"database/sql", "os", "os/exec", "syscall"} {
		pkg, err := importer.Default().Import(importPath)
		if err != nil || pkg == nil {
			return nil, workspaceEffectAnalysisMismatch()
		}
		loaded := &workspaceEffectLoadedPackage{typesPackage: pkg, interfaceOwners: workspaceEffectInterfaceOwners(pkg)}
		if err := workspaceEffectIndexPackageDeclarations(loaded, registry, result); err != nil {
			return nil, err
		}
	}
	for _, loaded := range indexed {
		if err := workspaceEffectIndexPackageDeclarations(loaded, registry, result); err != nil {
			return nil, err
		}
	}
	raw, err := workspaceEffectPackageRawSites(target, registry, result, nil)
	if err != nil {
		return nil, err
	}
	result.Sites, err = workspaceEffectFinalizeRawSites(raw, "fixture")
	if err != nil {
		return nil, err
	}
	return result, nil
}

func workspaceEffectScanFixture(target *workspaceEffectLoadedPackage, indexed ...*workspaceEffectLoadedPackage) ([]workspaceEffectDetectedSite, error) {
	result, err := workspaceEffectScanFixtureProfile(target, indexed...)
	if err != nil {
		return nil, err
	}
	return result.Sites, nil
}

func workspaceEffectNoCOWDomainSource() string {
	return `package domain
type BeadsDirFSUseCase interface { InitializeBeadsDir(string) error }
type BeadsDirFSAdapters struct { ApplyNoCOW func(string) error }
type beadsDirFSUseCaseImpl struct { adapters BeadsDirFSAdapters }
func (u *beadsDirFSUseCaseImpl) InitializeBeadsDir(path string) (err error) {
	if true && u.adapters.ApplyNoCOW != nil {
		err = u.adapters.ApplyNoCOW(path)
	}
	return err
}
`
}

func workspaceEffectNoCOWCommandSource() string {
	return `package bd
import domain "github.com/steveyegge/beads/internal/storage/domain"
func applyNoCOW(string) error { return nil }
func newFileSystemAdapters() domain.BeadsDirFSAdapters {
	return domain.BeadsDirFSAdapters{ApplyNoCOW: applyNoCOW}
}
`
}

func workspaceEffectValidateNoCOWFixture(domainSources map[string]string, commandSource string) error {
	domain, err := workspaceEffectCompileFixturePackage(
		workspaceEffectModulePath+"/internal/storage/domain",
		domainSources,
		nil,
	)
	if err != nil {
		return err
	}
	command, err := workspaceEffectCompileFixturePackage(
		workspaceEffectModulePath+"/cmd/bd",
		map[string]string{"cmd/bd/fs_adapters.go": commandSource},
		map[string]*types.Package{domain.typesPackage.Path(): domain.typesPackage},
	)
	if err != nil {
		return err
	}
	_, err = workspaceEffectValidateNoCOWBinding([]*workspaceEffectLoadedPackage{command, domain})
	return err
}

func TestWorkspaceEffectCensusExactNoCOWBinding(t *testing.T) {
	baseDomain := map[string]string{"internal/storage/domain/beads.go": workspaceEffectNoCOWDomainSource()}
	baseCommand := workspaceEffectNoCOWCommandSource()
	if err := workspaceEffectValidateNoCOWFixture(baseDomain, baseCommand); err != nil {
		t.Fatal(err)
	}

	commandCases := map[string]string{
		"missing binding": `package bd
import domain "github.com/steveyegge/beads/internal/storage/domain"
func applyNoCOW(string) error { return nil }
func newFileSystemAdapters() domain.BeadsDirFSAdapters { return domain.BeadsDirFSAdapters{} }
`,
		"duplicate adapter literal": baseCommand + `
func anotherAdapters() domain.BeadsDirFSAdapters { return domain.BeadsDirFSAdapters{} }
`,
		"unkeyed binding": `package bd
import domain "github.com/steveyegge/beads/internal/storage/domain"
func applyNoCOW(string) error { return nil }
func newFileSystemAdapters() domain.BeadsDirFSAdapters { return domain.BeadsDirFSAdapters{applyNoCOW} }
`,
		"wrong root": `package bd
import domain "github.com/steveyegge/beads/internal/storage/domain"
func applyNoCOW(string) error { return nil }
func otherNoCOW(string) error { return nil }
func newFileSystemAdapters() domain.BeadsDirFSAdapters { return domain.BeadsDirFSAdapters{ApplyNoCOW: otherNoCOW} }
`,
		"converted root": `package bd
import domain "github.com/steveyegge/beads/internal/storage/domain"
func applyNoCOW(string) error { return nil }
func newFileSystemAdapters() domain.BeadsDirFSAdapters {
	return domain.BeadsDirFSAdapters{ApplyNoCOW: (func(string) error)(applyNoCOW)}
}
`,
		"wrapper root": `package bd
import domain "github.com/steveyegge/beads/internal/storage/domain"
func applyNoCOW(string) error { return nil }
func newFileSystemAdapters() domain.BeadsDirFSAdapters {
	return domain.BeadsDirFSAdapters{ApplyNoCOW: func(path string) error { return applyNoCOW(path) }}
}
`,
		"intermediate alias": `package bd
import domain "github.com/steveyegge/beads/internal/storage/domain"
func applyNoCOW(string) error { return nil }
var boundNoCOW = applyNoCOW
func newFileSystemAdapters() domain.BeadsDirFSAdapters {
	return domain.BeadsDirFSAdapters{ApplyNoCOW: boundNoCOW}
}
`,
		"binding inside closure": `package bd
import domain "github.com/steveyegge/beads/internal/storage/domain"
func applyNoCOW(string) error { return nil }
func newFileSystemAdapters() domain.BeadsDirFSAdapters {
	return func() domain.BeadsDirFSAdapters {
		return domain.BeadsDirFSAdapters{ApplyNoCOW: applyNoCOW}
	}()
}
`,
		"field reassignment": baseCommand + `
func replace(adapters *domain.BeadsDirFSAdapters) { adapters.ApplyNoCOW = applyNoCOW }
`,
		"elided keyed adapter literal": baseCommand + `
func otherNoCOW(string) error { return nil }
func additional() { _ = []domain.BeadsDirFSAdapters{{ApplyNoCOW: otherNoCOW}} }
`,
		"elided unkeyed adapter literal": baseCommand + `
func otherNoCOW(string) error { return nil }
func additional() { _ = []domain.BeadsDirFSAdapters{{otherNoCOW}} }
`,
	}
	for name, commandSource := range commandCases {
		t.Run(name+" fails closed", func(t *testing.T) {
			err := workspaceEffectValidateNoCOWFixture(baseDomain, commandSource)
			workspaceEffectRequireMismatch(t, err, "unresolved_watched_reference")
		})
	}

	t.Run("invalid binding counts cannot recover", func(t *testing.T) {
		var command strings.Builder
		command.WriteString(`package bd
import domain "github.com/steveyegge/beads/internal/storage/domain"
func applyNoCOW(string) error { return nil }
func otherNoCOW(string) error { return nil }
func newFileSystemAdapters() domain.BeadsDirFSAdapters {
	_ = domain.BeadsDirFSAdapters{ApplyNoCOW: otherNoCOW}
	_ = func() domain.BeadsDirFSAdapters { return domain.BeadsDirFSAdapters{} }
`)
		for index := 0; index < 1000; index++ {
			command.WriteString("\t_ = domain.BeadsDirFSAdapters{ApplyNoCOW: applyNoCOW}\n")
		}
		command.WriteString("\treturn domain.BeadsDirFSAdapters{ApplyNoCOW: applyNoCOW}\n}\n")
		err := workspaceEffectValidateNoCOWFixture(baseDomain, command.String())
		workspaceEffectRequireMismatch(t, err, "unresolved_watched_reference")
	})

	domainCases := map[string]string{
		"guard and call on different adapter values": `package domain
type BeadsDirFSUseCase interface { InitializeBeadsDir(string) error }
type BeadsDirFSAdapters struct { ApplyNoCOW func(string) error }
type beadsDirFSUseCaseImpl struct { adapters BeadsDirFSAdapters }
func (u *beadsDirFSUseCaseImpl) InitializeBeadsDir(path string) (err error) {
	var other beadsDirFSUseCaseImpl
	if u.adapters.ApplyNoCOW != nil { err = other.adapters.ApplyNoCOW(path) }
	return err
}
`,
		"comparison in body": `package domain
type BeadsDirFSUseCase interface { InitializeBeadsDir(string) error }
type BeadsDirFSAdapters struct { ApplyNoCOW func(string) error }
type beadsDirFSUseCaseImpl struct { adapters BeadsDirFSAdapters }
func (u *beadsDirFSUseCaseImpl) InitializeBeadsDir(path string) (err error) {
	if true { _ = u.adapters.ApplyNoCOW != nil; err = u.adapters.ApplyNoCOW(path) }
	return err
}
`,
		"call in else": `package domain
type BeadsDirFSUseCase interface { InitializeBeadsDir(string) error }
type BeadsDirFSAdapters struct { ApplyNoCOW func(string) error }
type beadsDirFSUseCaseImpl struct { adapters BeadsDirFSAdapters }
func (u *beadsDirFSUseCaseImpl) InitializeBeadsDir(path string) (err error) {
	if u.adapters.ApplyNoCOW != nil {} else { err = u.adapters.ApplyNoCOW(path) }
	return err
}
`,
		"nondominating disjunction": `package domain
type BeadsDirFSUseCase interface { InitializeBeadsDir(string) error }
type BeadsDirFSAdapters struct { ApplyNoCOW func(string) error }
type beadsDirFSUseCaseImpl struct { adapters BeadsDirFSAdapters }
func (u *beadsDirFSUseCaseImpl) InitializeBeadsDir(path string) (err error) {
	if u.adapters.ApplyNoCOW != nil || true { err = u.adapters.ApplyNoCOW(path) }
	return err
}
`,
		"additional field read": workspaceEffectNoCOWDomainSource() + `
func additional(u *beadsDirFSUseCaseImpl) { _ = u.adapters.ApplyNoCOW }
`,
		"promoted additional field read": workspaceEffectNoCOWDomainSource() + `
type adapterWrapper struct { BeadsDirFSAdapters }
func additional(w adapterWrapper) { _ = w.ApplyNoCOW }
`,
		"additional field call": workspaceEffectNoCOWDomainSource() + `
func additional(u *beadsDirFSUseCaseImpl, path string) {
	if u.adapters.ApplyNoCOW != nil { _ = u.adapters.ApplyNoCOW(path) }
}
`,
		"deferred field call": `package domain
type BeadsDirFSUseCase interface { InitializeBeadsDir(string) error }
type BeadsDirFSAdapters struct { ApplyNoCOW func(string) error }
type beadsDirFSUseCaseImpl struct { adapters BeadsDirFSAdapters }
func (u *beadsDirFSUseCaseImpl) InitializeBeadsDir(path string) (err error) {
	if u.adapters.ApplyNoCOW != nil { defer u.adapters.ApplyNoCOW(path) }
	return err
}
`,
		"concurrent field call": `package domain
type BeadsDirFSUseCase interface { InitializeBeadsDir(string) error }
type BeadsDirFSAdapters struct { ApplyNoCOW func(string) error }
type beadsDirFSUseCaseImpl struct { adapters BeadsDirFSAdapters }
func (u *beadsDirFSUseCaseImpl) InitializeBeadsDir(path string) (err error) {
	if u.adapters.ApplyNoCOW != nil { go u.adapters.ApplyNoCOW(path) }
	return err
}
`,
		"field reads inside closure": `package domain
type BeadsDirFSUseCase interface { InitializeBeadsDir(string) error }
type BeadsDirFSAdapters struct { ApplyNoCOW func(string) error }
type beadsDirFSUseCaseImpl struct { adapters BeadsDirFSAdapters }
func (u *beadsDirFSUseCaseImpl) InitializeBeadsDir(path string) (err error) {
	operation := func() { if u.adapters.ApplyNoCOW != nil { err = u.adapters.ApplyNoCOW(path) } }
	operation()
	return err
}
`,
	}
	for name, domainSource := range domainCases {
		t.Run(name+" fails closed", func(t *testing.T) {
			err := workspaceEffectValidateNoCOWFixture(
				map[string]string{"internal/storage/domain/beads.go": domainSource},
				baseCommand,
			)
			workspaceEffectRequireMismatch(t, err, "unresolved_watched_reference")
		})
	}

	t.Run("moved field declaration fails closed", func(t *testing.T) {
		domainSource := workspaceEffectNoCOWDomainSource()
		domainSource = strings.Replace(domainSource, "type BeadsDirFSAdapters struct { ApplyNoCOW func(string) error }\n", "", 1)
		err := workspaceEffectValidateNoCOWFixture(map[string]string{
			"internal/storage/domain/beads.go": domainSource,
			"internal/storage/domain/moved.go": "package domain\ntype BeadsDirFSAdapters struct { ApplyNoCOW func(string) error }\n",
		}, baseCommand)
		workspaceEffectRequireMismatch(t, err, "unresolved_watched_reference")
	})

	t.Run("generic adapter type fails closed", func(t *testing.T) {
		domainSource := `package domain
type BeadsDirFSUseCase interface { InitializeBeadsDir(string) error }
type BeadsDirFSAdapters[T any] struct { ApplyNoCOW func(string) error }
type beadsDirFSUseCaseImpl struct { adapters BeadsDirFSAdapters[int] }
func (u *beadsDirFSUseCaseImpl) InitializeBeadsDir(path string) (err error) {
	if u.adapters.ApplyNoCOW != nil { err = u.adapters.ApplyNoCOW(path) }
	return err
}
`
		commandSource := `package bd
import domain "github.com/steveyegge/beads/internal/storage/domain"
func applyNoCOW(string) error { return nil }
func newFileSystemAdapters() domain.BeadsDirFSAdapters[int] {
	return domain.BeadsDirFSAdapters[int]{ApplyNoCOW: applyNoCOW}
}
`
		err := workspaceEffectValidateNoCOWFixture(map[string]string{"internal/storage/domain/beads.go": domainSource}, commandSource)
		workspaceEffectRequireMismatch(t, err, "unresolved_watched_reference")
	})

	t.Run("wrong field fails closed", func(t *testing.T) {
		domainSource := `package domain
type BeadsDirFSUseCase interface { InitializeBeadsDir(string) error }
type BeadsDirFSAdapters struct { Other func(string) error }
type beadsDirFSUseCaseImpl struct { adapters BeadsDirFSAdapters }
func (u *beadsDirFSUseCaseImpl) InitializeBeadsDir(path string) (err error) {
	if u.adapters.Other != nil { err = u.adapters.Other(path) }
	return err
}
`
		commandSource := `package bd
import domain "github.com/steveyegge/beads/internal/storage/domain"
func applyNoCOW(string) error { return nil }
func newFileSystemAdapters() domain.BeadsDirFSAdapters {
	return domain.BeadsDirFSAdapters{Other: applyNoCOW}
}
`
		err := workspaceEffectValidateNoCOWFixture(map[string]string{"internal/storage/domain/beads.go": domainSource}, commandSource)
		workspaceEffectRequireMismatch(t, err, "unresolved_watched_reference")
	})

	t.Run("wrong field signature fails closed", func(t *testing.T) {
		domainSource := `package domain
type BeadsDirFSUseCase interface { InitializeBeadsDir(string) error }
type BeadsDirFSAdapters struct { ApplyNoCOW func(int) error }
type beadsDirFSUseCaseImpl struct { adapters BeadsDirFSAdapters }
func (u *beadsDirFSUseCaseImpl) InitializeBeadsDir(path int) (err error) {
	if u.adapters.ApplyNoCOW != nil { err = u.adapters.ApplyNoCOW(path) }
	return err
}
`
		commandSource := `package bd
import domain "github.com/steveyegge/beads/internal/storage/domain"
func applyNoCOW(int) error { return nil }
func newFileSystemAdapters() domain.BeadsDirFSAdapters {
	return domain.BeadsDirFSAdapters{ApplyNoCOW: applyNoCOW}
}
`
		err := workspaceEffectValidateNoCOWFixture(map[string]string{"internal/storage/domain/beads.go": domainSource}, commandSource)
		workspaceEffectRequireMismatch(t, err, "unresolved_watched_reference")
	})

	t.Run("unrelated same-named field remains negative space", func(t *testing.T) {
		domainSource := workspaceEffectNoCOWDomainSource() + `
type unrelated struct { ApplyNoCOW func(string) error }
func unrelatedRead(value unrelated) { _ = value.ApplyNoCOW }
`
		if err := workspaceEffectValidateNoCOWFixture(
			map[string]string{"internal/storage/domain/beads.go": domainSource},
			baseCommand,
		); err != nil {
			t.Fatal(err)
		}
	})
}

func workspaceEffectAliasFixture(sources map[string]string) ([]workspaceEffectDetectedSite, error) {
	loaded, err := workspaceEffectCompileFixturePackage(
		workspaceEffectModulePath+"/cmd/bd/doctor/fix",
		sources,
		nil,
	)
	if err != nil {
		return nil, err
	}
	return workspaceEffectScanFixture(loaded, loaded)
}

func TestWorkspaceEffectCensusExactDoctorAliases(t *testing.T) {
	base := `package fix
import "os"
var renameFile = os.Rename
var removeFile = os.Remove
var openFileRW = os.OpenFile
`
	positive := base + `
func invoke() {
	_ = renameFile("old", "new")
	_ = removeFile("old")
	file, _ := openFileRW("file", os.O_RDWR, 0600)
	_ = file
}
`
	sites, err := workspaceEffectAliasFixture(map[string]string{"cmd/bd/doctor/fix/fs.go": positive})
	if err != nil {
		t.Fatal(err)
	}
	byCallee := workspaceEffectFixtureSitesByCallee(sites)
	for _, callee := range []string{"os.Rename", "os.Remove", "os.OpenFile"} {
		if len(byCallee[callee]) != 1 {
			t.Fatalf("exact alias did not produce one %s site", callee)
		}
	}

	cases := []struct {
		name    string
		sources map[string]string
	}{
		{
			name: "missing alias",
			sources: map[string]string{"cmd/bd/doctor/fix/fs.go": `package fix
import "os"
var removeFile = os.Remove
var openFileRW = os.OpenFile
`},
		},
		{
			name: "wrong root",
			sources: map[string]string{"cmd/bd/doctor/fix/fs.go": `package fix
import "os"
var renameFile = os.Remove
var removeFile = os.Remove
var openFileRW = os.OpenFile
`},
		},
		{
			name: "moved alias",
			sources: map[string]string{
				"cmd/bd/doctor/fix/fs.go": `package fix
import "os"
var removeFile = os.Remove
var openFileRW = os.OpenFile
`,
				"cmd/bd/doctor/fix/moved.go": `package fix
import "os"
var renameFile = os.Rename
`,
			},
		},
		{
			name: "function replaces alias",
			sources: map[string]string{"cmd/bd/doctor/fix/fs.go": `package fix
import "os"
func renameFile(string, string) error { return nil }
var removeFile = os.Remove
var openFileRW = os.OpenFile
`},
		},
		{
			name: "wrapped initializer",
			sources: map[string]string{"cmd/bd/doctor/fix/fs.go": `package fix
import "os"
var renameFile = func(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }
var removeFile = os.Remove
var openFileRW = os.OpenFile
`},
		},
		{
			name: "reassigned alias",
			sources: map[string]string{"cmd/bd/doctor/fix/fs.go": base + `
func mutate() { renameFile = os.Rename }
`},
		},
		{
			name: "escaped alias",
			sources: map[string]string{"cmd/bd/doctor/fix/fs.go": base + `
func consume(any) {}
func escape() { consume(renameFile) }
`},
		},
	}
	for _, test := range cases {
		t.Run(test.name+" fails closed", func(t *testing.T) {
			_, err := workspaceEffectAliasFixture(test.sources)
			workspaceEffectRequireMismatch(t, err, "unresolved_watched_reference")
		})
	}
}

func TestWorkspaceEffectCensusInterfaceMethodBoundaries(t *testing.T) {
	storageSource := `package storage
type BackupStore interface { BackupAdd(string) error }
`
	storage, err := workspaceEffectCompileFixturePackage(
		workspaceEffectModulePath+"/internal/storage",
		map[string]string{"internal/storage/storage.go": storageSource},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("exact imported aliases and embeddings keep watched object identity", func(t *testing.T) {
		caller, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/cmd/bd",
			map[string]string{"cmd/bd/backup_fixture.go": `package bd
import storage "github.com/steveyegge/beads/internal/storage"
type Alias = storage.BackupStore
type Embedded interface { storage.BackupStore }
func aliasCall(value Alias) { _ = value.BackupAdd("x") }
func embeddedCall(value Embedded) { _ = value.BackupAdd("x") }
`},
			map[string]*types.Package{storage.typesPackage.Path(): storage.typesPackage},
		)
		if err != nil {
			t.Fatal(err)
		}
		sites, err := workspaceEffectScanFixture(caller, storage, caller)
		if err != nil {
			t.Fatal(err)
		}
		exact := workspaceEffectModulePath + "/internal/storage.(BackupStore).BackupAdd"
		if len(workspaceEffectFixtureSitesByCallee(sites)[exact]) != 2 {
			t.Fatal("exact imported interface method identity was rebranded")
		}
	})

	alternateCases := map[string]string{
		"package interface": `package storage
type BackupStore interface { BackupAdd(string) error }
type Alternate interface { BackupAdd(string) error }
func call(value Alternate) { _ = value.BackupAdd("x") }
`,
		"anonymous interface": `package storage
type BackupStore interface { BackupAdd(string) error }
func call(value interface { BackupAdd(string) error }) { _ = value.BackupAdd("x") }
`,
		"local interface": `package storage
type BackupStore interface { BackupAdd(string) error }
func call() {
	type Alternate interface { BackupAdd(string) error }
	var value Alternate
	_ = value.BackupAdd("x")
}
`,
		"generic interface": `package storage
type BackupStore interface { BackupAdd(string) error }
type Alternate[T any] interface { BackupAdd(string) error }
func call(value Alternate[int]) { _ = value.BackupAdd("x") }
`,
		"alternate method value": `package storage
type BackupStore interface { BackupAdd(string) error }
type Alternate interface { BackupAdd(string) error }
func escape(value Alternate) { method := value.BackupAdd; _ = method }
`,
	}
	for name, source := range alternateCases {
		t.Run(name+" fails ambiguous", func(t *testing.T) {
			fixture, err := workspaceEffectCompileFixturePackage(
				workspaceEffectModulePath+"/internal/storage",
				map[string]string{"internal/storage/fixture.go": source},
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = workspaceEffectScanFixture(fixture, fixture)
			workspaceEffectRequireMismatch(t, err, "unresolved_watched_reference")
			var mismatch *workspaceEffectMismatch
			if !errors.As(err, &mismatch) || mismatch.Callee != workspaceEffectModulePath+"/internal/storage.(BackupStore).BackupAdd" {
				t.Fatal("alternate interface error did not retain the registry-owned callee")
			}
		})
	}

	t.Run("generic exact watched interface owner fails closed", func(t *testing.T) {
		fixture, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/internal/storage",
			map[string]string{"internal/storage/fixture.go": `package storage
type BackupStore[T any] interface { BackupAdd(string) error }
func call(value BackupStore[int]) { _ = value.BackupAdd("x") }
`},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = workspaceEffectScanFixture(fixture, fixture)
		workspaceEffectRequireMismatch(t, err, "source_analysis_failed")
	})

	t.Run("cross-profile alternate interface reconciles against union signature", func(t *testing.T) {
		exactProfile, err := workspaceEffectScanFixtureProfile(storage, storage)
		if err != nil {
			t.Fatal(err)
		}
		alternate, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/internal/storage",
			map[string]string{"internal/storage/profile_b.go": `package storage
type Alternate interface { BackupAdd(string) error }
func call(value Alternate) { _ = value.BackupAdd("x") }
`},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		alternateProfile, err := workspaceEffectScanFixtureProfile(alternate, alternate)
		if err != nil {
			t.Fatal(err)
		}
		if len(alternateProfile.PendingInterfaceReferences) != 1 {
			t.Fatal("profile-local alternate interface was not deferred for union reconciliation")
		}
		err = workspaceEffectValidatePendingInterfaceReferences(alternateProfile.PendingInterfaceReferences, exactProfile.Signatures)
		workspaceEffectRequireMismatch(t, err, "unresolved_watched_reference")

		different, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/internal/storage",
			map[string]string{"internal/storage/profile_b.go": `package storage
type Alternate interface { BackupAdd(int) error }
func call(value Alternate) { _ = value.BackupAdd(1) }
`},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		differentProfile, err := workspaceEffectScanFixtureProfile(different, different)
		if err != nil {
			t.Fatal(err)
		}
		if err := workspaceEffectValidatePendingInterfaceReferences(differentProfile.PendingInterfaceReferences, exactProfile.Signatures); err != nil {
			t.Fatal("different-signature cross-profile interface was not negative space")
		}
	})

	t.Run("promoted imported alternate interface fails ambiguous", func(t *testing.T) {
		other, err := workspaceEffectCompileFixturePackage(
			"example.com/other",
			map[string]string{"other/other.go": `package other
type Alternate interface { BackupAdd(string) error }
`},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		caller, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/cmd/bd",
			map[string]string{"cmd/bd/backup_fixture.go": `package bd
import other "example.com/other"
type Wrapper struct { other.Alternate }
func call(value Wrapper) { _ = value.BackupAdd("x") }
`},
			map[string]*types.Package{other.typesPackage.Path(): other.typesPackage},
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = workspaceEffectScanFixture(caller, storage, caller)
		workspaceEffectRequireMismatch(t, err, "unresolved_watched_reference")
	})

	t.Run("same name with different signature is negative space", func(t *testing.T) {
		fixture, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/internal/storage",
			map[string]string{"internal/storage/fixture.go": `package storage
type BackupStore interface { BackupAdd(string) error }
type Alternate interface { BackupAdd(int) error }
func call(value Alternate) { _ = value.BackupAdd(1) }
`},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		sites, err := workspaceEffectScanFixture(fixture, fixture)
		if err != nil || len(sites) != 0 {
			t.Fatal("different interface signature was treated as a watched method")
		}
	})

	domainSource := `package domain
type BeadsDirFSUseCase interface { InitializeBeadsDir(string) error }
type beadsDirFSUseCaseImpl struct{}
func (*beadsDirFSUseCaseImpl) InitializeBeadsDir(string) error { return nil }
func interfaceCall(value BeadsDirFSUseCase) { _ = value.InitializeBeadsDir("x") }
func concreteCall(value *beadsDirFSUseCaseImpl) { _ = value.InitializeBeadsDir("x") }
`
	domain, err := workspaceEffectCompileFixturePackage(
		workspaceEffectModulePath+"/internal/storage/domain",
		map[string]string{"internal/storage/domain/fixture.go": domainSource},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	domainSites, err := workspaceEffectScanFixture(domain, domain)
	if err != nil {
		t.Fatal(err)
	}
	domainByCallee := workspaceEffectFixtureSitesByCallee(domainSites)
	for _, callee := range []string{
		workspaceEffectModulePath + "/internal/storage/domain.(BeadsDirFSUseCase).InitializeBeadsDir",
		workspaceEffectModulePath + "/internal/storage/domain.(*beadsDirFSUseCaseImpl).InitializeBeadsDir",
	} {
		if len(domainByCallee[callee]) != 1 {
			t.Fatalf("InitializeBeadsDir fixture did not detect %s", callee)
		}
	}

	t.Run("alternate InitializeBeadsDir interface fails ambiguous", func(t *testing.T) {
		fixture, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/internal/storage/domain",
			map[string]string{"internal/storage/domain/fixture.go": `package domain
type BeadsDirFSUseCase interface { InitializeBeadsDir(string) error }
type Alternate interface { InitializeBeadsDir(string) error }
func call(value Alternate) { _ = value.InitializeBeadsDir("x") }
`},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = workspaceEffectScanFixture(fixture, fixture)
		workspaceEffectRequireMismatch(t, err, "unresolved_watched_reference")
	})

	t.Run("exact InitializeBeadsDir method value fails closed", func(t *testing.T) {
		fixture, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/internal/storage/domain",
			map[string]string{"internal/storage/domain/fixture.go": `package domain
type BeadsDirFSUseCase interface { InitializeBeadsDir(string) error }
func escape(value BeadsDirFSUseCase) { method := value.InitializeBeadsDir; _ = method }
`},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = workspaceEffectScanFixture(fixture, fixture)
		workspaceEffectRequireMismatch(t, err, "unresolved_watched_reference")
	})

	t.Run("concrete InitializeBeadsDir method value fails closed", func(t *testing.T) {
		fixture, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/internal/storage/domain",
			map[string]string{"internal/storage/domain/fixture.go": `package domain
type beadsDirFSUseCaseImpl struct{}
func (*beadsDirFSUseCaseImpl) InitializeBeadsDir(string) error { return nil }
func escape(value *beadsDirFSUseCaseImpl) { method := value.InitializeBeadsDir; _ = method }
`},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = workspaceEffectScanFixture(fixture, fixture)
		workspaceEffectRequireMismatch(t, err, "unresolved_watched_reference")
	})
}

func TestWorkspaceEffectCensusDedicatedProjectSinks(t *testing.T) {
	t.Run("both UOW constructors", func(t *testing.T) {
		fixture, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/internal/storage/uow",
			map[string]string{"internal/storage/uow/fixture.go": `package uow
func NewDoltServerUOWProvider() {}
func NewExternalDoltServerUOWProvider() {}
func call() { NewDoltServerUOWProvider(); NewExternalDoltServerUOWProvider() }
`},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		sites, err := workspaceEffectScanFixture(fixture, fixture)
		if err != nil {
			t.Fatal(err)
		}
		byCallee := workspaceEffectFixtureSitesByCallee(sites)
		for _, callee := range []string{
			workspaceEffectModulePath + "/internal/storage/uow.NewDoltServerUOWProvider",
			workspaceEffectModulePath + "/internal/storage/uow.NewExternalDoltServerUOWProvider",
		} {
			if len(byCallee[callee]) != 1 {
				t.Fatalf("UOW fixture did not detect %s", callee)
			}
		}
	})

	t.Run("all repository atomic writers", func(t *testing.T) {
		atomicPackage, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/internal/atomicfile",
			map[string]string{"internal/atomicfile/fixture.go": `package atomicfile
func WriteFile() {}
`},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		atomicCaller, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/cmd/bd",
			map[string]string{"cmd/bd/init_fixture.go": `package bd
import atomicfile "github.com/steveyegge/beads/internal/atomicfile"
func callAtomicPackage() { atomicfile.WriteFile() }
`},
			map[string]*types.Package{atomicPackage.typesPackage.Path(): atomicPackage.typesPackage},
		)
		if err != nil {
			t.Fatal(err)
		}
		configfile, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/internal/configfile",
			map[string]string{"internal/configfile/fixture.go": `package configfile
func writeFileAtomic() {}
func callConfigAtomic() { writeFileAtomic() }
`},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		command, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/cmd/bd",
			map[string]string{"cmd/bd/init_fixture.go": `package bd
func atomicWriteFile() {}
func callCommandAtomic() { atomicWriteFile() }
`},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		var sites []workspaceEffectDetectedSite
		for _, fixture := range []struct {
			target  *workspaceEffectLoadedPackage
			indexes []*workspaceEffectLoadedPackage
		}{
			{target: atomicCaller, indexes: []*workspaceEffectLoadedPackage{atomicPackage, atomicCaller}},
			{target: configfile, indexes: []*workspaceEffectLoadedPackage{configfile}},
			{target: command, indexes: []*workspaceEffectLoadedPackage{command}},
		} {
			detected, err := workspaceEffectScanFixture(fixture.target, fixture.indexes...)
			if err != nil {
				t.Fatal(err)
			}
			sites = append(sites, detected...)
		}
		byCallee := workspaceEffectFixtureSitesByCallee(sites)
		for _, callee := range []string{
			workspaceEffectModulePath + "/internal/atomicfile.WriteFile",
			workspaceEffectModulePath + "/internal/configfile.writeFileAtomic",
			workspaceEffectModulePath + "/cmd/bd.atomicWriteFile",
		} {
			if len(byCallee[callee]) != 1 {
				t.Fatalf("atomic writer fixture did not detect %s", callee)
			}
		}
	})
}

func TestWorkspaceEffectCensusFrozenSemantics(t *testing.T) {
	_, manifest := workspaceEffectReadManifestForTest(t)

	familyCounts := make(map[string]int)
	for _, site := range manifest.Sites {
		familyCounts[site.Family]++
	}
	for _, family := range manifest.Families {
		if familyCounts[family.Name] != family.SiteCount {
			t.Fatalf("family %s count drifted", family.Name)
		}
	}

	applyFixList := workspaceEffectModulePath + "/cmd/bd.applyFixList"
	applyFixCount := 0
	interfaceInitializeCount := 0
	concreteInitializeCount := 0
	directNoCOWCount := 0
	for _, site := range manifest.Sites {
		switch site.Callee {
		case applyFixList:
			applyFixCount++
			if site.Family != "doctor_fix" || site.FutureDisposition != "pre_effect_refusal_required" || site.EvidenceLayer != "semantic_boundary" {
				t.Fatal("applyFixList classification drifted")
			}
		case workspaceEffectModulePath + "/internal/storage/domain.(BeadsDirFSUseCase).InitializeBeadsDir":
			interfaceInitializeCount++
			if site.Family != "init_bootstrap" {
				t.Fatal("interface InitializeBeadsDir classification drifted")
			}
		case workspaceEffectModulePath + "/internal/storage/domain.(*beadsDirFSUseCaseImpl).InitializeBeadsDir":
			concreteInitializeCount++
		case workspaceEffectModulePath + "/cmd/bd.applyNoCOW":
			directNoCOWCount++
			if site.Family != "init_bootstrap" {
				t.Fatal("direct applyNoCOW classification drifted")
			}
		}
	}
	if applyFixCount != 5 || interfaceInitializeCount != 1 || concreteInitializeCount != 0 || directNoCOWCount != 2 {
		t.Fatalf("semantic baseline counts drifted: applyFix=%d interfaceInit=%d concreteInit=%d noCOW=%d", applyFixCount, interfaceInitializeCount, concreteInitializeCount, directNoCOWCount)
	}

	hookApply := workspaceEffectModulePath + "/cmd/bd.applyHookMigrationExecution"
	circuitCleanup := workspaceEffectModulePath + "/internal/storage/dolt.CleanStaleCircuitBreakerFiles"
	hookCount := 0
	circuitCount := 0
	hookLeafCount := 0
	diagnosticOutputCount := 0
	for _, exclusion := range manifest.Exclusions {
		if exclusion.Kind != "observed_site" {
			continue
		}
		switch exclusion.Callee {
		case hookApply:
			hookCount++
			if exclusion.Reason != "git_integration_artifact" {
				t.Fatal("hook application exclusion drifted")
			}
		case circuitCleanup:
			circuitCount++
			if exclusion.Reason != "non_workspace_artifact" {
				t.Fatal("circuit cleanup exclusion drifted")
			}
		}
		if exclusion.Path == "cmd/bd/migrate_hooks_apply.go" &&
			(exclusion.Callee == "os.WriteFile" || exclusion.Callee == "os.Remove" || exclusion.Callee == "os.Rename") {
			hookLeafCount++
			if exclusion.Reason != "git_integration_artifact" {
				t.Fatal("raw hook mutation classification drifted")
			}
		}
		if (exclusion.Path == "cmd/bd/doctor.go" || exclusion.Path == "cmd/bd/doctor/perf.go") && exclusion.Reason == "diagnostic_artifact" {
			diagnosticOutputCount++
		}
	}
	if hookCount != 2 || circuitCount != 2 || hookLeafCount != 3 || diagnosticOutputCount != 2 {
		t.Fatalf("semantic exclusion counts drifted: hook=%d circuit=%d hookLeaf=%d diagnosticOutput=%d", hookCount, circuitCount, hookLeafCount, diagnosticOutputCount)
	}

	recoveryCount := 0
	staleMQCount := 0
	for _, site := range manifest.Sites {
		if site.Callee == workspaceEffectModulePath+"/internal/doltserver.RecoverCorruptManifest" ||
			site.Callee == workspaceEffectModulePath+"/internal/doltserver.RecoverPreV56DoltDir" {
			recoveryCount++
			if site.Family != "provider_state_rename_restore" {
				t.Fatal("recovery root classification drifted")
			}
		}
		if strings.Contains(site.EnclosingSymbol, ".FixStaleMQFiles") {
			staleMQCount++
			if site.Callee != "os.RemoveAll" || site.Family != "doctor_fix" || !reflect.DeepEqual(site.BuildProfiles, []string{"linux_amd64_cgo"}) {
				t.Fatal("CGO stale-MQ classification drifted")
			}
		}
	}
	if recoveryCount != 2 || staleMQCount != 1 {
		t.Fatalf("recovery or stale-MQ evidence drifted: recovery=%d staleMQ=%d", recoveryCount, staleMQCount)
	}
	for _, sink := range workspaceEffectRegistry() {
		if strings.Contains(sink.Symbol, ").Exec") || strings.HasSuffix(sink.Symbol, ".SetMetadata") || strings.HasSuffix(sink.Symbol, ".SetLocalMetadata") {
			t.Fatalf("dominated-only SQL/store primitive entered the leaf registry: %s", sink.Symbol)
		}
	}

	profileSeen := make(map[string]bool)
	hasCGOOnly := false
	hasWindowsOnly := false
	hasNoCGOSelection := false
	visitProfiles := func(profiles []string) {
		for _, profile := range profiles {
			profileSeen[profile] = true
		}
		hasCGOOnly = hasCGOOnly || reflect.DeepEqual(profiles, []string{"linux_amd64_cgo"})
		hasWindowsOnly = hasWindowsOnly || reflect.DeepEqual(profiles, []string{"windows_amd64_nocgo"})
		containsNoCGO := false
		containsCGO := false
		for _, profile := range profiles {
			containsNoCGO = containsNoCGO || strings.HasSuffix(profile, "_nocgo")
			containsCGO = containsCGO || strings.HasSuffix(profile, "_cgo")
		}
		hasNoCGOSelection = hasNoCGOSelection || (containsNoCGO && !containsCGO)
	}
	for _, site := range manifest.Sites {
		visitProfiles(site.BuildProfiles)
	}
	for _, exclusion := range manifest.Exclusions {
		if exclusion.Kind == "observed_site" {
			visitProfiles(exclusion.BuildProfiles)
		}
	}
	for _, profile := range workspaceEffectBuildProfiles() {
		if !profileSeen[profile.Name] {
			t.Fatalf("build profile %s has no evidence", profile.Name)
		}
	}
	if !hasCGOOnly || !hasWindowsOnly || !hasNoCGOSelection {
		t.Fatal("platform-specific evidence sets are missing")
	}
	for _, exclusion := range manifest.Exclusions {
		if exclusion.Kind == "build_unselected_file" {
			t.Fatal("pinned baseline unexpectedly contains an unselected file")
		}
	}
}

func TestWorkspaceEffectCensusRejectsEverySemanticRootAndFamilySeed(t *testing.T) {
	_, manifest := workspaceEffectReadManifestForTest(t)
	baseRepository := workspaceEffectRepositoryFromManifest(manifest)

	for index, spec := range workspaceEffectRegistry() {
		if spec.EvidenceLayer != "semantic_boundary" {
			continue
		}
		t.Run("semantic root "+strconv.Itoa(index), func(t *testing.T) {
			seed := workspaceEffectDetectedSite{
				Path:            "cmd/bd/doctor_fixture.go",
				EnclosingSymbol: workspaceEffectModulePath + "/cmd/bd.seedSemantic" + strconv.Itoa(index),
				Callee:          spec.Symbol,
				EvidenceLayer:   spec.EvidenceLayer,
				InvocationKind:  "call",
				CallShapeSHA256: workspaceEffectCallShape(spec.Symbol, "call", 0, false, 0),
				Ordinal:         1,
				BuildProfiles:   []string{"linux_amd64_cgo"},
			}
			seed.ID = workspaceEffectSiteID(seed)
			repository := *baseRepository
			repository.Sites = append(append([]workspaceEffectDetectedSite(nil), baseRepository.Sites...), seed)
			workspaceEffectRequireMismatch(t, workspaceEffectCompareManifest(manifest, &repository), "uncensused_site")
		})
	}

	for familyIndex, family := range workspaceEffectFamilies() {
		t.Run("family "+family.Name, func(t *testing.T) {
			var representative workspaceEffectDetectedSite
			for _, site := range manifest.Sites {
				if site.Family == family.Name {
					representative = workspaceEffectDetectedFromSite(site)
					break
				}
			}
			if representative.ID == "" {
				representative = workspaceEffectDetectedSite{
					Path:            "cmd/bd/store_factory.go",
					EnclosingSymbol: workspaceEffectModulePath + "/cmd/bd.seedRedirectMutation",
					Callee:          "os.Rename",
					EvidenceLayer:   "leaf",
					InvocationKind:  "call",
					CallShapeSHA256: workspaceEffectCallShape("os.Rename", "call", 2, false, 0),
					Ordinal:         1,
					BuildProfiles:   []string{"linux_amd64_cgo"},
				}
			}
			representative = workspaceEffectSeededSite(representative, strconv.Itoa(8000+familyIndex))
			repository := *baseRepository
			repository.Sites = append(append([]workspaceEffectDetectedSite(nil), baseRepository.Sites...), representative)
			workspaceEffectRequireMismatch(t, workspaceEffectCompareManifest(manifest, &repository), "uncensused_site")
		})
	}
}

func TestWorkspaceEffectCensusDeferredSurfaceSentinels(t *testing.T) {
	t.Run("component-exact path matching", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "internal", "backendmigration"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := workspaceEffectCheckDeferredPaths(root); err != nil {
			t.Fatal("internal/backendmigration matched internal/backend sentinel")
		}
	})

	for _, sentinel := range workspaceEffectDeferredSurfaces()[0].Paths {
		t.Run("path "+strings.ReplaceAll(sentinel, "/", "_"), func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(sentinel)), 0o700); err != nil {
				t.Fatal(err)
			}
			workspaceEffectRequireMismatch(t, workspaceEffectCheckDeferredPaths(root), "deferred_surface_present")
		})
	}
	for index, sentinel := range workspaceEffectDeferredSurfaces()[0].Symbols {
		t.Run("symbol "+strconv.Itoa(index), func(t *testing.T) {
			workspaceEffectRequireMismatch(t, workspaceEffectCheckDeferredSymbols(map[string]struct{}{sentinel: {}}), "deferred_surface_present")
		})
	}
}

func workspaceEffectWriteFixtureFile(t *testing.T, relative, source string) string {
	t.Helper()
	root := t.TempDir()
	file := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestWorkspaceEffectCensusBuildMatrixAndSourceBoundaries(t *testing.T) {
	expectedProfiles := []workspaceEffectBuildProfile{
		{Name: "darwin_amd64_nocgo", GOOS: "darwin", GOARCH: "amd64", CGOEnabled: false, Tags: []string{"gms_pure_go"}},
		{Name: "linux_amd64_cgo", GOOS: "linux", GOARCH: "amd64", CGOEnabled: true, Tags: []string{"gms_pure_go"}},
		{Name: "linux_amd64_nocgo", GOOS: "linux", GOARCH: "amd64", CGOEnabled: false, Tags: []string{"gms_pure_go"}},
		{Name: "linux_arm64_nocgo", GOOS: "linux", GOARCH: "arm64", CGOEnabled: false, Tags: []string{"gms_pure_go"}},
		{Name: "windows_amd64_nocgo", GOOS: "windows", GOARCH: "amd64", CGOEnabled: false, Tags: []string{"gms_pure_go"}},
	}
	if !reflect.DeepEqual(workspaceEffectBuildProfiles(), expectedProfiles) {
		t.Fatal("build profile matrix drifted")
	}

	t.Run("package-relative CompiledGoFiles use package directory", func(t *testing.T) {
		listed := workspaceEffectGoListPackage{Dir: filepath.Join("root", "package")}
		if got, want := workspaceEffectListedFile(listed, "relative.go"), filepath.Join("root", "package", "relative.go"); got != want {
			t.Fatalf("relative CompiledGoFiles resolved to %q, want %q", got, want)
		}
	})

	t.Run("ordinary unselected production file is explicit negative space", func(t *testing.T) {
		root := workspaceEffectWriteFixtureFile(t, "cmd/bd/ignored.go", "//go:build ignore\n\npackage main\nvar ordinary = 1\n")
		declarations := make(map[string]struct{})
		if err := workspaceEffectValidateUnselectedFiles(root, []string{"cmd/bd/ignored.go"}, declarations); err != nil {
			t.Fatal(err)
		}
		if _, indexed := declarations[workspaceEffectModulePath+"/cmd/bd.ordinary"]; !indexed {
			t.Fatal("unselected declaration was not indexed")
		}
	})

	unselectedNoCOWCases := map[string]string{
		"function root":       "package main\nvar _ = applyNoCOW\n",
		"adapter field":       "package main\nvar _ = value.ApplyNoCOW\n",
		"adapter type":        "package main\nvar _ = BeadsDirFSAdapters{}\n",
		"keyed adapter field": "package main\nvar _ = struct{ ApplyNoCOW any }{ApplyNoCOW: nil}\n",
	}
	for name, source := range unselectedNoCOWCases {
		t.Run("unselected "+name+" fails closed", func(t *testing.T) {
			root := workspaceEffectWriteFixtureFile(t, "cmd/bd/ignored.go", "//go:build ignore\n\n"+source)
			err := workspaceEffectValidateUnselectedFiles(root, []string{"cmd/bd/ignored.go"}, make(map[string]struct{}))
			workspaceEffectRequireMismatch(t, err, "unresolved_watched_reference")
		})
	}

	t.Run("unselected exact alias use fails closed", func(t *testing.T) {
		root := workspaceEffectWriteFixtureFile(t, "cmd/bd/doctor/fix/ignored.go", "//go:build ignore\n\npackage fix\nfunc f() { _ = renameFile }\n")
		err := workspaceEffectValidateUnselectedFiles(root, []string{"cmd/bd/doctor/fix/ignored.go"}, make(map[string]struct{}))
		workspaceEffectRequireMismatch(t, err, "unresolved_watched_reference")
	})

	t.Run("unselected local alias shadow remains negative space", func(t *testing.T) {
		root := workspaceEffectWriteFixtureFile(t, "cmd/bd/doctor/fix/ignored.go", "//go:build ignore\n\npackage fix\nfunc f() { renameFile := 1; _ = renameFile }\n")
		if err := workspaceEffectValidateUnselectedFiles(root, []string{"cmd/bd/doctor/fix/ignored.go"}, make(map[string]struct{})); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unselected deferred declaration fails independently", func(t *testing.T) {
		root := workspaceEffectWriteFixtureFile(t, "internal/configfile/ignored.go", "//go:build ignore\n\npackage configfile\nfunc ResolveBackendPluginConfig() {}\n")
		err := workspaceEffectValidateUnselectedFiles(root, []string{"internal/configfile/ignored.go"}, make(map[string]struct{}))
		workspaceEffectRequireMismatch(t, err, "deferred_surface_present")
	})

	t.Run("control-bearing source path is never retained", func(t *testing.T) {
		root := t.TempDir()
		_, _, err := workspaceEffectRepoRelative(root, filepath.Join(root, "cmd", "bd", "a\x1b.go"))
		workspaceEffectRequireMismatch(t, err, "source_analysis_failed")
	})

	for index, separator := range []string{"\u2028", "\u2029"} {
		t.Run("line-breaking source path "+strconv.Itoa(index)+" is never retained", func(t *testing.T) {
			root := workspaceEffectWriteFixtureFile(t, "cmd/bd/a"+separator+".go", "package bd\n")
			_, _, err := workspaceEffectRepoRelative(root, filepath.Join(root, "cmd", "bd", "a"+separator+".go"))
			workspaceEffectRequireMismatch(t, err, "source_analysis_failed")
			workspaceEffectAssertSecretAbsent(t, err, separator)
		})
	}

	if runtime.GOOS != "windows" {
		t.Run("source symlink cannot escape the repository", func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "cmd", "bd")
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			external := filepath.Join(t.TempDir(), "external.go")
			if err := os.WriteFile(external, []byte("package bd\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(directory, "escape.go")
			if err := os.Symlink(external, link); err != nil {
				t.Fatal(err)
			}
			_, _, err := workspaceEffectRepoRelative(root, link)
			workspaceEffectRequireMismatch(t, err, "source_analysis_failed")
		})

		t.Run("nested source directory symlink fails closed", func(t *testing.T) {
			root := t.TempDir()
			for _, scanRoot := range workspaceEffectScanRoots() {
				path := filepath.Join(root, filepath.FromSlash(scanRoot.Path))
				if scanRoot.Kind == "tree" {
					if err := os.MkdirAll(path, 0o700); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("package beads\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			external := t.TempDir()
			link := filepath.Join(root, "internal", "storage", "hidden")
			if err := os.Symlink(external, link); err != nil {
				t.Fatal(err)
			}
			_, err := workspaceEffectProductionFiles(root)
			workspaceEffectRequireMismatch(t, err, "source_analysis_failed")
		})
	}
}

func TestWorkspaceEffectCensusCrossProfileAnchorsAndCallableIdentity(t *testing.T) {
	t.Run("arguments formatting and profiles are not physical identity", func(t *testing.T) {
		firstShape := workspaceEffectCallShape("os.Remove", "call", 1, false, 0)
		secondShape := workspaceEffectCallShape("os.Remove", "call", 1, false, 0)
		if firstShape != secondShape || firstShape == workspaceEffectCallShape("os.Remove", "call", 1, false, 1) {
			t.Fatal("call-shape identity did not isolate detector-owned structure")
		}
		site := workspaceEffectDetectedSite{
			Path:            "cmd/bd/doctor_fixture.go",
			EnclosingSymbol: workspaceEffectModulePath + "/cmd/bd.identity",
			Callee:          "os.Remove",
			EvidenceLayer:   "leaf",
			InvocationKind:  "call",
			CallShapeSHA256: firstShape,
			Ordinal:         1,
			BuildProfiles:   []string{"linux_amd64_cgo"},
		}
		firstID := workspaceEffectSiteID(site)
		site.BuildProfiles = []string{"darwin_amd64_nocgo", "windows_amd64_nocgo"}
		if workspaceEffectSiteID(site) != firstID {
			t.Fatal("build-profile evidence entered physical site identity")
		}
	})

	baseSite := workspaceEffectDetectedSite{
		ID:              strings.Repeat("1", 64),
		Path:            "cmd/bd/doctor_fixture.go",
		EnclosingSymbol: workspaceEffectModulePath + "/cmd/bd.anchor",
		Callee:          "os.Remove",
		EvidenceLayer:   "leaf",
		InvocationKind:  "call",
		CallShapeSHA256: workspaceEffectCallShape("os.Remove", "call", 1, false, 0),
		Ordinal:         1,
		SourceOffset:    10,
		SourceEndOffset: 20,
	}
	key := workspaceEffectCallAnchorKey(baseSite.Path, baseSite.SourceOffset, baseSite.SourceEndOffset)
	anchors := map[string]workspaceEffectCallAnchor{key: {Watched: true, Site: baseSite}}
	if err := workspaceEffectMergeCallAnchors(anchors, map[string]workspaceEffectCallAnchor{key: {Watched: true, Site: baseSite}}); err != nil {
		t.Fatal(err)
	}

	t.Run("watchedness disagreement fails", func(t *testing.T) {
		workspaceEffectRequireMismatch(t, workspaceEffectMergeCallAnchors(
			map[string]workspaceEffectCallAnchor{key: {Watched: true, Site: baseSite}},
			map[string]workspaceEffectCallAnchor{key: {}},
		), "source_analysis_failed")
	})

	t.Run("callee context disagreement fails", func(t *testing.T) {
		changed := baseSite
		changed.ID = strings.Repeat("2", 64)
		changed.Callee = "os.Rename"
		changed.CallShapeSHA256 = workspaceEffectCallShape("os.Rename", "call", 2, false, 0)
		workspaceEffectRequireMismatch(t, workspaceEffectMergeCallAnchors(
			map[string]workspaceEffectCallAnchor{key: {Watched: true, Site: baseSite}},
			map[string]workspaceEffectCallAnchor{key: {Watched: true, Site: changed}},
		), "source_analysis_failed")
	})

	t.Run("nested calls sharing a start offset remain distinct", func(t *testing.T) {
		fixture, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/cmd/bd",
			map[string]string{"cmd/bd/doctor_fixture.go": `package bd
func factory() func() int { return func() int { return 1 } }
var _ = factory()()
`},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := workspaceEffectScanFixture(fixture, fixture); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("package initializer and nested literals have recursive identities", func(t *testing.T) {
		fixture, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/cmd/bd",
			map[string]string{"cmd/bd/doctor_fixture.go": `package bd
import "os"
var first = func() { _ = os.Remove("one") }
var second = func() { func() { _ = os.Remove("two") }() }
`},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		sites, err := workspaceEffectScanFixture(fixture, fixture)
		if err != nil {
			t.Fatal(err)
		}
		if len(sites) != 2 {
			t.Fatalf("function literal fixture found %d sites, want 2", len(sites))
		}
		enclosings := map[string]bool{}
		for _, site := range sites {
			enclosings[site.EnclosingSymbol] = true
		}
		parent := workspaceEffectModulePath + "/cmd/bd.package_init@cmd/bd/doctor_fixture.go"
		if !enclosings[parent+"$func#1"] || !enclosings[parent+"$func#2$func#1"] {
			t.Fatal("function literal identities did not follow recursive lexical ordinals")
		}
	})
}

func workspaceEffectAssertSecretAbsent(t *testing.T, err error, secret string) {
	t.Helper()
	if err == nil {
		t.Fatal("secret-safety fixture did not fail")
	}
	queue := []error{err}
	seen := make(map[error]struct{})
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == nil {
			continue
		}
		if _, duplicate := seen[current]; duplicate {
			continue
		}
		seen[current] = struct{}{}
		for _, rendered := range []string{current.Error(), fmt.Sprintf("%v", current), fmt.Sprintf("%+v", current), fmt.Sprintf("%#v", current)} {
			if strings.Contains(rendered, secret) {
				t.Fatal("secret-bearing data escaped through the error graph")
			}
		}
		if many, ok := current.(interface{ Unwrap() []error }); ok {
			queue = append(queue, many.Unwrap()...)
		} else if next := errors.Unwrap(current); next != nil {
			queue = append(queue, next)
		}
	}
}

func TestWorkspaceEffectCensusErrorsAreSecretSafe(t *testing.T) {
	const secret = "credential-canary-7f8c9e"
	seed := `package seed; import "github.com/steveyegge/beads/internal/storage/embeddeddolt"`
	err := detectWorkspaceEffectCensusSeed(seed + `; func f() { _, _, _ = embeddeddolt.OpenSQL(nil, "` + secret + `", "", "") }`)
	workspaceEffectAssertSecretAbsent(t, err, secret)

	manifestBytes, manifest := workspaceEffectReadManifestForTest(t)
	if strings.Contains(string(manifestBytes), secret) {
		t.Fatal("secret canary appeared in the manifest")
	}
	manifest.Sites[0].EnclosingSymbol = workspaceEffectModulePath + "/cmd/bd." + secret
	manifest.Sites[0].ID = workspaceEffectSiteID(workspaceEffectDetectedFromSite(manifest.Sites[0]))
	sort.Slice(manifest.Sites, func(i, j int) bool { return manifest.Sites[i].ID < manifest.Sites[j].ID })
	_, err = workspaceEffectLoadManifest(workspaceEffectMarshalManifestForTest(t, manifest))
	workspaceEffectRequireMismatch(t, err, "stale_entry")
	workspaceEffectAssertSecretAbsent(t, err, secret)

	_, signatureManifest := workspaceEffectReadManifestForTest(t)
	signatureManifest.WatchedSinks[0].Signature = signatureManifest.WatchedSinks[0].Symbol + " func(" + secret + ")"
	_, err = workspaceEffectLoadManifest(workspaceEffectMarshalManifestForTest(t, signatureManifest))
	workspaceEffectRequireMismatch(t, err, "stale_entry")
	workspaceEffectAssertSecretAbsent(t, err, secret)

	t.Setenv("GODEBUG", secret)
	_, err = workspaceEffectCompileFixturePackage(
		workspaceEffectModulePath+"/cmd/bd",
		map[string]string{"cmd/bd/doctor_fixture.go": "package bd\n"},
		nil,
	)
	workspaceEffectRequireMismatch(t, err, "source_analysis_failed")
	workspaceEffectAssertSecretAbsent(t, err, secret)
}

func TestWorkspaceEffectCensusMismatchSelectionIsDeterministic(t *testing.T) {
	t.Run("watched value escapes use lexical source order", func(t *testing.T) {
		fixture, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/cmd/bd",
			map[string]string{
				"cmd/bd/doctor_a.go": "package bd\nimport \"os\"\nvar first = os.Remove\n",
				"cmd/bd/doctor_z.go": "package bd\nimport \"os\"\nvar second = os.Rename\n",
			},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		for iteration := 0; iteration < 64; iteration++ {
			_, err := workspaceEffectScanFixture(fixture, fixture)
			var mismatch *workspaceEffectMismatch
			if !errors.As(err, &mismatch) || mismatch.Path != "cmd/bd/doctor_a.go" || mismatch.Callee != "os.Remove" {
				t.Fatal("watched value escape mismatch selection was not lexical")
			}
		}
	})

	t.Run("missing aliases use detector declaration order", func(t *testing.T) {
		root := workspaceEffectWriteFixtureFile(t, "cmd/bd/doctor/fix/placeholder.go", "package fix\n")
		for iteration := 0; iteration < 64; iteration++ {
			err := workspaceEffectValidateAliasUniverse(root, []string{"cmd/bd/doctor/fix/placeholder.go"})
			var mismatch *workspaceEffectMismatch
			if !errors.As(err, &mismatch) || mismatch.Callee != "os.OpenFile" {
				t.Fatal("missing alias mismatch selection did not use detector declaration order")
			}
		}
	})
}

func TestWorkspaceEffectCensusMatchesRepository(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return
	}
	manifestBytes, err := os.ReadFile(workspaceEffectManifestPath)
	if err != nil {
		t.Fatal(workspaceEffectManifestMismatch("stale_entry"))
	}
	manifest, err := workspaceEffectLoadManifest(manifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := workspaceEffectAnalyzeRepository()
	if err != nil {
		t.Fatal(err)
	}
	if err := workspaceEffectCompareManifest(manifest, repository); err != nil {
		t.Fatal(err)
	}
}

func workspaceEffectReadManifestForTest(t *testing.T) ([]byte, *workspaceEffectManifest) {
	t.Helper()
	data, err := os.ReadFile(workspaceEffectManifestPath)
	if err != nil {
		t.Fatal(workspaceEffectManifestMismatch("stale_entry"))
	}
	manifest, err := workspaceEffectLoadManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	return data, manifest
}

func workspaceEffectMarshalManifestForTest(t *testing.T, manifest *workspaceEffectManifest) []byte {
	t.Helper()
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func workspaceEffectRequireMismatch(t *testing.T, err error, kind string) {
	t.Helper()
	if err == nil {
		t.Fatal("workspace effect census mutation was not rejected")
	}
	var mismatch *workspaceEffectMismatch
	if !errors.As(err, &mismatch) {
		t.Fatal("workspace effect census returned a noncanonical error")
	}
	if kind != "" && mismatch.Kind != kind {
		t.Fatalf("workspace effect census returned %q, want %q", mismatch.Kind, kind)
	}
}

func workspaceEffectReplaceOnceForTest(t *testing.T, data, old, replacement []byte) []byte {
	t.Helper()
	if bytes.Count(data, old) != 1 {
		t.Fatal("workspace effect manifest fixture did not have one replacement target")
	}
	return bytes.Replace(data, old, replacement, 1)
}

func TestWorkspaceEffectCensusRejectsStrictManifestMutations(t *testing.T) {
	canonical, _ := workspaceEffectReadManifestForTest(t)
	formatLine := []byte("  \"format\": \"" + workspaceEffectCensusFormat + "\",\n")
	firstRootBlock := []byte("      \"path\": \"beads.go\",\n      \"kind\": \"file\"\n")

	rawCases := []struct {
		name string
		kind string
		edit func([]byte) []byte
	}{
		{
			name: "duplicate top-level key",
			kind: "duplicate_entry",
			edit: func(data []byte) []byte {
				return workspaceEffectReplaceOnceForTest(t, data, formatLine, append(append([]byte(nil), formatLine...), formatLine...))
			},
		},
		{
			name: "duplicate nested key",
			kind: "duplicate_entry",
			edit: func(data []byte) []byte {
				replacement := []byte("      \"path\": \"beads.go\",\n      \"path\": \"beads.go\",\n      \"kind\": \"file\"\n")
				return workspaceEffectReplaceOnceForTest(t, data, firstRootBlock, replacement)
			},
		},
		{
			name: "unknown top-level field",
			kind: "unknown_schema",
			edit: func(data []byte) []byte {
				replacement := append(append([]byte(nil), formatLine...), []byte("  \"unexpected\": true,\n")...)
				return workspaceEffectReplaceOnceForTest(t, data, formatLine, replacement)
			},
		},
		{
			name: "unknown nested field",
			kind: "unknown_schema",
			edit: func(data []byte) []byte {
				replacement := []byte("      \"path\": \"beads.go\",\n      \"unexpected\": true,\n      \"kind\": \"file\"\n")
				return workspaceEffectReplaceOnceForTest(t, data, firstRootBlock, replacement)
			},
		},
		{
			name: "trailing value",
			kind: "unknown_schema",
			edit: func(data []byte) []byte { return append(append([]byte(nil), data...), []byte("{}\n")...) },
		},
		{
			name: "noncanonical whitespace",
			kind: "unknown_schema",
			edit: func(data []byte) []byte { return append([]byte(" "), data...) },
		},
		{
			name: "noncanonical field order",
			kind: "unknown_schema",
			edit: func(data []byte) []byte {
				old := []byte("  \"source_baseline\": \"" + workspaceEffectSourceBase + "\",\n  \"runtime_enforced\": false,\n")
				replacement := []byte("  \"runtime_enforced\": false,\n  \"source_baseline\": \"" + workspaceEffectSourceBase + "\",\n")
				return workspaceEffectReplaceOnceForTest(t, data, old, replacement)
			},
		},
	}
	for _, test := range rawCases {
		t.Run(test.name, func(t *testing.T) {
			_, err := workspaceEffectLoadManifest(test.edit(canonical))
			workspaceEffectRequireMismatch(t, err, test.kind)
		})
	}

	structCases := []struct {
		name string
		kind string
		edit func(*workspaceEffectManifest)
	}{
		{name: "runtime enforcement claim", kind: "unknown_schema", edit: func(m *workspaceEffectManifest) { m.RuntimeEnforced = true }},
		{name: "missing scan root", kind: "unknown_schema", edit: func(m *workspaceEffectManifest) { m.ScanRoots = m.ScanRoots[1:] }},
		{name: "missing build profile", kind: "unknown_schema", edit: func(m *workspaceEffectManifest) { m.BuildProfiles = m.BuildProfiles[1:] }},
		{name: "missing sensitive pattern", kind: "unknown_schema", edit: func(m *workspaceEffectManifest) { m.SensitiveFilePatterns = m.SensitiveFilePatterns[1:] }},
		{name: "missing watched sink", kind: "unknown_schema", edit: func(m *workspaceEffectManifest) { m.WatchedSinks = m.WatchedSinks[1:] }},
		{name: "missing family", kind: "unknown_family", edit: func(m *workspaceEffectManifest) { m.Families = m.Families[1:] }},
		{name: "missing deferred surface", kind: "unknown_schema", edit: func(m *workspaceEffectManifest) { m.DeferredSurfaces = nil }},
		{name: "unknown root kind", kind: "unknown_schema", edit: func(m *workspaceEffectManifest) { m.ScanRoots[0].Kind = "directory" }},
		{name: "unknown family", kind: "unknown_family", edit: func(m *workspaceEffectManifest) { m.Families[0].Name = "other" }},
		{name: "unknown disposition", kind: "unknown_disposition", edit: func(m *workspaceEffectManifest) { m.Families[0].FutureDisposition = "already_safe" }},
		{name: "mismatched observation state", kind: "unknown_schema", edit: func(m *workspaceEffectManifest) { m.Families[0].ObservationState = "no_in_process_site_observed" }},
		{name: "invalid site path", kind: "unknown_schema", edit: func(m *workspaceEffectManifest) { m.Sites[0].Path = "../outside.go" }},
		{name: "control rune in path", kind: "unknown_schema", edit: func(m *workspaceEffectManifest) { m.Sites[0].Path += "\x1b" }},
		{name: "unknown invocation", kind: "unknown_schema", edit: func(m *workspaceEffectManifest) { m.Sites[0].InvocationKind = "spawn" }},
		{name: "mismatched evidence layer", kind: "unknown_schema", edit: func(m *workspaceEffectManifest) {
			if m.Sites[0].EvidenceLayer == "leaf" {
				m.Sites[0].EvidenceLayer = "semantic_boundary"
			} else {
				m.Sites[0].EvidenceLayer = "leaf"
			}
		}},
		{name: "site family mismatch", kind: "", edit: func(m *workspaceEffectManifest) { m.Sites[0].Family = "other" }},
		{name: "site disposition mismatch", kind: "", edit: func(m *workspaceEffectManifest) { m.Sites[0].FutureDisposition = "shared_participation_required" }},
		{name: "unknown exclusion reason", kind: "unknown_schema", edit: func(m *workspaceEffectManifest) { m.Exclusions[0].Reason = "harmless" }},
		{name: "unsorted roots", kind: "unknown_schema", edit: func(m *workspaceEffectManifest) { m.ScanRoots[0], m.ScanRoots[1] = m.ScanRoots[1], m.ScanRoots[0] }},
		{name: "unsorted sites", kind: "unknown_schema", edit: func(m *workspaceEffectManifest) { m.Sites[0], m.Sites[1] = m.Sites[1], m.Sites[0] }},
		{name: "unsorted profiles", kind: "unknown_schema", edit: func(m *workspaceEffectManifest) {
			m.Sites[0].BuildProfiles[0], m.Sites[0].BuildProfiles[1] = m.Sites[0].BuildProfiles[1], m.Sites[0].BuildProfiles[0]
		}},
		{name: "duplicate site ID", kind: "", edit: func(m *workspaceEffectManifest) { m.Sites = append(m.Sites, m.Sites[len(m.Sites)-1]) }},
		{name: "duplicate exclusion ID", kind: "", edit: func(m *workspaceEffectManifest) {
			m.Exclusions = append(m.Exclusions, m.Exclusions[len(m.Exclusions)-1])
		}},
	}
	for _, test := range structCases {
		t.Run(test.name, func(t *testing.T) {
			_, manifest := workspaceEffectReadManifestForTest(t)
			test.edit(manifest)
			_, err := workspaceEffectLoadManifest(workspaceEffectMarshalManifestForTest(t, manifest))
			workspaceEffectRequireMismatch(t, err, test.kind)
		})
	}

	for index, separator := range []string{"\u2028", "\u2029"} {
		t.Run("line-breaking manifest path "+strconv.Itoa(index), func(t *testing.T) {
			_, manifest := workspaceEffectReadManifestForTest(t)
			manifest.Sites[0].Path += separator
			manifest.Sites[0].ID = workspaceEffectSiteID(workspaceEffectDetectedFromSite(manifest.Sites[0]))
			sort.Slice(manifest.Sites, func(i, j int) bool { return manifest.Sites[i].ID < manifest.Sites[j].ID })
			_, err := workspaceEffectLoadManifest(workspaceEffectMarshalManifestForTest(t, manifest))
			workspaceEffectRequireMismatch(t, err, "unknown_schema")
			workspaceEffectAssertSecretAbsent(t, err, separator)
		})
	}

	t.Run("forbidden build-unselected field", func(t *testing.T) {
		_, manifest := workspaceEffectReadManifestForTest(t)
		fixturePath := "cmd/bd/unrepresented_fixture.go"
		manifest.Exclusions = append(manifest.Exclusions, workspaceEffectExclusion{
			Kind:   "build_unselected_file",
			ID:     workspaceEffectUnselectedID(fixturePath),
			Path:   fixturePath,
			Reason: "unrepresented_build_constraint",
		})
		sort.Slice(manifest.Exclusions, func(i, j int) bool {
			left, right := manifest.Exclusions[i], manifest.Exclusions[j]
			if left.Kind != right.Kind {
				return left.Kind < right.Kind
			}
			if left.Path != right.Path {
				return left.Path < right.Path
			}
			return left.ID < right.ID
		})
		data := workspaceEffectMarshalManifestForTest(t, manifest)
		needle := []byte("      \"reason\": \"unrepresented_build_constraint\"\n")
		replacement := []byte("      \"reason\": \"unrepresented_build_constraint\",\n      \"callee\": \"os.Remove\"\n")
		data = workspaceEffectReplaceOnceForTest(t, data, needle, replacement)
		_, err := workspaceEffectLoadManifest(data)
		workspaceEffectRequireMismatch(t, err, "unknown_schema")
	})

	t.Run("valid exclusion reclassification is stale", func(t *testing.T) {
		_, manifest := workspaceEffectReadManifestForTest(t)
		if manifest.Exclusions[0].Reason == "diagnostic_only" {
			manifest.Exclusions[0].Reason = "diagnostic_artifact"
		} else {
			manifest.Exclusions[0].Reason = "diagnostic_only"
		}
		_, err := workspaceEffectLoadManifest(workspaceEffectMarshalManifestForTest(t, manifest))
		workspaceEffectRequireMismatch(t, err, "stale_entry")
	})

	t.Run("valid family reclassification is stale", func(t *testing.T) {
		_, manifest := workspaceEffectReadManifestForTest(t)
		siteIndex := -1
		for index, site := range manifest.Sites {
			if site.Family == "backup_restore" {
				siteIndex = index
				break
			}
		}
		if siteIndex < 0 {
			t.Fatal("family fixture missing")
		}
		manifest.Sites[siteIndex].Family = "doctor_fix"
		for index := range manifest.Families {
			switch manifest.Families[index].Name {
			case "backup_restore":
				manifest.Families[index].SiteCount--
			case "doctor_fix":
				manifest.Families[index].SiteCount++
			}
		}
		_, err := workspaceEffectLoadManifest(workspaceEffectMarshalManifestForTest(t, manifest))
		workspaceEffectRequireMismatch(t, err, "stale_entry")
	})

	t.Run("site-to-exclusion membership change is stale", func(t *testing.T) {
		_, manifest := workspaceEffectReadManifestForTest(t)
		site := manifest.Sites[0]
		manifest.Sites = append([]workspaceEffectSite(nil), manifest.Sites[1:]...)
		for index := range manifest.Families {
			if manifest.Families[index].Name == site.Family {
				manifest.Families[index].SiteCount--
			}
		}
		manifest.Exclusions = append(manifest.Exclusions, workspaceEffectExclusion{
			Kind:            "observed_site",
			ID:              site.ID,
			Path:            site.Path,
			EnclosingSymbol: site.EnclosingSymbol,
			Callee:          site.Callee,
			EvidenceLayer:   site.EvidenceLayer,
			InvocationKind:  site.InvocationKind,
			CallShapeSHA256: site.CallShapeSHA256,
			Ordinal:         site.Ordinal,
			BuildProfiles:   site.BuildProfiles,
			Reason:          "diagnostic_only",
		})
		sort.Slice(manifest.Exclusions, func(i, j int) bool {
			left, right := manifest.Exclusions[i], manifest.Exclusions[j]
			if left.Kind != right.Kind {
				return left.Kind < right.Kind
			}
			if left.Path != right.Path {
				return left.Path < right.Path
			}
			return left.ID < right.ID
		})
		_, err := workspaceEffectLoadManifest(workspaceEffectMarshalManifestForTest(t, manifest))
		workspaceEffectRequireMismatch(t, err, "stale_entry")
	})
}

func workspaceEffectRepositoryFromManifest(manifest *workspaceEffectManifest) *workspaceEffectRepositoryResult {
	repository := &workspaceEffectRepositoryResult{
		WatchedSinks: append([]workspaceEffectWatchedSink(nil), manifest.WatchedSinks...),
	}
	for _, site := range manifest.Sites {
		repository.Sites = append(repository.Sites, workspaceEffectDetectedFromSite(site))
	}
	for _, exclusion := range manifest.Exclusions {
		if exclusion.Kind == "observed_site" {
			repository.Sites = append(repository.Sites, workspaceEffectDetectedFromExclusion(exclusion))
		} else {
			repository.UnselectedFiles = append(repository.UnselectedFiles, exclusion.Path)
		}
	}
	sort.Slice(repository.Sites, func(i, j int) bool { return repository.Sites[i].ID < repository.Sites[j].ID })
	sort.Strings(repository.UnselectedFiles)
	return repository
}

func workspaceEffectSeededSite(site workspaceEffectDetectedSite, suffix string) workspaceEffectDetectedSite {
	seeded := site
	seeded.EnclosingSymbol += "$func#" + suffix
	seeded.ID = workspaceEffectSiteID(seeded)
	seeded.BuildProfiles = append([]string(nil), site.BuildProfiles...)
	return seeded
}

func TestWorkspaceEffectCensusRejectsBidirectionalDrift(t *testing.T) {
	_, manifest := workspaceEffectReadManifestForTest(t)
	repository := workspaceEffectRepositoryFromManifest(manifest)
	if err := workspaceEffectCompareManifest(manifest, repository); err != nil {
		t.Fatal(err)
	}

	t.Run("uncensused repository site", func(t *testing.T) {
		mutated := *repository
		mutated.Sites = append([]workspaceEffectDetectedSite(nil), repository.Sites...)
		mutated.Sites = append(mutated.Sites, workspaceEffectSeededSite(repository.Sites[0], "7001"))
		workspaceEffectRequireMismatch(t, workspaceEffectCompareManifest(manifest, &mutated), "uncensused_site")
	})

	t.Run("stale manifest site", func(t *testing.T) {
		mutated := *repository
		mutated.Sites = append([]workspaceEffectDetectedSite(nil), repository.Sites[1:]...)
		workspaceEffectRequireMismatch(t, workspaceEffectCompareManifest(manifest, &mutated), "stale_entry")
	})

	t.Run("missing manifest row", func(t *testing.T) {
		_, mutated := workspaceEffectReadManifestForTest(t)
		mutated.Sites = append([]workspaceEffectSite(nil), mutated.Sites[1:]...)
		workspaceEffectRequireMismatch(t, workspaceEffectCompareManifest(mutated, repository), "uncensused_site")
	})

	t.Run("profile context disagreement", func(t *testing.T) {
		mutated := *repository
		mutated.Sites = append([]workspaceEffectDetectedSite(nil), repository.Sites...)
		mutated.Sites[0].BuildProfiles = []string{"linux_amd64_cgo"}
		workspaceEffectRequireMismatch(t, workspaceEffectCompareManifest(manifest, &mutated), "stale_entry")
	})

	t.Run("stale exclusion", func(t *testing.T) {
		var excludedID string
		for _, exclusion := range manifest.Exclusions {
			if exclusion.Kind == "observed_site" {
				excludedID = exclusion.ID
				break
			}
		}
		mutated := *repository
		mutated.Sites = nil
		for _, site := range repository.Sites {
			if site.ID != excludedID {
				mutated.Sites = append(mutated.Sites, site)
			}
		}
		workspaceEffectRequireMismatch(t, workspaceEffectCompareManifest(manifest, &mutated), "stale_entry")
	})

	t.Run("watched registry mismatch", func(t *testing.T) {
		mutated := *repository
		mutated.WatchedSinks = append([]workspaceEffectWatchedSink(nil), repository.WatchedSinks[1:]...)
		workspaceEffectRequireMismatch(t, workspaceEffectCompareManifest(manifest, &mutated), "stale_entry")
	})

	t.Run("build-unselected file is bidirectional", func(t *testing.T) {
		_, mutatedManifest := workspaceEffectReadManifestForTest(t)
		fixturePath := "cmd/bd/unrepresented_fixture.go"
		mutatedManifest.Exclusions = append(mutatedManifest.Exclusions, workspaceEffectExclusion{
			Kind:   "build_unselected_file",
			ID:     workspaceEffectUnselectedID(fixturePath),
			Path:   fixturePath,
			Reason: "unrepresented_build_constraint",
		})
		mutatedRepository := workspaceEffectRepositoryFromManifest(mutatedManifest)
		if err := workspaceEffectCompareManifest(mutatedManifest, mutatedRepository); err != nil {
			t.Fatal(err)
		}
		mutatedRepository.UnselectedFiles = nil
		workspaceEffectRequireMismatch(t, workspaceEffectCompareManifest(mutatedManifest, mutatedRepository), "build_matrix_gap")
	})
}

func workspaceEffectFixtureSitesByCallee(sites []workspaceEffectDetectedSite) map[string][]workspaceEffectDetectedSite {
	result := make(map[string][]workspaceEffectDetectedSite)
	for _, site := range sites {
		result[site.Callee] = append(result[site.Callee], site)
	}
	return result
}

func TestWorkspaceEffectCensusTypedCallResolution(t *testing.T) {
	t.Run("aliases dot imports invocation kinds and scoped methods", func(t *testing.T) {
		syscallImport := ""
		syscallCall := ""
		if runtime.GOOS == "linux" {
			syscallImport = `"syscall"`
			syscallCall = `_ = syscall.Kill(1, syscall.SIGTERM)`
		}
		source := fmt.Sprintf(`package bd
import (
	db "database/sql"
	o "os"
	. "os/exec"
	%s
)
func calls() {
	_, _ = db.Open("driver", "secret-canary")
	_ = db.OpenDB(nil)
	_ = o.WriteFile("x", nil, 0600)
	_ = o.RemoveAll("x")
	file, _ := o.OpenFile("x", 0, 0600)
	_, _ = file.Write(nil)
	cmd := &Cmd{}
	_, _ = cmd.CombinedOutput()
	_, _ = cmd.Output()
	_ = cmd.Run()
	_ = cmd.Start()
	_ = cmd.Wait()
	process := &o.Process{}
	_ = process.Kill()
	_ = process.Signal(o.Kill)
	%s
}
func deferred(cmd *Cmd) { defer cmd.Wait() }
func concurrent(cmd *Cmd) { go cmd.Run() }
`, syscallImport, syscallCall)
		loaded, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/cmd/bd",
			map[string]string{"cmd/bd/doctor_fixture.go": source},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		sites, err := workspaceEffectScanFixture(loaded, loaded)
		if err != nil {
			t.Fatal(err)
		}
		byCallee := workspaceEffectFixtureSitesByCallee(sites)
		expectedCallees := []string{
			"database/sql.Open",
			"database/sql.OpenDB",
			"os.WriteFile",
			"os.RemoveAll",
			"os.(*File).Write",
			"os/exec.(*Cmd).CombinedOutput",
			"os/exec.(*Cmd).Output",
			"os/exec.(*Cmd).Run",
			"os/exec.(*Cmd).Start",
			"os/exec.(*Cmd).Wait",
			"os.(*Process).Kill",
			"os.(*Process).Signal",
		}
		if runtime.GOOS == "linux" {
			expectedCallees = append(expectedCallees, "syscall.Kill")
		}
		for _, callee := range expectedCallees {
			if len(byCallee[callee]) == 0 {
				t.Fatalf("typed fixture did not detect %s", callee)
			}
		}
		kinds := make(map[string]bool)
		for _, site := range append(byCallee["os/exec.(*Cmd).Wait"], byCallee["os/exec.(*Cmd).Run"]...) {
			kinds[site.InvocationKind] = true
		}
		for _, kind := range []string{"call", "defer", "go"} {
			if !kinds[kind] {
				t.Fatalf("typed fixture did not detect %s invocation", kind)
			}
		}
	})

	t.Run("duplicate identical calls receive lexical ordinals", func(t *testing.T) {
		loaded, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/cmd/bd",
			map[string]string{"cmd/bd/doctor_fixture.go": `package bd
import renamed "os"
func duplicate() { _ = renamed.Remove("one"); _ = renamed.Remove("two") }
`},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		sites, err := workspaceEffectScanFixture(loaded, loaded)
		if err != nil {
			t.Fatal(err)
		}
		removeSites := workspaceEffectFixtureSitesByCallee(sites)["os.Remove"]
		if len(removeSites) != 2 || removeSites[0].Ordinal != 1 || removeSites[1].Ordinal != 2 {
			t.Fatal("duplicate typed calls did not retain distinct lexical ordinals")
		}
	})

	t.Run("one and two-level wrappers add only the direct inner site", func(t *testing.T) {
		loaded, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/cmd/bd",
			map[string]string{"cmd/bd/doctor_fixture.go": `package bd
import "os"
func inner() { _ = os.Remove("secret-canary") }
func outer() { inner() }
func top() { outer() }
`},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		sites, err := workspaceEffectScanFixture(loaded, loaded)
		if err != nil {
			t.Fatal(err)
		}
		if len(sites) != 1 || sites[0].Callee != "os.Remove" || !strings.HasSuffix(sites[0].EnclosingSymbol, ".inner") {
			t.Fatal("wrapper fixture did not identify only the innermost watched call")
		}
		_, manifest := workspaceEffectReadManifestForTest(t)
		repository := workspaceEffectRepositoryFromManifest(manifest)
		repository.Sites = append(repository.Sites, sites[0])
		workspaceEffectRequireMismatch(t, workspaceEffectCompareManifest(manifest, repository), "uncensused_site")
	})

	valueCases := map[string]string{
		"function value": `package bd
import "os"
func escape() { value := os.Remove; _ = value }
`,
		"registration": `package bd
import "os"
func consume(any) {}
func escape() { consume(os.Remove) }
`,
		"method value": `package bd
import "os"
func escape(file *os.File) { value := file.Write; _ = value }
`,
	}
	for name, source := range valueCases {
		t.Run(name+" fails closed", func(t *testing.T) {
			loaded, err := workspaceEffectCompileFixturePackage(
				workspaceEffectModulePath+"/cmd/bd",
				map[string]string{"cmd/bd/doctor_fixture.go": source},
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = workspaceEffectScanFixture(loaded, loaded)
			workspaceEffectRequireMismatch(t, err, "unresolved_watched_reference")
		})
	}

	t.Run("negative source controls remain outside the census", func(t *testing.T) {
		loaded, err := workspaceEffectCompileFixturePackage(
			workspaceEffectModulePath+"/cmd/bd",
			map[string]string{"cmd/bd/negative.go": `package bd
// os.Remove("comment-only")
var text = "os.Remove(credential-only)"
`},
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		sites, err := workspaceEffectScanFixture(loaded, loaded)
		if err != nil || len(sites) != 0 {
			t.Fatal("comment or string negative control entered the census")
		}
		for _, file := range []string{"cmd/bd/x_test.go", "cmd/bd/testdata/x.go", "cmd/bd/vendor/x.go"} {
			if workspaceEffectProductionFile(file) {
				t.Fatalf("negative production-file control was selected: %s", file)
			}
		}
	})
}
