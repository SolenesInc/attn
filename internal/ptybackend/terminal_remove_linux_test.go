package ptybackend

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
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

func TestAnAcceptedRemovalFinishesAfterItsClientTimesOut(t *testing.T) {
	root := newWorkerBackendTestRoot(t)
	backend, err := NewWorker(WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-timeout", BinaryPath: buildAttnBinary(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Shutdown(context.Background())
	const id = "remove-timeout"
	if err := backend.Spawn(context.Background(), SpawnOptions{ID: id, CWD: root, Agent: "timeout-probe", Cols: 80, Rows: 24, AgentArgv: []string{"/bin/sh", "-c", `read start; trap 'printf "termination-started\n"' TERM; trap '' HUP; printf 'ready\n'; while :; do read hold; done`}}); err != nil {
		t.Fatal(err)
	}
	defer backend.Remove(context.Background(), id)
	entry, err := ptyworker.ReadRegistry(filepath.Join(backend.registryDir(), id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	pidfd, err := unix.PidfdOpen(entry.WorkerPID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pidfd)
	ctx, cancel := context.WithTimeout(context.Background(), ptyworker.TeardownRPCTimeout)
	defer cancel()
	_, stream, err := backend.Attach(ctx, id, "timeout-proof")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	awaitOutput := func(want string) {
		t.Helper()
		var output strings.Builder
		for {
			select {
			case event, ok := <-stream.Events():
				if !ok {
					t.Fatalf("terminal closed before %q: %s", want, output.String())
				}
				output.Write(event.Data)
				if strings.Contains(output.String(), want) {
					return
				}
			case <-ctx.Done():
				t.Fatalf("terminal never printed %q: %s", want, output.String())
			}
		}
	}
	if err := backend.Input(ctx, id, []byte("\r")); err != nil {
		t.Fatal(err)
	}
	awaitOutput("ready\r\n")
	conn, err := net.Dial("unix", entry.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
	params, _ := json.Marshal(ptyworker.HelloParams{RPCMajor: ptyworker.RPCMajor, RPCMinor: ptyworker.RPCMinor, DaemonInstanceID: entry.DaemonInstanceID, ControlToken: entry.ControlToken})
	if err := enc.Encode(ptyworker.RequestEnvelope{Type: "req", ID: "hello", Method: ptyworker.MethodHello, Params: params}); err != nil {
		t.Fatal(err)
	}
	var response ptyworker.ResponseEnvelope
	if err := dec.Decode(&response); err != nil || !response.OK {
		t.Fatalf("hello: %+v, %v", response, err)
	}
	if err := enc.Encode(ptyworker.RequestEnvelope{Type: "req", ID: "remove", Method: ptyworker.MethodRemove}); err != nil {
		t.Fatal(err)
	}
	awaitOutput("termination-started\r\n")
	if err := conn.SetReadDeadline(time.Now()); err != nil {
		t.Fatal(err)
	}
	var timedOut net.Error
	if err := dec.Decode(&response); !errors.As(err, &timedOut) || !timedOut.Timeout() {
		t.Fatalf("remove did not time out: %v", err)
	}
	_ = conn.Close()
	awaitTimer(t, resistantChildKillAfter)
	advance(t, resistantChildKillAfter)
	fds := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
	if n, err := unix.Poll(fds, int(ptyworker.TeardownRPCTimeout/time.Millisecond)); err != nil || n != 1 {
		t.Fatalf("worker %d did not exit after the client timed out: events=%d, %v", entry.WorkerPID, n, err)
	}
	if err := syscall.Kill(entry.ChildPID, 0); err != syscall.ESRCH {
		t.Fatalf("child %d was not reaped: %v", entry.ChildPID, err)
	}
	t.Logf("remove reply timed out after the program received TERM; worker %d exited and child %d was reaped", entry.WorkerPID, entry.ChildPID)
}

func TestTerminalRemovalStopsJobsAfterTheLeaderExits(t *testing.T) {
	binary := buildAttnBinary(t)
	for _, kind := range []string{"worker", "shared host"} {
		t.Run(kind, func(t *testing.T) {
			root := newWorkerBackendTestRoot(t)
			cfg := WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-exited", BinaryPath: binary}
			create := NewWorker
			if kind == "shared host" {
				cfg.BinaryPath = os.Getenv("ATTN_TEST_PTY_HOST")
				if cfg.BinaryPath == "" {
					t.Skip("set ATTN_TEST_PTY_HOST to verify exited leader cleanup")
				}
				create = NewSharedHost
				stopHostsAtCleanup(t, root)
			}
			backend, err := create(cfg)
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
			killed := make(chan error, 1)
			go func() { killed <- backend.Kill(context.Background(), id, syscall.SIGTERM) }()
			if kind == "worker" {
				awaitTimer(t, resistantChildKillAfter)
				advance(t, resistantChildKillAfter)
			}
			if err := <-killed; err != nil {
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
			if err := syscall.Kill(info.PID, 0); err != syscall.ESRCH {
				t.Fatalf("leader %d was not reaped: %v", info.PID, err)
			}
			t.Logf("leader %d exited first; Remove stopped job %d and reaped the leader", info.PID, job)
		})
	}
}
