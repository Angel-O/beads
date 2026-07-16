package dolt

import (
	"strings"
	"testing"
	"time"
)

func TestServerWireTimeoutParsing(t *testing.T) {
	const env = "BEADS_DOLT_SERVER_READ_TIMEOUT"
	def := 10 * time.Second

	t.Run("absent uses default", func(t *testing.T) {
		t.Setenv(env, "")
		if got := serverWireTimeout(env, def); got != def {
			t.Fatalf("got %s, want default %s", got, def)
		}
	})
	t.Run("valid positive seconds", func(t *testing.T) {
		t.Setenv(env, "45")
		if got := serverWireTimeout(env, def); got != 45*time.Second {
			t.Fatalf("got %s, want 45s", got)
		}
	})
	t.Run("zero is invalid, uses default", func(t *testing.T) {
		t.Setenv(env, "0")
		if got := serverWireTimeout(env, def); got != def {
			t.Fatalf("got %s, want default %s", got, def)
		}
	})
	t.Run("non-numeric is invalid, uses default", func(t *testing.T) {
		t.Setenv(env, "30s")
		if got := serverWireTimeout(env, def); got != def {
			t.Fatalf("got %s, want default %s", got, def)
		}
	})
}

// The overrides reach the baked DSN; defaults preserve today's values.
func TestBuildServerDSNTimeoutOverrides(t *testing.T) {
	cfg := &Config{ServerHost: "gw.example.com", ServerPort: 3306, ServerUser: "root"}

	t.Run("defaults", func(t *testing.T) {
		dsn := buildServerDSN(cfg, "beads")
		for _, want := range []string{"timeout=5s", "readTimeout=10s", "writeTimeout=10s"} {
			if !strings.Contains(dsn, want) {
				t.Fatalf("default DSN missing %q: %q", want, dsn)
			}
		}
	})
	t.Run("overrides", func(t *testing.T) {
		t.Setenv(envServerConnectTimeout, "3")
		t.Setenv(envServerReadTimeout, "20")
		t.Setenv(envServerWriteTimeout, "25")
		dsn := buildServerDSN(cfg, "beads")
		for _, want := range []string{"timeout=3s", "readTimeout=20s", "writeTimeout=25s"} {
			if !strings.Contains(dsn, want) {
				t.Fatalf("overridden DSN missing %q: %q", want, dsn)
			}
		}
	})
}
