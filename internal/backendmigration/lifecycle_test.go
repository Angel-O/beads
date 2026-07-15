package backendmigration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/configfile"
)

type redLifecycleBinding struct {
	closeOrder *[]string
}

func (*redLifecycleBinding) Snapshot() (BoundProviderConfiguration, error) {
	return BoundProviderConfiguration{}, nil
}

func (b *redLifecycleBinding) Close() error {
	*b.closeOrder = append(*b.closeOrder, "binding")
	return nil
}

func TestCommandResourceScopeClosesReverseOrderOnRunError(t *testing.T) {
	var closeOrder []string
	runErr := ErrCommandExecution

	err := withBoundProviderConfigurationWith(
		context.Background(),
		ProviderConfigurationRequest{},
		func(_ context.Context, scope *CommandResourceScope, _ BoundProviderConfiguration) error {
			if err := scope.deferSourceClose(func() error {
				closeOrder = append(closeOrder, "source")
				return nil
			}); err != nil {
				t.Fatalf("defer source close: %v", err)
			}
			if err := scope.deferTargetClose(func() error {
				closeOrder = append(closeOrder, "target")
				return nil
			}); err != nil {
				t.Fatalf("defer target close: %v", err)
			}
			return runErr
		},
		lifecycleDependencies{bind: func(ProviderConfigurationRequest) (lifecycleBinding, error) {
			return &redLifecycleBinding{closeOrder: &closeOrder}, nil
		}},
	)
	if !errors.Is(err, runErr) {
		t.Fatalf("run error = %v, want %v", err, runErr)
	}
	want := []string{"target", "source", "binding"}
	if !reflect.DeepEqual(closeOrder, want) {
		t.Fatalf("close order=%v, want %v", closeOrder, want)
	}
}

type lifecycleFakeBinding struct {
	mu            sync.Mutex
	configuration BoundProviderConfiguration
	snapshotErr   error
	closeErr      error
	onSnapshot    func()
	onClose       func()
	snapshotCalls int
	closeCalls    int
}

func (b *lifecycleFakeBinding) Snapshot() (BoundProviderConfiguration, error) {
	b.mu.Lock()
	b.snapshotCalls++
	onSnapshot := b.onSnapshot
	configuration := b.configuration
	err := b.snapshotErr
	b.mu.Unlock()
	if onSnapshot != nil {
		onSnapshot()
	}
	return configuration, err
}

func (b *lifecycleFakeBinding) Close() error {
	b.mu.Lock()
	b.closeCalls++
	onClose := b.onClose
	err := b.closeErr
	b.mu.Unlock()
	if onClose != nil {
		onClose()
	}
	return err
}

func (b *lifecycleFakeBinding) counts() (snapshots, closes int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshotCalls, b.closeCalls
}

func lifecycleDeps(binding lifecycleBinding, bindErr error, onBind func()) lifecycleDependencies {
	return lifecycleDependencies{bind: func(ProviderConfigurationRequest) (lifecycleBinding, error) {
		if onBind != nil {
			onBind()
		}
		return binding, bindErr
	}}
}

func TestProductionLifecycleDependenciesPreservesNilBinding(t *testing.T) {
	binding, err := productionLifecycleDependencies().bind(ProviderConfigurationRequest{})
	if err == nil {
		t.Fatal("production binder error=nil, want W3 refusal")
	}
	if binding != nil {
		t.Fatalf("production binder=%#v, want nil lifecycle interface", binding)
	}
}

func TestWithBoundProviderConfigurationValidatesBeforeBinding(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		run  BoundProviderCommand
	}{
		{
			name: "nil context",
			run:  func(context.Context, *CommandResourceScope, BoundProviderConfiguration) error { return nil },
		},
		{name: "nil callback", ctx: context.Background()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bindCalls := 0
			err := withBoundProviderConfigurationWith(test.ctx, ProviderConfigurationRequest{}, test.run,
				lifecycleDependencies{bind: func(ProviderConfigurationRequest) (lifecycleBinding, error) {
					bindCalls++
					return nil, nil
				}})
			if err != ErrCommandResourceState || bindCalls != 0 {
				t.Fatalf("error=%#v bindCalls=%d, want state/0", err, bindCalls)
			}
		})
	}
}

func TestWithBoundProviderConfigurationSuccessAndFrontiers(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		configuration := BoundProviderConfiguration{state: &boundProviderConfigurationState{}}
		binding := &lifecycleFakeBinding{configuration: configuration}
		callbackCalls := 0
		err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
			func(_ context.Context, _ *CommandResourceScope, got BoundProviderConfiguration) error {
				callbackCalls++
				if got.state != configuration.state {
					t.Fatal("callback did not receive the binding snapshot")
				}
				return nil
			}, lifecycleDeps(binding, nil, nil))
		if err != nil {
			t.Fatalf("WithBoundProviderConfiguration: %v", err)
		}
		if snapshots, closes := binding.counts(); snapshots != 1 || closes != 1 || callbackCalls != 1 {
			t.Fatalf("snapshots=%d closes=%d callback=%d, want 1/1/1", snapshots, closes, callbackCalls)
		}
	})

	t.Run("bind refusal", func(t *testing.T) {
		bindErr := refusal(CodePairUnsupported, ReasonTargetBackend, false, nil)
		callbackCalls := 0
		err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
			func(context.Context, *CommandResourceScope, BoundProviderConfiguration) error {
				callbackCalls++
				return nil
			}, lifecycleDeps(nil, bindErr, nil))
		var got *Refusal
		if !errors.As(err, &got) || got == bindErr || got.Code != bindErr.Code || got.Reason != bindErr.Reason || callbackCalls != 0 {
			t.Fatalf("error=%#v callback=%d, want cloned refusal and no callback", err, callbackCalls)
		}
	})

	t.Run("bind resource and error closes immediately", func(t *testing.T) {
		binding := &lifecycleFakeBinding{closeErr: errors.New("private bind cleanup")}
		err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
			func(context.Context, *CommandResourceScope, BoundProviderConfiguration) error {
				t.Fatal("callback ran after bind error")
				return nil
			}, lifecycleDeps(binding, errors.New("private bind error"), nil))
		if !errors.Is(err, ErrCommandExecution) || !errors.Is(err, ErrCommandResourceCleanup) || strings.Contains(err.Error(), "private") {
			t.Fatalf("error=%#v, want safe execution+cleanup", err)
		}
		if snapshots, closes := binding.counts(); snapshots != 0 || closes != 1 {
			t.Fatalf("snapshots=%d closes=%d, want 0/1", snapshots, closes)
		}
	})

	t.Run("bind cleanup primary is not duplicated by close failure", func(t *testing.T) {
		binding := &lifecycleFakeBinding{closeErr: errors.New("private bind cleanup")}
		err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
			func(context.Context, *CommandResourceScope, BoundProviderConfiguration) error {
				t.Fatal("callback ran after bind error")
				return nil
			}, lifecycleDeps(binding, ErrCommandResourceCleanup, nil))
		if err != ErrCommandResourceCleanup {
			t.Fatalf("error=%#v, want the cleanup singleton", err)
		}
		if strings.Count(err.Error(), ErrCommandResourceCleanup.Error()) != 1 {
			t.Fatalf("cleanup error rendered more than once: %q", err)
		}
	})

	t.Run("nil binding", func(t *testing.T) {
		err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
			func(context.Context, *CommandResourceScope, BoundProviderConfiguration) error {
				t.Fatal("callback ran with nil binding")
				return nil
			}, lifecycleDeps(nil, nil, nil))
		if err != ErrCommandResourceState {
			t.Fatalf("error=%v, want ErrCommandResourceState", err)
		}
	})

	t.Run("snapshot error closes binding", func(t *testing.T) {
		binding := &lifecycleFakeBinding{snapshotErr: errors.New("private snapshot error")}
		err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
			func(context.Context, *CommandResourceScope, BoundProviderConfiguration) error {
				t.Fatal("callback ran after snapshot error")
				return nil
			}, lifecycleDeps(binding, nil, nil))
		if err != ErrCommandExecution {
			t.Fatalf("error=%#v, want ErrCommandExecution", err)
		}
		if snapshots, closes := binding.counts(); snapshots != 1 || closes != 1 {
			t.Fatalf("snapshots=%d closes=%d, want 1/1", snapshots, closes)
		}
	})

	t.Run("snapshot refusal is cloned", func(t *testing.T) {
		snapshotErr := refusalWithCauses(CodeWorkspaceChanged, ReasonWorkspaceObservation, true, causeIneligible|causePermission)
		binding := &lifecycleFakeBinding{snapshotErr: snapshotErr}
		err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
			func(context.Context, *CommandResourceScope, BoundProviderConfiguration) error {
				t.Fatal("callback ran after snapshot refusal")
				return nil
			}, lifecycleDeps(binding, nil, nil))
		var got *Refusal
		if !errors.As(err, &got) {
			t.Fatalf("error=%#v, want cloned workspace-changed refusal", err)
		}
		if got == snapshotErr || got.Code != CodeWorkspaceChanged || got.Reason != ReasonWorkspaceObservation || got.causes != causeChanged {
			t.Fatalf("error=%#v causes=%d, want canonical cloned workspace-changed refusal", err, got.causes)
		}
		if _, closes := binding.counts(); closes != 1 {
			t.Fatalf("binding closes=%d, want 1", closes)
		}
	})
}

func TestWithBoundProviderConfigurationClosesRealW3BindingBeforeReturn(t *testing.T) {
	fixture := newSelectionFixture(t, configfile.Config{
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeEmbedded,
		DoltDatabase: "witnessed_database",
	})
	request := validProviderConfigurationRequest(fixture.workspace)
	var retained *ProviderConfigurationBinding
	err := withBoundProviderConfigurationWith(context.Background(), request,
		func(_ context.Context, _ *CommandResourceScope, configuration BoundProviderConfiguration) error {
			if configuration.Source().Database() != "witnessed_database" {
				t.Fatalf("database=%q, want witnessed_database", configuration.Source().Database())
			}
			return nil
		}, lifecycleDependencies{bind: func(request ProviderConfigurationRequest) (lifecycleBinding, error) {
			binding, bindErr := bindProviderConfigurationWith(request, fixture.dependencies())
			retained = binding
			if binding == nil {
				return nil, bindErr
			}
			return binding, bindErr
		}})
	if err != nil {
		t.Fatalf("WithBoundProviderConfiguration: %v", err)
	}
	if retained == nil {
		t.Fatal("test binder did not retain the W3 binding")
	}
	if _, snapshotErr := retained.Snapshot(); snapshotErr == nil {
		t.Fatal("W3 Snapshot succeeded after lifecycle cleanup")
	} else {
		var refusal *Refusal
		if !errors.As(snapshotErr, &refusal) || refusal.Code != CodeWorkspaceUnverifiable || refusal.Reason != ReasonBindingClosed {
			t.Fatalf("snapshot error=%#v, want binding-closed refusal", snapshotErr)
		}
	}
	if fixture.witness.closes != 1 {
		t.Fatalf("witness closes=%d, want 1", fixture.witness.closes)
	}
}

func TestCommandResourceScopeAcquisitionAndCallbackErrorFrontiers(t *testing.T) {
	tests := []struct {
		name      string
		callback  func(*CommandResourceScope, *[]string) error
		wantOrder []string
	}{
		{
			name: "source open failure before adoption",
			callback: func(*CommandResourceScope, *[]string) error {
				return errors.New("private source open failure")
			},
			wantOrder: []string{"binding"},
		},
		{
			name: "target open failure after source adoption",
			callback: func(scope *CommandResourceScope, order *[]string) error {
				if err := scope.deferSourceClose(func() error {
					*order = append(*order, "source")
					return nil
				}); err != nil {
					return err
				}
				return errors.New("private target open failure")
			},
			wantOrder: []string{"source", "binding"},
		},
		{
			name: "callback error before adoption",
			callback: func(*CommandResourceScope, *[]string) error {
				return errors.New("private callback failure")
			},
			wantOrder: []string{"binding"},
		},
		{
			name: "callback error after source and target adoption",
			callback: func(scope *CommandResourceScope, order *[]string) error {
				if err := scope.deferSourceClose(func() error {
					*order = append(*order, "source")
					return nil
				}); err != nil {
					return err
				}
				if err := scope.deferTargetClose(func() error {
					*order = append(*order, "target")
					return nil
				}); err != nil {
					return err
				}
				return errors.New("private callback failure")
			},
			wantOrder: []string{"target", "source", "binding"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var order []string
			binding := &lifecycleFakeBinding{onClose: func() { order = append(order, "binding") }}
			err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
				func(_ context.Context, scope *CommandResourceScope, _ BoundProviderConfiguration) error {
					return test.callback(scope, &order)
				}, lifecycleDeps(binding, nil, nil))
			if err != ErrCommandExecution {
				t.Fatalf("error=%#v, want ErrCommandExecution", err)
			}
			if !reflect.DeepEqual(order, test.wantOrder) {
				t.Fatalf("close order=%v, want %v", order, test.wantOrder)
			}
		})
	}
}

func TestWithBoundProviderConfigurationCancellationPrecedence(t *testing.T) {
	t.Run("pre-canceled does not bind", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		bindCalls := 0
		err := withBoundProviderConfigurationWith(ctx, ProviderConfigurationRequest{},
			func(context.Context, *CommandResourceScope, BoundProviderConfiguration) error {
				t.Fatal("callback ran for pre-canceled context")
				return nil
			}, lifecycleDeps(nil, nil, func() { bindCalls++ }))
		if err != context.Canceled || bindCalls != 0 {
			t.Fatalf("error=%v bindCalls=%d, want canceled/0", err, bindCalls)
		}
	})

	t.Run("bind error wins same-call cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		binding := &lifecycleFakeBinding{}
		err := withBoundProviderConfigurationWith(ctx, ProviderConfigurationRequest{},
			func(context.Context, *CommandResourceScope, BoundProviderConfiguration) error {
				t.Fatal("callback ran after bind error")
				return nil
			}, lifecycleDeps(binding, errors.New("private bind failure"), cancel))
		if err != ErrCommandExecution {
			t.Fatalf("error=%#v, want bind execution error", err)
		}
		if _, closes := binding.counts(); closes != 1 {
			t.Fatalf("binding closes=%d, want 1", closes)
		}
	})

	t.Run("successful bind then cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		binding := &lifecycleFakeBinding{}
		err := withBoundProviderConfigurationWith(ctx, ProviderConfigurationRequest{},
			func(context.Context, *CommandResourceScope, BoundProviderConfiguration) error {
				t.Fatal("callback ran after bind-time cancellation")
				return nil
			}, lifecycleDeps(binding, nil, cancel))
		if err != context.Canceled {
			t.Fatalf("error=%#v, want context.Canceled", err)
		}
		if snapshots, closes := binding.counts(); snapshots != 0 || closes != 1 {
			t.Fatalf("snapshots=%d closes=%d, want 0/1", snapshots, closes)
		}
	})

	t.Run("snapshot error wins same-call cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		binding := &lifecycleFakeBinding{snapshotErr: errors.New("private snapshot failure"), onSnapshot: cancel}
		err := withBoundProviderConfigurationWith(ctx, ProviderConfigurationRequest{},
			func(context.Context, *CommandResourceScope, BoundProviderConfiguration) error {
				t.Fatal("callback ran after snapshot error")
				return nil
			}, lifecycleDeps(binding, nil, nil))
		if err != ErrCommandExecution {
			t.Fatalf("error=%#v, want snapshot execution error", err)
		}
	})

	t.Run("successful snapshot then cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		binding := &lifecycleFakeBinding{onSnapshot: cancel}
		err := withBoundProviderConfigurationWith(ctx, ProviderConfigurationRequest{},
			func(context.Context, *CommandResourceScope, BoundProviderConfiguration) error {
				t.Fatal("callback ran after snapshot-time cancellation")
				return nil
			}, lifecycleDeps(binding, nil, nil))
		if err != context.Canceled {
			t.Fatalf("error=%#v, want context.Canceled", err)
		}
	})

	t.Run("callback error wins cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		binding := &lifecycleFakeBinding{}
		err := withBoundProviderConfigurationWith(ctx, ProviderConfigurationRequest{},
			func(context.Context, *CommandResourceScope, BoundProviderConfiguration) error {
				cancel()
				return errors.New("private callback failure")
			}, lifecycleDeps(binding, nil, nil))
		if err != ErrCommandExecution {
			t.Fatalf("error=%#v, want callback execution error", err)
		}
	})

	t.Run("nil callback after cancellation is state error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := withBoundProviderConfigurationWith(ctx, ProviderConfigurationRequest{}, nil, lifecycleDependencies{})
		if err != ErrCommandResourceState {
			t.Fatalf("error=%#v, want state validation before cancellation", err)
		}
	})
}

func TestWithBoundProviderConfigurationCancellationWaitsForCallbackReturn(t *testing.T) {
	const secretCause = "private cancellation cause"
	ctx, cancel := context.WithCancelCause(context.Background())
	callbackStarted := make(chan struct{})
	callbackObservedCancellation := make(chan struct{})
	releaseCallback := make(chan struct{})
	wrapperReturned := make(chan error, 1)
	binding := &lifecycleFakeBinding{}

	go func() {
		wrapperReturned <- withBoundProviderConfigurationWith(ctx, ProviderConfigurationRequest{},
			func(ctx context.Context, _ *CommandResourceScope, _ BoundProviderConfiguration) error {
				close(callbackStarted)
				<-ctx.Done()
				close(callbackObservedCancellation)
				<-releaseCallback
				return nil
			}, lifecycleDeps(binding, nil, nil))
	}()
	waitForSignal(t, callbackStarted, "callback start")
	cancel(errors.New(secretCause))
	waitForSignal(t, callbackObservedCancellation, "callback cancellation observation")
	if _, closes := binding.counts(); closes != 0 {
		t.Fatalf("binding closed under running callback: %d", closes)
	}
	select {
	case err := <-wrapperReturned:
		t.Fatalf("wrapper returned before callback: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseCallback)
	err := <-wrapperReturned
	if err != context.Canceled {
		t.Fatalf("error=%#v, want bare context.Canceled", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", err), secretCause) {
		t.Fatalf("cancellation result leaked cause: %+v", err)
	}
	if _, closes := binding.counts(); closes != 1 {
		t.Fatalf("binding closes=%d, want 1 after callback return", closes)
	}
}

func TestCommandResourceScopeClosesBeforeCobraPostRun(t *testing.T) {
	var closeOrder []string
	postErr := errors.New("post-run sentinel")
	binding := &lifecycleFakeBinding{onClose: func() { closeOrder = append(closeOrder, "binding") }}
	postCalls := 0
	command := &cobra.Command{
		Use:           "lifecycle",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(*cobra.Command, []string) error {
			return withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
				func(_ context.Context, scope *CommandResourceScope, _ BoundProviderConfiguration) error {
					if err := scope.deferSourceClose(func() error {
						closeOrder = append(closeOrder, "source")
						return nil
					}); err != nil {
						return err
					}
					if err := scope.deferTargetClose(func() error {
						closeOrder = append(closeOrder, "target")
						return nil
					}); err != nil {
						return err
					}
					return nil
				}, lifecycleDeps(binding, nil, nil))
		},
		PostRunE: func(*cobra.Command, []string) error {
			postCalls++
			if want := []string{"target", "source", "binding"}; !reflect.DeepEqual(closeOrder, want) {
				t.Fatalf("post-run close order=%v, want %v", closeOrder, want)
			}
			return postErr
		},
	}
	if err := command.Execute(); err != postErr || postCalls != 1 {
		t.Fatalf("Execute error=%#v postCalls=%d, want post error/1", err, postCalls)
	}
}

func TestCommandResourceCleanupFailureSkipsCobraPostRun(t *testing.T) {
	binding := &lifecycleFakeBinding{closeErr: errors.New("private close failure")}
	postCalls := 0
	command := &cobra.Command{
		Use:           "lifecycle",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(*cobra.Command, []string) error {
			return withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
				func(context.Context, *CommandResourceScope, BoundProviderConfiguration) error { return nil },
				lifecycleDeps(binding, nil, nil))
		},
		PostRunE: func(*cobra.Command, []string) error {
			postCalls++
			return nil
		},
	}
	err := command.Execute()
	if err != ErrCommandResourceCleanup || postCalls != 0 {
		t.Fatalf("Execute error=%#v postCalls=%d, want cleanup/0", err, postCalls)
	}
}

func TestCommandResourceScopeFormattingAndJSONAreSafe(t *testing.T) {
	binding := &lifecycleFakeBinding{}
	var retained *CommandResourceScope
	if err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
		func(_ context.Context, scope *CommandResourceScope, _ BoundProviderConfiguration) error {
			retained = scope
			return nil
		}, lifecycleDeps(binding, nil, nil)); err != nil {
		t.Fatalf("WithBoundProviderConfiguration: %v", err)
	}
	copyValue := *retained
	type privateScopeEnvelope struct{ scope CommandResourceScope }
	for _, value := range []any{retained, copyValue, privateScopeEnvelope{scope: copyValue}} {
		for _, rendered := range []string{fmt.Sprint(value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			if strings.Contains(rendered, "lifecycleFakeBinding") || strings.Contains(rendered, "closeActions") {
				t.Fatalf("format leaked private scope state: %q", rendered)
			}
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("json.Marshal(%T): %v", value, err)
		}
		if string(encoded) != "{}" {
			t.Fatalf("json.Marshal(%T)=%s, want {}", value, encoded)
		}
	}
}

func TestCommandResourceScopeRejectedAdoptionIsLatched(t *testing.T) {
	tests := []struct {
		name        string
		callback    func(*CommandResourceScope, *[]string) error
		wantCleanup bool
		wantOrder   []string
	}{
		{
			name: "target before source ignored",
			callback: func(scope *CommandResourceScope, order *[]string) error {
				_ = scope.deferTargetClose(func() error {
					*order = append(*order, "rejected-target")
					return nil
				})
				return nil
			},
			wantOrder: []string{"rejected-target", "binding"},
		},
		{
			name: "nil source ignored",
			callback: func(scope *CommandResourceScope, _ *[]string) error {
				_ = scope.deferSourceClose(nil)
				return nil
			},
			wantOrder: []string{"binding"},
		},
		{
			name: "duplicate source failure ignored",
			callback: func(scope *CommandResourceScope, order *[]string) error {
				if err := scope.deferSourceClose(func() error {
					*order = append(*order, "source")
					return nil
				}); err != nil {
					return err
				}
				_ = scope.deferSourceClose(func() error {
					*order = append(*order, "rejected-source")
					return errors.New("private immediate-close failure")
				})
				return nil
			},
			wantCleanup: true,
			wantOrder:   []string{"rejected-source", "source", "binding"},
		},
		{
			name: "duplicate source panic propagated",
			callback: func(scope *CommandResourceScope, order *[]string) error {
				if err := scope.deferSourceClose(func() error {
					*order = append(*order, "source")
					return nil
				}); err != nil {
					return err
				}
				return scope.deferSourceClose(func() error {
					*order = append(*order, "rejected-source")
					panic("private immediate-close panic")
				})
			},
			wantCleanup: true,
			wantOrder:   []string{"rejected-source", "source", "binding"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var order []string
			binding := &lifecycleFakeBinding{onClose: func() { order = append(order, "binding") }}
			err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
				func(_ context.Context, scope *CommandResourceScope, _ BoundProviderConfiguration) error {
					return test.callback(scope, &order)
				}, lifecycleDeps(binding, nil, nil))
			if !errors.Is(err, ErrCommandResourceState) {
				t.Fatalf("error=%#v, want state failure", err)
			}
			if errors.Is(err, ErrCommandResourceCleanup) != test.wantCleanup {
				t.Fatalf("error=%#v cleanup=%v, want %v", err, errors.Is(err, ErrCommandResourceCleanup), test.wantCleanup)
			}
			if !reflect.DeepEqual(order, test.wantOrder) {
				t.Fatalf("close order=%v, want %v", order, test.wantOrder)
			}
			if strings.Contains(fmt.Sprintf("%+v", err), "private") {
				t.Fatalf("error leaked rejected close detail: %+v", err)
			}
		})
	}
}

func TestCommandResourceScopeRejectedAdoptionPropagationMatrix(t *testing.T) {
	tests := []struct {
		name        string
		closeAction func(*[]string) func() error
		wantCleanup bool
	}{
		{
			name: "successful immediate close",
			closeAction: func(order *[]string) func() error {
				return func() error {
					*order = append(*order, "rejected-source")
					return nil
				}
			},
		},
		{
			name: "failed immediate close",
			closeAction: func(order *[]string) func() error {
				return func() error {
					*order = append(*order, "rejected-source")
					return errors.New("private rejected close failure")
				}
			},
			wantCleanup: true,
		},
		{
			name: "panicking immediate close",
			closeAction: func(order *[]string) func() error {
				return func() error {
					*order = append(*order, "rejected-source")
					panic("private rejected close panic")
				}
			},
			wantCleanup: true,
		},
	}
	for _, test := range tests {
		for _, propagate := range []bool{false, true} {
			name := fmt.Sprintf("%s/propagate=%t", test.name, propagate)
			t.Run(name, func(t *testing.T) {
				var order []string
				binding := &lifecycleFakeBinding{onClose: func() { order = append(order, "binding") }}
				err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
					func(_ context.Context, scope *CommandResourceScope, _ BoundProviderConfiguration) error {
						if adoptErr := scope.deferSourceClose(func() error {
							order = append(order, "source")
							return nil
						}); adoptErr != nil {
							return adoptErr
						}
						adoptErr := scope.deferSourceClose(test.closeAction(&order))
						if propagate {
							return adoptErr
						}
						return nil
					}, lifecycleDeps(binding, nil, nil))
				if !errors.Is(err, ErrCommandResourceState) {
					t.Fatalf("error=%#v, want state failure", err)
				}
				if errors.Is(err, ErrCommandResourceCleanup) != test.wantCleanup {
					t.Fatalf("error=%#v cleanup=%v, want %v", err, errors.Is(err, ErrCommandResourceCleanup), test.wantCleanup)
				}
				if want := []string{"rejected-source", "source", "binding"}; !reflect.DeepEqual(order, want) {
					t.Fatalf("close order=%v, want %v", order, want)
				}
				if strings.Contains(fmt.Sprintf("%+v", err), "private") {
					t.Fatalf("error leaked rejected close detail: %+v", err)
				}
			})
		}
	}
}

func TestCommandResourceScopeCombinedResultBranchOrder(t *testing.T) {
	binding := &lifecycleFakeBinding{}
	err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
		func(_ context.Context, scope *CommandResourceScope, _ BoundProviderConfiguration) error {
			_ = scope.deferTargetClose(func() error {
				return errors.New("private rejected target cleanup")
			})
			return errors.New("private callback failure")
		}, lifecycleDeps(binding, nil, nil))
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("error=%#v, want a three-branch joined result", err)
	}
	want := []error{ErrCommandExecution, ErrCommandResourceState, ErrCommandResourceCleanup}
	if got := joined.Unwrap(); !reflect.DeepEqual(got, want) {
		t.Fatalf("branches=%#v, want %#v", got, want)
	}
}

func TestCommandResourceScopeRejectedAdoptionAfterCloseIsImmediate(t *testing.T) {
	binding := &lifecycleFakeBinding{}
	var retained *CommandResourceScope
	if err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
		func(_ context.Context, scope *CommandResourceScope, _ BoundProviderConfiguration) error {
			retained = scope
			return nil
		}, lifecycleDeps(binding, nil, nil)); err != nil {
		t.Fatalf("WithBoundProviderConfiguration: %v", err)
	}
	closeCalls := 0
	err := retained.deferSourceClose(func() error {
		closeCalls++
		return errors.New("private late close failure")
	})
	if !errors.Is(err, ErrCommandResourceState) || !errors.Is(err, ErrCommandResourceCleanup) || closeCalls != 1 {
		t.Fatalf("late adoption error=%#v closeCalls=%d", err, closeCalls)
	}
	if closeErr := retained.close(); closeErr != nil {
		t.Fatalf("completed scope result changed after late adoption: %v", closeErr)
	}
}

func TestCommandResourceScopeAllClosersRunAndCleanupIsStable(t *testing.T) {
	var closeOrder []string
	binding := &lifecycleFakeBinding{
		closeErr: errors.New("private binding close failure"),
		onClose:  func() { closeOrder = append(closeOrder, "binding") },
	}
	var retained *CommandResourceScope
	err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
		func(_ context.Context, scope *CommandResourceScope, _ BoundProviderConfiguration) error {
			retained = scope
			if err := scope.deferSourceClose(func() error {
				closeOrder = append(closeOrder, "source")
				panic("private source close panic")
			}); err != nil {
				return err
			}
			if err := scope.deferTargetClose(func() error {
				closeOrder = append(closeOrder, "target")
				return errors.New("private target close failure")
			}); err != nil {
				return err
			}
			return errors.New("private callback failure")
		}, lifecycleDeps(binding, nil, nil))
	if !errors.Is(err, ErrCommandExecution) || !errors.Is(err, ErrCommandResourceCleanup) {
		t.Fatalf("error=%#v, want execution+cleanup", err)
	}
	if want := []string{"target", "source", "binding"}; !reflect.DeepEqual(closeOrder, want) {
		t.Fatalf("close order=%v, want %v", closeOrder, want)
	}
	if strings.Contains(err.Error(), "private") || strings.Contains(fmt.Sprintf("%#v", err), "private") {
		t.Fatalf("error rendered private closer detail: %#v", err)
	}
	if first, second := retained.close(), retained.close(); first != ErrCommandResourceCleanup || second != first {
		t.Fatalf("cached close results=%#v/%#v, want identical cleanup singleton", first, second)
	}
	if _, closes := binding.counts(); closes != 1 {
		t.Fatalf("binding closes=%d, want 1", closes)
	}

	const callers = 16
	results := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- retained.close()
		}()
	}
	wg.Wait()
	close(results)
	for result := range results {
		if result != ErrCommandResourceCleanup {
			t.Errorf("concurrent close result=%#v, want cleanup singleton", result)
		}
	}
}

func TestCommandResourceScopeCallbackPanicIsRedactedAfterCleanup(t *testing.T) {
	var closeOrder []string
	binding := &lifecycleFakeBinding{
		closeErr: errors.New("private binding cleanup failure"),
		onClose:  func() { closeOrder = append(closeOrder, "binding") },
	}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
			func(_ context.Context, scope *CommandResourceScope, _ BoundProviderConfiguration) error {
				if err := scope.deferSourceClose(func() error {
					closeOrder = append(closeOrder, "source")
					return errors.New("private source cleanup failure")
				}); err != nil {
					return err
				}
				if err := scope.deferTargetClose(func() error {
					closeOrder = append(closeOrder, "target")
					panic("private target cleanup panic")
				}); err != nil {
					return err
				}
				panic("private callback panic")
			}, lifecycleDeps(binding, nil, nil))
	}()
	if recovered != ErrCommandExecution {
		t.Fatalf("recovered=%#v, want fixed ErrCommandExecution", recovered)
	}
	if want := []string{"target", "source", "binding"}; !reflect.DeepEqual(closeOrder, want) {
		t.Fatalf("close order=%v, want %v", closeOrder, want)
	}
	if strings.Contains(fmt.Sprint(recovered), "private") {
		t.Fatalf("panic leaked private value: %v", recovered)
	}
}

func TestCommandResourceScopeConcurrentCloseWaitsForCallback(t *testing.T) {
	callbackStarted := make(chan struct{})
	releaseCallback := make(chan struct{})
	closeReturned := make(chan error, 1)
	wrapperReturned := make(chan error, 1)
	var closeOrder []string
	var orderMu sync.Mutex
	binding := &lifecycleFakeBinding{onClose: func() {
		orderMu.Lock()
		closeOrder = append(closeOrder, "binding")
		orderMu.Unlock()
	}}
	var retained *CommandResourceScope
	go func() {
		wrapperReturned <- withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
			func(_ context.Context, scope *CommandResourceScope, _ BoundProviderConfiguration) error {
				retained = scope
				if err := scope.deferSourceClose(func() error {
					orderMu.Lock()
					closeOrder = append(closeOrder, "source")
					orderMu.Unlock()
					return nil
				}); err != nil {
					return err
				}
				close(callbackStarted)
				<-releaseCallback
				return nil
			}, lifecycleDeps(binding, nil, nil))
	}()
	waitForSignal(t, callbackStarted, "callback start")
	go func() { closeReturned <- retained.close() }()
	select {
	case err := <-closeReturned:
		t.Fatalf("concurrent close returned under active callback: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseCallback)
	if err := <-wrapperReturned; err != nil {
		t.Fatalf("wrapper error: %v", err)
	}
	if err := <-closeReturned; err != nil {
		t.Fatalf("concurrent close error: %v", err)
	}
	orderMu.Lock()
	defer orderMu.Unlock()
	if want := []string{"source", "binding"}; !reflect.DeepEqual(closeOrder, want) {
		t.Fatalf("close order=%v, want %v", closeOrder, want)
	}
}

func TestCommandResourceScopeConcurrentCloseWaitsForInProgressCleanup(t *testing.T) {
	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	wrapperReturned := make(chan error, 1)
	binding := &lifecycleFakeBinding{}
	var scope *CommandResourceScope
	sourceCloseCalls := 0
	go func() {
		wrapperReturned <- withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
			func(_ context.Context, current *CommandResourceScope, _ BoundProviderConfiguration) error {
				scope = current
				return scope.deferSourceClose(func() error {
					sourceCloseCalls++
					close(closeStarted)
					<-releaseClose
					return nil
				})
			}, lifecycleDeps(binding, nil, nil))
	}()
	waitForSignal(t, closeStarted, "source cleanup start")

	const callers = 16
	results := make(chan error, callers)
	for range callers {
		go func() { results <- scope.close() }()
	}
	select {
	case result := <-results:
		t.Fatalf("concurrent close returned during cleanup: %v", result)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseClose)
	if err := <-wrapperReturned; err != nil {
		t.Fatalf("wrapper error: %v", err)
	}
	for range callers {
		if result := <-results; result != nil {
			t.Errorf("concurrent close result=%#v, want nil", result)
		}
	}
	if sourceCloseCalls != 1 {
		t.Fatalf("source close calls=%d, want 1", sourceCloseCalls)
	}
	if _, closes := binding.counts(); closes != 1 {
		t.Fatalf("binding closes=%d, want 1", closes)
	}
}

func TestCommandResourceScopeCompletionWindowRejectsConcurrentSecondExecution(t *testing.T) {
	binding := &lifecycleFakeBinding{}
	scope := newCommandResourceScope()
	if err := scope.installBinding(binding); err != nil {
		t.Fatalf("install binding: %v", err)
	}
	scope.state.mu.Lock()
	scope.state.phase = commandResourceRunning
	scope.state.mu.Unlock()
	scope.finishExecution()

	const callers = 16
	start := make(chan struct{})
	results := make(chan error, callers)
	var callbackCalls int
	var callbackMu sync.Mutex
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- scope.execute(func() error {
				callbackMu.Lock()
				callbackCalls++
				callbackMu.Unlock()
				return nil
			})
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for result := range results {
		if result != ErrCommandResourceState {
			t.Errorf("second execution result=%#v, want state singleton", result)
		}
	}
	callbackMu.Lock()
	defer callbackMu.Unlock()
	if callbackCalls != 0 {
		t.Fatalf("second execution callbacks=%d, want 0", callbackCalls)
	}
}

func TestCommandResourceScopeDetachesBeforeCloserInvocation(t *testing.T) {
	binding := &lifecycleFakeBinding{}
	var scope *CommandResourceScope
	checkDetached := func() error {
		state := scope.state
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.binding != nil || state.closeActions != nil || state.phase != commandResourceClosing {
			return fmt.Errorf("scope retained resources during close")
		}
		return nil
	}
	if err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
		func(_ context.Context, current *CommandResourceScope, _ BoundProviderConfiguration) error {
			scope = current
			return scope.deferSourceClose(checkDetached)
		}, lifecycleDeps(binding, nil, nil)); err != nil {
		t.Fatalf("WithBoundProviderConfiguration: %v", err)
	}
}

func TestCommandResourceScopeNilZeroCopyAndReuse(t *testing.T) {
	var nilScope *CommandResourceScope
	nilCloseCalls := 0
	if err := nilScope.deferSourceClose(func() error { nilCloseCalls++; return nil }); err != ErrCommandResourceState || nilCloseCalls != 1 {
		t.Fatalf("nil adoption error=%#v calls=%d", err, nilCloseCalls)
	}
	if err := nilScope.close(); err != ErrCommandResourceState {
		t.Fatalf("nil close=%#v", err)
	}
	if _, err := nilScope.snapshot(); err != ErrCommandResourceState {
		t.Fatalf("nil snapshot=%#v", err)
	}
	zero := &CommandResourceScope{}
	if err := zero.close(); err != ErrCommandResourceState {
		t.Fatalf("zero close=%#v", err)
	}
	if _, err := zero.snapshot(); err != ErrCommandResourceState {
		t.Fatalf("zero snapshot=%#v", err)
	}
	zeroCloseCalls := 0
	if err := zero.deferTargetClose(func() error { zeroCloseCalls++; return nil }); err != ErrCommandResourceState || zeroCloseCalls != 1 {
		t.Fatalf("zero adoption error=%#v calls=%d", err, zeroCloseCalls)
	}
	zeroCallbackCalled := false
	if err := zero.execute(func() error { zeroCallbackCalled = true; return nil }); err != ErrCommandResourceState || zeroCallbackCalled {
		t.Fatalf("zero execute error=%#v callback=%v", err, zeroCallbackCalled)
	}

	var scopes []*CommandResourceScope
	var order []string
	bindRun := 0
	deps := lifecycleDependencies{bind: func(ProviderConfigurationRequest) (lifecycleBinding, error) {
		bindRun++
		run := bindRun
		return &lifecycleFakeBinding{onClose: func() { order = append(order, fmt.Sprintf("binding-%d", run)) }}, nil
	}}
	for run := 1; run <= 2; run++ {
		if err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
			func(_ context.Context, scope *CommandResourceScope, _ BoundProviderConfiguration) error {
				scopes = append(scopes, scope)
				copyScope := *scope
				return copyScope.deferSourceClose(func() error {
					order = append(order, fmt.Sprintf("source-%d", run))
					return nil
				})
			}, deps); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	if scopes[0] == scopes[1] || scopes[0].state == scopes[1].state {
		t.Fatal("sequential runs reused a scope")
	}
	want := []string{"source-1", "binding-1", "source-2", "binding-2"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("reuse close order=%v, want %v", order, want)
	}
	callbackCalled := false
	if err := scopes[0].execute(func() error { callbackCalled = true; return nil }); err != ErrCommandResourceState || callbackCalled {
		t.Fatalf("closed execute error=%#v callback=%v", err, callbackCalled)
	}
}

func TestCanonicalLifecycleRefusalCauseMasksMatchExactW3Table(t *testing.T) {
	type causeRule func(refusalCauseMask) (bool, refusalCauseMask)
	type refusalRow struct {
		code      RefusalCode
		reason    RefusalReason
		retryable bool
		causes    causeRule
	}
	zeroCauses := func(causes refusalCauseMask) (bool, refusalCauseMask) {
		return causes == 0, 0
	}
	var rows []refusalRow
	for _, reason := range []RefusalReason{
		ReasonTargetBackend, ReasonSourceBackend, ReasonTargetLocatorSource, ReasonTargetSchemaSource,
		ReasonTargetLocator, ReasonTargetTransport, ReasonTargetOptions, ReasonTargetSchema,
	} {
		rows = append(rows, refusalRow{code: CodePairUnsupported, reason: reason, causes: zeroCauses})
	}
	for _, reason := range []RefusalReason{
		ReasonSelector, ReasonAmbientSelection, ReasonWorkspaceAlias, ReasonRedirect, ReasonLegacyMetadata,
		ReasonShadowLegacyMetadata, ReasonMetadataValues, ReasonDoltMode, ReasonCustomProviderPath,
		ReasonServerConfiguration, ReasonForeignProviderConfiguration, ReasonServerArtifact, ReasonProviderPath,
	} {
		rows = append(rows, refusalRow{code: CodeWorkspaceShapeUnsupported, reason: reason, causes: zeroCauses})
	}
	for _, reason := range []RefusalReason{ReasonOperatingSystem, ReasonWSL, ReasonEmbeddedBuild} {
		rows = append(rows, refusalRow{code: CodePlatformUnsupported, reason: reason, causes: zeroCauses})
	}
	rows = append(rows,
		refusalRow{
			code: CodePlatformUnsupported, reason: ReasonFilesystem,
			causes: func(causes refusalCauseMask) (bool, refusalCauseMask) {
				return causes&^(causeWorkspaceUnsupported|causeStandardUnsupported) == 0, 0
			},
		},
		refusalRow{
			code: CodeWorkspaceChanged, reason: ReasonWorkspaceObservation, retryable: true,
			causes: func(causes refusalCauseMask) (bool, refusalCauseMask) {
				class := causes & (causeChanged | causeIneligible)
				allowed := causeChanged | causeIneligible | lifecycleOSRefusalCauses
				return causes&^allowed == 0 && (class == causeChanged || class == causeIneligible), causeChanged
			},
		},
		refusalRow{
			code: CodeWorkspaceUnverifiable, reason: ReasonBindingClosed,
			causes: func(causes refusalCauseMask) (bool, refusalCauseMask) {
				return causes == causeClosed, causeClosed
			},
		},
		refusalRow{
			code: CodeWorkspaceUnverifiable, reason: ReasonCleanup,
			causes: func(causes refusalCauseMask) (bool, refusalCauseMask) {
				return causes&causeCleanup != 0, causeCleanup
			},
		},
	)
	for _, reason := range []RefusalReason{ReasonRequest, ReasonPlatformProbe, ReasonWorkspace, ReasonMetadata, ReasonProvider, ReasonFilesystemProbe} {
		rows = append(rows, refusalRow{
			code: CodeWorkspaceUnverifiable, reason: reason,
			causes: func(causes refusalCauseMask) (bool, refusalCauseMask) {
				allowed := causeUnverifiable | causeClosed | lifecycleOSRefusalCauses
				return causes&^allowed == 0, 0
			},
		})
	}
	rows = append(rows, refusalRow{code: CodeCredentialInLocator, reason: ReasonTargetCredential, causes: zeroCauses})

	for _, row := range rows {
		name := fmt.Sprintf("%s/%s/%t", row.code, row.reason, row.retryable)
		t.Run(name, func(t *testing.T) {
			for rawCauses := 0; rawCauses <= int(allLifecycleRefusalCauses); rawCauses++ {
				causes := refusalCauseMask(rawCauses)
				wantValid, wantCanonical := row.causes(causes)
				input := refusalWithCauses(row.code, row.reason, row.retryable, causes)
				got := normalizeW3LifecycleError(input)
				if !wantValid {
					if got != ErrCommandExecution {
						t.Fatalf("causes=%d normalized=%#v, want ErrCommandExecution", causes, got)
					}
					continue
				}
				refusal, ok := got.(*Refusal)
				if !ok || refusal == input {
					t.Fatalf("causes=%d normalized=%#v, want distinct Refusal clone", causes, got)
				}
				if refusal.Code != row.code || refusal.Reason != row.reason || refusal.Retryable != row.retryable ||
					refusal.Effect != effectNone || refusal.causes != wantCanonical {
					t.Fatalf("causes=%d normalized=%#v canonical=%d, want %s/%s/%t/%d", causes, refusal,
						refusal.causes, row.code, row.reason, row.retryable, wantCanonical)
				}
			}
		})
	}
}

func TestCanonicalLifecycleRefusalRejectsForgedAndAmbiguousResults(t *testing.T) {
	unknownCause := refusalCauseMask(1 << 15)
	tests := []struct {
		name string
		err  error
	}{
		{name: "reserved credentials required", err: refusal(CodeCredentialsRequired, ReasonTargetCredential, true, nil)},
		{name: "unknown code", err: &Refusal{Code: RefusalCode("private-code"), Reason: ReasonRequest, Effect: effectNone}},
		{name: "unknown reason", err: &Refusal{Code: CodePairUnsupported, Reason: RefusalReason("private-reason"), Effect: effectNone}},
		{name: "wrong retryability", err: &Refusal{Code: CodePairUnsupported, Reason: ReasonTargetBackend, Retryable: true, Effect: effectNone}},
		{name: "wrong effect", err: &Refusal{Code: CodePairUnsupported, Reason: ReasonTargetBackend, Effect: "private-effect"}},
		{name: "static cause", err: refusalWithCauses(CodePairUnsupported, ReasonTargetBackend, false, causeNotExist)},
		{name: "unknown cause", err: refusalWithCauses(CodeWorkspaceUnverifiable, ReasonCleanup, false, causeCleanup|unknownCause)},
		{name: "changed no class", err: refusalWithCauses(CodeWorkspaceChanged, ReasonWorkspaceObservation, true, causeNotExist)},
		{name: "changed two classes", err: refusalWithCauses(CodeWorkspaceChanged, ReasonWorkspaceObservation, true, causeChanged|causeIneligible)},
		{name: "changed cleanup", err: refusalWithCauses(CodeWorkspaceChanged, ReasonWorkspaceObservation, true, causeChanged|causeCleanup)},
		{name: "cleanup missing marker", err: refusalWithCauses(CodeWorkspaceUnverifiable, ReasonCleanup, false, causeUnverifiable)},
		{name: "operational changed marker", err: refusalWithCauses(CodeWorkspaceUnverifiable, ReasonMetadata, false, causeChanged)},
		{name: "wrapped valid refusal", err: fmt.Errorf("private wrapper: %w", refusal(CodePairUnsupported, ReasonTargetBackend, false, nil))},
		{name: "joined valid refusals", err: errors.Join(
			refusal(CodePairUnsupported, ReasonTargetBackend, false, nil),
			refusal(CodeWorkspaceShapeUnsupported, ReasonSelector, false, nil),
		)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeW3LifecycleError(test.err); got != ErrCommandExecution {
				t.Fatalf("normalized=%#v, want ErrCommandExecution", got)
			}
		})
	}
}

type panicIsLifecycleError struct{}

func (panicIsLifecycleError) Error() string { return "private panic-Is error" }
func (panicIsLifecycleError) Is(error) bool { panic("private Is panic") }

type panicNilIsLifecycleError struct{}

func (panicNilIsLifecycleError) Error() string { return "private panic-nil-Is error" }
func (panicNilIsLifecycleError) Is(error) bool { panic(nil) }

func TestCommandLifecycleErrorNormalizationPrecedence(t *testing.T) {
	private := errors.New("private nested error")
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "canceled first", err: errors.Join(context.DeadlineExceeded, ErrCommandResourceState, context.Canceled, ErrCommandResourceCleanup, private), want: context.Canceled},
		{name: "deadline before state", err: errors.Join(ErrCommandResourceState, context.DeadlineExceeded, private), want: context.DeadlineExceeded},
		{name: "state before cleanup", err: errors.Join(ErrCommandResourceCleanup, ErrCommandResourceState, private), want: ErrCommandResourceState},
		{name: "cleanup before execution", err: errors.Join(ErrCommandExecution, ErrCommandResourceCleanup, private), want: ErrCommandResourceCleanup},
		{name: "unknown", err: private, want: ErrCommandExecution},
		{name: "panic in Is", err: panicIsLifecycleError{}, want: ErrCommandExecution},
		{name: "nil panic in Is", err: panicNilIsLifecycleError{}, want: ErrCommandExecution},
		{name: "callback refusal", err: refusal(CodePairUnsupported, ReasonTargetBackend, false, nil), want: ErrCommandExecution},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeCommandLifecycleError(test.err); got != test.want {
				t.Fatalf("normalized=%#v, want %#v", got, test.want)
			}
		})
	}
}

func TestCommandLifecyclePanicNilCompatibility(t *testing.T) {
	t.Setenv("GODEBUG", "panicnil=1")

	t.Run("callback", func(t *testing.T) {
		binding := &lifecycleFakeBinding{}
		returned := false
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			_ = withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
				func(context.Context, *CommandResourceScope, BoundProviderConfiguration) error {
					panic(nil)
				}, lifecycleDeps(binding, nil, nil))
			returned = true
		}()
		if returned || recovered != ErrCommandExecution {
			t.Fatalf("returned=%v recovered=%#v, want fixed execution panic", returned, recovered)
		}
		if _, closes := binding.counts(); closes != 1 {
			t.Fatalf("binding closes=%d, want 1", closes)
		}
	})

	t.Run("closer", func(t *testing.T) {
		binding := &lifecycleFakeBinding{}
		err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
			func(_ context.Context, scope *CommandResourceScope, _ BoundProviderConfiguration) error {
				return scope.deferSourceClose(func() error { panic(nil) })
			}, lifecycleDeps(binding, nil, nil))
		if err != ErrCommandResourceCleanup {
			t.Fatalf("error=%#v, want cleanup singleton", err)
		}
		if _, closes := binding.counts(); closes != 1 {
			t.Fatalf("binding closes=%d, want 1", closes)
		}
	})

	t.Run("normalization", func(t *testing.T) {
		if got := normalizeCommandLifecycleError(panicNilIsLifecycleError{}); got != ErrCommandExecution {
			t.Fatalf("normalized=%#v, want ErrCommandExecution", got)
		}
	})
}

func TestCallbackRefusalNeverRetainsZeroEffectClaim(t *testing.T) {
	for _, adoptSource := range []bool{false, true} {
		t.Run(fmt.Sprintf("adopt-source=%t", adoptSource), func(t *testing.T) {
			binding := &lifecycleFakeBinding{}
			err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
				func(_ context.Context, scope *CommandResourceScope, _ BoundProviderConfiguration) error {
					if adoptSource {
						if adoptErr := scope.deferSourceClose(func() error { return nil }); adoptErr != nil {
							return adoptErr
						}
					}
					return refusal(CodePairUnsupported, ReasonTargetBackend, false, nil)
				}, lifecycleDeps(binding, nil, nil))
			if err != ErrCommandExecution {
				t.Fatalf("error=%#v, want ErrCommandExecution", err)
			}
			var got *Refusal
			if errors.As(err, &got) {
				t.Fatalf("callback refusal escaped after adoption=%t: %#v", adoptSource, got)
			}
		})
	}
}

type privateLifecycleError struct{ value string }

func (e *privateLifecycleError) Error() string { return e.value }

func TestCommandLifecycleErrorsAndPanicGraphsDiscardSecrets(t *testing.T) {
	const secret = "private-dsn-password-path-schema-cause"
	secretErr := &privateLifecycleError{value: secret}
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(secretErr)
	bindCalls := 0
	if err := withBoundProviderConfigurationWith(ctx, ProviderConfigurationRequest{},
		func(context.Context, *CommandResourceScope, BoundProviderConfiguration) error {
			t.Fatal("callback ran for canceled context")
			return nil
		}, lifecycleDeps(nil, nil, func() { bindCalls++ })); err != context.Canceled || bindCalls != 0 {
		t.Fatalf("canceled error=%#v bindCalls=%d", err, bindCalls)
	}

	binding := &lifecycleFakeBinding{closeErr: fmt.Errorf("wrapped: %w", secretErr)}
	err := withBoundProviderConfigurationWith(context.Background(), ProviderConfigurationRequest{},
		func(_ context.Context, scope *CommandResourceScope, _ BoundProviderConfiguration) error {
			if adoptErr := scope.deferSourceClose(func() error { return errors.Join(secretErr, errors.New(secret)) }); adoptErr != nil {
				return adoptErr
			}
			return errors.Join(secretErr, fmt.Errorf("nested: %w", secretErr))
		}, lifecycleDeps(binding, nil, nil))
	if !errors.Is(err, ErrCommandExecution) || !errors.Is(err, ErrCommandResourceCleanup) {
		t.Fatalf("error=%#v, want execution+cleanup", err)
	}
	assertLifecycleErrorGraphHasNoText(t, err, secret)
	var reached *privateLifecycleError
	if errors.As(err, &reached) {
		t.Fatalf("private error remained reachable: %#v", reached)
	}
}

func assertLifecycleErrorGraphHasNoText(t *testing.T, root error, forbidden string) {
	t.Helper()
	seen := make(map[error]bool)
	var visit func(error, int)
	visit = func(err error, depth int) {
		if err == nil || depth > 32 {
			return
		}
		value := reflect.ValueOf(err)
		if value.IsValid() && value.Type().Comparable() {
			if seen[err] {
				return
			}
			seen[err] = true
		}
		for _, rendered := range []string{err.Error(), fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
			if strings.Contains(rendered, forbidden) {
				t.Fatalf("error graph rendered forbidden text %q in %q", forbidden, rendered)
			}
		}
		switch unwrapped := any(err).(type) {
		case interface{ Unwrap() []error }:
			for _, child := range unwrapped.Unwrap() {
				visit(child, depth+1)
			}
		case interface{ Unwrap() error }:
			visit(unwrapped.Unwrap(), depth+1)
		}
	}
	visit(root, 0)
}

func TestLifecycleProductionSourceFence(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "lifecycle.go", nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse lifecycle.go: %v", err)
	}
	allowedImports := map[string]map[string]bool{
		"context": {"Canceled": true, "Context": true, "DeadlineExceeded": true},
		"errors":  {"Is": true, "Join": true, "New": true},
		"sync":    {"Cond": true, "Mutex": true, "NewCond": true},
	}
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		if _, ok := allowedImports[path]; !ok {
			t.Errorf("lifecycle.go imports non-allowlisted package %q", path)
		}
		if spec.Name != nil {
			t.Errorf("lifecycle.go aliases or dot-imports %q", path)
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		packageName, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		if selectors, imported := allowedImports[packageName.Name]; imported && !selectors[selector.Sel.Name] {
			t.Errorf("lifecycle.go uses non-allowlisted selector %s.%s", packageName.Name, selector.Sel.Name)
		}
		return true
	})
}

func waitForSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}
