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

func seedReloadableClaudeSession(t *testing.T, d *Daemon, sessionID string) (workspaceID, cwd string) {
	t.Helper()
	workspaceID = "workspace-" + sessionID
	cwd = t.TempDir()
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "revive",
		Directory: cwd,
	})
	d.store.Add(&protocol.Session{
		ID:          sessionID,
		Agent:       protocol.SessionAgentClaude,
		WorkspaceID: workspaceID,
		Directory:   cwd,
		Label:       "revive",
	})
	return workspaceID, cwd
}

func TestSpawnDowngradesResumeWhenTranscriptMissing(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	sessionID := "attn-revive-claude"
	workspaceID, cwd := seedReloadableClaudeSession(t, d, sessionID)
	d.persistResumeSessionID(sessionID, sessionID)
	t.Setenv(toolhome.EnvVar, t.TempDir())

	since := spawnCount(backend)
	d.handleSpawnSession(spawnTestClient(), &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Cwd:         cwd,
		Agent:       "claude",
		WorkspaceID: workspaceID,
		Cols:        80,
		Rows:        24,
	})

	spawn := resumeSpawnForSession(t, backend, sessionID, since)
	if spawn.ResumeSessionID != "" {
		t.Fatalf("ResumeSessionID = %q, want empty (no transcript → fresh spawn reusing --session-id)", spawn.ResumeSessionID)
	}
}

func TestSpawnPreservesSelfResumeWhenTranscriptPresent(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	sessionID := "attn-revive-claude-live"
	workspaceID, cwd := seedReloadableClaudeSession(t, d, sessionID)
	d.persistResumeSessionID(sessionID, sessionID)
	home := t.TempDir()
	t.Setenv(toolhome.EnvVar, home)
	seedClaudeTranscript(t, home, sessionID)

	since := spawnCount(backend)
	d.handleSpawnSession(spawnTestClient(), &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Cwd:         cwd,
		Agent:       "claude",
		WorkspaceID: workspaceID,
		Cols:        80,
		Rows:        24,
	})

	spawn := resumeSpawnForSession(t, backend, sessionID, since)
	if spawn.ResumeSessionID != sessionID {
		t.Fatalf("ResumeSessionID = %q, want %q (transcript present → self-resume preserved)", spawn.ResumeSessionID, sessionID)
	}
}

func TestSpawnPreservesDistinctNativeResumeID(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	sessionID := "attn-revive-claude-distinct"
	workspaceID, cwd := seedReloadableClaudeSession(t, d, sessionID)
	nativeID := "claude-native-xyz"
	d.persistResumeSessionID(sessionID, nativeID)
	t.Setenv(toolhome.EnvVar, t.TempDir()) // no transcript on disk

	since := spawnCount(backend)
	d.handleSpawnSession(spawnTestClient(), &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Cwd:         cwd,
		Agent:       "claude",
		WorkspaceID: workspaceID,
		Cols:        80,
		Rows:        24,
	})

	spawn := resumeSpawnForSession(t, backend, sessionID, since)
	if spawn.ResumeSessionID != nativeID {
		t.Fatalf("ResumeSessionID = %q, want %q (distinct native id trusted, not downgraded)", spawn.ResumeSessionID, nativeID)
	}
}

func TestSpawnReviveReentersLaunchLifecycle(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	cwd := t.TempDir()
	addTestWorkspace(d, "workspace", cwd)
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "recoverable",
		Label:          "recoverable",
		Agent:          protocol.SessionAgentClaude,
		Directory:      cwd,
		WorkspaceID:    "workspace",
		State:          protocol.SessionStateRecoverable,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	client := spawnTestClient()
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          "recoverable",
		Cwd:         cwd,
		Agent:       "claude",
		WorkspaceID: "workspace",
		Cols:        80,
		Rows:        24,
	})
	expectSpawnResult(t, client, "recoverable", true)

	if session := d.store.Get("recoverable"); session == nil || session.State != protocol.SessionStateLaunching {
		t.Fatalf("session = %+v, want launching", session)
	}
}
