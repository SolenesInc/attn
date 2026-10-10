package ptybackend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/buildinfo"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptyhost"
	"github.com/victorarias/attn/internal/ptyhost/ptyhosttest"
)

func TestSharedHost_HandoverMovesEveryTerminalToTheNewBuildInPlace(t *testing.T) {
	if os.Getenv("ATTN_TEST_PTY_HOST") == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to run the shared PTY host integration tests")
	}
	world := newHandoverWorld(t)
	oldBackend := world.backend(t, "handover-old", ptyhosttest.BuildWithSnapshotFormat(t, "handover-old"))
	for _, id := range []string{"numbers", "neighbour"} {
		if err := oldBackend.Spawn(context.Background(), SpawnOptions{ID: harness.TerminalID(id), CWD: t.TempDir(), Agent: "shell", Cols: 80, Rows: 24}); err != nil {
			t.Fatal(err)
		}
	}
	hostPID := oldBackend.WorkerPIDs(context.Background())["numbers"]
	t.Cleanup(func() {
		_ = syscall.Kill(hostPID, syscall.SIGTERM)
		_ = waitForPIDsGone(3*time.Second, hostPID)
	})
	numbers := fillTerminal(t, oldBackend, "numbers")

	newBackend := world.backend(t, "handover-new", ptyhosttest.BuildWithSnapshotFormat(t, "handover-new"))
	if _, err := newBackend.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []harness.TerminalID{"numbers", "neighbour"} {
		if err := newBackend.UpgradeWorker(context.Background(), id); err != nil {
			t.Fatalf("UpgradeWorker(%s): %v", id, err)
		}
		if format, known := newBackend.SessionTerminalBuild(id); !known || format != "handover-new" {
			t.Fatalf("%s reports terminal format %q (known=%t) after the handover, want handover-new", id, format, known)
		}
	}
	if pids := newBackend.WorkerPIDs(context.Background()); pids["numbers"] != hostPID || pids["neighbour"] != hostPID {
		t.Fatalf("host pids after the handover = %v, want both terminals still in pid %d", pids, hostPID)
	}
	numbers.requireCarriedBy(t, newBackend, "handover-new")
}

func TestSharedHost_ABuildThatCannotAdoptNeverTouchesTheLiveHost(t *testing.T) {
	if os.Getenv("ATTN_TEST_PTY_HOST") == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to run the shared PTY host integration tests")
	}
	world := newHandoverWorld(t)
	oldBackend := world.backend(t, "rehearsal-old", ptyhosttest.BuildWithSnapshotFormat(t, "rehearsal-old"))
	if err := oldBackend.Spawn(context.Background(), SpawnOptions{ID: "kept", CWD: t.TempDir(), Agent: "shell", Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	hostPID := oldBackend.WorkerPIDs(context.Background())["kept"]
	t.Cleanup(func() {
		_ = syscall.Kill(hostPID, syscall.SIGTERM)
		_ = waitForPIDsGone(3*time.Second, hostPID)
	})

	newBuild := ptyhosttest.BuildWithSnapshotFormat(t, "rehearsal-new")
	refusesToAdopt := filepath.Join(t.TempDir(), "attn-pty-host")
	script := "#!/bin/sh\nif [ \"$1\" = --adopt-handoff ]; then echo 'this build cannot adopt' >&2; exit 1; fi\nexec " + newBuild + " \"$@\"\n"
	if err := os.WriteFile(refusesToAdopt, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	newBackend := world.backend(t, "rehearsal-new", refusesToAdopt)
	if _, err := newBackend.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := newBackend.UpgradeWorker(context.Background(), "kept")
	if err == nil || !strings.Contains(err.Error(), "rehearse handover") {
		t.Fatalf("UpgradeWorker = %v, want the rehearsal to refuse a build that cannot adopt", err)
	}
	if pids := newBackend.WorkerPIDs(context.Background()); pids["kept"] != hostPID {
		t.Fatalf("host pid after the refused handover = %d, want the untouched %d", pids["kept"], hostPID)
	}
	if format, _ := newBackend.SessionTerminalBuild("kept"); format != "rehearsal-old" {
		t.Fatalf("terminal format after the refused handover = %q, want rehearsal-old", format)
	}
	_, stream, err := newBackend.Attach(context.Background(), "kept", "still-here", AttachOptions{OmitReplay: true})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := newBackend.Input(context.Background(), "kept", []byte("printf 'kept-%s\\n' seven\n")); err != nil {
		t.Fatal(err)
	}
	waitForStreamText(t, stream, "kept-seven")
}

func TestSharedHost_ABuildThatFailsToAdoptHandsEveryTerminalBack(t *testing.T) {
	if os.Getenv("ATTN_TEST_PTY_HOST") == "" {
		t.Skip("set ATTN_TEST_PTY_HOST to run the shared PTY host integration tests")
	}
	for _, fault := range []string{"error", "panic"} {
		t.Run(fault, func(t *testing.T) {
			world := newHandoverWorld(t)
			t.Setenv("ATTN_PTY_HOST_ADOPT_FAULT", fault)
			oldBackend := world.backend(t, "fallback-old", ptyhosttest.BuildWithSnapshotFormat(t, "fallback-old"))
			for _, id := range []harness.TerminalID{"kept", "neighbour"} {
				if err := oldBackend.Spawn(context.Background(), SpawnOptions{ID: id, CWD: t.TempDir(), Agent: "shell", Cols: 80, Rows: 24}); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Unsetenv("ATTN_PTY_HOST_ADOPT_FAULT"); err != nil {
				t.Fatal(err)
			}
			hostPID := oldBackend.WorkerPIDs(context.Background())["kept"]
			t.Cleanup(func() {
				_ = syscall.Kill(hostPID, syscall.SIGTERM)
				_ = waitForPIDsGone(3*time.Second, hostPID)
			})
			terminals := []filledTerminal{fillTerminal(t, oldBackend, "kept"), fillTerminal(t, oldBackend, "neighbour")}
			registryBefore := world.hostRegistry(t, hostPID)

			newBackend := world.backend(t, "fallback-new", ptyhosttest.BuildWithAdoptFault(t, "fallback-new"))
			if _, err := newBackend.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			err := newBackend.UpgradeWorker(context.Background(), "kept")
			if err == nil || !strings.Contains(err.Error(), "did not take effect") {
				t.Fatalf("UpgradeWorker = %v, want the live host's failed adopt reported as a handover that did not take effect", err)
			}
			if pids := newBackend.WorkerPIDs(context.Background()); pids["kept"] != hostPID || pids["neighbour"] != hostPID {
				t.Fatalf("host pids after the hand-back = %v, want both terminals in pid %d", pids, hostPID)
			}
			if registryAfter := world.hostRegistry(t, hostPID); registryAfter.ArtifactID != registryBefore.ArtifactID ||
				registryAfter.Executable != registryBefore.Executable || registryAfter.SnapshotFormat != registryBefore.SnapshotFormat {
				t.Fatalf("host registry after the hand-back names build %s (%s, format %s), want the old build %s (%s, format %s)",
					registryAfter.ArtifactID, registryAfter.Executable, registryAfter.SnapshotFormat,
					registryBefore.ArtifactID, registryBefore.Executable, registryBefore.SnapshotFormat)
			}
			for _, terminal := range terminals {
				if format, _ := newBackend.SessionTerminalBuild(terminal.id); format != "fallback-old" {
					t.Fatalf("terminal format of %s after the hand-back = %q, want fallback-old", terminal.id, format)
				}
				terminal.requireCarriedBy(t, newBackend, "fallback-old")
			}
		})
	}
}

type handoverWorld struct {
	root string
}

func newHandoverWorld(t *testing.T) handoverWorld {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "attn-handover-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	previous := buildinfo.SnapshotFormat
	t.Cleanup(func() { buildinfo.SnapshotFormat = previous })
	return handoverWorld{root: root}
}

func (w handoverWorld) backend(t *testing.T, format, binary string) *WorkerBackend {
	t.Helper()
	buildinfo.SnapshotFormat = format
	backend, err := NewSharedHost(WorkerBackendConfig{
		DataRoot: w.root, DaemonInstanceID: "d-handover", BinaryPath: binary, Logf: t.Logf,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Shutdown(context.Background()) })
	if err := backend.ValidateSharedCandidate(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	return backend
}

func (w handoverWorld) hostRegistry(t *testing.T, hostPID int) ptyhost.HostRegistry {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(ptyhost.HostRegistryDir(w.root, "d-handover"), "*.json"))
	for _, path := range paths {
		if entry, err := ptyhost.ReadHostRegistry(path); err == nil && entry.HostPID == hostPID {
			return entry
		}
	}
	t.Fatalf("no host registry entry names pid %d among %v", hostPID, paths)
	return ptyhost.HostRegistry{}
}

type filledTerminal struct {
	id       harness.TerminalID
	shellPID int
	screen   *pty.ViewportSnapshot
}

func fillTerminal(t *testing.T, backend *WorkerBackend, id harness.TerminalID) filledTerminal {
	t.Helper()
	_, live, err := backend.Attach(context.Background(), id, "fill", AttachOptions{OmitReplay: true})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if err := backend.Input(context.Background(), id, []byte("seq 1 40 | sed s/^/"+string(id)+"-/; cat\n")); err != nil {
		t.Fatal(err)
	}
	waitForStreamText(t, live, string(id)+"-40")
	snapshot, err := backend.ScreenSnapshot(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	info, err := backend.SessionInfo(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return filledTerminal{id: id, shellPID: info.PID, screen: snapshot.Screen}
}

func (f filledTerminal) requireCarriedBy(t *testing.T, backend *WorkerBackend, format string) {
	t.Helper()
	info, err := backend.SessionInfo(context.Background(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	if info.PID != f.shellPID || !info.Running {
		t.Fatalf("shell of %s: pid=%d running=%t, want the same running shell pid %d", f.id, info.PID, info.Running, f.shellPID)
	}
	now, err := backend.ScreenSnapshot(context.Background(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	if now.Screen == nil || f.screen == nil || now.Screen.Text != f.screen.Text {
		t.Fatalf("screen of %s differs:\nbefore:\n%s\nnow:\n%s", f.id, screenText(f.screen), screenText(now.Screen))
	}
	attached, stream, err := backend.Attach(context.Background(), f.id, "carried")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if attached.GhosttySnapshotFormat != format || len(attached.GhosttySnapshot) == 0 {
		t.Fatalf("attach of %s: format=%q snapshot=%dB, want a %s snapshot", f.id, attached.GhosttySnapshotFormat, len(attached.GhosttySnapshot), format)
	}
	if err := backend.Input(context.Background(), f.id, []byte("\x04printf 'carried-%s\\n' over\n")); err != nil {
		t.Fatal(err)
	}
	waitForStreamText(t, stream, "carried-over")
}

func screenText(screen *pty.ViewportSnapshot) string {
	if screen == nil {
		return "<no screen>"
	}
	return screen.Text
}
