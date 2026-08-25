package daemonctl

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Not a real test: re-exec'd as a subprocess so a *different* process holds the pid
// file's flock. Unset ATTN_STOP_TEST_HELPER_MODE makes it a no-op.
func TestStopHelperProcess(t *testing.T) {
	mode := os.Getenv("ATTN_STOP_TEST_HELPER_MODE")
	if mode == "" {
		return
	}

	pidPath := os.Getenv("ATTN_STOP_TEST_HELPER_PIDPATH")
	if pidPath == "" {
		fmt.Fprintln(os.Stderr, "helper: missing ATTN_STOP_TEST_HELPER_PIDPATH")
		os.Exit(1)
	}

	f, err := os.OpenFile(pidPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper: open pid file: %v\n", err)
		os.Exit(1)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		fmt.Fprintf(os.Stderr, "helper: flock: %v\n", err)
		os.Exit(1)
	}

	if mode == "lock-pause" {
		readyPath := os.Getenv("ATTN_STOP_TEST_HELPER_READYPATH")
		if readyPath == "" {
			fmt.Fprintln(os.Stderr, "helper: missing ATTN_STOP_TEST_HELPER_READYPATH")
			os.Exit(1)
		}
		// Deliberately never writes pid-file content: this models the window between
		// acquiring the flock and publishing it.
		if err := os.WriteFile(readyPath, []byte("ready"), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "helper: write ready file: %v\n", err)
			os.Exit(1)
		}
		time.Sleep(time.Hour)
		return
	}

	var content string
	switch mode {
	case "lock-self":
		content = strconv.Itoa(os.Getpid())
	case "lock-write-pid":
		content = os.Getenv("ATTN_STOP_TEST_HELPER_WRITE_PID")
	case "lock-malformed":
		content = "not-a-pid"
	default:
		fmt.Fprintf(os.Stderr, "helper: unknown mode %q\n", mode)
		os.Exit(1)
	}
	if _, err := f.WriteString(content); err != nil {
		fmt.Fprintf(os.Stderr, "helper: write pid file: %v\n", err)
		os.Exit(1)
	}
	if err := f.Sync(); err != nil {
		fmt.Fprintf(os.Stderr, "helper: sync pid file: %v\n", err)
		os.Exit(1)
	}

	// time.Sleep waits on a runtime timer, so unlike a bare `select {}` the
	// scheduler does not call it a deadlock.
	time.Sleep(time.Hour)
}

// The subprocess is reaped as soon as it exits, so a helper killed by Stop never
// lingers as a zombie that processGoneWithin's kill(pid, 0) reads as still alive.
func spawnStopHelper(t *testing.T, pidPath string, mode string, extraEnv ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestStopHelperProcess$")
	cmd.Env = append(os.Environ(),
		"ATTN_STOP_TEST_HELPER_MODE="+mode,
		"ATTN_STOP_TEST_HELPER_PIDPATH="+pidPath,
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn helper (mode %s): %v", mode, err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	return cmd
}

func waitForFlockHeld(t *testing.T, pidPath string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(pidPath, os.O_RDWR, 0)
		if err == nil {
			flockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			if flockErr == nil {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			} else {
				f.Close()
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s to become locked", pidPath)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForPIDFileContent(t *testing.T, pidPath string, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(pidPath)
		if err == nil && strings.TrimSpace(string(data)) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s to contain %q (last read: %q, err: %v)", pidPath, want, string(data), err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s to exist", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func isAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func TestStop_NoPidFile(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "attn.pid")

	result, err := Stop(pidPath)
	if err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}
	if result.Stopped {
		t.Fatalf("Stop() = %+v, want Stopped=false", result)
	}
	if !strings.Contains(result.Note, "no pid file") {
		t.Fatalf("Stop().Note = %q, want it to mention 'no pid file'", result.Note)
	}
}

func TestStop_StalePidFile(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")

	helper := exec.Command("sleep", "30")
	if err := helper.Start(); err != nil {
		t.Fatalf("start sleep helper: %v", err)
	}
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_, _ = helper.Process.Wait()
	})

	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(helper.Process.Pid)), 0644); err != nil {
		t.Fatalf("write stale pid file: %v", err)
	}

	result, err := Stop(pidPath)
	if err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}
	if result.Stopped {
		t.Fatalf("Stop() = %+v, want Stopped=false (must not signal an unlocked pid)", result)
	}
	if !strings.Contains(result.Note, "stale") {
		t.Fatalf("Stop().Note = %q, want it to mention 'stale'", result.Note)
	}
	if !isAlive(helper.Process.Pid) {
		t.Fatal("helper process is gone: Stop() signaled a pid it never held the lock for")
	}
}

func TestStop_LiveHolder(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")

	helper := spawnStopHelper(t, pidPath, "lock-self")
	waitForPIDFileContent(t, pidPath, strconv.Itoa(helper.Process.Pid), 5*time.Second)
	waitForFlockHeld(t, pidPath, 5*time.Second)

	result, err := Stop(pidPath)
	if err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}
	if !result.Stopped {
		t.Fatalf("Stop() = %+v, want Stopped=true", result)
	}
	if result.PID != helper.Process.Pid {
		t.Fatalf("Stop().PID = %d, want %d", result.PID, helper.Process.Pid)
	}
	if result.Forced {
		t.Fatalf("Stop() = %+v, want Forced=false (helper exits cleanly on SIGTERM)", result)
	}
	if isAlive(helper.Process.Pid) {
		t.Fatal("helper process is still alive after Stop() reported Stopped=true")
	}
}

func TestStop_RefusesOwnProcessTree(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")
	ownPID := os.Getpid()

	spawnStopHelper(t, pidPath, "lock-write-pid", "ATTN_STOP_TEST_HELPER_WRITE_PID="+strconv.Itoa(ownPID))
	waitForPIDFileContent(t, pidPath, strconv.Itoa(ownPID), 5*time.Second)
	waitForFlockHeld(t, pidPath, 5*time.Second)

	result, err := Stop(pidPath)
	if err == nil {
		t.Fatalf("Stop() = %+v, err = nil, want an own-process-tree refusal error", result)
	}
	if !strings.Contains(err.Error(), "own process tree") {
		t.Fatalf("Stop() error = %v, want it to mention 'own process tree'", err)
	}
	if result.Stopped {
		t.Fatalf("Stop() = %+v, want Stopped=false", result)
	}
}

func TestStop_NonDaemonHolderSentinel(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")

	stalePidHolder := exec.Command("sleep", "30")
	if err := stalePidHolder.Start(); err != nil {
		t.Fatalf("start sleep helper: %v", err)
	}
	t.Cleanup(func() {
		_ = stalePidHolder.Process.Kill()
		_, _ = stalePidHolder.Process.Wait()
	})
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(stalePidHolder.Process.Pid)), 0644); err != nil {
		t.Fatalf("seed stale pid file: %v", err)
	}

	spawnStopHelper(t, pidPath, "lock-write-pid", "ATTN_STOP_TEST_HELPER_WRITE_PID="+NonDaemonHolderSentinel)
	waitForPIDFileContent(t, pidPath, NonDaemonHolderSentinel, 5*time.Second)
	waitForFlockHeld(t, pidPath, 5*time.Second)

	result, err := Stop(pidPath)
	if err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}
	if result.Stopped {
		t.Fatalf("Stop() = %+v, want Stopped=false (must not signal a pid the current lock holder didn't write)", result)
	}
	if !strings.Contains(result.Note, "another attn process") {
		t.Fatalf("Stop().Note = %q, want it to mention the lock being held by another attn process", result.Note)
	}
	if !isAlive(stalePidHolder.Process.Pid) {
		t.Fatal("stale-pid-holder process is gone: Stop() signaled a pid the current lock holder never wrote")
	}
}

func TestStop_AcquireBeforeContentWriteGap(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")
	readyPath := filepath.Join(dir, "ready")

	stalePidHolder := exec.Command("sleep", "30")
	if err := stalePidHolder.Start(); err != nil {
		t.Fatalf("start sleep helper: %v", err)
	}
	t.Cleanup(func() {
		_ = stalePidHolder.Process.Kill()
		_, _ = stalePidHolder.Process.Wait()
	})
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(stalePidHolder.Process.Pid)), 0644); err != nil {
		t.Fatalf("seed stale pid file: %v", err)
	}

	spawnStopHelper(t, pidPath, "lock-pause", "ATTN_STOP_TEST_HELPER_READYPATH="+readyPath)
	waitForFile(t, readyPath, 5*time.Second)
	waitForFlockHeld(t, pidPath, 5*time.Second)

	result, err := Stop(pidPath)
	if err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}
	if result.Stopped {
		t.Fatalf("Stop() = %+v, want Stopped=false (the content pid does not itself hold the lock)", result)
	}
	if !strings.Contains(result.Note, "does not hold the daemon lock") {
		t.Fatalf("Stop().Note = %q, want it to mention the pid not holding the daemon lock", result.Note)
	}
	if !isAlive(stalePidHolder.Process.Pid) {
		t.Fatal("stale-pid-holder process is gone: Stop() signaled a pid that never itself held the lock")
	}
}

func TestStop_NonContentionFlockErrorFailsClosed(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")

	helper := exec.Command("sleep", "30")
	if err := helper.Start(); err != nil {
		t.Fatalf("start sleep helper: %v", err)
	}
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_, _ = helper.Process.Wait()
	})
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(helper.Process.Pid)), 0644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	originalFlockFn := flockFn
	flockFn = func(fd int, how int) error {
		return syscall.ENOLCK
	}
	t.Cleanup(func() { flockFn = originalFlockFn })

	result, err := Stop(pidPath)
	if err == nil {
		t.Fatalf("Stop() = %+v, err = nil, want an indeterminate-state error", result)
	}
	if !strings.Contains(err.Error(), "cannot determine daemon state") {
		t.Fatalf("Stop() error = %v, want it to mention the indeterminate-state message", err)
	}
	if result.Stopped {
		t.Fatalf("Stop() = %+v, want Stopped=false", result)
	}
	if !isAlive(helper.Process.Pid) {
		t.Fatal("helper process is gone: Stop() signaled a pid on an inconclusive flock result")
	}
}

func TestStop_MalformedPIDFile(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attn.pid")

	helper := spawnStopHelper(t, pidPath, "lock-malformed")
	waitForFlockHeld(t, pidPath, 5*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(pidPath)
		if err == nil && strings.TrimSpace(string(data)) == "not-a-pid" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for malformed pid file content, last read: %q, err: %v", string(data), err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	result, err := Stop(pidPath)
	if err == nil {
		t.Fatalf("Stop() = %+v, err = nil, want a malformed-pid error", result)
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("Stop() error = %v, want it to mention 'malformed'", err)
	}
	if result.Stopped {
		t.Fatalf("Stop() = %+v, want Stopped=false", result)
	}
	if !isAlive(helper.Process.Pid) {
		t.Fatal("helper process is gone: Stop() should not have signaled anything for malformed content")
	}
}
