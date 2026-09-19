//go:build unix

package store

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestAFreshFileDatabaseHonoursTheUmask(t *testing.T) {
	previous := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(previous) })

	path := filepath.Join(t.TempDir(), "attn.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("database mode = %o, want 600 under umask 077", got)
	}
}
