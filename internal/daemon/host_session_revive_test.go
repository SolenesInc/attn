package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/hostsession"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func registerConversationDriver(t *testing.T, d *Daemon, agent string) {
	t.Helper()
	plugin := &pluginConnection{name: agent + "-plugin"}
	if err := d.ensurePluginRegistry().register(plugin); err != nil {
		t.Fatalf("register plugin: %v", err)
	}
	if err := d.ensurePluginRegistry().registerDriver(plugin, pluginDriverRegisterParams{
		Agent:        agent,
		Capabilities: map[string]bool{pluginDriverConversationCapability: true, "state_reporting": true},
	}); err != nil {
		t.Fatalf("register conversation driver: %v", err)
	}
}

func TestHostExitMakesTheSessionRecoverable(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addHostSession(t, d, "conv-r1")
	declare(d, "conv-r1", 1, "run_started", protocol.StateWorking)

	d.handleHostExit(hostsession.ExitInfo{SessionID: "conv-r1", ExitCode: -1, Signal: "SIGKILL", LifecycleID: "run-conv-r1"})

	if got := stateOf(t, d, "conv-r1"); got != string(protocol.SessionStateRecoverable) {
		t.Fatalf("state after the host was killed = %q, want recoverable", got)
	}
}

func TestHostExitDuringAReloadIsSuppressed(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addHostSession(t, d, "conv-r2")
	declare(d, "conv-r2", 1, "run_started", protocol.StateWorking)

	d.markReloading("conv-r2")
	d.handleHostExit(hostsession.ExitInfo{SessionID: "conv-r2", ExitCode: 0, LifecycleID: "run-conv-r2"})

	if got := stateOf(t, d, "conv-r2"); got != protocol.StateWorking {
		t.Fatalf("state = %q, want working: the reload's own kill says nothing about the session", got)
	}
}

func TestHostExitForAClosedSessionMovesNothing(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))

	d.handleHostExit(hostsession.ExitInfo{SessionID: "never-existed", ExitCode: 0})

	if d.store.Get("never-existed") != nil {
		t.Fatal("a host exit created a session row")
	}
}

func TestConversationSessionSurvivesADaemonRestart(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addHostSession(t, d, "conv-r3")
	d.store.SetLaunchIntent("conv-r3", store.LaunchIntent{ApprovalRoute: launchcontract.ApprovalRouteUser})
	writeHostConversationFile(t, "conv-r3")
	d.store.EndAgentDriverRun("conv-r3")

	session := d.store.Get("conv-r3")
	if !d.canReviveSession(session) {
		t.Fatal("a conversation session with no live host was going to be dropped, not recovered")
	}
}

func TestNonConversationPluginSessionIsStillReaped(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	plugin := &pluginConnection{name: "snipe-plugin"}
	if err := d.ensurePluginRegistry().register(plugin); err != nil {
		t.Fatalf("register plugin: %v", err)
	}
	if err := d.ensurePluginRegistry().registerDriver(plugin, pluginDriverRegisterParams{
		Agent:        "snipe",
		Capabilities: map[string]bool{},
	}); err != nil {
		t.Fatalf("register driver: %v", err)
	}
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: "plain-1", Agent: "snipe", Label: "plain-1", Directory: t.TempDir(),
		State: protocol.StateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	d.store.SetLaunchIntent("plain-1", store.LaunchIntent{ApprovalRoute: launchcontract.ApprovalRouteUser})

	if d.canReviveSession(d.store.Get("plain-1")) {
		t.Fatal("an agent with nothing to resume was marked recoverable")
	}
}

func writeHostConversationFile(t *testing.T, sessionID string) {
	t.Helper()
	dir := hostSessionStateDir(sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir host state dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write host session file: %v", err)
	}
}

func TestConversationReloadWithoutALaunchIntentKeepsTheHost(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	registerConversationDriver(t, d, "nisse")
	addHostSession(t, d, "conv-r4")
	manager := spawnStubHost(t, d, "conv-r4")

	err := d.reloadSessionForClient("conv-r4", 0, 0)

	if err == nil || !strings.Contains(err.Error(), "launch intent") {
		t.Fatalf("reload error = %v, want one naming the missing launch intent", err)
	}
	if !manager.Has("conv-r4") {
		t.Fatal("the live host was killed for a reload that could not respawn it")
	}
}

func TestConversationReloadThatCannotRespawnParksTheSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	registerConversationDriver(t, d, "nisse")
	addHostSession(t, d, "conv-r5")
	declare(d, "conv-r5", 1, "session_ready", protocol.StateIdle)
	manager := spawnStubHost(t, d, "conv-r5")
	d.store.SetLaunchIntent("conv-r5", store.LaunchIntent{ApprovalRoute: launchcontract.ApprovalRouteUser})

	if err := d.reloadSessionForClient("conv-r5", 0, 0); err == nil {
		t.Fatal("reload reported success with no plugin able to answer the spawn")
	}
	if manager.Has("conv-r5") {
		t.Fatal("the old host outlived the reload that killed it")
	}
	if got := stateOf(t, d, "conv-r5"); got != string(protocol.SessionStateRecoverable) {
		t.Fatalf("state = %q, want recoverable: the reload killed the host and could not replace it", got)
	}
}

func TestAgentAttachReachesTheHost(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	addHostSession(t, d, "conv-r6")

	received := make(chan string, 4)
	manager := hostsession.New(d.logf, func(event hostsession.Event) {
		if verb, ok := event.Body["verb"].(string); ok {
			received <- verb
		}
	}, func(hostsession.ExitInfo) {})
	d.hostSessions = manager
	if err := manager.Spawn(hostsession.SpawnOptions{SessionID: "conv-r6", Command: []string{echoHostScript(t, "conv-r6")}}); err != nil {
		t.Fatalf("spawn echo host: %v", err)
	}
	t.Cleanup(func() { _ = manager.Kill("conv-r6") })

	client := &wsClient{send: make(chan outboundMessage, 10)}
	d.handleAgentAttach(client, &protocol.AgentAttachMessage{Cmd: protocol.CmdAgentAttach, ID: "conv-r6"})

	select {
	case verb := <-received:
		if !strings.Contains(verb, `"verb":"snapshot"`) {
			t.Fatalf("host received %q, want the snapshot verb", verb)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the snapshot verb never reached the host")
	}
}

func TestAgentAttachWithoutAHostIsAnError(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := &wsClient{send: make(chan outboundMessage, 10)}

	d.handleAgentAttach(client, &protocol.AgentAttachMessage{Cmd: protocol.CmdAgentAttach, ID: "gone"})

	select {
	case msg := <-client.send:
		if !strings.Contains(string(msg.payload), "no live conversation host") {
			t.Fatalf("client got %q, want an error naming the missing host", string(msg.payload))
		}
	default:
		t.Fatal("no command error for an attach against a session with no host")
	}
}

func echoHostScript(t *testing.T, sessionID string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "echo-host.sh")
	script := "#!/bin/sh\nwhile IFS= read -r line; do\n" +
		"  escaped=$(printf '%s' \"$line\" | sed 's/\"/\\\\\"/g')\n" +
		"  printf '{\"session_id\":\"" + sessionID + "\",\"seq\":1,\"kind\":\"message_end\",\"body\":{\"verb\":\"%s\"}}\\n' \"$escaped\" >&3\n" +
		"done\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write echo host: %v", err)
	}
	return path
}

func spawnStubHost(t *testing.T, d *Daemon, sessionID string) *hostsession.Manager {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stub-host.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nwhile IFS= read -r line; do :; done\n"), 0o755); err != nil {
		t.Fatalf("write stub host: %v", err)
	}
	manager := hostsession.New(d.logf, d.handleHostEvent, d.handleHostExit)
	d.hostSessions = manager
	if err := manager.Spawn(hostsession.SpawnOptions{SessionID: sessionID, Command: []string{path}}); err != nil {
		t.Fatalf("spawn stub host: %v", err)
	}
	t.Cleanup(func() { _ = manager.Kill(sessionID) })
	return manager
}
