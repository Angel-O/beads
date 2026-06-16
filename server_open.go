package beads

import (
	"context"
	"os"
	"path/filepath"

	"github.com/steveyegge/beads/internal/storage/dolt"
)

// ServerConfig describes how to reach an external dolt sql-server (server mode).
// It is the programmatic equivalent of the dolt_mode="server" metadata.json
// settings, for hosts that embed bd's storage layer and manage their own
// connection details — e.g. a multi-tenant service connecting to a different
// database per tenant. Unlike OpenFromConfig, it requires no .beads directory
// or metadata.json on disk, and the password is supplied directly rather than
// via a credentials file or environment variable.
type ServerConfig struct {
	Host     string // dolt sql-server host
	Port     int    // dolt sql-server port
	User     string // MySQL user
	Password string // MySQL password (supplied directly)
	Database string // SQL database name to USE
	TLS      bool   // enable TLS (required for Hosted Dolt)

	// CreateIfMissing issues CREATE DATABASE for Database if it does not exist.
	CreateIfMissing bool

	// WorkDir is an optional writable directory for transient server-mode
	// bookkeeping (e.g. a resolved-port file when connecting to a localhost
	// server). It is not used for remote servers. Defaults to os.TempDir().
	WorkDir string
}

// OpenServer opens a Storage backed by an external dolt sql-server using the
// connection details in cfg directly, without reading metadata.json. Use it when
// embedding bd's storage layer in a service that owns its own configuration.
//
// The returned Storage must be closed when no longer needed.
func OpenServer(ctx context.Context, cfg ServerConfig) (Storage, error) {
	workDir := cfg.WorkDir
	if workDir == "" {
		workDir = os.TempDir()
	}
	return dolt.New(ctx, &dolt.Config{
		ServerMode:      true,
		ServerHost:      cfg.Host,
		ServerPort:      cfg.Port,
		ServerUser:      cfg.User,
		ServerPassword:  cfg.Password,
		ServerTLS:       cfg.TLS,
		Database:        cfg.Database,
		CreateIfMissing: cfg.CreateIfMissing,
		BeadsDir:        workDir,
		// Path is vestigial in server mode (the server holds the data) but New
		// requires it non-empty; point it inside WorkDir.
		Path: filepath.Join(workDir, "dolt"),
	})
}
