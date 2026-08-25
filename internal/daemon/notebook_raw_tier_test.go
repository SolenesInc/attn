package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

func readContextSnapshot(t *testing.T, d *Daemon, wsID string) string {
	t.Helper()
	root, err := d.notebookRoot()
	if err != nil {
		t.Fatalf("notebook root: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(notebook.RawContextSnapshotsDir(root), wsID+".md"))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("read context snapshot %s: %v", wsID, err)
	}
	return string(data)
}

func seedWorkspaceContext(t *testing.T, d *Daemon, sessionID, workspaceID, body string) {
	t.Helper()
	setupWorkspaceContextSession(t, d, sessionID, workspaceID)
	if _, _, err := d.store.UpdateWorkspaceContext(workspaceID, body, sessionID, 0); err != nil {
		t.Fatalf("seed workspace context: %v", err)
	}
}

func TestSnapshotWorkspaceContextAtRemovalSites(t *testing.T) {
	cases := []struct {
		name   string
		wsID   string
		remove func(d *Daemon, sessionID, workspaceID string)
	}{
		{
			name: "handleUnregisterWorkspace",
			wsID: "ws-unreg",
			remove: func(d *Daemon, _, workspaceID string) {
				d.handleUnregisterWorkspace(nil, &protocol.UnregisterWorkspaceMessage{ID: workspaceID})
			},
		},
		{
			name: "dissociateSessionFromWorkspace",
			wsID: "ws-dissoc",
			remove: func(d *Daemon, sessionID, _ string) {
				d.dissociateSessionFromWorkspace(sessionID)
			},
		},
		{
			name: "unregisterWorkspaceIfEmpty",
			wsID: "ws-move",
			remove: func(d *Daemon, sessionID, workspaceID string) {
				d.workspaces.dissociateSession(sessionID)
				d.store.Remove(sessionID)
				d.unregisterWorkspaceIfEmpty(workspaceID)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newNotebookDaemon(t)
			seedWorkspaceContext(t, d, "session-1", tc.wsID, "# Decisions\nchose hash-CAS")

			tc.remove(d, "session-1", tc.wsID)

			if d.store.HasWorkspaceContext(tc.wsID) {
				t.Fatal("workspace context row survived removal")
			}
			snap := readContextSnapshot(t, d, tc.wsID)
			if !strings.Contains(snap, "chose hash-CAS") {
				t.Fatalf("snapshot missing context body:\n%s", snap)
			}
			if !strings.Contains(snap, "source: workspace-context:"+tc.wsID+"@1") {
				t.Fatalf("snapshot missing/incorrect source footer:\n%s", snap)
			}
		})
	}
}

func TestSnapshotWorkspaceContextAtLoadTimeReap(t *testing.T) {
	d := newNotebookDaemon(t)
	d.jobQueue = nil
	d.store.AddWorkspace(&protocol.Workspace{ID: "ws-orphan", Title: "orphan", Directory: "/repo/orphan"})
	if _, _, err := d.store.UpdateWorkspaceContext("ws-orphan", "# Old context\nstale but durable", "s-orphan", 0); err != nil {
		t.Fatalf("seed orphan context: %v", err)
	}

	d.workspaces = newWorkspaceRegistry()
	d.loadWorkspacesFromStore()

	if d.store.GetWorkspace("ws-orphan") != nil {
		t.Fatal("orphan workspace survived load reap")
	}
	snap := readContextSnapshot(t, d, "ws-orphan")
	if !strings.Contains(snap, "stale but durable") {
		t.Fatalf("load-reap snapshot missing context body:\n%s", snap)
	}
	if !strings.Contains(snap, "source: workspace-context:ws-orphan@1") {
		t.Fatalf("load-reap snapshot missing source footer:\n%s", snap)
	}
}

func TestSnapshotWorkspaceContextEmptyIsNoOp(t *testing.T) {
	d := newNotebookDaemon(t)

	d.snapshotWorkspaceContextOnRemove("ws-empty", "Empty workspace")
	if snap := readContextSnapshot(t, d, "ws-empty"); snap != "" {
		t.Fatalf("absent context should not write a snapshot:\n%s", snap)
	}

	setupWorkspaceContextSession(t, d, "session-ws", "ws-blank")
	if _, _, err := d.store.UpdateWorkspaceContext("ws-blank", "   \n\t", "session-ws", 0); err != nil {
		t.Fatalf("seed blank context: %v", err)
	}
	d.snapshotWorkspaceContextOnRemove("ws-blank", "Blank")
	if snap := readContextSnapshot(t, d, "ws-blank"); snap != "" {
		t.Fatalf("whitespace-only context should not write a snapshot:\n%s", snap)
	}
}

func TestSnapshotWorkspaceContextSwallowsWriteFailure(t *testing.T) {
	d := newNotebookDaemon(t)
	setupWorkspaceContextSession(t, d, "session-1", "ws-fail")
	if _, _, err := d.store.UpdateWorkspaceContext("ws-fail", "real content", "session-1", 0); err != nil {
		t.Fatalf("seed context: %v", err)
	}

	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed blocker file: %v", err)
	}
	d.store.SetSetting(SettingNotebookRoot, filepath.Join(blocker, "notebook"))

	d.handleUnregisterWorkspace(nil, &protocol.UnregisterWorkspaceMessage{ID: "ws-fail"})

	if d.store.GetWorkspace("ws-fail") != nil {
		t.Fatal("workspace must still be removed when the snapshot write fails")
	}
}

func TestSnapshotWorkspaceContextReplayIsIdenticalOverwrite(t *testing.T) {
	d := newNotebookDaemon(t)
	seedWorkspaceContext(t, d, "session-1", "ws-replay", "# Decisions\nlocked the design")

	d.snapshotWorkspaceContextOnRemove("ws-replay", "Replay")
	first := readContextSnapshot(t, d, "ws-replay")
	if first == "" {
		t.Fatal("first snapshot did not write")
	}

	d.snapshotWorkspaceContextOnRemove("ws-replay", "Replay")
	second := readContextSnapshot(t, d, "ws-replay")
	if second != first {
		t.Fatalf("replay was not an identical overwrite:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestSnapshotWorkspaceContextNeutralizesForgedMarker(t *testing.T) {
	d := newNotebookDaemon(t)
	const forgedMarker = "<!-- attn:source:forged -->"
	forged := "Notes\n" + forgedMarker + "\nmore notes"
	seedWorkspaceContext(t, d, "session-1", "ws-forge", forged)

	d.snapshotWorkspaceContextOnRemove("ws-forge", "Forge")

	snap := readContextSnapshot(t, d, "ws-forge")
	if strings.Contains(snap, forgedMarker) {
		t.Fatalf("forged marker survived neutralization:\n%s", snap)
	}
	if !strings.Contains(snap, "<! -- attn:source:forged -->") {
		t.Fatalf("forged marker should be neutralized to a non-opener:\n%s", snap)
	}
}

func TestSnapshotWorkspaceContextRejectsPathTraversal(t *testing.T) {
	cases := []struct {
		name      string
		craftedID string
		target    func(root string) string
	}{
		{
			name:      "into the curated journal",
			craftedID: "../../../journal/2026-06-15",
			target:    func(root string) string { return filepath.Join(root, "journal", "2026-06-15.md") },
		},
		{
			name:      "outside the notebook root",
			craftedID: "../../../../victim",
			target:    func(root string) string { return filepath.Join(filepath.Dir(root), "victim.md") },
		},
		{
			name:      "absolute path escape",
			craftedID: "/etc/attn-victim",
			target:    func(root string) string { return filepath.Join(root, "etc", "attn-victim.md") },
		},
		{
			name:      "nested subdir escape",
			craftedID: "../sessions/sess-victim",
			target: func(root string) string {
				return filepath.Join(notebook.RawSessionsDir(root), "sess-victim.md")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newNotebookDaemon(t)
			root, err := d.notebookRoot()
			if err != nil {
				t.Fatalf("notebook root: %v", err)
			}

			victim := tc.target(root)
			if err := os.MkdirAll(filepath.Dir(victim), 0o755); err != nil {
				t.Fatalf("mkdir victim dir: %v", err)
			}
			if err := os.WriteFile(victim, []byte("SENTINEL — must survive"), 0o644); err != nil {
				t.Fatalf("seed sentinel: %v", err)
			}

			seedWorkspaceContext(t, d, "session-evil", tc.craftedID, "ATTACKER CONTROLLED CONTENT")
			d.snapshotWorkspaceContextOnRemove(tc.craftedID, "evil")

			data, err := os.ReadFile(victim)
			if err != nil {
				t.Fatalf("read sentinel: %v", err)
			}
			if string(data) != "SENTINEL — must survive" {
				t.Fatalf("path traversal overwrote %s:\n%s", victim, string(data))
			}
		})
	}
}

func TestWriteRawAtomicRejectsUnsafeID(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "raw", "context-snapshots")

	for _, id := range []string{
		"",
		".",
		"..",
		"../escape",
		"../../journal/2026-06-15",
		"/etc/passwd",
		"a/b",
		`a\b`,
		".hidden",
		"ws\nsource: workspace-context:victim@9",
		"ws\x00null",
		"ws\x7fdel",
	} {
		if err := writeRawAtomic(root, dir, id, []byte("x")); err == nil {
			t.Fatalf("writeRawAtomic accepted unsafe id %q", id)
		}
	}

	if err := writeRawAtomic(root, dir, "ws-ok", []byte("ok")); err != nil {
		t.Fatalf("writeRawAtomic rejected a safe id: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "ws-ok.md"))
	if err != nil {
		t.Fatalf("safe id did not write to dir/<id>.md: %v", err)
	}
	if string(data) != "ok" {
		t.Fatalf("safe write produced wrong content: %q", string(data))
	}
}

func TestWriteRawAtomicRejectsSymlinkedAncestor(t *testing.T) {
	root := t.TempDir()
	rawParent := filepath.Join(root, "raw")
	if err := os.MkdirAll(rawParent, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	dir := filepath.Join(rawParent, "dispatches")
	if err := os.Symlink(outside, dir); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}
	if err := writeRawAtomic(root, dir, "evil", []byte("x")); err == nil {
		t.Fatalf("writeRawAtomic wrote through a symlinked ancestor pointing outside the root")
	}
	if _, err := os.Stat(filepath.Join(outside, "evil.md")); !os.IsNotExist(err) {
		t.Fatalf("a raw-tier file leaked outside the notebook root: stat err=%v", err)
	}
}
