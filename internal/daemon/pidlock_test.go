package daemon

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The flock, not the file's presence, is the mutual-exclusion signal shared with
// cmd/attn/db.go's acquireDaemonLock, so release must not unlink the pathname.
func TestDaemon_ReleasePIDLock_LeavesFileInPlace(t *testing.T) {
	dir := t.TempDir()
	d := &Daemon{pidPath: filepath.Join(dir, "attn.pid")}

	if err := d.acquirePIDLock(); err != nil {
		t.Fatalf("acquirePIDLock error: %v", err)
	}
	d.releasePIDLock()

	if _, err := os.Stat(d.pidPath); err != nil {
		t.Fatalf("expected pid file to remain on disk after release, stat err = %v", err)
	}

	second := &Daemon{pidPath: d.pidPath}
	if err := second.acquirePIDLock(); err != nil {
		t.Fatalf("second acquirePIDLock after release error: %v", err)
	}
	second.releasePIDLock()
}

// The stand-in's fd is opened WHILE the daemon still holds the lock, and without O_CREATE:
// opened after releasePIDLock, O_CREATE would fabricate a fresh inode and pass vacuously.
func TestDaemon_ReleasePIDLock_DoesNotOrphanAConcurrentHolder(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")

	d := &Daemon{pidPath: pidPath}
	if err := d.acquirePIDLock(); err != nil {
		t.Fatalf("acquirePIDLock error: %v", err)
	}

	// No O_CREATE: the file must already exist because the daemon created it, so
	// this fd references the daemon's inode rather than a fabricated one.
	restoreHolder, err := os.OpenFile(pidPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open pid file as restore stand-in: %v", err)
	}
	defer restoreHolder.Close()

	restoreHolderInfo, err := restoreHolder.Stat()
	if err != nil {
		t.Fatalf("stat restore stand-in fd: %v", err)
	}

	d.releasePIDLock()

	if err := syscall.Flock(int(restoreHolder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("restore stand-in flock after daemon release: %v", err)
	}

	if pathInfo, err := os.Stat(pidPath); err != nil {
		t.Fatalf("stat pid path after release: %v", err)
	} else if !os.SameFile(restoreHolderInfo, pathInfo) {
		t.Fatal("releasePIDLock changed the inode at pidPath; restore stand-in's held fd now refers to an orphaned inode")
	}

	next := &Daemon{pidPath: pidPath}
	if err := next.acquirePIDLock(); err == nil {
		t.Fatal("expected daemon acquirePIDLock to be excluded while the restore stand-in holds the lock")
	}

	syscall.Flock(int(restoreHolder.Fd()), syscall.LOCK_UN)
	restoreHolder.Close()

	if err := next.acquirePIDLock(); err != nil {
		t.Fatalf("expected daemon acquirePIDLock to succeed after the restore stand-in released, got: %v", err)
	}
	next.releasePIDLock()
}
