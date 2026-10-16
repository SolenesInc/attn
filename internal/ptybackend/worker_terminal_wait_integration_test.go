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
	"slices"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptyworker"
)

func TestAnExitWaitKeepsItsTerminalResultWhenTheWorkerReplyIsLost(t *testing.T) {
	binary := buildAttnBinary(t)
	for _, kind := range []string{"worker", "shared host"} {
		for _, exits := range []bool{false, true} {
			name := kind + "/running"
			if exits {
				name = kind + "/exited"
			}
			t.Run(name, func(t *testing.T) {
				root := newWorkerBackendTestRoot(t)
				cfg := WorkerBackendConfig{DataRoot: root, DaemonInstanceID: "d-wait", BinaryPath: binary}
				create := NewWorker
				if kind == "shared host" {
					if os.Getenv("ATTN_TEST_PTY_HOST") == "" {
						t.Skip("set ATTN_TEST_PTY_HOST to verify shared-host wait results")
					}
					cfg.BinaryPath = os.Getenv("ATTN_TEST_PTY_HOST")
					create = NewSharedHost
					stopHostsAtCleanup(t, root)
				}
				backend, err := create(cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer backend.Shutdown(context.Background())
				const terminal harness.TerminalID = "exit-wait"
				if err := backend.Spawn(context.Background(), SpawnOptions{ID: terminal, CWD: root, Agent: "exit-probe", AgentArgv: []string{"/bin/sh", "-c", "read hold; exit 0"}, Cols: 80, Rows: 24}); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := backend.Remove(context.Background(), terminal); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
						t.Error(err)
					}
				}()
				removed := make(chan error, 1)
				if exits {
					backend.SetExitHandler(func(info ExitInfo) { removed <- backend.Remove(context.Background(), info.ID) })
				}
				registry, err := ptyworker.ReadRegistry(filepath.Join(backend.registryDir(), string(terminal)+".json"))
				if err != nil {
					t.Fatal(err)
				}
				armed, drop := interceptTerminalWait(t, registry.SocketPath)
				ctx, cancel := context.WithTimeout(context.Background(), fakeagent.HangGuard)
				defer cancel()
				type answer struct {
					result pty.WaitBlockResult
					err    error
				}
				done := make(chan answer, 1)
				go func() {
					r, err := backend.WaitBlock(ctx, terminal, pty.WaitBlockParams{Until: "exit"})
					done <- answer{r, err}
				}()
				select {
				case <-armed:
				case got := <-done:
					t.Fatalf("wait ended before its worker request: %v", got.err)
				case <-ctx.Done():
					t.Fatal("wait never reached its worker connection", ctx.Err())
				}
				if exits {
					if err := backend.Input(ctx, terminal, []byte("\r")); err != nil {
						t.Fatal(err)
					}
					select {
					case err := <-removed:
						if err != nil {
							t.Fatal(err)
						}
					case <-ctx.Done():
						t.Fatal("exited terminal was not removed", ctx.Err())
					}
					if slices.Contains(backend.TerminalIDs(ctx), terminal) {
						t.Fatal("exited terminal remains registered")
					}
				}
				drop()
				got := <-done
				if exits {
					if got.err != nil || !got.result.Exited || got.result.ExitCode == nil || *got.result.ExitCode != 0 {
						t.Fatalf("removed terminal's result: %+v, %v", got.result, got.err)
					}
				} else if got.err == nil || got.result.Exited {
					t.Fatalf("lost RPC became a process exit: %+v, %v", got.result, got.err)
				}
			})
		}
	}
}

func interceptTerminalWait(t *testing.T, socket string) (<-chan struct{}, func()) {
	t.Helper()
	upstream := filepath.Join(filepath.Dir(socket), "wait-upstream.sock")
	if err := os.Rename(socket, upstream); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	armed, dropped := make(chan struct{}), make(chan struct{})
	var once sync.Once
	drop := func() { once.Do(func() { close(dropped) }) }
	t.Cleanup(drop)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				peer, err := net.Dial("unix", upstream)
				if err != nil {
					return
				}
				defer peer.Close()
				go func() { _, _ = io.Copy(conn, peer) }()
				reader := bufio.NewReader(conn)
				for {
					line, err := reader.ReadBytes('\n')
					if err != nil {
						return
					}
					var req ptyworker.RequestEnvelope
					if err := json.Unmarshal(line, &req); err != nil {
						return
					}
					if req.Method == ptyworker.MethodWaitBlock {
						close(armed)
						<-dropped
						return
					}
					if _, err := peer.Write(line); err != nil {
						return
					}
				}
			}()
		}
	}()
	return armed, drop
}
