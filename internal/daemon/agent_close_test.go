package daemon

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/hub"
	"github.com/victorarias/attn/internal/protocol"
)

func newAgentCloseDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := newEnrolledDaemon(t, "")
	t.Cleanup(func() {
		d.stopEventBus()
		d.sessionInputs().stopRetries()
	})
	d.ensureGardenCollections()
	writeCrewHomes(t, d.dataRoot)
	d.ensureCrewCollections()
	d.importCrewHomes()
	d.ptyBackend = &fakeSpawnBackend{}
	return d
}

func addAgentCloseSession(t *testing.T, d *Daemon, id, label string) {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: id, Label: label, Agent: protocol.SessionAgentClaude,
		Directory: "/tmp/" + id, WorkspaceID: "ws-" + id,
		State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
}

func callAgentClose(t *testing.T, d *Daemon, target, source, reason string) protocol.Response {
	t.Helper()
	return callHandler(t, func(conn net.Conn) {
		d.handleAgentClose(conn, &protocol.AgentCloseMessage{
			Cmd:             protocol.CmdAgentClose,
			TargetSessionID: target,
			SourceSessionID: source,
			Reason:          reason,
		})
	})
}

func closedEntry(t *testing.T, d *Daemon, sessionID string) protocol.SessionLedgerEntry {
	t.Helper()
	entry := d.store.SessionLedgerEntry(sessionID)
	if entry == nil {
		t.Fatalf("SessionLedgerEntry(%s) = nil, want a closed row", sessionID)
	}
	if protocol.Deref(entry.ClosedAt) == "" {
		t.Fatalf("%s is still live; want closed_at set", sessionID)
	}
	return *entry
}

func agentCloseFailure(resp protocol.Response) string {
	return fmt.Sprintf("%s: %s", protocol.Deref(resp.ErrorCode), protocol.Deref(resp.Error))
}

func refusal(t *testing.T, resp protocol.Response) (string, string) {
	t.Helper()
	if resp.Ok {
		t.Fatalf("response = %+v, want a refusal", resp)
	}
	return protocol.Deref(resp.ErrorCode), protocol.Deref(resp.Error)
}

func startAgentCloseOutpost(t *testing.T, d *Daemon, sessions ...protocol.Session) *Daemon {
	t.Helper()
	port, err := freeTCPPort()
	if err != nil {
		t.Fatalf("freeTCPPort: %v", err)
	}
	t.Setenv("ATTN_WS_PORT", strconv.Itoa(port))

	outpost := NewForTesting(filepath.Join(shortTempDir(t), "test.sock"))
	outpostID, err := enrollment.EnsureDaemonID(outpost.dataRoot)
	if err != nil {
		t.Fatalf("EnsureDaemonID: %v", err)
	}
	outpost.daemonInstanceID = outpostID
	if err := outpost.ensureEnrollment(); err != nil {
		t.Fatalf("enroll the outpost: %v", err)
	}
	outpost.ptyBackend = &fakeSpawnBackend{}
	go outpost.Start()
	t.Cleanup(func() {
		outpost.Stop()
		outpost.stopEventBus()
		outpost.sessionInputs().stopRetries()
	})
	waitForSocket(t, outpost.socketPath, 10*time.Second)

	outpostClient := client.New(outpost.socketPath)
	for _, session := range sessions {
		if err := outpostClient.Register(session.ID, session.Label, session.Directory); err != nil {
			t.Fatalf("register %s on the outpost: %v", session.ID, err)
		}
	}

	d.hubManager = hub.NewManager(d.store, nil, nil, nil, nil, nil)
	endpoint, err := d.hubManager.AddEndpoint("gpu-box", "gpu", "")
	if err != nil {
		t.Fatalf("AddEndpoint: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, fmt.Sprintf("ws://127.0.0.1:%d/ws", port), nil)
	if err != nil {
		t.Fatalf("dial the outpost: %v", err)
	}
	go func() {
		_ = d.hubManager.AttachEndpointConnection(ctx, endpoint.ID, conn, outpost.clientToken)
	}()
	waitFor(t, "the hub to mirror the sessions the outpost owns", func() bool {
		for _, session := range sessions {
			if d.hubManager.RemoteSession(session.ID) == nil {
				return false
			}
		}
		return true
	})
	waitFor(t, "the outpost to finish recovering", func() bool { return !outpost.isRecovering() })
	return outpost
}

func remoteAgentCloseSession(id, label string) protocol.Session {
	return protocol.Session{ID: id, Label: label, Directory: "/srv/" + id}
}

func TestAgentCloseReachesADelegateAnEndpointOwns(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "orchestrator", "Orchestrator")
	startAgentCloseOutpost(t, d, remoteAgentCloseSession("remote-delegate", "Remote delegate"))
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Bench the kernel", Body: protocol.Ptr("run the sweep on the gpu box")})
	move(t, d, "remote-delegate", seed.ID, garden.VerbTend, "", "")
	if err := d.recordGardenDispatch("remote-delegate", seed.ID, "orchestrator", "/srv/remote-delegate", "claude", false); err != nil {
		t.Fatalf("recordGardenDispatch: %v", err)
	}

	resp := callAgentClose(t, d, "remote-delegate", "orchestrator", "the sweep finished and its numbers are on the seed")

	if !resp.Ok || resp.AgentCloseResult == nil {
		t.Fatalf("close refused with %s, want the hub to close the session its outpost owns", agentCloseFailure(resp))
	}
	if rule := resp.AgentCloseResult.Rule; rule != protocol.AgentCloseRuleDispatcher {
		t.Errorf("rule = %q, want dispatcher", rule)
	}
	if got := resp.AgentCloseResult.SeedIds; len(got) != 1 || got[0] != seed.ID {
		t.Errorf("seed_ids = %v, want the seed the remote delegate tended", got)
	}
}

func TestAgentCloseLetsTheChiefCloseASessionOnAnotherEndpoint(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "chief", "Chief")
	startAgentCloseOutpost(t, d, remoteAgentCloseSession("remote-worker", "Remote worker"))
	if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}

	resp := callAgentClose(t, d, "remote-worker", "chief", "it went quiet three hours ago")

	if !resp.Ok || resp.AgentCloseResult == nil {
		t.Fatalf("close refused with %s, want the chief's authority to cross the endpoint", agentCloseFailure(resp))
	}
	if rule := resp.AgentCloseResult.Rule; rule != protocol.AgentCloseRuleChiefOfStaff {
		t.Errorf("rule = %q, want chief_of_staff", rule)
	}
}

func TestAgentCloseWritesItsReceiptInTheOwningDaemonsLedger(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "orchestrator", "Orchestrator")
	outpost := startAgentCloseOutpost(t, d, remoteAgentCloseSession("remote-delegate", "Remote delegate"))
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Bench the kernel", Body: protocol.Ptr("sweep on the gpu box")})
	if err := d.recordGardenDispatch("remote-delegate", seed.ID, "orchestrator", "/srv/remote-delegate", "claude", false); err != nil {
		t.Fatalf("recordGardenDispatch: %v", err)
	}

	const reason = "the sweep finished and its numbers are on the seed"
	resp := callAgentClose(t, d, "remote-delegate", "orchestrator", reason)

	if !resp.Ok || resp.AgentCloseResult == nil {
		t.Fatalf("close refused with %s, want the close to cross to the outpost", agentCloseFailure(resp))
	}
	entry := closedEntry(t, outpost, "remote-delegate")
	if by := protocol.Deref(entry.ClosedBy); by != "orchestrator" {
		t.Errorf("remote closed_by = %q, want the dispatcher that authorized it", by)
	}
	if got := protocol.Deref(entry.CloseReason); got != reason {
		t.Errorf("remote close_reason = %q, want %q", got, reason)
	}
}

func TestAgentCloseRepeatsWhyTheOwningDaemonRefused(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "chief", "Chief")
	outpost := startAgentCloseOutpost(t, d, remoteAgentCloseSession("remote-worker", "Remote worker"))
	if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}
	outpost.setRecovering(true)

	code, message := refusal(t, callAgentClose(t, d, "remote-worker", "chief", "it went quiet three hours ago"))

	if code != "close_failed" {
		t.Fatalf("code = %q, want close_failed", code)
	}
	if !strings.Contains(message, "daemon_recovering") {
		t.Errorf("refusal %q does not repeat what the owning daemon said", message)
	}
	if strings.Contains(message, "did not confirm") {
		t.Errorf("refusal %q waited out the tripwire instead of reading the answer", message)
	}
	if d.hubManager.RemoteSession("remote-worker") == nil {
		t.Error("the hub dropped a session the refusal left running")
	}
}

func TestAgentCloseRefusesWhenTheOwningEndpointCannotTakeIt(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "chief", "Chief")
	if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}
	d.hubManager = hub.NewManager(d.store, nil, nil, nil, nil, nil)
	endpoint, err := d.hubManager.AddEndpoint("gpu-box", "gpu", "")
	if err != nil {
		t.Fatalf("AddEndpoint: %v", err)
	}
	if !d.hubManager.ReplaceRemoteSessions(endpoint.ID, []protocol.Session{
		remoteAgentCloseSession("remote-worker", "Remote worker"),
	}) {
		t.Fatal("the endpoint mirrored nothing")
	}

	code, message := refusal(t, callAgentClose(t, d, "remote-worker", "chief", "it went quiet three hours ago"))

	if code != "close_failed" {
		t.Fatalf("code = %q, want close_failed", code)
	}
	if !strings.Contains(message, "still running") {
		t.Errorf("refusal %q does not say the session survived", message)
	}
	if d.hubManager.RemoteSession("remote-worker") == nil {
		t.Error("the hub dropped a session it never managed to close")
	}
}

func TestAgentCloseRefusesAPrefixTwoEndpointsBothAnswer(t *testing.T) {
	d := newAgentCloseDaemon(t)
	addAgentCloseSession(t, d, "shared-local", "Local")
	startAgentCloseOutpost(t, d, remoteAgentCloseSession("shared-remote", "Remote"))

	code, message := refusal(t, callAgentClose(t, d, "shared-", "shared-local", "either one, apparently"))

	if code != "ambiguous_session" {
		t.Fatalf("code = %q, want ambiguous_session", code)
	}
	if !strings.Contains(message, "more than one session") {
		t.Errorf("refusal %q does not say the prefix matched twice", message)
	}
}
