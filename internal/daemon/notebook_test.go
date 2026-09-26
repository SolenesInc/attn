package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

func newNotebookDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.store.SetSetting(SettingNotebookRoot, t.TempDir())
	return d
}

func readNotebookWSEvent(t *testing.T, ch chan outboundMessage, target any) {
	t.Helper()
	select {
	case message := <-ch:
		if err := json.Unmarshal(message.payload, target); err != nil {
			t.Fatalf("decode ws event: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("no websocket result event was sent")
	}
}

func addIdleNotebookSession(d *Daemon, id string, state protocol.SessionState) {
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: id, Agent: protocol.SessionAgentClaude,
		Directory: "/tmp/" + id, WorkspaceID: "workspace-" + id,
		State: state, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

func TestNotebookRootFollowsTheSettingAndFallsBackToTheDefault(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	custom := t.TempDir()
	for _, tc := range []struct{ setting, want string }{
		{custom, custom},
		{"~/notes", filepath.Join(home, "notes")},
		{"", notebook.DefaultRoot(home, config.Instance())},
	} {
		d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
		d.store.SetSetting(SettingNotebookRoot, tc.setting)
		if got, err := d.notebookRoot(); err != nil || got != tc.want {
			t.Errorf("notebook.root %q resolves to %q (%v), want %q", tc.setting, got, err, tc.want)
		}
	}
}

func TestNotebookWatcherFollowsRootChange(t *testing.T) {
	d := newNotebookDaemon(t)
	t.Cleanup(d.stopNotebookWatcher)
	rootA := d.store.GetSetting(SettingNotebookRoot)
	rootB := t.TempDir()
	client := &wsClient{send: make(chan outboundMessage, 64)}
	d.wsHub.clients[client] = true
	hubStopped := make(chan struct{})
	t.Cleanup(func() { close(hubStopped) })
	go d.wsHub.runUntil(hubStopped)

	listNotebook(t, d)
	d.store.SetSetting(SettingNotebookRoot, rootB)
	if listed := listNotebook(t, d); len(listed) != 0 {
		t.Fatalf("listing after the root moved to an empty directory returned %v", listed)
	}

	for _, edit := range []string{filepath.Join(rootA, "a.md"), filepath.Join(rootB, "b.md")} {
		if err := os.WriteFile(edit, []byte("# edit\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case message := <-client.send:
			var changed protocol.NotebookChangedMessage
			if json.Unmarshal(message.payload, &changed) != nil || changed.Event != protocol.EventNotebookChanged || changed.Origin != originExternal {
				continue
			}
			if slices.Contains(changed.Paths, "a.md") {
				t.Fatalf("an edit under the old root was still reported: %v", changed.Paths)
			}
			if slices.Contains(changed.Paths, "b.md") {
				return
			}
		case <-deadline:
			t.Fatal("the edit under the new root was not reported")
		}
	}
}

func listNotebook(t *testing.T, d *Daemon) []protocol.NotebookEntry {
	t.Helper()
	client := &wsClient{send: make(chan outboundMessage, 1)}
	d.sendNotebookListWSResult(client, "list", "")
	var listed protocol.NotebookListResultMessage
	readNotebookWSEvent(t, client.send, &listed)
	if !listed.Success {
		t.Fatalf("listing the notebook failed: %s", protocol.Deref(listed.Error))
	}
	return listed.Entries
}
