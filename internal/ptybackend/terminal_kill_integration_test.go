package ptybackend

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/ptyworker"
)

func TestTerminalKillReachesEveryProcessGroup(t *testing.T) {
	binary := buildAttnBinary(t)
	for _, kind := range []string{"worker", "shared host"} {
		for _, program := range []string{"regrouped bash", "disowned job"} {
			t.Run(kind+"/"+program, func(t *testing.T) {
				root := newWorkerBackendTestRoot(t)
				cfg := WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-kill", BinaryPath: binary}
				create := NewWorker
				if kind == "shared host" {
					cfg.BinaryPath = os.Getenv("ATTN_TEST_PTY_HOST")
					if cfg.BinaryPath == "" {
						t.Skip("set ATTN_TEST_PTY_HOST to verify shared-host process groups")
					}
					create = NewSharedHost
					stopHostsAtCleanup(t, root)
				}
				backend, err := create(cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer backend.Shutdown(context.Background())
				fifo := filepath.Join(root, "held")
				if err := syscall.Mkfifo(fifo, 0o600); err != nil {
					t.Fatal(err)
				}
				opened := make(chan *os.File, 1)
				go func() {
					f, err := os.Open(fifo)
					if err != nil {
						t.Error(err)
					}
					opened <- f
				}()
				command := `trap '' TERM HUP; /bin/bash --noprofile --norc -i -c 'trap "" TERM HUP; exec 3>"$1"; printf "%s\n" "$$" >&3; read hold' probe "$1"; :`
				if program == "disowned job" {
					command = `exec /bin/bash --noprofile --norc -i -c 'trap "" TERM; /bin/sh -c '\''trap "" HUP TERM; exec 3>"$1"; printf "%s\n" "$$" >&3; read hold <"$2"'\'' job "$1" "$2" </dev/null >/dev/null 2>&1 & disown; read hold' probe "$1" "$2"`
				}
				gate := filepath.Join(root, "gate")
				if err := syscall.Mkfifo(gate, 0o600); err != nil {
					t.Fatal(err)
				}
				const id harness.TerminalID = "kill-tree"
				if err := backend.Spawn(context.Background(), SpawnOptions{ID: id, CWD: root, Agent: "kill-probe", Cols: 80, Rows: 24, AgentArgv: []string{"/bin/sh", "-c", command, "probe", fifo, gate}}); err != nil {
					t.Fatal(err)
				}
				held := <-opened
				if held == nil {
					t.Fatal("child never opened its pipe")
				}
				defer held.Close()
				line, err := bufio.NewReader(held).ReadString('\n')
				if err != nil {
					t.Fatal(err)
				}
				pid, err := strconv.Atoi(strings.TrimSpace(line))
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					_ = syscall.Kill(pid, syscall.SIGKILL)
					_ = backend.Remove(context.Background(), id)
				}()
				info, err := backend.SessionInfo(context.Background(), id)
				if err != nil {
					t.Fatal(err)
				}
				group, err := syscall.Getpgid(pid)
				if err != nil || group == info.PID {
					t.Fatalf("child %d group=%d, leader=%d, err=%v", pid, group, info.PID, err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), ptyworker.TeardownRPCTimeout)
				defer cancel()
				if err := backend.Kill(ctx, id, syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
				if err := backend.Remove(ctx, id); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Kill(info.PID, 0); err != syscall.ESRCH {
					t.Errorf("leader %d remains (including zombie): %v", info.PID, err)
				}
				released := make(chan error, 1)
				go func() {
					data, err := io.ReadAll(held)
					if err == nil && len(data) != 0 {
						err = fmt.Errorf("unexpected child output %q", data)
					}
					released <- err
				}()
				select {
				case err := <-released:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("descendant still holds its descriptor", ctx.Err())
				}
				t.Logf("leader %d reaped; regrouped child %d released its descriptor", info.PID, pid)
			})
		}
	}
}
