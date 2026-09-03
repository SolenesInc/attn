package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/ptyworker"
)

func TestProfileCleanStopsSharedHostGenerationsAndChildren(t *testing.T) {
	binary := os.Getenv("ATTN_TEST_PTY_HOST")
	if binary == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to run live profile cleanup")
	}
	root, err := os.MkdirTemp("/tmp", "pty-clean-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	r := profileResolved{Label: "test", DataDir: filepath.Join(root, "data"), AppPath: filepath.Join(root, "absent-app"), AppLocalData: filepath.Join(root, "app-data"), AppLock: filepath.Join(root, "app.lock")}
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
		for i := 0; i < 2; i++ {
			id := fmt.Sprintf("session-%d-%d", generation, i)
			if err := backend.Spawn(context.Background(), ptybackend.SpawnOptions{ID: id, Agent: "cleanup-fixture", CWD: root, Cols: 80, Rows: 24, ExternalCommand: []string{"/bin/cat"}}); err != nil {
				t.Fatal(err)
			}
			pids[backend.WorkerPIDs(context.Background())[id]] = true
			info, err := backend.SessionInfo(context.Background(), id)
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
				if err := ptyhost.WriteHostRegistryAtomic(paths[i], invalid); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := ptyhost.WriteHostRegistryAtomic(paths[i], entry); err != nil {
						t.Error(err)
					}
				})
			}
			var out bytes.Buffer
			if err := cleanProfile(&out, r); err == nil {
				t.Fatalf("cleanup accepted invalid %s: %s", field, out.String())
			}
			for pid := range pids {
				if !ptyworker.ProcessAlive(pid) {
					t.Fatalf("identity mismatch stopped host %d", pid)
				}
			}
		})
	}
	var out bytes.Buffer
	if err := cleanProfile(&out, r); err != nil {
		t.Fatalf("clean profile: %v\n%s", err, out.String())
	}
	for _, pid := range children {
		if ptyworker.ProcessAlive(pid) {
			t.Errorf("child %d survived profile cleanup", pid)
		}
	}
	for pid := range pids {
		if ptyworker.ProcessAlive(pid) {
			t.Errorf("host %d survived profile cleanup", pid)
		}
	}
	if _, err := os.Stat(r.DataDir); !os.IsNotExist(err) {
		t.Fatalf("data dir still exists: %v", err)
	}
}

func TestProfileCleanPreservesUnreachableSharedHostRegistry(t *testing.T) {
	r := stoppedProfile(t)
	path := ptyhost.HostRegistryPath(r.DataDir, "d-unknown", "unknown")
	if err := ptyhost.WriteHostRegistryAtomic(path, ptyhost.HostRegistry{Version: 1, DaemonInstanceID: "d-unknown", Generation: "unknown", HostPID: os.Getpid(), SocketPath: filepath.Join(ptyhost.Root(r.DataDir, "d-unknown"), "sock", "unknown.sock"), ControlToken: "unreachable"}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := cleanProfile(&out, r); err == nil {
		t.Fatalf("cleanup accepted an unreachable live host: %s", out.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cleanup destroyed the unreaped registry: %v", err)
	}
}
