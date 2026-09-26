package daemon

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func spawnForChiefTest(t *testing.T, d *Daemon, client *wsClient, workspaceID, sessionID, agent string, chief bool) {
	t.Helper()
	cwd := t.TempDir()
	d.handleRegisterWorkspace(client, &protocol.RegisterWorkspaceMessage{
		Cmd:       protocol.CmdRegisterWorkspace,
		ID:        workspaceID,
		Title:     "Chief Test",
		Directory: cwd,
	})
	paneID := "pane-" + sessionID
	d.handleWorkspaceLayoutAddSessionPane(client, &protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd:         protocol.CmdWorkspaceLayoutAddSessionPane,
		WorkspaceID: workspaceID,
		PaneID:      protocol.Ptr(paneID),
		SessionID:   sessionID,
		Title:       protocol.Ptr(sessionID),
	})
	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutAddSessionPane, workspaceID, paneID, true)
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:          protocol.CmdSpawnSession,
		ID:           sessionID,
		Label:        protocol.Ptr(sessionID),
		Cwd:          cwd,
		Agent:        agent,
		WorkspaceID:  workspaceID,
		Cols:         80,
		Rows:         24,
		ChiefOfStaff: protocol.Ptr(chief),
	})
}

func TestCreateAsChiefRejectsPluginWithoutLaunchInstructions(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &fakeSpawnBackend{}
	plugin, done := startPluginPipe(t, d, "fixture-plugin", nil)
	defer func() {
		_ = plugin.Close()
		<-done
	}()
	registerTestPluginDriver(t, plugin, "fixture", map[string]bool{"resume": true})
	client := newWorkspaceProtocolTestClient()

	spawnForChiefTest(t, d, client, "ws-plugin", "sess-plugin", "fixture", true)
	expectSpawnResult(t, client, "sess-plugin", false)
	if got := d.chiefOfStaffSessionID(); got != "" {
		t.Fatalf("chief role holder = %q, want empty", got)
	}
}
