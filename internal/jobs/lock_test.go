package jobs

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAcquireDirLockAdoptsLegacyPIDFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, lockFileName)
	legacyPID := os.Getppid()
	if err := os.WriteFile(path, []byte(strconv.Itoa(legacyPID)), 0o644); err != nil {
		t.Fatalf("write legacy lock: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat legacy lock: %v", err)
	}

	lock, err := AcquireDirLock(dir, nil)
	if err != nil {
		t.Fatalf("AcquireDirLock() rejected legacy file naming live pid %d: %v", legacyPID, err)
	}
	defer lock.Release()

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat adopted lock: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("AcquireDirLock() replaced the legacy lock inode")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read adopted lock: %v", err)
	}
	if got, want := strings.TrimSpace(string(data)), strconv.Itoa(os.Getpid()); got != want {
		t.Fatalf("adopted lock pid = %q, want %q", got, want)
	}
}

func TestAcquireDirLockRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	const original = "keep me"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(dir, lockFileName)); err != nil {
		t.Fatalf("create lock symlink: %v", err)
	}

	if lock, err := AcquireDirLock(dir, nil); err == nil {
		lock.Release()
		t.Fatal("AcquireDirLock() accepted a symlink")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != original {
		t.Fatalf("target content = %q, want %q", data, original)
	}
}

func TestAcquireDirLockExcludesCompetingOwnerAndReleaseKeepsPath(t *testing.T) {
	dir := t.TempDir()
	first, err := AcquireDirLock(dir, nil)
	if err != nil {
		t.Fatalf("first AcquireDirLock() error: %v", err)
	}
	before, err := os.Stat(first.Path())
	if err != nil {
		t.Fatalf("stat held lock: %v", err)
	}

	if _, err := AcquireDirLock(dir, nil); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("competing AcquireDirLock() error = %v, want ErrAlreadyRunning", err)
	} else {
		for _, want := range []string{first.Path(), strconv.Itoa(os.Getpid())} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("competing error = %q, want it to name %q", err, want)
			}
		}
	}

	first.Release()
	after, err := os.Stat(first.Path())
	if err != nil {
		t.Fatalf("release removed lock path: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("release changed the lock inode")
	}

	second, err := AcquireDirLock(dir, nil)
	if err != nil {
		t.Fatalf("AcquireDirLock() after release error: %v", err)
	}
	second.Release()
}

func TestAcquireDirLockReleasedWhenOwnerExits(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestDirLockHelperProcess", "--", dir)
	cmd.Env = append(os.Environ(), "ATTN_JOBS_LOCK_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("helper stdout: %v", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("helper stdin: %v", err)
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read helper readiness: %v", err)
	}
	if strings.TrimSpace(line) != "ready" {
		t.Fatalf("helper readiness = %q", line)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("abruptly killed helper exited successfully")
	}

	lock, err := AcquireDirLock(dir, nil)
	if err != nil {
		t.Fatalf("AcquireDirLock() after owner exit error: %v", err)
	}
	lock.Release()
}

func TestDirLockHelperProcess(t *testing.T) {
	if os.Getenv("ATTN_JOBS_LOCK_HELPER") != "1" {
		return
	}
	dir := os.Args[len(os.Args)-1]
	lock, err := AcquireDirLock(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
}
