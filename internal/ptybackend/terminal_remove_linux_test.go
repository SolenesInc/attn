package ptybackend

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/ptyworker"
	"golang.org/x/sys/unix"
)

func TestTerminalRemovalStopsJobsAfterTheLeaderExits(t *testing.T) {
	binary := os.Getenv("ATTN_TEST_PTY_HOST")
	if binary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to verify exited leader cleanup")
	}
	root := newWorkerBackendTestRoot(t)
	stopHostsAtCleanup(t, root)
	backend, err := NewSharedHost(WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-exited", BinaryPath: binary})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Shutdown(context.Background())
	fifo, gate := filepath.Join(root, "held"), filepath.Join(root, "gate")
	for _, path := range []string{fifo, gate} {
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const id = "exited-leader"
	command := `/bin/sh -c 'trap "" HUP TERM; exec 3>"$1"; printf "%s\n" "$$" >&3; read hold <"$2"' job "$1" "$2" </dev/null & disown; read release; exit 7`
	if err := backend.Spawn(context.Background(), SpawnOptions{ID: id, CWD: root, Agent: "exit-probe", Cols: 80, Rows: 24, AgentArgv: []string{"/bin/bash", "--noprofile", "--norc", "-c", command, "probe", fifo, gate}}); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(fifo)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	line, err := bufio.NewReader(held).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	job, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(job, syscall.SIGKILL); _ = backend.Remove(context.Background(), id) }()
	info, err := backend.SessionInfo(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	pidfd, err := unix.PidfdOpen(info.PID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pidfd)
	if err := backend.Input(context.Background(), id, []byte("\r")); err != nil {
		t.Fatal(err)
	}
	if n, err := unix.Poll([]unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}, int(ptyworker.TeardownRPCTimeout/time.Millisecond)); err != nil || n != 1 {
		t.Fatalf("leader did not exit: events=%d, %v", n, err)
	}
	if err := backend.Kill(context.Background(), id, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := backend.Remove(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := held.SetReadDeadline(time.Now().Add(ptyworker.TeardownRPCTimeout)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(held); err != nil {
		t.Fatalf("job %d still holds its descriptor after the leader exited and Remove completed: %v", job, err)
	}
	awaitSharedHostRemoval(t, backend, id, root)
	if err := syscall.Kill(info.PID, 0); err != syscall.ESRCH {
		t.Fatalf("leader %d was not reaped: %v", info.PID, err)
	}
	t.Logf("leader %d exited first; Remove stopped job %d and reaped the leader", info.PID, job)
}
