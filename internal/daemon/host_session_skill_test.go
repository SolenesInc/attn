package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/toolhome"
)

func TestConversationHostSpawnInstallsTheAttnSkill(t *testing.T) {
	toolHome := t.TempDir()
	t.Setenv(toolhome.EnvVar, toolHome)

	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	backend := &fakeSpawnBackend{}
	workspaceID, _, cwd := setupDelegationSource(t, d, backend)
	pipe, done := startPluginPipe(t, d, "pi-fixture-plugin", nil)
	defer func() {
		_ = pipe.Close()
		<-done
	}()
	registerTestPluginDriver(t, pipe, "pi-fixture", map[string]bool{
		pluginDriverConversationCapability: true,
	})
	// The daemon's agent-availability pass installs this too when codex is on the machine, which would let the assertion below pass without the spawn doing anything.
	skillDir := filepath.Join(toolHome, ".agents", "skills", "attn")
	if err := os.RemoveAll(skillDir); err != nil {
		t.Fatalf("clear the skill dir: %v", err)
	}
	argv, dumpPath := envDumpingHostCommand(t)
	serveOneDriverSpawn(t, pipe, argv)

	const sessionID = "conv-skill"
	client := newWorkspaceProtocolTestClient()
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:         protocol.CmdSpawnSession,
		ID:          sessionID,
		Cwd:         cwd,
		WorkspaceID: workspaceID,
		Agent:       "pi-fixture",
		Cols:        80,
		Rows:        24,
	})
	expectSpawnResult(t, client, sessionID, true)
	t.Cleanup(func() { _ = d.ensureHostSessions().Kill(sessionID) })
	readHostEnv(t, dumpPath)

	skill := filepath.Join(skillDir, "SKILL.md")
	if _, err := os.Stat(skill); err != nil {
		t.Fatalf("spawning a conversation host left no attn skill at %s: %v", skill, err)
	}
}
