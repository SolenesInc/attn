package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const spawnProbeReadDeadline = 250 * time.Millisecond

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

func spawnCharacterizationMessage(id, profileID, cwd string) *protocol.SpawnSessionMessage {
	return &protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: id, Cwd: cwd, Agent: protocol.AgentShellValue, ProfileID: profileID, Cols: 80, Rows: 24}
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

func TestSpawnCharacterizationRejectsUnknownAgent(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	msg := spawnCharacterizationMessage("unknown-agent", defaultProfileID(t, d.store), cwd)
	msg.Agent = "no-such-agent"
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, false)
	assertNoSpawnCharacterizationSession(t, d, backend, msg.ID)
}

func TestSpawnCharacterizationRejectsShellInitialPrompt(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	msg := spawnCharacterizationMessage("shell-prompt", defaultProfileID(t, d.store), cwd)
	msg.InitialPrompt = protocol.Ptr("hello")
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, false)
	assertNoSpawnCharacterizationSession(t, d, backend, msg.ID)
}

func TestSpawnCharacterizationRejectsZeroColumns(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	msg := spawnCharacterizationMessage("zero-columns", defaultProfileID(t, d.store), cwd)
	msg.Cols = 0
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, false)
	assertNoSpawnCharacterizationSession(t, d, backend, msg.ID)
}

func TestSpawnCharacterizationRejectsOversizedDimensions(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	msg := spawnCharacterizationMessage("large-dimensions", defaultProfileID(t, d.store), cwd)
	msg.Cols, msg.Rows = maxPTYDimValue+1, maxPTYDimValue+1
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, false)
	assertNoSpawnCharacterizationSession(t, d, backend, msg.ID)
}

func TestSpawnCharacterizationRefusesAMissingOrDeletedProfileBeforeAnySideEffect(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	work := createTestProfile(t, d.store, "Work")
	deleteTestProfile(t, d.store, work.ID, defaultProfileID(t, d.store))
	for _, tt := range []struct {
		name, profileID, want string
	}{
		{"no profile", "", "missing profile_id"},
		{"unknown profile", "profile-nowhere", "profile-nowhere"},
		{"deleted profile", work.ID, "was deleted"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			msg := spawnCharacterizationMessage("refused-"+strings.ReplaceAll(tt.name, " ", "-"), tt.profileID, cwd)
			d.handleSpawnSession(client, msg)
			result := expectSpawnResult(t, client, msg.ID, false)
			if !strings.Contains(protocol.Deref(result.Error), tt.want) {
				t.Fatalf("refusal = %q, want it to name %q", protocol.Deref(result.Error), tt.want)
			}
			assertNoSpawnCharacterizationSession(t, d, backend, msg.ID)
		})
	}
}

func TestSpawnCharacterizationRefusesADesktopOfAnotherProfile(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	work := createTestProfile(t, d.store, "Work")
	msg := spawnCharacterizationMessage("cross-profile", defaultProfileID(t, d.store), cwd)
	msg.Placement = &protocol.SessionPlacement{DesktopID: protocol.Ptr(work.CurrentDesktopID)}
	d.handleSpawnSession(client, msg)
	result := expectSpawnResult(t, client, msg.ID, false)
	if !strings.Contains(protocol.Deref(result.Error), work.ID) {
		t.Fatalf("refusal = %q, want it to name profile %s", protocol.Deref(result.Error), work.ID)
	}
	assertNoSpawnCharacterizationSession(t, d, backend, msg.ID)
}

func TestSpawnCharacterizationRejectsUnattendedContractMismatch(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	msg := spawnCharacterizationMessage("contract-mismatch", defaultProfileID(t, d.store), cwd)
	msg.Agent, msg.Model, msg.Effort, msg.Executable = "claude", protocol.Ptr("wrong-model"), protocol.Ptr("high"), protocol.Ptr("/opt/claude")
	policy := internalSpawnPolicy{unattendedLaunch: launchcontract.UnattendedLaunchSpec{Agent: "claude", Model: "right-model", Effort: "high", Executable: "/opt/claude", ApprovalProductMode: launchcontract.ApprovalAuto, ApprovalDriverMode: launchcontract.ApprovalAuto, DirectoryTrust: launchcontract.TrustConfiguredDirectory, Recovery: launchcontract.RecoveryAdoptOrRestartFresh}}
	d.handleSpawnSessionWithPolicy(client, msg, policy)
	expectSpawnResult(t, client, msg.ID, false)
	assertNoSpawnCharacterizationSession(t, d, backend, msg.ID)
}

func TestSpawnCharacterizationDefaultsLabelAndRecordsRecentLocation(t *testing.T) {
	d, _, client, root := newSpawnCharacterizationDaemon(t)
	cwd := filepath.Join(root, "myproj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	msg := spawnCharacterizationMessage("label-default", defaultProfileID(t, d.store), cwd)
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, true)
	if got := d.store.Get(msg.ID).Label; got != "myproj" {
		t.Fatalf("stored label = %q, want myproj", got)
	}
	for _, location := range d.store.GetRecentLocations(50) {
		if location.Path == cwd {
			return
		}
	}
	t.Fatalf("recent locations did not include %q", cwd)
}

func TestSpawnCharacterizationStampsTheProfileAndPlacesBesideTheAnchor(t *testing.T) {
	d, _, client, cwd := newSpawnCharacterizationDaemon(t)
	profileID := defaultProfileID(t, d.store)
	first := spawnCharacterizationMessage("first", profileID, cwd)
	first.Placement = &protocol.SessionPlacement{}
	d.handleSpawnSession(client, first)
	expectSpawnResult(t, client, first.ID, true)
	firstPlacement, placed, err := d.store.SessionPlacement(first.ID)
	if err != nil || !placed {
		t.Fatalf("first placement placed=%v err=%v, want it on the current desktop", placed, err)
	}

	second := spawnCharacterizationMessage("second", profileID, cwd)
	second.Placement = &protocol.SessionPlacement{DesktopID: protocol.Ptr(firstPlacement.DesktopID), AnchorPaneID: protocol.Ptr(firstPlacement.PaneID)}
	d.handleSpawnSession(client, second)
	expectSpawnResult(t, client, second.ID, true)
	if got := d.store.Get(second.ID).ProfileID; got != profileID {
		t.Fatalf("stored profile = %q, want %q", got, profileID)
	}
	desktop, err := d.store.GetDesktop(firstPlacement.DesktopID)
	if err != nil {
		t.Fatal(err)
	}
	if got := layouttree.PaneIDs(desktop.Tree); len(got) != 2 || desktop.ActivePaneID == firstPlacement.PaneID {
		t.Fatalf("desktop panes = %v active=%s, want the second agent split beside the first and focused", got, desktop.ActivePaneID)
	}

	unplaced := spawnCharacterizationMessage("unplaced", profileID, cwd)
	d.handleSpawnSession(client, unplaced)
	expectSpawnResult(t, client, unplaced.ID, true)
	if _, placed, _ := d.store.SessionPlacement(unplaced.ID); placed {
		t.Fatal("a spawn without a placement was placed")
	}
}

func TestSpawnCharacterizationPersistsCodexResumeID(t *testing.T) {
	d, _, client, cwd := newSpawnCharacterizationDaemon(t)
	msg := spawnCharacterizationMessage("codex-resume", defaultProfileID(t, d.store), cwd)
	msg.Agent, msg.ResumeSessionID = "codex", protocol.Ptr("native-codex-resume")
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, true)
	if got := d.store.GetResumeSessionID(msg.ID); got != "native-codex-resume" {
		t.Fatalf("persisted resume id = %q, want native-codex-resume", got)
	}
}

func TestSpawnCharacterizationConsumesQueuedConversationObservation(t *testing.T) {
	d, _, client, cwd := newSpawnCharacterizationDaemon(t)
	const sessionID = "queued-resume"
	transcriptPath := filepath.Join(t.TempDir(), "rollout-queued-native-id.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(`{"type":"session_meta","payload":{"id":"queued-native-id"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d.observeOrQueueAgentConversation(agentConversationObservation{
		SessionID:      sessionID,
		NativeID:       "queued-native-id",
		TranscriptPath: transcriptPath,
	})
	d.handleSpawnSession(client, spawnCharacterizationMessage(sessionID, defaultProfileID(t, d.store), cwd))
	expectSpawnResult(t, client, sessionID, true)
	if got := d.store.GetResumeSessionID(sessionID); got != "queued-native-id" {
		t.Fatalf("persisted queued resume id = %q, want queued-native-id", got)
	}
	if got := d.store.GetSessionTranscriptPath(sessionID); got != transcriptPath {
		t.Fatalf("persisted queued transcript = %q, want %q", got, transcriptPath)
	}
	if got, ok := d.consumePendingAgentConversation(sessionID); ok {
		t.Fatalf("pending conversation = %+v, want consumed", got)
	}
}

func TestSpawnCharacterizationBroadcastsRegistrationThenStateChange(t *testing.T) {
	d, _, client, cwd := newSpawnCharacterizationDaemon(t)
	var events []string
	d.wsHub.broadcastListener = func(event *protocol.WebSocketEvent) { events = append(events, event.Event) }
	msg := spawnCharacterizationMessage("broadcast-choice", defaultProfileID(t, d.store), cwd)
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, true)
	d.ptyBackend = &fakeSpawnBackend{}
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, true)
	var sessionEvents []string
	for _, event := range events {
		if event == protocol.EventSessionRegistered || event == protocol.EventSessionStateChanged {
			sessionEvents = append(sessionEvents, event)
		}
	}
	if len(sessionEvents) != 2 || sessionEvents[0] != protocol.EventSessionRegistered || sessionEvents[1] != protocol.EventSessionStateChanged {
		t.Fatalf("session events = %v, want registered then state_changed", sessionEvents)
	}
}

func TestSpawnCharacterizationRearmsTicketReconciliation(t *testing.T) {
	d, _, client, cwd := newSpawnCharacterizationDaemon(t)
	const sessionID = "ticket-rearm"
	ticket, err := d.store.CreateTicket(store.Ticket{ID: "ticket-rearm", Title: "Rearm", Assignee: sessionID, Status: store.TicketStatusWorking}, "test", time.Now())
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if claimed, err := d.store.ClaimTicketReconciliation(ticket.ID, time.Now()); err != nil || !claimed {
		t.Fatalf("seed reconciliation flag = (%v, %v), want (true, nil)", claimed, err)
	}
	d.handleSpawnSession(client, spawnCharacterizationMessage(sessionID, defaultProfileID(t, d.store), cwd))
	expectSpawnResult(t, client, sessionID, true)
	if claimed, err := d.store.ClaimTicketReconciliation(ticket.ID, time.Now()); err != nil || !claimed {
		t.Fatalf("rearmed reconciliation flag = (%v, %v), want (true, nil)", claimed, err)
	}
}

func TestSpawnCharacterizationChiefSettingsFillModelAndEffort(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	d.store.SetSetting(SettingChiefModelPrefix+"claude", "chief-model")
	d.store.SetSetting(SettingChiefEffortPrefix+"claude", "chief-effort")
	msg := spawnCharacterizationMessage("chief-fallback", defaultProfileID(t, d.store), cwd)
	msg.Agent, msg.ChiefOfStaff = "claude", protocol.Ptr(true)
	d.handleSpawnSession(client, msg)
	expectSpawnResult(t, client, msg.ID, true)
	spawn, ok := backend.LastSpawn()
	if !ok {
		t.Fatal("expected PTY spawn")
	}
	if spawn.Model != "chief-model" || spawn.Effort != "chief-effort" {
		t.Fatalf("chief spawn pins = (%q, %q), want configured fallback", spawn.Model, spawn.Effort)
	}
}

func TestSpawnCharacterizationRejectsPluginChiefWithoutResumeCapability(t *testing.T) {
	d, backend, client, cwd := newSpawnCharacterizationDaemon(t)
	plugin, done := startPluginPipe(t, d, "characterization-plugin", nil)
	defer func() { _ = plugin.Close(); <-done }()
	registerTestPluginDriver(t, plugin, "characterization", map[string]bool{"launch_instructions": true})
	msg := spawnCharacterizationMessage("plugin-chief-resume", defaultProfileID(t, d.store), cwd)
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
		msg := spawnCharacterizationMessage("plugin-chief-error", defaultProfileID(t, d.store), cwd)
		msg.Agent, msg.ChiefOfStaff = "characterization-error", protocol.Ptr(true)
		d.handleSpawnSession(client, msg)
		outbound := requireOutbound(t, client, "no spawn failure reached the client")
		if !strings.Contains(string(outbound.payload), "resume capability") {
			t.Fatalf("failure payload = %s, want resume capability", outbound.payload)
		}
	})
}

func TestSpawnCharacterizationAlreadyLivePluginRespawnSkipsPluginPrep(t *testing.T) {
	base := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	synctest.Test(t, func(t *testing.T) {
		d, backend, client, cwd := newSpawnCharacterizationDaemonOn(t, base)
		stopDaemonBackground(t, d)
		plugin, pluginDone := startPluginPipe(t, d, "characterization-live-plugin", nil)
		defer func() { _ = plugin.Close(); <-pluginDone }()
		registerTestPluginDriver(t, plugin, "characterization-live", nil)
		addTestWorkspace(d, "workspace", cwd)

		driverSpawns := make(chan bool, 1)
		go func() {
			request := decodeJSONRPCMessage(t, plugin)
			if request.Method != "driver.spawn" {
				driverSpawns <- false
				return
			}
			respondPluginRequest(t, plugin, request, pluginDriverSpawnResult{Argv: []string{"characterization-live"}})

			_ = plugin.SetReadDeadline(time.Now().Add(spawnProbeReadDeadline))
			var secondRequest jsonRPCMessage
			if err := json.NewDecoder(plugin).Decode(&secondRequest); err != nil {
				driverSpawns <- false
				return
			}
			driverSpawns <- secondRequest.Method == "driver.spawn"
			if secondRequest.Method == "driver.spawn" {
				respondPluginRequest(t, plugin, secondRequest, pluginDriverSpawnResult{Argv: []string{"characterization-live"}})
			}
		}()

		msg := spawnCharacterizationMessage("already-live-plugin", defaultProfileID(t, d.store), cwd)
		msg.Agent = "characterization-live"
		d.handleSpawnSession(client, msg)
		expectSpawnResult(t, client, msg.ID, true)

		backend.mu.Lock()
		backend.sessionIDs = []string{msg.ID}
		backend.mu.Unlock()

		secondSpawnDone := make(chan struct{})
		go func() {
			d.handleSpawnSession(client, msg)
			close(secondSpawnDone)
		}()
		requireDone(t, secondSpawnDone, "the already-live spawn never returned")
		expectSpawnResult(t, client, msg.ID, true)

		time.Sleep(spawnProbeReadDeadline)
		synctest.Wait()
		select {
		case gotSecondDriverSpawn := <-driverSpawns:
			if gotSecondDriverSpawn {
				t.Fatal("already-live respawn sent a second driver.spawn request")
			}
		default:
			t.Fatal("the plugin spawn probe never reported")
		}
		if got := spawnCount(backend); got != 1 {
			t.Fatalf("Spawn calls = %d, want 1", got)
		}
	})
}
