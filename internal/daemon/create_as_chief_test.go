package daemon

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func spawnForChiefTest(t *testing.T, d *Daemon, client *wsClient, sessionID, agent string, chief bool) {
	t.Helper()
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd:          protocol.CmdSpawnSession,
		ID:           sessionID,
		Label:        protocol.Ptr(sessionID),
		Cwd:          t.TempDir(),
		Agent:        agent,
		ProfileID:    defaultProfileID(t, d.store),
		Placement:    &protocol.SessionPlacement{},
		Cols:         80,
		Rows:         24,
		ChiefOfStaff: protocol.Ptr(chief),
	})
}

func TestCreateAsChiefAssignsRoleAtLaunch(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &fakeSpawnBackend{}
	client := newProtocolTestClient()

	spawnForChiefTest(t, d, client, "sess-chief", string(protocol.SessionAgentClaude), true)
	expectSpawnResult(t, client, "sess-chief", true)

	if got := d.chiefForCaller(""); got != "sess-chief" {
		t.Fatalf("chief role holder = %q, want sess-chief", got)
	}
	session := d.store.Get("sess-chief")
	if session == nil {
		t.Fatal("session was not registered")
	}
	decorated := d.sessionForBroadcast(session)
	if decorated.ChiefOfStaff == nil || !*decorated.ChiefOfStaff {
		t.Fatalf("broadcast session ChiefOfStaff = %v, want true", decorated.ChiefOfStaff)
	}
}

func TestCreateAsChiefSkippedWhenChiefExists(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &fakeSpawnBackend{}
	client := newProtocolTestClient()

	if err := setTestChief(d, "incumbent"); err != nil {
		t.Fatalf("seed incumbent chief: %v", err)
	}

	spawnForChiefTest(t, d, client, "sess-second", string(protocol.SessionAgentClaude), true)
	expectSpawnResult(t, client, "sess-second", true)

	if got := d.chiefForCaller(""); got != "incumbent" {
		t.Fatalf("chief role holder = %q, want incumbent (unchanged)", got)
	}
	if d.isChiefOfStaffSession("sess-second") {
		t.Fatal("second session must not have taken the chief role")
	}
}

func TestCreateAsChiefIgnoredForShell(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &fakeSpawnBackend{}
	client := newProtocolTestClient()

	spawnForChiefTest(t, d, client, "sess-shell", protocol.AgentShellValue, true)
	expectSpawnResult(t, client, "sess-shell", true)

	if got := d.chiefForCaller(""); got != "" {
		t.Fatalf("chief role holder = %q, want empty (shell cannot be chief)", got)
	}
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
	client := newProtocolTestClient()

	spawnForChiefTest(t, d, client, "sess-plugin", "fixture", true)
	expectSpawnResult(t, client, "sess-plugin", false)
	if got := d.chiefForCaller(""); got != "" {
		t.Fatalf("chief role holder = %q, want empty", got)
	}
}

func TestCreateAsChiefRolledBackOnSpawnFailure(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &failingSpawnBackend{err: errors.New("boom")}
	client := newProtocolTestClient()

	spawnForChiefTest(t, d, client, "sess-fail", string(protocol.SessionAgentClaude), true)
	expectSpawnResult(t, client, "sess-fail", false)

	if got := d.chiefForCaller(""); got != "" {
		t.Fatalf("chief role holder = %q, want empty (assignment rolled back on spawn failure)", got)
	}
}

func TestMaybeAssignChiefOnSpawnSkipsRespawn(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })

	existing := &protocol.Session{ID: "sess-respawn"}
	if assigned := d.maybeAssignChiefOnSpawn("sess-respawn", string(protocol.SessionAgentClaude), defaultProfileID(t, d.store), true, existing); assigned {
		t.Fatal("respawn (existingSession != nil) must not assign the chief role")
	}
	if got := d.chiefForCaller(""); got != "" {
		t.Fatalf("chief role holder = %q, want empty", got)
	}
}
