package main_test

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/daemonctl"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/testworld"
)

func startBystander(t *testing.T, args ...string) *os.Process {
	t.Helper()
	return startBystanderWith(t, nil, args...)
}

func startBystanderWith(t *testing.T, env []string, args ...string) *os.Process {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	ready := make(chan error, 1)
	go func() {
		_, err := bufio.NewReader(stdout).ReadString('\n')
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatalf("%q exited before it was ready: %v", args, err)
		}
	case <-time.After(fakeagent.HangGuard):
		t.Fatalf("%q was not ready within %s", args, fakeagent.HangGuard)
	}
	return cmd.Process
}

func holdDaemonLock(t *testing.T, pidPath, content string) *os.Process {
	t.Helper()
	return startBystanderWith(t, []string{"ATTN_TEST_HOLD_DAEMON_LOCK=" + pidPath, "ATTN_TEST_DAEMON_LOCK_CONTENT=" + content}, os.Args[0])
}

// Runs in a re-exec of the test binary: flock(1) is Linux-only, so the lock
// holder takes the daemon's flock itself and reports ready on stdout.
func holdDaemonLockForever(pidPath, content string) {
	// A raw fd has no finalizer, so the idle loop's GCs never close it and drop the lock.
	fd, err := syscall.Open(pidPath, syscall.O_RDWR|syscall.O_CREAT, 0o644)
	if err == nil {
		err = syscall.Flock(fd, syscall.LOCK_EX)
	}
	if err == nil {
		err = syscall.Ftruncate(fd, 0)
	}
	if err == nil {
		_, err = syscall.Pwrite(fd, []byte(content), 0)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("locked")
	for {
		time.Sleep(time.Hour)
	}
}

func alive(p *os.Process) bool {
	var status syscall.WaitStatus
	reaped, err := syscall.Wait4(p.Pid, &status, syscall.WNOHANG, nil)
	return err == nil && reaped == 0
}

func TestDaemonStopSignalsOnlyTheProcessThatHoldsTheDaemonLock(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	pidPath := filepath.Join(s.Dir, "attn.pid")
	stop := func(env ...string) testworld.Result {
		t.Helper()
		return s.Run(testworld.Invocation{Args: []string{"daemon", "stop"}, Env: env})
	}

	if got := stop(); got.Code != 0 || got.Stdout != "daemon not running (no pid file)\n" {
		t.Errorf("attn daemon stop with no pid file exited %d printing %q", got.Code, got.Stdout)
	}

	bystander := startBystander(t, "/bin/sh", "-c", "echo up && exec sleep 3600")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(bystander.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := stop(); got.Code != 0 || !strings.Contains(got.Stdout, "stale pid file") || !alive(bystander) {
		t.Errorf("attn daemon stop over an unlocked pid file naming pid %d exited %d printing %q (bystander alive: %t), want a stale note and no signal", bystander.Pid, got.Code, got.Stdout, alive(bystander))
	}

	for _, tc := range []struct {
		name    string
		content string
		code    int
		says    string
	}{
		{name: "a daemon still starting while the file names an older pid", content: strconv.Itoa(bystander.Pid), says: "does not hold the daemon lock"},
		{name: "a database restore", content: daemonctl.NonDaemonHolderSentinel, says: "held by another attn process"},
		{name: "unreadable content", content: "not-a-pid", code: 1, says: "malformed pid file"},
		{name: "the caller's own process", content: strconv.Itoa(os.Getpid()), code: 1, says: "own process tree"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			holder := holdDaemonLock(t, pidPath, tc.content)
			got := stop()
			if got.Code != tc.code || !strings.Contains(got.Stdout+got.Stderr, tc.says) || !alive(holder) || !alive(bystander) {
				t.Errorf("attn daemon stop exited %d with stdout %q and stderr %q (lock holder alive: %t, bystander alive: %t), want %d, %q and nobody signalled",
					got.Code, got.Stdout, got.Stderr, alive(holder), alive(bystander), tc.code, tc.says)
			}
			_ = holder.Kill()
			_, _ = holder.Wait()
		})
	}

	if got := s.Attn("daemon", "ensure"); got.Code != 0 {
		t.Fatalf("attn daemon ensure exited %d: %s", got.Code, got.Stderr)
	}
	stopped := stop("PATH=" + filepath.Join(s.Dir, "no-tools"))
	if stopped.Code != 0 || !strings.HasPrefix(stopped.Stdout, "stopped daemon (pid ") {
		t.Fatalf("attn daemon stop of a running daemon with no lsof on PATH exited %d printing %q: %s", stopped.Code, stopped.Stdout, stopped.Stderr)
	}
	if after := s.Attn("agent", "list"); after.Code == 0 || !strings.Contains(after.Stderr, "connect to daemon") {
		t.Errorf("after attn daemon stop, attn agent list exited %d: %s", after.Code, after.Stdout)
	}
}
