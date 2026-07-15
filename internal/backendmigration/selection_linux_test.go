//go:build linux && cgo

package backendmigration

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/configfile"
)

func TestInspectSourceShapeRealCandidateClosesDescriptorsAndHasNoEffects(t *testing.T) {
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
	t.Setenv("HOME", home)
	credentialCanary := filepath.Join(home, "credential-helper-ran")
	t.Setenv("BEADS_DOLT_CREDENTIAL_COMMAND", "touch "+credentialCanary)
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close() //nolint:errcheck // test listener
	t.Setenv("BEADS_POSTGRES_URL", "postgres://"+listener.Addr().String()+"/private")
	beforeDescriptors := openDescriptorSnapshot(t)
	beforeTree := digestTree(t, workspace)

	candidate, err := InspectSourceShape(validSelectionRequest(workspace))
	if err != nil {
		var refusal *Refusal
		if errors.As(err, &refusal) && refusal.Code == CodePlatformUnsupported && refusal.Reason == ReasonFilesystem {
			t.Skip("host filesystem is outside the exact ext4/XFS qualification")
		}
		t.Fatal(err)
	}
	want := SourceShapeCandidate{SourceBackend: configfile.BackendDolt, TargetBackend: configfile.BackendPostgres}
	if candidate != want {
		t.Fatalf("candidate=%#v, want %#v", candidate, want)
	}
	if after := openDescriptorSnapshot(t); !reflect.DeepEqual(after, beforeDescriptors) {
		t.Fatalf("process descriptors changed after inspection:\nbefore=%v\nafter=%v", beforeDescriptors, after)
	}
	if afterTree := digestTree(t, workspace); afterTree != beforeTree {
		t.Fatal("candidate inspection changed workspace tree bytes, paths, or modes")
	}
	afterMetadata, err := os.ReadFile(metadataPath)
	if err != nil || !bytes.Equal(afterMetadata, metadata) {
		t.Fatalf("metadata after inspection=%q, %v", afterMetadata, err)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("HOME side effects=%v, %v", entries, err)
	}
	if _, err := os.Stat(credentialCanary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credential helper canary was touched: %v", err)
	}
	if _, err := os.Stat(deepTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deep provider canary was touched: %v", err)
	}
	if err := listener.SetDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	connection, acceptErr := listener.AcceptTCP()
	if connection != nil {
		_ = connection.Close()
		t.Fatal("migration shape inspection connected to the network canary")
	}
	if networkErr, ok := acceptErr.(net.Error); !ok || !networkErr.Timeout() {
		t.Fatalf("network canary accept error=%v, want timeout", acceptErr)
	}
}

func TestInspectSourceShapeAllRealOutcomesReleaseDescriptorsAndPreserveTree(t *testing.T) {
	requireNativeLinuxEmbeddedBuild(t)
	tests := []struct {
		name     string
		metadata string
		redirect bool
		provider bool
	}{
		{name: "pair refusal", metadata: `{"backend":"sqlite"}`, provider: true},
		{name: "shape refusal", metadata: `{"backend":"dolt","dolt_mode":"server"}`, provider: true},
		{name: "unverifiable parse", metadata: `{"backend":"dolt","unknown":"private"}`, provider: true},
		{name: "unverifiable provider", metadata: `{"backend":"dolt","dolt_mode":"embedded"}`},
		{name: "prebind redirect", redirect: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspace := filepath.Join(t.TempDir(), ".beads")
			if err := os.Mkdir(workspace, 0o700); err != nil {
				t.Fatal(err)
			}
			if test.metadata != "" {
				if err := os.WriteFile(filepath.Join(workspace, configfile.ConfigFileName), []byte(test.metadata), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if test.provider {
				if err := os.Mkdir(filepath.Join(workspace, "embeddeddolt"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if test.redirect {
				if err := os.WriteFile(filepath.Join(workspace, "redirect"), []byte("private-target"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			beforeDescriptors := openDescriptorSnapshot(t)
			beforeTree := digestTree(t, workspace)
			candidate, err := InspectSourceShape(validSelectionRequest(workspace))
			if candidate != (SourceShapeCandidate{}) || err == nil {
				t.Fatalf("candidate=%#v err=%v, want refusal", candidate, err)
			}
			if after := openDescriptorSnapshot(t); !reflect.DeepEqual(after, beforeDescriptors) {
				t.Fatalf("process descriptors changed:\nbefore=%v\nafter=%v", beforeDescriptors, after)
			}
			if afterTree := digestTree(t, workspace); afterTree != beforeTree {
				t.Fatal("refusal changed workspace tree bytes, paths, or modes")
			}
		})
	}
}

func TestInspectSourceShapeRedirectDoesNotReadContents(t *testing.T) {
	requireNativeLinuxEmbeddedBuild(t)
	workspace := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	redirect := filepath.Join(workspace, "redirect")
	if err := os.WriteFile(redirect, []byte("private-target"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1, 0)
	if err := os.Chtimes(redirect, old, time.Now()); err != nil {
		t.Fatal(err)
	}
	before := linuxStat(t, redirect)
	candidate, err := InspectSourceShape(validSelectionRequest(workspace))
	requireRefusal(t, candidate, err, CodeWorkspaceShapeUnsupported, ReasonRedirect)
	after := linuxStat(t, redirect)
	if before.Atim != after.Atim {
		t.Fatalf("redirect access time changed from %v to %v; contents may have been read", before.Atim, after.Atim)
	}
}

func openDescriptorSnapshot(t *testing.T) map[string]int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("descriptor inspection unavailable: %v", err)
	}
	snapshot := make(map[string]int)
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err == nil {
			snapshot[strings.TrimSuffix(target, " (deleted)")]++
		}
	}
	return snapshot
}

func linuxStat(t *testing.T, path string) syscall.Stat_t {
	t.Helper()
	var stat syscall.Stat_t
	if err := syscall.Stat(path, &stat); err != nil {
		t.Fatal(err)
	}
	return stat
}
