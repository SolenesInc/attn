package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/toolhome"
)

func spawnTestClient() *wsClient {
	return &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
}

func seedClaudeTranscript(t *testing.T, home, resumeID string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", "seed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, resumeID+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func TestSpawnResumePickerIgnoresGardenReceipt(t *testing.T) {
	d := newGardenDaemon(t)
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	const sessionID = "attn-picker-with-garden-receipt"
	cwd := t.TempDir()
	addTestWorkspace(d, "workspace-picker", cwd)
	if err := d.recordGardenDispatch(sessionID, "", "", cwd, "claude", false); err != nil {
		t.Fatal(err)
	}
	if err := d.rememberDispatchResume(sessionID, "garden-native-id"); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)
	seedClaudeTranscript(t, home, "garden-native-id")

	since := spawnCount(backend)
	d.handleSpawnSession(spawnTestClient(), &protocol.SpawnSessionMessage{
		Cmd:          protocol.CmdSpawnSession,
		ID:           sessionID,
		Cwd:          cwd,
		Agent:        "claude",
		WorkspaceID:  "workspace-picker",
		Cols:         80,
		Rows:         24,
		ResumePicker: protocol.Ptr(true),
	})

	spawn := resumeSpawnForSession(t, backend, sessionID, since)
	if spawn.ResumeSessionID != "" {
		t.Fatalf("ResumeSessionID = %q, want the native picker to choose", spawn.ResumeSessionID)
	}
	if !spawn.ResumePicker {
		t.Fatal("ResumePicker = false, want true")
	}
}
