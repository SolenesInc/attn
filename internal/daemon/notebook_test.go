package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
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

func recordingBackend(inputs *[]string, mu *sync.Mutex) *fakeSpawnBackend {
	return &fakeSpawnBackend{onInput: func(_ string, data []byte) {
		mu.Lock()
		*inputs = append(*inputs, string(data))
		mu.Unlock()
	}}
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
