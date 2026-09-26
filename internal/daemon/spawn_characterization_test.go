package daemon

import (
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func newSpawnCharacterizationDaemon(t *testing.T) (*Daemon, *fakeSpawnBackend, *wsClient, string) {
	t.Helper()
	return newSpawnCharacterizationDaemonOn(t, NewForTesting(filepath.Join(t.TempDir(), "test.sock")))
}

func newSpawnCharacterizationDaemonOn(t *testing.T, d *Daemon) (*Daemon, *fakeSpawnBackend, *wsClient, string) {
	t.Helper()
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend
	client := newWorkspaceProtocolTestClient()
	cwd := t.TempDir()
	return d, backend, client, cwd
}

func spawnCharacterizationMessage(id, workspaceID, cwd string) *protocol.SpawnSessionMessage {
	return &protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: id, Cwd: cwd, Agent: protocol.AgentShellValue, WorkspaceID: workspaceID, Cols: 80, Rows: 24}
}

func assertNoSpawnCharacterizationSession(t *testing.T, d *Daemon, backend *fakeSpawnBackend, id string) {
	t.Helper()
	if session := d.store.Get(id); session != nil {
		t.Fatalf("rejected spawn persisted session: %+v", session)
	}
	if got := spawnCount(backend); got != 0 {
		t.Fatalf("Spawn calls = %d, want 0", got)
	}
}

func TestSpawnCharacterizationRearmsTicketReconciliation(t *testing.T) {
	d, _, client, cwd := newSpawnCharacterizationDaemon(t)
	addTestWorkspace(d, "workspace", cwd)
	const sessionID = "ticket-rearm"
	ticket, err := d.store.CreateTicket(store.Ticket{ID: "ticket-rearm", Title: "Rearm", Assignee: sessionID, Status: store.TicketStatusWorking}, "test", time.Now())
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if claimed, err := d.store.ClaimTicketReconciliation(ticket.ID, time.Now()); err != nil || !claimed {
		t.Fatalf("seed reconciliation flag = (%v, %v), want (true, nil)", claimed, err)
	}
	d.handleSpawnSession(client, spawnCharacterizationMessage(sessionID, "workspace", cwd))
	expectSpawnResult(t, client, sessionID, true)
	if claimed, err := d.store.ClaimTicketReconciliation(ticket.ID, time.Now()); err != nil || !claimed {
		t.Fatalf("rearmed reconciliation flag = (%v, %v), want (true, nil)", claimed, err)
	}
}

func TestSpawnCharacterizationRejectsPluginChiefWithoutResumeCapability(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	plugin, done := startPluginPipe(t, d, "characterization-plugin", nil)
	defer func() { _ = plugin.Close(); <-done }()
	registerTestPluginDriver(t, plugin, "characterization", map[string]bool{"launch_instructions": true})
	addTestWorkspace(d, "workspace", cwd)
	msg := spawnCharacterizationMessage("plugin-chief-resume", "workspace", cwd)
	msg.Agent, msg.ChiefOfStaff = "characterization", protocol.Ptr(true)
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, false)
	assertNoSpawnCharacterizationSession(t, d, backend, msg.ID)
}

func TestSpawnCharacterizationPluginChiefResumeFailureMentionsCapability(t *testing.T) {
	base := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	synctest.Test(t, func(t *testing.T) {
		d, _, client, cwd := newSpawnCharacterizationDaemonOn(t, base)
		stopDaemonBackground(t, d)
		plugin, done := startPluginPipe(t, d, "characterization-plugin-error", nil)
		defer func() { _ = plugin.Close(); <-done }()
		registerTestPluginDriver(t, plugin, "characterization-error", map[string]bool{"launch_instructions": true})
		addTestWorkspace(d, "workspace", cwd)
		msg := spawnCharacterizationMessage("plugin-chief-error", "workspace", cwd)
		msg.Agent, msg.ChiefOfStaff = "characterization-error", protocol.Ptr(true)
		d.handleSpawnSession(client, msg)
		outbound := requireOutbound(t, client, "no spawn failure reached the client")
		if !strings.Contains(string(outbound.payload), "resume capability") {
			t.Fatalf("failure payload = %s, want resume capability", outbound.payload)
		}
	})
}
