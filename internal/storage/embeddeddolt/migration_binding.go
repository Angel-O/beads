//go:build cgo

package embeddeddolt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/steveyegge/beads/internal/storage"
)

// MigrationSourceBinding retains handles to the admitted workspace and Dolt
// directories and detects ordinary namespace replacement around driver opens.
// The Dolt driver still accepts a pathname, so a hostile swap-away/swap-back
// between checks cannot be descriptor-bound without a driver API change.
type MigrationSourceBinding struct {
	beadsPath        string
	dataPath         string
	databasePath     string
	database         string
	beadsHandle      *os.File
	dataHandle       *os.File
	databaseHandle   *os.File
	beadsIdentity    os.FileInfo
	dataIdentity     os.FileInfo
	databaseIdentity os.FileInfo
}

func BindMigrationSource(beadsDir, database string) (*MigrationSourceBinding, error) {
	if !filepath.IsAbs(beadsDir) || filepath.Clean(beadsDir) != beadsDir {
		return nil, errors.New("embeddeddolt: migration workspace path must be absolute and clean")
	}
	if !validIdentifier.MatchString(database) {
		return nil, errors.New("embeddeddolt: migration database name is invalid")
	}
	beadsHandle, beadsIdentity, err := openBoundMigrationDirectory(beadsDir)
	if err != nil {
		return nil, fmt.Errorf("embeddeddolt: bind migration workspace: %w", err)
	}
	dataPath := filepath.Join(beadsDir, "embeddeddolt")
	dataHandle, dataIdentity, err := openBoundMigrationDirectory(dataPath)
	if err != nil {
		_ = beadsHandle.Close()
		return nil, fmt.Errorf("embeddeddolt: bind migration source: %w", err)
	}
	databasePath := filepath.Join(dataPath, database)
	databaseHandle, databaseIdentity, err := openBoundMigrationDirectory(databasePath)
	if err != nil {
		_ = dataHandle.Close()
		_ = beadsHandle.Close()
		return nil, fmt.Errorf("embeddeddolt: bind migration database: %w", err)
	}
	return &MigrationSourceBinding{
		beadsPath: beadsDir, dataPath: dataPath, databasePath: databasePath, database: database,
		beadsHandle: beadsHandle, dataHandle: dataHandle, databaseHandle: databaseHandle,
		beadsIdentity: beadsIdentity, dataIdentity: dataIdentity, databaseIdentity: databaseIdentity,
	}, nil
}

func openBoundMigrationDirectory(path string) (*os.File, os.FileInfo, error) {
	named, err := os.Lstat(path)
	if err != nil || !named.IsDir() || named.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errors.New("directory is missing, linked, or unsafe")
	}
	handle, err := os.Open(path) // #nosec G304 -- named/opened identity is checked before use.
	if err != nil {
		return nil, nil, err
	}
	opened, err := handle.Stat()
	if err != nil || !opened.IsDir() || !os.SameFile(named, opened) {
		_ = handle.Close()
		return nil, nil, errors.New("directory changed while binding")
	}
	return handle, opened, nil
}

func (b *MigrationSourceBinding) Verify() error {
	if b == nil || b.beadsHandle == nil || b.dataHandle == nil || b.databaseHandle == nil {
		return errors.New("embeddeddolt: migration source binding is closed")
	}
	if err := verifyBoundMigrationDirectory(b.beadsPath, b.beadsHandle, b.beadsIdentity); err != nil {
		return fmt.Errorf("embeddeddolt: migration workspace identity changed: %w", err)
	}
	if err := verifyBoundMigrationDirectory(b.dataPath, b.dataHandle, b.dataIdentity); err != nil {
		return fmt.Errorf("embeddeddolt: migration source identity changed: %w", err)
	}
	if err := verifyBoundMigrationDirectory(b.databasePath, b.databaseHandle, b.databaseIdentity); err != nil {
		return fmt.Errorf("embeddeddolt: migration database identity changed: %w", err)
	}
	return nil
}

func (b *MigrationSourceBinding) Witness() (string, error) {
	if err := b.Verify(); err != nil {
		return "", err
	}
	beadsIdentity, err := migrationDirectoryIdentity(b.beadsHandle)
	if err != nil {
		return "", err
	}
	dataIdentity, err := migrationDirectoryIdentity(b.dataHandle)
	if err != nil {
		return "", err
	}
	databaseIdentity, err := migrationDirectoryIdentity(b.databaseHandle)
	if err != nil {
		return "", err
	}
	return "v2:" + beadsIdentity + "/" + dataIdentity + "/" + databaseIdentity, nil
}

func (b *MigrationSourceBinding) VerifyWitness(expected string) error {
	actual, err := b.Witness()
	if err != nil {
		return err
	}
	if expected == "" || actual != expected {
		return errors.New("embeddeddolt: migration source identity differs from the durable witness")
	}
	return nil
}

func verifyBoundMigrationDirectory(path string, handle *os.File, identity os.FileInfo) error {
	named, err := os.Lstat(path)
	if err != nil || !named.IsDir() || named.Mode()&os.ModeSymlink != 0 {
		return errors.New("named directory is missing, linked, or unsafe")
	}
	opened, err := handle.Stat()
	if err != nil || !opened.IsDir() || !os.SameFile(identity, opened) || !os.SameFile(opened, named) {
		return errors.New("named directory no longer matches the retained handle")
	}
	return nil
}

func (b *MigrationSourceBinding) OpenReadOnly(ctx context.Context, database, branch string) (storage.DoltStorage, error) {
	return b.openReadOnlyUsing(ctx, database, branch, func(ctx context.Context, beadsDir, database, branch string) (storage.DoltStorage, error) {
		return openReadOnly(ctx, beadsDir, database, branch, true)
	})
}

func (b *MigrationSourceBinding) openReadOnlyUsing(
	ctx context.Context,
	database, branch string,
	opener func(context.Context, string, string, string) (storage.DoltStorage, error),
) (storage.DoltStorage, error) {
	if database != b.database {
		return nil, errors.New("embeddeddolt: migration database differs from the bound authority")
	}
	if err := b.Verify(); err != nil {
		return nil, err
	}
	source, err := opener(ctx, b.beadsPath, database, branch)
	if err != nil {
		return nil, err
	}
	if err := b.Verify(); err != nil {
		return nil, errors.Join(err, source.Close())
	}
	return source, nil
}

func (b *MigrationSourceBinding) Close() error {
	if b == nil {
		return nil
	}
	beadsHandle, dataHandle, databaseHandle := b.beadsHandle, b.dataHandle, b.databaseHandle
	b.beadsHandle, b.dataHandle, b.databaseHandle = nil, nil, nil
	var beadsErr, dataErr, databaseErr error
	if databaseHandle != nil {
		databaseErr = databaseHandle.Close()
	}
	if dataHandle != nil {
		dataErr = dataHandle.Close()
	}
	if beadsHandle != nil {
		beadsErr = beadsHandle.Close()
	}
	return errors.Join(databaseErr, dataErr, beadsErr)
}
