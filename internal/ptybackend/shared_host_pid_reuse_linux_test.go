package ptybackend

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pty"
	"golang.org/x/sys/unix"
)

const (
	insidePIDNamespaceEnv   = "ATTN_TEST_INSIDE_PID_NAMESPACE"
	pidNamespaceUnavailable = 77
	reusedNumberAttempts    = 200
)

func TestSharedHost_RemovingAnExitedTerminalSparesASessionThatReusedItsNumber(t *testing.T) {
	if os.Getenv(insidePIDNamespaceEnv) == "" {
		rerunInsideOwnPIDNamespace(t)
		return
	}
	mountOwnProc()

	binary, root := sharedHostTestRoot(t, "pty-pid-reuse-")
	stopHostsAtCleanup(t, root)
	backend, err := NewSharedHost(WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-pid-reuse", BinaryPath: binary})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Shutdown(context.Background()) })
	ctx := context.Background()

	detached := filepath.Join(root, "detached")
	if err := syscall.Mkfifo(detached, 0o600); err != nil {
		t.Fatal(err)
	}
	const id = harness.TerminalID("detached-holder")
	script := `setsid /bin/sh -c 'printf ready >"$1"; exec sleep 600' holder "$1" & read ready <"$1"; exit 7`
	if err := backend.Spawn(ctx, SpawnOptions{ID: id, CWD: root, Agent: "reuse-probe", Cols: 80, Rows: 24,
		AgentArgv: []string{"/bin/sh", "-c", script, "probe", detached}}); err != nil {
		t.Fatal(err)
	}
	info, err := backend.SessionInfo(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	res, err := backend.WaitBlock(ctx, id, pty.WaitBlockParams{Until: "exit"})
	if err != nil || !res.Exited {
		t.Fatalf("exit not reported while the detached holder keeps the terminal open: res=%+v err=%v", res, err)
	}
	exited, err := backend.SessionInfo(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if exited.ExitCode == nil || *exited.ExitCode != 7 || exited.ExitSignal != nil {
		t.Fatalf("exit status = %+v, want 7 and no signal", exited)
	}

	unrelated, reused := startSessionTryingToReuse(t, info.PID)
	t.Logf("unrelated session %d, tried for number %d, reused=%v", unrelated, info.PID, reused)

	if err := backend.Remove(ctx, id); err != nil {
		t.Fatal(err)
	}
	awaitSharedHostRemoval(t, backend, id, root)
	if err := backend.Remove(ctx, id); err != nil {
		t.Fatal(err)
	}

	var status unix.Siginfo
	if err := unix.Waitid(unix.P_PID, unrelated, &status, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil); err != nil {
		t.Fatal(err)
	}
	if status.Signo != 0 {
		t.Fatalf("removing the exited terminal ended unrelated session %d (reused number: %v)", unrelated, reused)
	}
}

func rerunInsideOwnPIDNamespace(t *testing.T) {
	t.Helper()
	if os.Getenv("ATTN_TEST_PTY_HOST") == "" {
		t.Skip("set ATTN_TEST_PTY_HOST")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v", "-test.count=1")
	clockKey, _, _ := strings.Cut(testClock.Env(), "=")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, clockKey+"=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, insidePIDNamespaceEnv+"=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:                 syscall.CLONE_NEWUSER | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
		GidMappingsEnableSetgroups: false,
	}
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EACCES):
		t.Skipf("this machine refuses a private PID namespace: %v", err)
	case errors.As(err, &exit) && exit.ExitCode() == pidNamespaceUnavailable:
		t.Skipf("this machine refuses a private PID namespace: %s", output)
	case err != nil:
		t.Fatalf("inside the PID namespace: %v\n%s", err, output)
	}
	t.Logf("inside the PID namespace:\n%s", output)
}

func mountOwnProc() {
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		println("private mounts:", err.Error())
		os.Exit(pidNamespaceUnavailable)
	}
	if err := unix.Mount("proc", "/proc", "proc", 0, ""); err != nil {
		println("mount proc:", err.Error())
		os.Exit(pidNamespaceUnavailable)
	}
}

func startSessionTryingToReuse(t *testing.T, number int) (pid int, reused bool) {
	t.Helper()
	for range reusedNumberAttempts {
		if err := os.WriteFile("/proc/sys/kernel/ns_last_pid", []byte(strconv.Itoa(number-1)), 0o644); err != nil {
			println("ns_last_pid:", err.Error())
			os.Exit(pidNamespaceUnavailable)
		}
		candidate := exec.Command("sleep", "600")
		candidate.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := candidate.Start(); err != nil {
			t.Fatal(err)
		}
		pid = candidate.Process.Pid
		if pid == number {
			return pid, true
		}
		if err := candidate.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = candidate.Wait()
	}
	candidate := exec.Command("sleep", "600")
	candidate.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := candidate.Start(); err != nil {
		t.Fatal(err)
	}
	return candidate.Process.Pid, false
}
