package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestSpawnRefusesAMissingConversationToPickUp(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	sessionID := "attn-resume-missing"
	workspaceID, cwd := seedReloadableClaudeSession(t, d, sessionID)
	missing := filepath.Join(t.TempDir(), "deleted-by-a-profile-clean.jsonl")

	since := spawnCount(backend)
	rejection := d.runSpawnPipeline(&protocol.SpawnSessionMessage{
		Cmd:                    protocol.CmdSpawnSession,
		ID:                     sessionID,
		Cwd:                    cwd,
		Agent:                  "claude",
		WorkspaceID:            workspaceID,
		Cols:                   80,
		Rows:                   24,
		ResumeConversationFile: protocol.Ptr(missing),
	}, internalSpawnPolicy{})

	if rejection == nil {
		t.Fatal("spawn was accepted; want a refusal naming the conversation file that is gone")
	}
	if got := rejection.reason().Error(); !strings.Contains(got, missing) {
		t.Fatalf("refusal = %q, want it to name %q — an agent cannot fix a failure that does not say which file", got, missing)
	}
	if spawnCount(backend) != since {
		t.Fatal("a session was spawned anyway; the refusal must stop before the host starts")
	}
}

func TestSpawnStillRevivesWhenTheSourceConversationIsGone(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ATTN_DATA_DIR", dataDir)
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend

	sessionID := "attn-resume-established"
	workspaceID, cwd := seedReloadableClaudeSession(t, d, sessionID)

	stateDir := hostSessionStateDir(sessionID)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir host state dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "2026-08-09T00-00-00-000Z_forked.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write forked conversation: %v", err)
	}

	rejection := d.runSpawnPipeline(&protocol.SpawnSessionMessage{
		Cmd:                    protocol.CmdSpawnSession,
		ID:                     sessionID,
		Cwd:                    cwd,
		Agent:                  "claude",
		WorkspaceID:            workspaceID,
		Cols:                   80,
		Rows:                   24,
		ResumeConversationFile: protocol.Ptr(filepath.Join(t.TempDir(), "long-since-deleted.jsonl")),
	}, internalSpawnPolicy{})

	if rejection != nil {
		t.Fatalf("spawn refused with %v; a session holding its own history does not read the resume file", rejection.reason())
	}
}

func TestSpawnRefusesADirectoryAsAConversation(t *testing.T) {
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}

	sessionID := "attn-resume-directory"
	workspaceID, cwd := seedReloadableClaudeSession(t, d, sessionID)

	rejection := d.runSpawnPipeline(&protocol.SpawnSessionMessage{
		Cmd:                    protocol.CmdSpawnSession,
		ID:                     sessionID,
		Cwd:                    cwd,
		Agent:                  "claude",
		WorkspaceID:            workspaceID,
		Cols:                   80,
		Rows:                   24,
		ResumeConversationFile: protocol.Ptr(t.TempDir()),
	}, internalSpawnPolicy{})

	if rejection == nil {
		t.Fatal("a directory was accepted as a conversation to pick up")
	}
	if got := rejection.reason().Error(); !strings.Contains(got, "directory") {
		t.Fatalf("refusal = %q, want it to say the path is a directory", got)
	}
}
