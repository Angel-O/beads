//go:build linux && cgo

package backendmigration

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/configfile"
)

func TestCommandResourceLifecycleRealBindingRestoresEffectsAcrossReuse(t *testing.T) {
	requireNativeLinuxEmbeddedBuild(t)

	workspace := filepath.Join(t.TempDir(), ".beads")
	provider := filepath.Join(workspace, "embeddeddolt")
	if err := os.MkdirAll(provider, 0o700); err != nil {
		t.Fatal(err)
	}
	deepTarget := filepath.Join(t.TempDir(), "deep-provider-target-must-not-be-read")
	if err := os.Symlink(deepTarget, filepath.Join(provider, ".dolt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	metadata := []byte(`{"backend":"dolt","dolt_mode":"embedded","dolt_database":"custom_allowed"}`)
	metadataPath := filepath.Join(workspace, configfile.ConfigFileName)
	if err := os.WriteFile(metadataPath, metadata, 0o600); err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	passwordFIFO := filepath.Join(home, "pgpass-fifo-must-not-be-read")
	if err := syscall.Mkfifo(passwordFIFO, 0o600); err != nil {
		t.Skipf("FIFO unavailable: %v", err)
	}
	serviceFile := filepath.Join(home, "pg-service-must-not-be-read")
	tlsFile := filepath.Join(home, "pg-tls-must-not-be-read")
	for _, path := range []string{serviceFile, tlsFile} {
		if err := os.WriteFile(path, []byte("private-provider-material"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	credentialCanary := filepath.Join(home, "credential-helper-ran")

	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close() //nolint:errcheck // test listener

	t.Setenv("HOME", home)
	t.Setenv("PGPASSFILE", passwordFIFO)
	t.Setenv("PGSERVICEFILE", serviceFile)
	t.Setenv("PGSSLCERT", tlsFile)
	t.Setenv("PGSSLKEY", tlsFile)
	t.Setenv("PGSSLROOTCERT", tlsFile)
	t.Setenv("PGPASSWORD", "ambient-password-must-not-be-used")
	t.Setenv("BEADS_DOLT_CREDENTIAL_COMMAND", "touch "+credentialCanary)
	t.Setenv("BEADS_POSTGRES_URL", "postgresql://ambient@"+listener.Addr().String()+"/ambient?sslmode=require")
	t.Setenv("BEADS_METRICS_ENDPOINT", "http://"+listener.Addr().String())

	environmentBefore := append([]string(nil), os.Environ()...)
	descriptorsBefore := openDescriptorSnapshot(t)
	workspaceBefore := digestTree(t, workspace)
	homeBefore := digestTree(t, home)
	assertNoEffects := func() {
		t.Helper()
		if after := openDescriptorSnapshot(t); !reflect.DeepEqual(after, descriptorsBefore) {
			t.Fatalf("lifecycle did not restore descriptors:\nbefore=%v\nafter=%v", descriptorsBefore, after)
		}
		if digestTree(t, workspace) != workspaceBefore || digestTree(t, home) != homeBefore {
			t.Fatal("lifecycle changed workspace, credential, service, TLS, or FIFO tree state")
		}
		afterMetadata, readErr := os.ReadFile(metadataPath)
		if readErr != nil || !bytes.Equal(afterMetadata, metadata) {
			t.Fatalf("metadata after lifecycle=%q, %v", afterMetadata, readErr)
		}
		if !reflect.DeepEqual(environmentBefore, os.Environ()) {
			t.Fatal("lifecycle mutated the process environment")
		}
		if _, statErr := os.Stat(credentialCanary); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("credential helper canary was touched: %v", statErr)
		}
		if _, statErr := os.Stat(deepTarget); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("deep provider canary was touched: %v", statErr)
		}
		if err := listener.SetDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		connection, acceptErr := listener.AcceptTCP()
		if connection != nil {
			_ = connection.Close()
			t.Fatal("lifecycle connected to a provider, plugin, ambient, or telemetry listener")
		}
		if networkErr, ok := acceptErr.(net.Error); !ok || !networkErr.Timeout() {
			t.Fatalf("listener error=%v, want timeout", acceptErr)
		}
	}

	request := validProviderConfigurationRequest(workspace)
	request.Target.Locator = "postgresql://explicit@" + listener.Addr().String() + "/target?sslmode=require"
	tests := []struct {
		name         string
		firstOutcome error
		cancelFirst  bool
	}{
		{name: "success then success"},
		{name: "run error then success", firstOutcome: errors.New("private first-run failure")},
		{name: "cancellation then success", cancelFirst: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var scopes []*CommandResourceScope
			command := &cobra.Command{Use: "lifecycle", SilenceErrors: true, SilenceUsage: true}
			for run := 0; run < 2; run++ {
				ctx, cancel := context.WithCancel(context.Background())
				command.RunE = func(*cobra.Command, []string) error {
					return WithBoundProviderConfiguration(ctx, request,
						func(_ context.Context, scope *CommandResourceScope, configuration BoundProviderConfiguration) error {
							scopes = append(scopes, scope)
							if source := configuration.Source(); source.BeadsDir() != workspace || source.Database() != "custom_allowed" {
								t.Fatalf("source=%#v, want witnessed workspace/custom_allowed", source)
							}
							if run == 0 && test.cancelFirst {
								cancel()
								return nil
							}
							if run == 0 {
								return test.firstOutcome
							}
							return nil
						})
				}

				err := command.ExecuteContext(ctx)
				cancel()
				assertNoEffects()
				var refusal *Refusal
				if errors.As(err, &refusal) && refusal.Code == CodePlatformUnsupported && refusal.Reason == ReasonFilesystem {
					t.Skip("host filesystem is outside the exact ext4/XFS qualification")
				}
				var want error
				switch {
				case run == 0 && test.cancelFirst:
					want = context.Canceled
				case run == 0 && test.firstOutcome != nil:
					want = ErrCommandExecution
				}
				if err != want {
					t.Fatalf("run %d error=%#v, want %#v", run+1, err, want)
				}
				if closeErr := scopes[run].close(); closeErr != nil {
					t.Fatalf("run %d retained scope close=%v, want cached success", run+1, closeErr)
				}
			}
			if len(scopes) != 2 || scopes[0] == scopes[1] || scopes[0].state == scopes[1].state {
				t.Fatalf("sequential executions reused scopes: %#v", scopes)
			}
		})
	}
}
