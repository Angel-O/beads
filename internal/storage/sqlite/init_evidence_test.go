package sqlite

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/safefile"
)

func TestHasLocalInitializationEvidence(t *testing.T) {
	t.Run("empty workspace", func(t *testing.T) {
		exists, err := HasLocalInitializationEvidence(t.TempDir(), "")
		if err != nil || exists {
			t.Fatalf("got exists=%v err=%v, want verified absence", exists, err)
		}
	})

	t.Run("missing workspace", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), "missing")
		exists, err := HasLocalInitializationEvidence(beadsDir, "")
		if err != nil || exists {
			t.Fatalf("got exists=%v err=%v, want verified absence", exists, err)
		}
	})

	for _, tt := range []struct {
		name       string
		configured string
	}{
		{name: "canonical"},
		{name: "custom arbitrary name", configured: "custom-provider-file"},
		{name: "custom nested path", configured: filepath.Join("nested", "provider")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			beadsDir := t.TempDir()
			path := tt.configured
			if path == "" {
				path = defaultSQLitePath
			}
			path = filepath.Join(beadsDir, path)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			store, err := Provision(t.Context(), path)
			if err != nil {
				t.Fatalf("provision SQLite evidence: %v", err)
			}
			if err := store.Close(); err != nil {
				t.Fatalf("close SQLite evidence: %v", err)
			}

			exists, err := HasLocalInitializationEvidence(beadsDir, tt.configured)
			if err != nil || !exists {
				t.Fatalf("got exists=%v err=%v, want initialized", exists, err)
			}
		})
	}

	t.Run("custom absolute path", func(t *testing.T) {
		beadsDir := t.TempDir()
		path := filepath.Join(t.TempDir(), "provider.db")
		store, err := Provision(t.Context(), path)
		if err != nil {
			t.Fatalf("provision SQLite evidence: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("close SQLite evidence: %v", err)
		}
		exists, err := HasLocalInitializationEvidence(beadsDir, path)
		if err != nil || !exists {
			t.Fatalf("got exists=%v err=%v, want initialized", exists, err)
		}
	})

	t.Run("valid hard-linked database fails closed", func(t *testing.T) {
		beadsDir := t.TempDir()
		path := filepath.Join(beadsDir, defaultSQLitePath)
		store, err := Provision(t.Context(), path)
		if err != nil {
			t.Fatalf("provision SQLite evidence: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("close SQLite evidence: %v", err)
		}
		if err := os.Link(path, filepath.Join(beadsDir, "database-alias.db")); err != nil {
			t.Skipf("hard links unavailable: %v", err)
		}

		exists, err := HasLocalInitializationEvidence(beadsDir, "")
		if err == nil || exists || !strings.Contains(err.Error(), "hard links") {
			t.Fatalf("got exists=%v err=%v, want hard-link rejection", exists, err)
		}
	})

	t.Run("opened database must remain bound to its name", func(t *testing.T) {
		beadsDir := t.TempDir()
		path := filepath.Join(beadsDir, defaultSQLitePath)
		store, err := Provision(t.Context(), path)
		if err != nil {
			t.Fatalf("provision SQLite evidence: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("close SQLite evidence: %v", err)
		}

		moved := path + ".moved"
		var replacementErr error
		opener := func(candidate string) (*os.File, error) {
			file, err := safefile.OpenReadOnlyNoFollow(candidate)
			if err != nil {
				return nil, err
			}
			if err := os.Rename(candidate, moved); err != nil {
				_ = file.Close()
				return nil, err
			}
			if err := os.Symlink(moved, candidate); err != nil {
				replacementErr = err
				_ = file.Close()
				return nil, err
			}
			return file, nil
		}

		valid, present, err := sqliteDatabaseAtWithOpener(path, opener)
		if replacementErr != nil {
			t.Skipf("symlink replacement unavailable: %v", replacementErr)
		}
		if err == nil || !present || valid {
			t.Fatalf("got valid=%v present=%v err=%v, want replacement rejection", valid, present, err)
		}
	})

	t.Run("unrelated SQLite side databases are excluded", func(t *testing.T) {
		beadsDir := t.TempDir()
		for _, name := range []string{"vc.db", "ephemeral.sqlite3", "backup.db"} {
			store, err := Provision(t.Context(), filepath.Join(beadsDir, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
		}
		exists, err := HasLocalInitializationEvidence(beadsDir, "")
		if err != nil || exists {
			t.Fatalf("got exists=%v err=%v, want unrelated databases ignored", exists, err)
		}
	})

	for _, tt := range []struct {
		name       string
		configured string
		create     func(t *testing.T, path string)
	}{
		{
			name: "damaged canonical file",
			create: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte("not sqlite"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:       "damaged custom file",
			configured: "custom.db",
			create: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte("not sqlite"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "header only",
			create: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte(sqliteHeader), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:       "custom directory",
			configured: "custom.db",
			create: func(t *testing.T, path string) {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "dangling canonical symlink",
			create: func(t *testing.T, path string) {
				if err := os.Symlink(path+".missing", path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
		},
		{
			name:       "dangling extensionless custom symlink",
			configured: "custom-provider-file",
			create: func(t *testing.T, path string) {
				if err := os.Symlink(path+".missing", path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
		},
	} {
		t.Run(tt.name+" fails closed", func(t *testing.T) {
			beadsDir := t.TempDir()
			path := tt.configured
			if path == "" {
				path = defaultSQLitePath
			}
			path = filepath.Join(beadsDir, path)
			tt.create(t, path)
			if exists, err := HasLocalInitializationEvidence(beadsDir, tt.configured); err == nil || exists {
				t.Fatalf("got exists=%v err=%v, want operational error", exists, err)
			}
		})
	}

	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		for _, configured := range []string{"", "custom-provider-file"} {
			name := "canonical"
			if configured != "" {
				name = "custom"
			}
			t.Run("orphaned "+name+" "+suffix+" fails closed", func(t *testing.T) {
				beadsDir := t.TempDir()
				path := configured
				if path == "" {
					path = defaultSQLitePath
				}
				if err := os.WriteFile(filepath.Join(beadsDir, path)+suffix, []byte("orphan"), 0o600); err != nil {
					t.Fatal(err)
				}
				if exists, err := HasLocalInitializationEvidence(beadsDir, configured); err == nil || exists {
					t.Fatalf("got exists=%v err=%v, want operational error", exists, err)
				}
			})
		}
	}

	t.Run("invalid reserved space is rejected", func(t *testing.T) {
		header := make([]byte, sqliteHeaderBytes)
		copy(header, sqliteHeader)
		binary.BigEndian.PutUint16(header[16:18], 512)
		header[18] = 1
		header[19] = 1
		header[20] = 33
		header[21] = 64
		header[22] = 32
		header[23] = 32
		if validSQLiteHeader(header, 512) {
			t.Fatal("accepted a SQLite header with fewer than 480 usable bytes")
		}
	})

	t.Run("dangling workspace root fails closed", func(t *testing.T) {
		parent := t.TempDir()
		root := filepath.Join(parent, "workspace")
		if err := os.Symlink(filepath.Join(parent, "missing"), root); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if exists, err := HasLocalInitializationEvidence(root, ""); err == nil || exists {
			t.Fatalf("got exists=%v err=%v, want operational error", exists, err)
		}
	})

	t.Run("dangling configured path parent fails closed", func(t *testing.T) {
		beadsDir := t.TempDir()
		parent := filepath.Join(beadsDir, "nested")
		if err := os.Symlink(filepath.Join(beadsDir, "missing"), parent); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		configured := filepath.Join("nested", "provider.db")
		if exists, err := HasLocalInitializationEvidence(beadsDir, configured); err == nil || exists {
			t.Fatalf("got exists=%v err=%v, want operational error", exists, err)
		}
	})

	t.Run("empty workspace path is invalid", func(t *testing.T) {
		if exists, err := HasLocalInitializationEvidence("", ""); err == nil || exists {
			t.Fatalf("got exists=%v err=%v, want invalid-input error", exists, err)
		}
	})

	t.Run("path errors quote control characters", func(t *testing.T) {
		cause := errors.New("permission denied")
		err := safePathError("inspect SQLite path", "unsafe\n\x1b[31m", &os.PathError{
			Op:   "lstat",
			Path: "unsafe\n\x1b[31m",
			Err:  cause,
		})
		if !errors.Is(err, cause) {
			t.Fatalf("path error lost its cause: %v", err)
		}
		if strings.ContainsAny(err.Error(), "\n\x1b") {
			t.Fatalf("path error contains raw terminal-control characters: %q", err)
		}
	})
}

func TestSQLiteEvidenceClassifiesMissingWorkspaceAncestors(t *testing.T) {
	t.Run("genuine absence is allowed", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), "missing", "workspace")
		if exists, err := HasLocalInitializationEvidence(beadsDir, ""); err != nil || exists {
			t.Fatalf("got exists=%v err=%v, want verified absence", exists, err)
		}
	})

	t.Run("dangling symlink ancestor fails closed", func(t *testing.T) {
		root := t.TempDir()
		ancestor := filepath.Join(root, "dangling")
		if err := os.Symlink(filepath.Join(root, "missing-target"), ancestor); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		beadsDir := filepath.Join(ancestor, "workspace")
		exists, err := HasLocalInitializationEvidence(beadsDir, "")
		if err == nil || exists {
			t.Fatalf("got exists=%v err=%v, want dangling-ancestor rejection", exists, err)
		}
		if errors.Is(err, os.ErrNotExist) {
			t.Fatalf("dangling-ancestor error was misclassified as absence: %v", err)
		}
	})

	t.Run("non-directory ancestor fails closed", func(t *testing.T) {
		ancestor := filepath.Join(t.TempDir(), "regular-file")
		if err := os.WriteFile(ancestor, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		beadsDir := filepath.Join(ancestor, "workspace")
		if exists, err := HasLocalInitializationEvidence(beadsDir, ""); err == nil || exists {
			t.Fatalf("got exists=%v err=%v, want non-directory-ancestor rejection", exists, err)
		}
	})

	t.Run("dangling final symlink syntax fails closed", func(t *testing.T) {
		root := t.TempDir()
		link := filepath.Join(root, "dangling")
		if err := os.Symlink(filepath.Join(root, "missing-target"), link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		for _, tt := range []struct {
			name string
			path string
		}{
			{name: "trailing separator", path: link + string(os.PathSeparator)},
			{name: "dot suffix", path: link + string(os.PathSeparator) + "."},
		} {
			t.Run(tt.name, func(t *testing.T) {
				exists, err := HasLocalInitializationEvidence(tt.path, "")
				if err == nil || exists {
					t.Fatalf("got exists=%v err=%v, want dangling-final-symlink rejection", exists, err)
				}
				if errors.Is(err, os.ErrNotExist) {
					t.Fatalf("dangling-final-symlink error was misclassified as absence: %v", err)
				}
			})
		}
	})

	t.Run("valid symlink ancestor is allowed", func(t *testing.T) {
		target := t.TempDir()
		ancestor := filepath.Join(t.TempDir(), "ancestor")
		if err := os.Symlink(target, ancestor); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		beadsDir := filepath.Join(ancestor, "workspace")
		if exists, err := HasLocalInitializationEvidence(beadsDir, ""); err != nil || exists {
			t.Fatalf("got exists=%v err=%v, want verified absence through valid ancestor", exists, err)
		}
	})
}

func TestSQLiteEvidenceKeepsWorkspaceRootBound(t *testing.T) {
	parent := t.TempDir()
	beadsDir := filepath.Join(parent, "workspace")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(beadsDir, defaultSQLitePath)
	store, err := Provision(t.Context(), path)
	if err != nil {
		t.Fatalf("provision original SQLite evidence: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close original SQLite evidence: %v", err)
	}

	replacement := t.TempDir()
	replacementStore, err := Provision(t.Context(), filepath.Join(replacement, defaultSQLitePath))
	if err != nil {
		t.Fatalf("provision replacement SQLite evidence: %v", err)
	}
	if err := replacementStore.Close(); err != nil {
		t.Fatalf("close replacement SQLite evidence: %v", err)
	}
	moved := beadsDir + ".moved"
	var replacementErr error
	opener := func(candidate string) (*os.File, error) {
		file, err := safefile.OpenReadOnlyNoFollow(candidate)
		if err != nil {
			return nil, err
		}
		if err := os.Rename(candidate, moved); err != nil {
			_ = file.Close()
			return nil, err
		}
		if err := os.Symlink(replacement, candidate); err != nil {
			replacementErr = err
			_ = file.Close()
			return nil, err
		}
		return file, nil
	}

	exists, err := hasLocalInitializationEvidenceWithWorkspaceOpener(beadsDir, "", opener)
	if replacementErr != nil {
		t.Skipf("symlink replacement unavailable: %v", replacementErr)
	}
	if err == nil || exists {
		t.Fatalf("got exists=%v err=%v, want workspace-root replacement rejection", exists, err)
	}
}
