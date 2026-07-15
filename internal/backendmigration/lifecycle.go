package backendmigration

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrCommandResourceState   = errors.New("backend migration command resource state is invalid")
	ErrCommandResourceCleanup = errors.New("backend migration command resource cleanup failed")
	ErrCommandExecution       = errors.New("backend migration command failed")
)

const safeCommandResourceScopeText = "backend migration command resources"

type commandResourcePhase uint8

const (
	commandResourceOpen commandResourcePhase = iota + 1
	commandResourceRunning
	commandResourceClosing
	commandResourceClosed
)

// CommandResourceScope owns the resources used by one backend migration
// command callback. W4 deliberately exposes no generic adoption or close API;
// later same-package provider helpers may use the private typed adoption seams.
type CommandResourceScope struct {
	state *commandResourceScopeState
}

type commandResourceScopeState struct {
	mu   sync.Mutex
	cond *sync.Cond

	phase        commandResourcePhase
	binding      lifecycleBinding
	closeActions []func() error

	sourceAdopted    bool
	targetAdopted    bool
	executionStarted bool
	stateFailed      bool
	cleanupFailed    bool
	closeErr         error
}

// BoundProviderCommand runs with one revalidated provider configuration.
type BoundProviderCommand func(context.Context, *CommandResourceScope, BoundProviderConfiguration) error

type lifecycleBinding interface {
	Snapshot() (BoundProviderConfiguration, error)
	Close() error
}

type lifecycleDependencies struct {
	bind func(ProviderConfigurationRequest) (lifecycleBinding, error)
}

func productionLifecycleDependencies() lifecycleDependencies {
	return lifecycleDependencies{
		bind: func(request ProviderConfigurationRequest) (lifecycleBinding, error) {
			binding, err := BindProviderConfiguration(request)
			if binding == nil {
				return nil, err
			}
			return binding, err
		},
	}
}

// WithBoundProviderConfiguration runs one callback with a revalidated W3
// configuration and closes its complete resource scope before returning. It is
// intended to be called from RunE; Cobra post-run hooks never own these
// resources.
func WithBoundProviderConfiguration(ctx context.Context, request ProviderConfigurationRequest, run BoundProviderCommand) error {
	return withBoundProviderConfigurationWith(ctx, request, run, productionLifecycleDependencies())
}

func withBoundProviderConfigurationWith(
	ctx context.Context,
	request ProviderConfigurationRequest,
	run BoundProviderCommand,
	deps lifecycleDependencies,
) error {
	if ctx == nil || run == nil || deps.bind == nil {
		return ErrCommandResourceState
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	scope := newCommandResourceScope()
	binding, bindErr := deps.bind(request)
	if bindErr != nil {
		primary := normalizeW3LifecycleError(bindErr)
		if binding != nil && invokeLifecycleClose(binding.Close) {
			return joinLifecycleResults(primary, ErrCommandResourceCleanup)
		}
		return primary
	}
	if binding == nil {
		return ErrCommandResourceState
	}
	if installErr := scope.installBinding(binding); installErr != nil {
		if invokeLifecycleClose(binding.Close) {
			return joinLifecycleResults(installErr, ErrCommandResourceCleanup)
		}
		return installErr
	}

	return scope.execute(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		configuration, snapshotErr := scope.snapshot()
		if snapshotErr != nil {
			return normalizeW3LifecycleError(snapshotErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if runErr := run(ctx, scope, configuration); runErr != nil {
			return normalizeCommandLifecycleError(runErr)
		}
		return ctx.Err()
	})
}

func newCommandResourceScope() *CommandResourceScope {
	state := &commandResourceScopeState{phase: commandResourceOpen}
	state.cond = sync.NewCond(&state.mu)
	return &CommandResourceScope{state: state}
}

func (s *CommandResourceScope) installBinding(binding lifecycleBinding) error {
	if s == nil || s.state == nil || binding == nil {
		return ErrCommandResourceState
	}
	state := s.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.phase != commandResourceOpen || state.binding != nil || len(state.closeActions) != 0 {
		return ErrCommandResourceState
	}
	state.binding = binding
	state.closeActions = []func() error{binding.Close}
	return nil
}

func (s *CommandResourceScope) execute(body func() error) (result error) {
	if s == nil || s.state == nil || body == nil {
		return ErrCommandResourceState
	}
	state := s.state
	state.mu.Lock()
	if state.phase != commandResourceOpen || state.executionStarted {
		state.mu.Unlock()
		return ErrCommandResourceState
	}
	state.executionStarted = true
	state.phase = commandResourceRunning
	state.mu.Unlock()

	bodyReturned := false
	defer func() {
		_ = recover()
		panicked := !bodyReturned
		s.finishExecution()
		cleanupErr := s.close()
		result = s.combineResult(result, cleanupErr)
		if panicked {
			panic(ErrCommandExecution)
		}
	}()

	result = body()
	bodyReturned = true
	return result
}

func (s *CommandResourceScope) finishExecution() {
	if s == nil || s.state == nil {
		return
	}
	state := s.state
	state.mu.Lock()
	if state.phase == commandResourceRunning {
		state.executionStarted = true
		state.phase = commandResourceOpen
	}
	state.cond.Broadcast()
	state.mu.Unlock()
}

func (s *CommandResourceScope) snapshot() (BoundProviderConfiguration, error) {
	if s == nil || s.state == nil {
		return BoundProviderConfiguration{}, ErrCommandResourceState
	}
	state := s.state
	state.mu.Lock()
	if state.phase != commandResourceRunning || state.binding == nil {
		state.mu.Unlock()
		return BoundProviderConfiguration{}, ErrCommandResourceState
	}
	binding := state.binding
	state.mu.Unlock()
	return binding.Snapshot()
}

func (s *CommandResourceScope) deferSourceClose(closeAction func() error) error {
	return s.adoptCloseAction(false, closeAction)
}

func (s *CommandResourceScope) deferTargetClose(closeAction func() error) error {
	return s.adoptCloseAction(true, closeAction)
}

func (s *CommandResourceScope) adoptCloseAction(target bool, closeAction func() error) error {
	if s == nil || s.state == nil {
		return rejectedLifecycleAdoption(closeAction)
	}
	state := s.state
	state.mu.Lock()
	valid := state.phase == commandResourceRunning && closeAction != nil
	if target {
		valid = valid && state.sourceAdopted && !state.targetAdopted
	} else {
		valid = valid && !state.sourceAdopted && !state.targetAdopted
	}
	if valid {
		state.closeActions = append(state.closeActions, closeAction)
		if target {
			state.targetAdopted = true
		} else {
			state.sourceAdopted = true
		}
		state.mu.Unlock()
		return nil
	}

	latch := state.phase == commandResourceRunning
	if latch {
		state.stateFailed = true
	}
	state.mu.Unlock()

	cleanupFailed := closeAction != nil && invokeLifecycleClose(closeAction)
	if latch && cleanupFailed {
		state.mu.Lock()
		state.cleanupFailed = true
		state.mu.Unlock()
	}
	if cleanupFailed {
		return joinLifecycleResults(ErrCommandResourceState, ErrCommandResourceCleanup)
	}
	return ErrCommandResourceState
}

func rejectedLifecycleAdoption(closeAction func() error) error {
	if closeAction != nil && invokeLifecycleClose(closeAction) {
		return joinLifecycleResults(ErrCommandResourceState, ErrCommandResourceCleanup)
	}
	return ErrCommandResourceState
}

func (s *CommandResourceScope) close() error {
	if s == nil || s.state == nil {
		return ErrCommandResourceState
	}
	state := s.state
	state.mu.Lock()
	for state.phase == commandResourceRunning || state.phase == commandResourceClosing {
		state.cond.Wait()
	}
	if state.phase == commandResourceClosed {
		result := state.closeErr
		state.mu.Unlock()
		return result
	}
	if state.phase != commandResourceOpen {
		state.mu.Unlock()
		return ErrCommandResourceState
	}
	state.phase = commandResourceClosing
	actions := state.closeActions
	state.closeActions = nil
	state.binding = nil
	state.sourceAdopted = false
	state.targetAdopted = false
	cleanupFailed := state.cleanupFailed
	state.mu.Unlock()

	for index := len(actions) - 1; index >= 0; index-- {
		if invokeLifecycleClose(actions[index]) {
			cleanupFailed = true
		}
	}
	var result error
	if cleanupFailed {
		result = ErrCommandResourceCleanup
	}

	state.mu.Lock()
	state.cleanupFailed = cleanupFailed
	state.closeErr = result
	state.phase = commandResourceClosed
	state.cond.Broadcast()
	state.mu.Unlock()
	return result
}

func (s *CommandResourceScope) combineResult(primary, cleanup error) error {
	if s == nil || s.state == nil {
		return joinLifecycleResults(primary, ErrCommandResourceState, cleanup)
	}
	state := s.state
	state.mu.Lock()
	stateFailed := state.stateFailed
	state.mu.Unlock()

	results := make([]error, 0, 3)
	if primary != nil {
		results = append(results, primary)
	}
	if stateFailed && !errors.Is(primary, ErrCommandResourceState) {
		results = append(results, ErrCommandResourceState)
	}
	if cleanup != nil && !errors.Is(primary, ErrCommandResourceCleanup) {
		results = append(results, ErrCommandResourceCleanup)
	}
	return joinLifecycleResults(results...)
}

func invokeLifecycleClose(closeAction func() error) (failed bool) {
	if closeAction == nil {
		return true
	}
	returned := false
	defer func() {
		_ = recover()
		if !returned {
			failed = true
		}
	}()
	failed = closeAction() != nil
	returned = true
	return failed
}

func joinLifecycleResults(results ...error) error {
	nonNil := results[:0]
	for _, result := range results {
		if result == nil || duplicateLifecycleSingleton(nonNil, result) {
			continue
		}
		nonNil = append(nonNil, result)
	}
	switch len(nonNil) {
	case 0:
		return nil
	case 1:
		return nonNil[0]
	default:
		return errors.Join(nonNil...)
	}
}

func duplicateLifecycleSingleton(results []error, candidate error) bool {
	switch candidate {
	case context.Canceled, context.DeadlineExceeded, ErrCommandResourceState, ErrCommandResourceCleanup, ErrCommandExecution:
	default:
		return false
	}
	for _, result := range results {
		if result == candidate {
			return true
		}
	}
	return false
}

func normalizeW3LifecycleError(err error) error {
	if err == nil {
		return nil
	}
	if refusal, ok := err.(*Refusal); ok {
		if normalized, valid := canonicalLifecycleRefusal(refusal); valid {
			return normalized
		}
		return ErrCommandExecution
	}
	return normalizeCommandLifecycleError(err)
}

func normalizeCommandLifecycleError(err error) (result error) {
	if err == nil {
		return nil
	}
	completed := false
	defer func() {
		_ = recover()
		if !completed {
			result = ErrCommandExecution
		}
	}()
	switch {
	case errors.Is(err, context.Canceled):
		result = context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		result = context.DeadlineExceeded
	case errors.Is(err, ErrCommandResourceState):
		result = ErrCommandResourceState
	case errors.Is(err, ErrCommandResourceCleanup):
		result = ErrCommandResourceCleanup
	default:
		result = ErrCommandExecution
	}
	completed = true
	return result
}

func canonicalLifecycleRefusal(input *Refusal) (*Refusal, bool) {
	if input == nil || input.Effect != effectNone {
		return nil, false
	}
	causes := input.causes
	if causes&^allLifecycleRefusalCauses != 0 {
		return nil, false
	}

	var canonical refusalCauseMask
	valid := false
	switch input.Code {
	case CodePairUnsupported:
		valid = !input.Retryable && causes == 0 && lifecycleReasonIn(input.Reason,
			ReasonTargetBackend, ReasonSourceBackend, ReasonTargetLocatorSource, ReasonTargetSchemaSource,
			ReasonTargetLocator, ReasonTargetTransport, ReasonTargetOptions, ReasonTargetSchema)
	case CodeWorkspaceShapeUnsupported:
		valid = !input.Retryable && causes == 0 && lifecycleReasonIn(input.Reason,
			ReasonSelector, ReasonAmbientSelection, ReasonWorkspaceAlias, ReasonRedirect, ReasonLegacyMetadata,
			ReasonShadowLegacyMetadata, ReasonMetadataValues, ReasonDoltMode, ReasonCustomProviderPath,
			ReasonServerConfiguration, ReasonForeignProviderConfiguration, ReasonServerArtifact, ReasonProviderPath)
	case CodePlatformUnsupported:
		valid = !input.Retryable && (causes == 0 && lifecycleReasonIn(input.Reason,
			ReasonOperatingSystem, ReasonWSL, ReasonEmbeddedBuild) ||
			input.Reason == ReasonFilesystem && causes&^(causeWorkspaceUnsupported|causeStandardUnsupported) == 0)
	case CodeWorkspaceChanged:
		changeClass := causes & (causeChanged | causeIneligible)
		allowed := causeChanged | causeIneligible | lifecycleOSRefusalCauses
		valid = input.Retryable && input.Reason == ReasonWorkspaceObservation &&
			(changeClass == causeChanged || changeClass == causeIneligible) && causes&^allowed == 0
		canonical = causeChanged
	case CodeWorkspaceUnverifiable:
		if input.Retryable {
			break
		}
		switch input.Reason {
		case ReasonBindingClosed:
			valid = causes == causeClosed
			canonical = causeClosed
		case ReasonCleanup:
			valid = causes&causeCleanup != 0
			canonical = causeCleanup
		default:
			valid = lifecycleReasonIn(input.Reason, ReasonRequest, ReasonPlatformProbe, ReasonWorkspace,
				ReasonMetadata, ReasonProvider, ReasonFilesystemProbe) &&
				causes&^(causeUnverifiable|causeClosed|lifecycleOSRefusalCauses) == 0
		}
	case CodeCredentialInLocator:
		valid = !input.Retryable && input.Reason == ReasonTargetCredential && causes == 0
	}
	if !valid {
		return nil, false
	}
	return refusalWithCauses(input.Code, input.Reason, input.Retryable, canonical), true
}

const lifecycleOSRefusalCauses = causeNotExist | causePermission | causeExist | causeOSClosed | causeStandardUnsupported
const allLifecycleRefusalCauses = causeCleanup | causeChanged | causeIneligible | causeWorkspaceUnsupported |
	causeUnverifiable | causeClosed | lifecycleOSRefusalCauses

func lifecycleReasonIn(reason RefusalReason, allowed ...RefusalReason) bool {
	for _, candidate := range allowed {
		if reason == candidate {
			return true
		}
	}
	return false
}

func (CommandResourceScope) String() string   { return safeCommandResourceScopeText }
func (CommandResourceScope) GoString() string { return safeCommandResourceScopeText }
