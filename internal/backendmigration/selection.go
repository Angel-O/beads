// Package backendmigration classifies narrowly supported migration source
// shapes without opening either storage provider.
package backendmigration

import (
	"errors"
	"os"
	"path/filepath"
	"unicode"
	"unicode/utf8"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/workspaceidentity"
)

// SelectorKind identifies how a caller selected the source workspace.
type SelectorKind uint8

const (
	SelectorPhysicalWorkspace SelectorKind = iota + 1
	SelectorDatabase
	SelectorWorkspaceEnv
	SelectorWorktreeShared
	SelectorAmbiguous
)

// AmbientSelection records whether the caller observed another source-selection authority.
type AmbientSelection uint8

const (
	AmbientSelectionUnknown AmbientSelection = iota
	AmbientSelectionAbsent
	AmbientSelectionPresent
)

// SelectionRequest describes an explicit migration source selection.
type SelectionRequest struct {
	Workspace        string
	TargetBackend    string
	Selector         SelectorKind
	AmbientSelection AmbientSelection
}

// SourceShapeCandidate is an inert classification, not migration authority.
type SourceShapeCandidate struct {
	SourceBackend string
	TargetBackend string
}

// RefusalCode is a stable pre-effect migration admission code.
type RefusalCode string

const (
	CodePairUnsupported           RefusalCode = "backend_migration_pair_unsupported"
	CodeWorkspaceShapeUnsupported RefusalCode = "backend_migration_workspace_shape_unsupported"
	CodePlatformUnsupported       RefusalCode = "backend_migration_platform_unsupported"
	CodeWorkspaceChanged          RefusalCode = "backend_migration_workspace_changed"
	CodeWorkspaceUnverifiable     RefusalCode = "backend_migration_workspace_unverifiable"
	CodeCredentialInLocator       RefusalCode = "backend_migration_credential_in_locator"
	CodeCredentialsRequired       RefusalCode = "backend_migration_credentials_required"
)

// RefusalReason is an allowlisted explanation category with no dynamic text.
type RefusalReason string

const (
	ReasonTargetBackend                RefusalReason = "target_backend"
	ReasonSourceBackend                RefusalReason = "source_backend"
	ReasonOperatingSystem              RefusalReason = "operating_system"
	ReasonWSL                          RefusalReason = "wsl"
	ReasonEmbeddedBuild                RefusalReason = "embedded_build"
	ReasonFilesystem                   RefusalReason = "filesystem"
	ReasonSelector                     RefusalReason = "selector"
	ReasonAmbientSelection             RefusalReason = "ambient_selection"
	ReasonWorkspaceAlias               RefusalReason = "workspace_alias"
	ReasonRedirect                     RefusalReason = "redirect"
	ReasonLegacyMetadata               RefusalReason = "legacy_metadata"
	ReasonShadowLegacyMetadata         RefusalReason = "shadow_legacy_metadata"
	ReasonMetadataValues               RefusalReason = "metadata_values"
	ReasonDoltMode                     RefusalReason = "dolt_mode"
	ReasonCustomProviderPath           RefusalReason = "custom_provider_path"
	ReasonServerConfiguration          RefusalReason = "server_configuration"
	ReasonForeignProviderConfiguration RefusalReason = "foreign_provider_configuration"
	ReasonServerArtifact               RefusalReason = "server_artifact"
	ReasonProviderPath                 RefusalReason = "provider_path"
	ReasonWorkspaceObservation         RefusalReason = "workspace_observation"
	ReasonRequest                      RefusalReason = "request"
	ReasonWorkspace                    RefusalReason = "workspace"
	ReasonMetadata                     RefusalReason = "metadata"
	ReasonProvider                     RefusalReason = "provider"
	ReasonPlatformProbe                RefusalReason = "platform_probe"
	ReasonFilesystemProbe              RefusalReason = "filesystem_probe"
	ReasonCleanup                      RefusalReason = "cleanup"
	ReasonTargetLocatorSource          RefusalReason = "target_locator_source"
	ReasonTargetSchemaSource           RefusalReason = "target_schema_source"
	ReasonTargetLocator                RefusalReason = "target_locator"
	ReasonTargetCredential             RefusalReason = "target_credential"
	ReasonTargetTransport              RefusalReason = "target_transport"
	ReasonTargetOptions                RefusalReason = "target_options"
	ReasonTargetSchema                 RefusalReason = "target_schema"
	ReasonBindingClosed                RefusalReason = "binding_closed"
)

const effectNone = "none"

// Refusal is the sole typed negative result. Error deliberately renders only
// its stable code; causes are available only through errors.Is/errors.As.
type Refusal struct {
	Code      RefusalCode
	Reason    RefusalReason
	Retryable bool
	Effect    string
	causes    refusalCauseMask
}

func (r *Refusal) Error() string { return string(r.Code) }

// Is exposes only allowlisted sentinel identities. Raw operational errors are
// deliberately not retained in a Refusal, so unwrapping cannot reveal paths,
// metadata values, locators, or OS error text.
func (r *Refusal) Is(target error) bool {
	if r == nil {
		return false
	}
	switch target {
	case workspaceidentity.ErrCleanup:
		return r.causes&causeCleanup != 0
	case workspaceidentity.ErrChanged:
		return r.causes&causeChanged != 0
	case workspaceidentity.ErrIneligible:
		return r.causes&causeIneligible != 0
	case workspaceidentity.ErrUnsupported:
		return r.causes&causeWorkspaceUnsupported != 0
	case workspaceidentity.ErrUnverifiable:
		return r.causes&causeUnverifiable != 0
	case workspaceidentity.ErrClosed:
		return r.causes&causeClosed != 0
	case os.ErrNotExist:
		return r.causes&causeNotExist != 0
	case os.ErrPermission:
		return r.causes&causePermission != 0
	case os.ErrExist:
		return r.causes&causeExist != 0
	case os.ErrClosed:
		return r.causes&causeOSClosed != 0
	case errors.ErrUnsupported:
		return r.causes&causeStandardUnsupported != 0
	default:
		return false
	}
}

type refusalCauseMask uint16

const (
	causeCleanup refusalCauseMask = 1 << iota
	causeChanged
	causeIneligible
	causeWorkspaceUnsupported
	causeUnverifiable
	causeClosed
	causeNotExist
	causePermission
	causeExist
	causeOSClosed
	causeStandardUnsupported
)

type sourceWitness interface {
	Revalidate() error
	InspectEmbeddedDoltFilesystem() (workspaceidentity.FilesystemSnapshot, error)
	Close() error
}

type selectionDependencies struct {
	platform      func() (nativeLinux, wsl bool, err error)
	embeddedBuild bool
	observe       func(string) (shapeObservation, error)
	bind          func(string, int64) (sourceWitness, []byte, error)
	parseMetadata func([]byte) (*configfile.Config, error)
	inspectFS     func(sourceWitness) (workspaceidentity.FilesystemSnapshot, error)
	equalFS       func(workspaceidentity.FilesystemSnapshot, workspaceidentity.FilesystemSnapshot) bool
	qualifiedFS   func(workspaceidentity.FilesystemSnapshot) bool
}

// retainedSourceAdmission is the private successful state shared by W2 and W3.
// W2 never requests retention; W3 takes ownership of the still-live witness.
type retainedSourceAdmission struct {
	witness    sourceWitness
	workspace  string
	database   string
	shape      shapeObservation
	filesystem workspaceidentity.FilesystemSnapshot
	observe    func(string) (shapeObservation, error)
	inspectFS  func(sourceWitness) (workspaceidentity.FilesystemSnapshot, error)
	equalFS    func(workspaceidentity.FilesystemSnapshot, workspaceidentity.FilesystemSnapshot) bool
}

func productionSelectionDependencies() selectionDependencies {
	return selectionDependencies{
		platform:      probeNativeLinux,
		embeddedBuild: embeddedBuildCapable,
		observe:       observeShape,
		bind: func(path string, limit int64) (sourceWitness, []byte, error) {
			return workspaceidentity.BindExisting(path, limit)
		},
		parseMetadata: configfile.ParseReadOnlyMetadata,
		inspectFS: func(witness sourceWitness) (workspaceidentity.FilesystemSnapshot, error) {
			return witness.InspectEmbeddedDoltFilesystem()
		},
		equalFS: func(left, right workspaceidentity.FilesystemSnapshot) bool { return left.Equal(right) },
		qualifiedFS: func(snapshot workspaceidentity.FilesystemSnapshot) bool {
			return snapshot.Qualified()
		},
	}
}

// InspectSourceShape classifies an explicit source selection without opening a
// provider, reading ambient configuration, or retaining authority after return.
func InspectSourceShape(request SelectionRequest) (SourceShapeCandidate, error) {
	return inspectSourceShapeWith(request, productionSelectionDependencies())
}

func inspectSourceShapeWith(request SelectionRequest, deps selectionDependencies) (SourceShapeCandidate, error) {
	return inspectSourceShapeRetainedWith(request, deps, nil)
}

func inspectSourceShapeRetainedWith(
	request SelectionRequest,
	deps selectionDependencies,
	retained **retainedSourceAdmission,
) (SourceShapeCandidate, error) {
	if retained != nil {
		*retained = nil
	}
	if request.TargetBackend != configfile.BackendPostgres {
		return refuse(CodePairUnsupported, ReasonTargetBackend, nil)
	}
	if deps.platform == nil {
		return refuse(CodeWorkspaceUnverifiable, ReasonPlatformProbe, nil)
	}
	nativeLinux, wsl, err := deps.platform()
	if err != nil {
		return refuse(CodeWorkspaceUnverifiable, ReasonPlatformProbe, err)
	}
	if !nativeLinux {
		return refuse(CodePlatformUnsupported, ReasonOperatingSystem, nil)
	}
	if wsl {
		return refuse(CodePlatformUnsupported, ReasonWSL, nil)
	}
	if !deps.embeddedBuild {
		return refuse(CodePlatformUnsupported, ReasonEmbeddedBuild, nil)
	}
	if request.Selector != SelectorPhysicalWorkspace {
		return refuse(CodeWorkspaceShapeUnsupported, ReasonSelector, nil)
	}
	switch request.AmbientSelection {
	case AmbientSelectionAbsent:
	case AmbientSelectionPresent:
		return refuse(CodeWorkspaceShapeUnsupported, ReasonAmbientSelection, nil)
	default:
		return refuse(CodeWorkspaceUnverifiable, ReasonRequest, nil)
	}
	if err := validateWorkspaceText(request.Workspace); err != nil {
		return refuse(CodeWorkspaceUnverifiable, ReasonRequest, err)
	}
	if !filepath.IsAbs(request.Workspace) || filepath.Clean(request.Workspace) != request.Workspace ||
		filepath.Base(request.Workspace) != ".beads" {
		return refuse(CodeWorkspaceShapeUnsupported, ReasonWorkspaceAlias, nil)
	}
	if deps.observe == nil || deps.bind == nil || deps.parseMetadata == nil || deps.inspectFS == nil ||
		deps.equalFS == nil || deps.qualifiedFS == nil {
		return refuse(CodeWorkspaceUnverifiable, ReasonRequest, nil)
	}

	initial, err := deps.observe(request.Workspace)
	if err != nil {
		return SourceShapeCandidate{}, classifyObservationError(err)
	}
	prebindCode, prebindReason := classifyPrebindShape(request.Workspace, initial)
	if prebindReason != "" {
		repeated, repeatErr := deps.observe(request.Workspace)
		if repeatErr != nil {
			return SourceShapeCandidate{}, classifyObservationError(repeatErr)
		}
		if !initial.Equal(repeated) {
			return SourceShapeCandidate{}, changedRefusal(workspaceidentity.ErrChanged)
		}
		return refuse(prebindCode, prebindReason, nil)
	}

	witness, metadata, err := deps.bind(request.Workspace, workspaceidentity.MaxMetadataBytes)
	if err != nil {
		return SourceShapeCandidate{}, classifyWitnessError(err)
	}
	if witness == nil {
		return refuse(CodeWorkspaceUnverifiable, ReasonMetadata, nil)
	}
	return inspectBoundSource(request, deps, witness, metadata, initial, retained)
}

func inspectBoundSource(
	request SelectionRequest,
	deps selectionDependencies,
	witness sourceWitness,
	metadata []byte,
	initial shapeObservation,
	retained **retainedSourceAdmission,
) (candidate SourceShapeCandidate, returnErr error) {
	owned := true
	defer func() {
		if !owned {
			return
		}
		if closeErr := witness.Close(); closeErr != nil {
			candidate = SourceShapeCandidate{}
			returnErr = cleanupRefusal(returnErr, closeErr)
		}
	}()

	if err := witness.Revalidate(); err != nil {
		return SourceShapeCandidate{}, classifyWitnessError(err)
	}
	authoritative, err := deps.observe(request.Workspace)
	if err != nil {
		return SourceShapeCandidate{}, classifyObservationError(err)
	}
	if err := witness.Revalidate(); err != nil {
		return SourceShapeCandidate{}, classifyWitnessError(err)
	}
	if !initial.Equal(authoritative) {
		return SourceShapeCandidate{}, changedRefusal(workspaceidentity.ErrChanged)
	}

	cfg, err := deps.parseMetadata(metadata)
	if err != nil || cfg == nil {
		return stabilizeBoundResult(request, deps, witness, authoritative,
			SourceShapeCandidate{}, refusal(CodeWorkspaceUnverifiable, ReasonMetadata, false, err))
	}
	staticErr := classifyMetadataAndShape(cfg, request.Workspace, authoritative)
	if staticErr != nil {
		return stabilizeBoundResult(request, deps, witness, authoritative, SourceShapeCandidate{}, staticErr)
	}

	firstFilesystem, err := deps.inspectFS(witness)
	if err != nil {
		return SourceShapeCandidate{}, classifyFilesystemError(err)
	}
	if err := witness.Revalidate(); err != nil {
		return SourceShapeCandidate{}, classifyWitnessError(err)
	}
	finalShape, err := deps.observe(request.Workspace)
	if err != nil {
		return SourceShapeCandidate{}, classifyObservationError(err)
	}
	if err := witness.Revalidate(); err != nil {
		return SourceShapeCandidate{}, classifyWitnessError(err)
	}
	if !authoritative.Equal(finalShape) {
		return SourceShapeCandidate{}, changedRefusal(workspaceidentity.ErrChanged)
	}
	secondFilesystem, err := deps.inspectFS(witness)
	if err != nil {
		return SourceShapeCandidate{}, classifyFilesystemError(err)
	}
	if !deps.equalFS(firstFilesystem, secondFilesystem) {
		return SourceShapeCandidate{}, changedRefusal(workspaceidentity.ErrChanged)
	}
	if !deps.qualifiedFS(secondFilesystem) {
		return refuse(CodePlatformUnsupported, ReasonFilesystem, nil)
	}
	candidate = SourceShapeCandidate{SourceBackend: configfile.BackendDolt, TargetBackend: configfile.BackendPostgres}
	if retained != nil {
		*retained = &retainedSourceAdmission{
			witness:    witness,
			workspace:  request.Workspace,
			database:   cfg.DoltDatabase,
			shape:      finalShape,
			filesystem: secondFilesystem,
			observe:    deps.observe,
			inspectFS:  deps.inspectFS,
			equalFS:    deps.equalFS,
		}
		owned = false
	}
	return candidate, nil
}

func stabilizeBoundResult(
	request SelectionRequest,
	deps selectionDependencies,
	witness sourceWitness,
	authoritative shapeObservation,
	candidate SourceShapeCandidate,
	primary error,
) (SourceShapeCandidate, error) {
	if err := witness.Revalidate(); err != nil {
		return SourceShapeCandidate{}, classifyWitnessError(err)
	}
	finalShape, err := deps.observe(request.Workspace)
	if err != nil {
		return SourceShapeCandidate{}, classifyObservationError(err)
	}
	if err := witness.Revalidate(); err != nil {
		return SourceShapeCandidate{}, classifyWitnessError(err)
	}
	if !authoritative.Equal(finalShape) {
		return SourceShapeCandidate{}, changedRefusal(workspaceidentity.ErrChanged)
	}
	return candidate, primary
}

func classifyPrebindShape(workspace string, observation shapeObservation) (RefusalCode, RefusalReason) {
	switch {
	case observation.root.canonical != workspace:
		return CodeWorkspaceShapeUnsupported, ReasonWorkspaceAlias
	case observation.redirect.present:
		return CodeWorkspaceShapeUnsupported, ReasonRedirect
	case !observation.current.present && observation.legacy.present:
		return CodeWorkspaceShapeUnsupported, ReasonLegacyMetadata
	case observation.current.present && observation.legacy.present:
		return CodeWorkspaceShapeUnsupported, ReasonShadowLegacyMetadata
	case !observation.current.present:
		return CodeWorkspaceUnverifiable, ReasonMetadata
	default:
		return "", ""
	}
}

func classifyMetadataAndShape(cfg *configfile.Config, workspace string, observation shapeObservation) error {
	if cfg.Backend == "" {
		return refusal(CodeWorkspaceShapeUnsupported, ReasonMetadataValues, false, nil)
	}
	if cfg.Backend != configfile.BackendDolt {
		return refusal(CodePairUnsupported, ReasonSourceBackend, false, nil)
	}
	if cfg.DoltMode != configfile.DoltModeEmbedded {
		return refusal(CodeWorkspaceShapeUnsupported, ReasonDoltMode, false, nil)
	}
	if cfg.DoltDataDir != "" {
		return refusal(CodeWorkspaceShapeUnsupported, ReasonCustomProviderPath, false, nil)
	}
	if filepath.IsAbs(cfg.Database) {
		return refusal(CodeWorkspaceShapeUnsupported, ReasonCustomProviderPath, false, nil)
	}
	if cfg.Database != "" && cfg.Database != configfile.BackendDolt {
		return refusal(CodeWorkspaceShapeUnsupported, ReasonMetadataValues, false, nil)
	}
	if cfg.DoltServerHost != "" || cfg.DoltServerPort != 0 || cfg.DoltServerSocket != "" || cfg.DoltServerUser != "" ||
		cfg.DoltServerTLS || cfg.DoltRemotesAPIPort != 0 || cfg.GlobalDoltDatabase != "" || cfg.GlobalProjectID != "" {
		return refusal(CodeWorkspaceShapeUnsupported, ReasonServerConfiguration, false, nil)
	}
	if cfg.PostgresDSN != "" || cfg.PostgresSchema != "" || cfg.MySQLDSN != "" || cfg.MySQLDatabase != "" || cfg.SQLitePath != "" {
		return refusal(CodeWorkspaceShapeUnsupported, ReasonForeignProviderConfiguration, false, nil)
	}
	for _, artifact := range observation.artifacts {
		if artifact.present {
			return refusal(CodeWorkspaceShapeUnsupported, ReasonServerArtifact, false, nil)
		}
	}
	if !observation.provider.present {
		return refusal(CodeWorkspaceUnverifiable, ReasonProvider, false, nil)
	}
	if observation.provider.canonical != filepath.Join(workspace, "embeddeddolt") {
		return refusal(CodeWorkspaceShapeUnsupported, ReasonProviderPath, false, nil)
	}
	return nil
}

func classifyObservationError(err error) error {
	if errors.Is(err, workspaceidentity.ErrCleanup) {
		return refusal(CodeWorkspaceUnverifiable, ReasonCleanup, false, err)
	}
	var failure *shapeObservationError
	if errors.As(err, &failure) {
		return refusal(CodeWorkspaceUnverifiable, failure.reason, false, failure.cause)
	}
	return refusal(CodeWorkspaceUnverifiable, ReasonWorkspace, false, err)
}

func classifyWitnessError(err error) error {
	switch {
	case errors.Is(err, workspaceidentity.ErrCleanup):
		return refusal(CodeWorkspaceUnverifiable, ReasonCleanup, false, err)
	case errors.Is(err, workspaceidentity.ErrChanged), errors.Is(err, workspaceidentity.ErrIneligible):
		return refusal(CodeWorkspaceChanged, ReasonWorkspaceObservation, true, err)
	case errors.Is(err, workspaceidentity.ErrUnsupported):
		return refusal(CodePlatformUnsupported, ReasonFilesystem, false, err)
	default:
		return refusal(CodeWorkspaceUnverifiable, ReasonMetadata, false, err)
	}
}

func classifyFilesystemError(err error) error {
	if errors.Is(err, workspaceidentity.ErrCleanup) {
		return refusal(CodeWorkspaceUnverifiable, ReasonCleanup, false, err)
	}
	if errors.Is(err, workspaceidentity.ErrChanged) {
		return changedRefusal(err)
	}
	if errors.Is(err, workspaceidentity.ErrUnsupported) {
		return refusal(CodePlatformUnsupported, ReasonFilesystem, false, err)
	}
	var probeFailure interface{ FilesystemProbeFailure() }
	if errors.As(err, &probeFailure) {
		return refusal(CodeWorkspaceUnverifiable, ReasonFilesystemProbe, false, err)
	}
	return classifyWitnessError(err)
}

func changedRefusal(cause error) error {
	return refusal(CodeWorkspaceChanged, ReasonWorkspaceObservation, true, cause)
}

func cleanupRefusal(primary, cleanup error) error {
	causes := refusalCauseMaskFor(cleanup)
	var prior *Refusal
	if errors.As(primary, &prior) {
		causes |= prior.causes
	}
	return refusalWithCauses(CodeWorkspaceUnverifiable, ReasonCleanup, false, causes)
}

func refuse(code RefusalCode, reason RefusalReason, cause error) (SourceShapeCandidate, error) {
	return SourceShapeCandidate{}, refusal(code, reason, code == CodeWorkspaceChanged, cause)
}

func refusal(code RefusalCode, reason RefusalReason, retryable bool, cause error) *Refusal {
	return refusalWithCauses(code, reason, retryable, refusalCauseMaskFor(cause))
}

func refusalWithCauses(code RefusalCode, reason RefusalReason, retryable bool, causes refusalCauseMask) *Refusal {
	return &Refusal{Code: code, Reason: reason, Retryable: retryable, Effect: effectNone, causes: causes}
}

func refusalCauseMaskFor(err error) refusalCauseMask {
	if err == nil {
		return 0
	}
	var mask refusalCauseMask
	for _, marker := range []struct {
		err error
		bit refusalCauseMask
	}{
		{workspaceidentity.ErrCleanup, causeCleanup},
		{workspaceidentity.ErrChanged, causeChanged},
		{workspaceidentity.ErrIneligible, causeIneligible},
		{workspaceidentity.ErrUnsupported, causeWorkspaceUnsupported},
		{workspaceidentity.ErrUnverifiable, causeUnverifiable},
		{workspaceidentity.ErrClosed, causeClosed},
		{os.ErrNotExist, causeNotExist},
		{os.ErrPermission, causePermission},
		{os.ErrExist, causeExist},
		{os.ErrClosed, causeOSClosed},
		{errors.ErrUnsupported, causeStandardUnsupported},
	} {
		if errors.Is(err, marker.err) {
			mask |= marker.bit
		}
	}
	return mask
}

func validateWorkspaceText(path string) error {
	if path == "" || !utf8.ValidString(path) {
		return errors.New("workspace path is empty or invalid UTF-8")
	}
	for _, value := range path {
		if unicode.IsControl(value) {
			return errors.New("workspace path contains a control character")
		}
	}
	return nil
}

const artifactCount = 6

var fixedArtifacts = [...]struct {
	name      string
	directory bool
}{
	{name: "dolt", directory: true},
	{name: "proxieddb", directory: true},
	{name: "dolt-server.pid"},
	{name: "dolt-server.port"},
	{name: "dolt-server.lock"},
	{name: "dolt-server.log"},
}

type shapeObservation struct {
	root, redirect, current, legacy, provider observedObject
	artifacts                                 [artifactCount]observedObject
}

func (s shapeObservation) Equal(other shapeObservation) bool {
	if !s.root.Equal(other.root) || !s.redirect.Equal(other.redirect) || !s.current.Equal(other.current) ||
		!s.legacy.Equal(other.legacy) || !s.provider.Equal(other.provider) {
		return false
	}
	for i := range s.artifacts {
		if !s.artifacts[i].Equal(other.artifacts[i]) {
			return false
		}
	}
	return true
}

type observedObject struct {
	present        bool
	canonical      string
	info           os.FileInfo
	linkCount      uint64
	linkCountKnown bool
}

func (o observedObject) Equal(other observedObject) bool {
	if o.present != other.present {
		return false
	}
	if !o.present {
		return true
	}
	return o.canonical == other.canonical && o.info != nil && other.info != nil && os.SameFile(o.info, other.info) &&
		o.info.Mode() == other.info.Mode() && o.info.Size() == other.info.Size() && o.info.ModTime().Equal(other.info.ModTime()) &&
		o.linkCountKnown == other.linkCountKnown && o.linkCount == other.linkCount
}

type shapeObservationError struct {
	reason RefusalReason
	cause  error
}

func (e *shapeObservationError) Error() string { return "source shape observation failed" }
func (e *shapeObservationError) Unwrap() error { return e.cause }

func observeShape(workspace string) (shapeObservation, error) {
	var result shapeObservation
	root, err := observeRequiredObject(workspace, ReasonWorkspace, true, false)
	if err != nil {
		return shapeObservation{}, err
	}
	result.root = root

	if result.redirect, err = observeOptionalObject(filepath.Join(workspace, "redirect"), ReasonWorkspace, false, true); err != nil {
		return shapeObservation{}, err
	}
	for i, artifact := range fixedArtifacts {
		result.artifacts[i], err = observeOptionalObject(filepath.Join(workspace, artifact.name), ReasonWorkspace, artifact.directory, !artifact.directory)
		if err != nil {
			return shapeObservation{}, err
		}
	}
	if result.current, err = observeOptionalObject(filepath.Join(workspace, configfile.ConfigFileName), ReasonMetadata, false, true); err != nil {
		return shapeObservation{}, err
	}
	if result.legacy, err = observeOptionalObject(filepath.Join(workspace, "config.json"), ReasonMetadata, false, true); err != nil {
		return shapeObservation{}, err
	}
	result.provider, err = observeOptionalObject(filepath.Join(workspace, "embeddeddolt"), ReasonProvider, true, false)
	if err != nil {
		return shapeObservation{}, err
	}
	return result, nil
}

func observeRequiredObject(path string, reason RefusalReason, directory, regular bool) (observedObject, error) {
	object, err := observeOptionalObject(path, reason, directory, regular)
	if err != nil {
		return observedObject{}, err
	}
	if !object.present {
		return observedObject{}, &shapeObservationError{reason: reason, cause: os.ErrNotExist}
	}
	return object, nil
}

func observeOptionalObject(path string, reason RefusalReason, directory, regular bool) (observedObject, error) {
	named, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return observedObject{}, nil
	}
	if err != nil {
		return observedObject{}, &shapeObservationError{reason: reason, cause: err}
	}
	if named.Mode()&os.ModeSymlink != 0 {
		return observedObject{}, &shapeObservationError{reason: reason, cause: errors.New("symbolic link is not allowed")}
	}
	observation, err := observeMetadataNoFollow(path)
	if err != nil {
		return observedObject{}, &shapeObservationError{reason: reason, cause: err}
	}
	if observation == nil || observation.Info == nil || !os.SameFile(named, observation.Info) {
		return observedObject{}, &shapeObservationError{reason: reason, cause: errors.New("named object changed during observation")}
	}
	if directory && !observation.Info.IsDir() {
		return observedObject{}, &shapeObservationError{reason: reason, cause: errors.New("object is not a directory")}
	}
	if regular {
		if !observation.Info.Mode().IsRegular() || observation.Info.Size() > workspaceidentity.MaxMetadataBytes ||
			!observation.LinkCountKnown || observation.LinkCount != 1 {
			return observedObject{}, &shapeObservationError{reason: reason, cause: errors.New("object is not a bounded single-link regular file")}
		}
	}
	if !directory && !regular {
		return observedObject{}, &shapeObservationError{reason: reason, cause: errors.New("object type contract is unspecified")}
	}
	return observedObject{
		present:        true,
		canonical:      observation.CanonicalPath,
		info:           observation.Info,
		linkCount:      observation.LinkCount,
		linkCountKnown: observation.LinkCountKnown,
	}, nil
}
