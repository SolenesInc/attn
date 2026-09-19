package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

var migratedDBFile struct {
	once  sync.Once
	image []byte
	err   error
}

func openSeededDB(dbPath string) (*sql.DB, error) {
	seedMigratedDB(dbPath)
	return OpenDB(dbPath)
}

func newSeededStore(dbPath string) (*Store, error) {
	seedMigratedDB(dbPath)
	return NewWithDB(dbPath)
}

func seedMigratedDB(dbPath string) {
	if _, err := os.Stat(dbPath); dbPath == ":memory:" || !errors.Is(err, os.ErrNotExist) {
		return
	}
	migratedDBFile.once.Do(func() {
		migratedDBFile.image, migratedDBFile.err = buildMigratedDBFile()
	})
	if migratedDBFile.err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(dbPath, migratedDBFile.image, 0o644)
}

func buildMigratedDBFile() ([]byte, error) {
	dir, err := os.MkdirTemp("", "attn-store-migrated-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "attn.db")
	db, err := OpenDB(path)
	if err != nil {
		return nil, err
	}
	if err := db.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}
