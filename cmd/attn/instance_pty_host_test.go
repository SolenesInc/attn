package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/procreap"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/ptyworker"
	"github.com/victorarias/attn/internal/testworld"
)

func TestInstanceCleanStopsSharedHostGenerationsAndChildren(t *testing.T) {
	binary := os.Getenv("ATTN_TEST_PTY_HOST")
	if binary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to run live instance cleanup")
	}
	root, err := os.MkdirTemp("/tmp", "pty-clean-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	r := instanceResolved{Label: "test", DataDir: filepath.Join(root, "data"), AppPath: filepath.Join(root, "absent-app"), AppLocalData: filepath.Join(root, "app-data"), AppLock: filepath.Join(root, "app.lock")}
	if err := os.MkdirAll(r.AppLocalData, 0o700); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	nextBinary := filepath.Join(root, "next-host")
	if err := os.WriteFile(nextBinary, append(contents, 0), 0o700); err != nil {
		t.Fatal(err)
	}
	pids := make(map[int]bool)
	var children []int
	t.Cleanup(func() {
		for pid := range pids {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
	})
	for generation, executable := range []string{binary, nextBinary} {
		backend, err := ptybackend.NewSharedHost(ptybackend.WorkerBackendConfig{DataRoot: r.DataDir, DaemonInstanceID: "d-clean", BinaryPath: executable})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = backend.Shutdown(context.Background()) })
		if err := backend.Probe(context.Background()); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			id := fmt.Sprintf("session-%d-%d", generation, i)
			if err := backend.Spawn(context.Background(), ptybackend.SpawnOptions{ID: harness.TerminalID(id), Agent: "cleanup-fixture", CWD: root, Cols: 80, Rows: 24, ExternalCommand: []string{"/bin/cat"}}); err != nil {
				t.Fatal(err)
			}
			pids[backend.WorkerPIDs(context.Background())[id]] = true
			info, err := backend.SessionInfo(context.Background(), harness.TerminalID(id))
			if err != nil {
				t.Fatal(err)
			}
			children = append(children, info.PID)
		}
		if err := backend.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := countLiveWorkers(r.DataDir); got != 2 {
		t.Fatalf("live workers = %d, want two hosts for four PTYs", got)
	}
	paths := ptyhost.HostRegistryPaths(r.DataDir)
	entries := make([]ptyhost.HostRegistry, len(paths))
	for i, path := range paths {
		entries[i], err = ptyhost.ReadHostRegistry(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, field := range []string{"token", "pid"} {
		t.Run("reject-invalid-"+field, func(t *testing.T) {
			for i, entry := range entries {
				invalid := entry
				if field == "token" {
					invalid.ControlToken = "invalid-token"
				} else {
					invalid.HostPID = os.Getpid()
				}
				if err := writeHostRegistry(paths[i], invalid); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := writeHostRegistry(paths[i], entry); err != nil {
						t.Error(err)
					}
				})
			}
			var out bytes.Buffer
			if err := cleanInstance(&out, r); err == nil {
				t.Fatalf("cleanup accepted invalid %s: %s", field, out.String())
			}
			for pid := range pids {
				if !procreap.ProcessAlive(pid) {
					t.Fatalf("identity mismatch stopped host %d", pid)
				}
			}
		})
	}
	var out bytes.Buffer
	if err := cleanInstance(&out, r); err != nil {
		t.Fatalf("clean instance: %v\n%s", err, out.String())
	}
	for _, pid := range children {
		if procreap.ProcessAlive(pid) {
			t.Errorf("child %d survived instance cleanup", pid)
		}
	}
	for pid := range pids {
		if procreap.ProcessAlive(pid) {
			t.Errorf("host %d survived instance cleanup", pid)
		}
	}
	if _, err := os.Stat(r.DataDir); !os.IsNotExist(err) {
		t.Fatalf("data dir still exists: %v", err)
	}
}

func TestInstanceCleanPreservesUnreachableSharedHostRegistry(t *testing.T) {
	r := stoppedInstance(t)
	path := ptyhost.HostRegistryPath(r.DataDir, "d-unknown", "unknown")
	if err := writeHostRegistry(path, ptyhost.HostRegistry{Version: 1, DaemonInstanceID: "d-unknown", ArtifactID: "unknown", HostPID: os.Getpid(), SocketPath: filepath.Join(ptyhost.Root(r.DataDir, "d-unknown"), "sock", "unknown.sock"), ControlToken: "unreachable"}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cleanInstance(&out, r); err == nil {
		t.Fatalf("cleanup accepted an unreachable live host: %s", out.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cleanup destroyed the unreaped registry: %v", err)
	}
}

func writeHostRegistry(path string, entry ptyhost.HostRegistry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func TestInstanceCleanPreservesUnreachableWorkerRegistry(t *testing.T) {
	r := stoppedInstance(t)
	path := filepath.Join(r.DataDir, "workers", "d-unknown", "registry", "unreachable.json")
	if err := ptyworker.WriteRegistryAtomic(path, ptyworker.RegistryEntry{Version: 1, SessionID: "unreachable", WorkerPID: os.Getpid(), SocketPath: filepath.Join(r.DataDir, "absent.sock")}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cleanInstance(&out, r); err == nil {
		t.Fatalf("cleanup accepted an unreachable live worker: %s", out.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cleanup destroyed the unreaped registry: %v", err)
	}
}

func TestInstanceCleanWaitsForWorkerChildResistingTermination(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "pty-clean-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	r := instanceResolved{Label: "test", DataDir: filepath.Join(root, "data"), AppPath: filepath.Join(root, "absent-app"), AppLocalData: filepath.Join(root, "app-data"), AppLock: filepath.Join(root, "app.lock")}
	if err := os.MkdirAll(r.AppLocalData, 0o700); err != nil {
		t.Fatal(err)
	}
	backend, err := ptybackend.NewWorker(ptybackend.WorkerBackendConfig{DataRoot: r.DataDir, DaemonInstanceID: "d-clean", BinaryPath: testworld.AttnBinary(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Shutdown(context.Background()) })
	readyPath := filepath.Join(root, "ready")
	if err := syscall.Mkfifo(readyPath, 0o600); err != nil {
		t.Fatal(err)
	}
	ready := make(chan error, 1)
	go func() {
		f, err := os.Open(readyPath)
		if err != nil {
			ready <- err
			return
		}
		defer f.Close()
		line, err := bufio.NewReader(f).ReadString('\n')
		if err == nil && line != "ready\n" {
			err = fmt.Errorf("unexpected readiness %q", line)
		}
		ready <- err
	}()
	const id = "resistant-child"
	if err := backend.Spawn(context.Background(), ptybackend.SpawnOptions{
		ID: id, CWD: r.AppLocalData, Agent: "cleanup-probe", Cols: 80, Rows: 24,
		ExternalCommand: []string{"/bin/sh", "-c", `trap '' TERM HUP; exec 3>"$1"; printf 'ready\n' >&3; while :; do read hold || true; done`, "cleanup-probe", readyPath},
	}); err != nil {
		t.Fatal(err)
	}
	workerPID := backend.WorkerPIDs(context.Background())[id]
	info, err := backend.SessionInfo(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if procreap.ProcessAlive(info.PID) {
			_ = syscall.Kill(-info.PID, syscall.SIGKILL)
		}
		if procreap.ProcessAlive(workerPID) {
			_ = syscall.Kill(workerPID, syscall.SIGKILL)
		}
	})
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cleanInstance(&out, r); err != nil {
		t.Fatalf("clean instance: %v\n%s", err, out.String())
	}
	if procreap.ProcessAlive(info.PID) {
		t.Fatalf("child %d survived instance cleanup", info.PID)
	}
}
