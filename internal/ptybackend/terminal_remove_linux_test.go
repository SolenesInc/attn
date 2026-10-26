package ptybackend

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
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
	fds := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
	if n, err := unix.Poll(fds, int(ptyworker.TeardownRPCTimeout/time.Millisecond)); err != nil || n != 1 {
		t.Fatalf("worker %d did not exit after the client timed out: events=%d, %v", entry.WorkerPID, n, err)
	}
	if err := syscall.Kill(entry.ChildPID, 0); err != syscall.ESRCH {
		t.Fatalf("child %d was not reaped: %v", entry.ChildPID, err)
	}
	if _, err := os.Stat(filepath.Join(backend.registryDir(), id+".json")); !os.IsNotExist(err) {
		t.Fatalf("worker registry remains after removal: %v", err)
	}
	t.Logf("remove reply timed out after the program received TERM; worker %d exited and child %d was reaped", entry.WorkerPID, entry.ChildPID)
}
