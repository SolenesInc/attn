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
	_, live, err := oldBackend.Attach(context.Background(), "numbers", "before", AttachOptions{OmitReplay: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := oldBackend.Input(context.Background(), "numbers", []byte("seq 1 40 | sed s/^/row-/; cat\n")); err != nil {
		t.Fatal(err)
	}
	waitForStreamText(t, live, "row-40")
	_ = live.Close()
	before, err := oldBackend.ScreenSnapshot(context.Background(), "numbers")
	if err != nil {
		t.Fatal(err)
	}
	child, err := oldBackend.SessionInfo(context.Background(), "numbers")
	if err != nil {
		t.Fatal(err)
	}

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
	adopted, err := newBackend.SessionInfo(context.Background(), "numbers")
	if err != nil {
		t.Fatal(err)
	}
	if adopted.PID != child.PID || !adopted.Running {
		t.Fatalf("shell after the handover: pid=%d running=%t, want the same running shell pid %d", adopted.PID, adopted.Running, child.PID)
	}
	after, err := newBackend.ScreenSnapshot(context.Background(), "numbers")
	if err != nil {
		t.Fatal(err)
	}
	if after.Screen == nil || before.Screen == nil || after.Screen.Text != before.Screen.Text {
		t.Fatalf("screen after the handover differs:\nbefore:\n%s\nafter:\n%s", screenText(before.Screen), screenText(after.Screen))
	}

	attached, stream, err := newBackend.Attach(context.Background(), "numbers", "after")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if attached.GhosttySnapshotFormat != "handover-new" || len(attached.GhosttySnapshot) == 0 {
		t.Fatalf("attach after the handover: format=%q snapshot=%dB, want a handover-new snapshot", attached.GhosttySnapshotFormat, len(attached.GhosttySnapshot))
	}
	if err := newBackend.Input(context.Background(), "numbers", []byte("\x04printf 'after-%s\\n' five\n")); err != nil {
		t.Fatal(err)
	}
	waitForStreamText(t, stream, "after-five")
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
	if err := backend.ValidateSharedCandidate(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	return backend
}

func screenText(screen *pty.ViewportSnapshot) string {
	if screen == nil {
		return "<no screen>"
	}
	return screen.Text
}
