//go:build linux && cgo

package backendmigration

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/configfile"
)

func TestProviderConfigurationBindingRealSnapshotHasNoAmbientEffects(t *testing.T) {
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
	if err := os.WriteFile(serviceFile, []byte("private-service-value"), 0o600); err != nil {
		t.Fatal(err)
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
	t.Setenv("PGPASSWORD", "ambient-password-must-not-be-used")
	t.Setenv("BEADS_DOLT_CREDENTIAL_COMMAND", "touch "+credentialCanary)
	t.Setenv("BEADS_POSTGRES_URL", "postgresql://ambient@"+listener.Addr().String()+"/ambient?sslmode=require")
	t.Setenv("BEADS_METRICS_ENDPOINT", "http://"+listener.Addr().String())
	environmentBefore := append([]string(nil), os.Environ()...)
	descriptorsBefore := openDescriptorSnapshot(t)
	workspaceBefore := digestTree(t, workspace)
	homeBefore := digestTree(t, home)
	assertNoStateEffects := func() {
		t.Helper()
		if digestTree(t, workspace) != workspaceBefore || digestTree(t, home) != homeBefore {
			t.Fatal("binding changed workspace, credential, service, helper, or FIFO tree state")
		}
		afterMetadata, readErr := os.ReadFile(metadataPath)
		if readErr != nil || !bytes.Equal(afterMetadata, metadata) {
			t.Fatalf("metadata after binding=%q, %v", afterMetadata, readErr)
		}
		if !reflect.DeepEqual(environmentBefore, os.Environ()) {
			t.Fatal("binding mutated the process environment")
		}
		if _, statErr := os.Stat(credentialCanary); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("credential helper canary was touched: %v", statErr)
		}
		if _, statErr := os.Stat(deepTarget); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("deep provider canary was touched: %v", statErr)
		}
	}
	assertNoNetworkEffect := func() {
		t.Helper()
		if err := listener.SetDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		connection, acceptErr := listener.AcceptTCP()
		if connection != nil {
			_ = connection.Close()
			t.Fatal("binding connected to the target, ambient, or telemetry listener")
		}
		if networkErr, ok := acceptErr.(net.Error); !ok || !networkErr.Timeout() {
			t.Fatalf("listener error=%v, want timeout", acceptErr)
		}
	}

	request := validProviderConfigurationRequest(workspace)
	request.Target.Locator = "postgresql://explicit@" + listener.Addr().String() + "/target?sslmode=require"
	binding, err := BindProviderConfiguration(request)
	if err != nil {
		if after := openDescriptorSnapshot(t); !reflect.DeepEqual(after, descriptorsBefore) {
			t.Fatalf("failed bind leaked descriptors:\nbefore=%v\nafter=%v", descriptorsBefore, after)
		}
		assertNoStateEffects()
		assertNoNetworkEffect()
		var refusal *Refusal
		if errors.As(err, &refusal) && refusal.Code == CodePlatformUnsupported && refusal.Reason == ReasonFilesystem {
			t.Skip("host filesystem is outside the exact ext4/XFS qualification")
		}
		t.Fatal(err)
	}
	liveDescriptors := openDescriptorSnapshot(t)
	configuration, err := binding.Snapshot()
	if err != nil {
		_ = binding.Close()
		t.Fatalf("Snapshot: %v", err)
	}
	if source := configuration.Source(); source.BeadsDir() != workspace || source.Database() != "custom_allowed" || source.Branch() != "main" {
		_ = binding.Close()
		t.Fatalf("source=%#v, want witnessed workspace/custom_allowed/main", source)
	}
	if after := openDescriptorSnapshot(t); !reflect.DeepEqual(after, liveDescriptors) {
		_ = binding.Close()
		t.Fatalf("Snapshot changed live descriptors:\nbefore=%v\nafter=%v", liveDescriptors, after)
	}
	if err := binding.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := binding.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if after := openDescriptorSnapshot(t); !reflect.DeepEqual(after, descriptorsBefore) {
		t.Fatalf("Close did not restore descriptors:\nbefore=%v\nafter=%v", descriptorsBefore, after)
	}
	assertNoStateEffects()
	assertNoNetworkEffect()
}
