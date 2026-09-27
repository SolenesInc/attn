package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/workspacelayout"
)

type fakeDeferredRecoveryBackend struct {
	fakeWorkerReconcileBackend
	mu          sync.Mutex
	reports     []ptybackend.RecoveryReport
	likelyAlive map[string]bool
	likelyErr   map[string]error
}

func (b *fakeDeferredRecoveryBackend) Recover(context.Context) (ptybackend.RecoveryReport, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.reports) == 0 {
		return ptybackend.RecoveryReport{}, nil
	}
	report := b.reports[0]
	b.reports = b.reports[1:]
	return report, nil
}

func (b *fakeDeferredRecoveryBackend) SessionLikelyAlive(_ context.Context, sessionID string) (bool, error) {
	if err, ok := b.likelyErr[sessionID]; ok {
		return false, err
	}
	return b.likelyAlive[sessionID], nil
}

func TestDaemon_ReconcileSessionsWithWorkerBackend_PreservesLivePluginReportedState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "plugin-live",
		Label:          "plugin-live",
		Agent:          "snipe",
		Directory:      "/tmp/plugin-live",
		State:          protocol.SessionStateLaunching,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	if !d.store.BeginAgentDriverRun("plugin-live", "snipe-plugin", "run-live") {
		t.Fatal("BeginAgentDriverRun(plugin-live) failed")
	}
	if !d.store.ApplyAgentDriverState("plugin-live", "run-live", 1, protocol.StateWaitingInput, time.Time{}) {
		t.Fatal("ApplyAgentDriverState(plugin-live) failed")
	}
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: []string{"plugin-live"},
		info: map[string]ptybackend.SessionInfo{
			"plugin-live": {
				SessionID: "plugin-live",
				Agent:     "snipe",
				CWD:       "/tmp/plugin-live",
				Running:   true,
				State:     protocol.StateWorking,
			},
		},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, d.storedSessionIDs(), time.Time{})
	if report.StateUpdated != 0 {
		t.Fatalf("state_updated = %d, want 0 for plugin-owned state", report.StateUpdated)
	}
	session := d.store.Get("plugin-live")
	if session == nil || session.State != protocol.SessionStateWaitingInput {
		t.Fatalf("plugin-live session = %+v, want waiting_input retained from plugin report", session)
	}
}

func TestDaemon_RunDeferredWorkerReconciliationForcesIdleDemotion(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "stale-running",
		Label:          "stale-running",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/stale-running",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.ptyBackend = &fakeDeferredRecoveryBackend{
		fakeWorkerReconcileBackend: fakeWorkerReconcileBackend{
			liveIDs: nil,
			info:    map[string]ptybackend.SessionInfo{},
		},
		reports: []ptybackend.RecoveryReport{
			{Missing: 1},
		},
	}

	d.runDeferredWorkerReconciliation(1, 0, d.storedSessionIDs(), time.Time{})

	session := d.store.Get("stale-running")
	if session != nil {
		t.Fatal("stale-running session should be reaped: no worker and nothing to resume")
	}

	warnings := d.getWarnings()
	hasPartial := false
	for _, w := range warnings {
		if w.Code == "worker_recovery_partial" && strings.Contains(w.Message, "Forced stale-session reconciliation") {
			hasPartial = true
			break
		}
	}
	if !hasPartial {
		t.Fatalf("expected forced reconciliation warning, got %+v", warnings)
	}
}

func TestDaemon_RunDeferredWorkerReconciliation_BroadcastsSessionsUpdatedOnChange(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "stale-running",
		Label:          "stale-running",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/stale-running",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.ptyBackend = &fakeDeferredRecoveryBackend{
		fakeWorkerReconcileBackend: fakeWorkerReconcileBackend{
			liveIDs: nil,
			info:    map[string]ptybackend.SessionInfo{},
		},
		reports: []ptybackend.RecoveryReport{
			{Missing: 1},
		},
	}

	broadcasts := 0
	d.wsHub.broadcastListener = func(event *protocol.WebSocketEvent) {
		if event != nil && event.Event == protocol.EventSessionsUpdated {
			broadcasts++
		}
	}

	d.runDeferredWorkerReconciliation(1, 0, d.storedSessionIDs(), time.Time{})

	if broadcasts == 0 {
		t.Fatal("expected deferred reconciliation to broadcast sessions_updated after state changes")
	}
}

func TestDaemon_ReconcileSessionsWithWorkerBackend_PreservesLikelyAliveSessions(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "stale-running",
		Label:          "stale-running",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/stale-running",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.ptyBackend = &fakeDeferredRecoveryBackend{
		fakeWorkerReconcileBackend: fakeWorkerReconcileBackend{
			liveIDs: nil,
			info:    map[string]ptybackend.SessionInfo{},
		},
		likelyAlive: map[string]bool{
			"stale-running": true,
		},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, d.storedSessionIDs(), time.Time{})
	if report.MarkedIdle != 0 {
		t.Fatalf("marked_idle = %d, want 0", report.MarkedIdle)
	}
	if report.LikelyAlive != 1 {
		t.Fatalf("likely_alive = %d, want 1", report.LikelyAlive)
	}
	session := d.store.Get("stale-running")
	if session == nil {
		t.Fatal("stale-running session missing")
	}
	if session.State != protocol.SessionStateWorking {
		t.Fatalf("state = %q, want %q", session.State, protocol.SessionStateWorking)
	}
}

func TestDaemon_ReconcileSessionsWithWorkerBackend_SkipsIdleDemotionOnLivenessProbeError(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "stale-running",
		Label:          "stale-running",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/stale-running",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})
	d.ptyBackend = &fakeDeferredRecoveryBackend{
		fakeWorkerReconcileBackend: fakeWorkerReconcileBackend{
			liveIDs: nil,
			info:    map[string]ptybackend.SessionInfo{},
		},
		likelyErr: map[string]error{
			"stale-running": errors.New("probe timeout"),
		},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, d.storedSessionIDs(), time.Time{})
	if report.MarkedIdle != 0 {
		t.Fatalf("marked_idle = %d, want 0", report.MarkedIdle)
	}
	if report.LivenessUnknown != 1 {
		t.Fatalf("liveness_unknown = %d, want 1", report.LivenessUnknown)
	}
	session := d.store.Get("stale-running")
	if session == nil {
		t.Fatal("stale-running session missing")
	}
	if session.State != protocol.SessionStateWorking {
		t.Fatalf("state = %q, want %q", session.State, protocol.SessionStateWorking)
	}
}

func TestDaemon_ReconcileSessionsWithWorkerBackend_SkipsIdleDemotionOnIncompleteRecovery(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "missing-running",
		Label:          "missing",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/missing",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: nil,
		info:    map[string]ptybackend.SessionInfo{},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), false, d.storedSessionIDs(), time.Time{})
	if report.MarkedIdle != 0 {
		t.Fatalf("marked_idle = %d, want 0", report.MarkedIdle)
	}
	if report.SkippedIdle != 1 {
		t.Fatalf("skipped_idle = %d, want 1", report.SkippedIdle)
	}
	session := d.store.Get("missing-running")
	if session == nil {
		t.Fatal("missing-running session missing after reconcile")
	}
	if session.State != protocol.SessionStateWorking {
		t.Fatalf("missing-running state = %s, want working", session.State)
	}
}

func TestDaemon_BroadcastRawWSMessage_RoutesRemotePTYTrafficToInterestedClients(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	clientAttached := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
		attachedRemote:  make(map[string]struct{}),
		pendingRemote:   make(map[string]struct{}),
	}
	clientOther := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
		attachedRemote:  make(map[string]struct{}),
		pendingRemote:   make(map[string]struct{}),
	}
	d.wsHub.clients[clientAttached] = true
	d.wsHub.clients[clientOther] = true

	clientAttached.notePendingRemoteAttach("remote-runtime-1")

	attachPayload, err := json.Marshal(protocol.AttachResultMessage{
		Event:   protocol.EventAttachResult,
		ID:      "remote-runtime-1",
		Success: true,
	})
	if err != nil {
		t.Fatalf("marshal attach_result: %v", err)
	}
	d.broadcastRawWSMessage(attachPayload)

	attachEvent := readOutboundEvent(t, clientAttached)
	if asString(attachEvent["event"]) != protocol.EventAttachResult || asString(attachEvent["id"]) != "remote-runtime-1" {
		t.Fatalf("unexpected attach event: %+v", attachEvent)
	}
	assertNoOutboundEvent(t, clientOther)
	if !clientAttached.hasRemoteAttach("remote-runtime-1") {
		t.Fatal("client should track remote runtime after attach_result success")
	}

	outputPayload, err := json.Marshal(protocol.WebSocketEvent{
		Event: protocol.EventPtyOutput,
		ID:    protocol.Ptr("remote-runtime-1"),
		Data:  protocol.Ptr(base64.StdEncoding.EncodeToString([]byte("hello"))),
		Seq:   protocol.Ptr(7),
	})
	if err != nil {
		t.Fatalf("marshal pty_output: %v", err)
	}
	d.broadcastRawWSMessage(outputPayload)

	outputEvent := readOutboundEvent(t, clientAttached)
	if asString(outputEvent["event"]) != protocol.EventPtyOutput || asString(outputEvent["id"]) != "remote-runtime-1" {
		t.Fatalf("unexpected pty_output event: %+v", outputEvent)
	}
	assertNoOutboundEvent(t, clientOther)

	resizePayload, err := json.Marshal(protocol.WebSocketEvent{
		Event: protocol.EventPtyResized,
		ID:    protocol.Ptr("remote-runtime-1"),
		Cols:  protocol.Ptr(100),
		Rows:  protocol.Ptr(30),
	})
	if err != nil {
		t.Fatalf("marshal pty_resized: %v", err)
	}
	d.broadcastRawWSMessage(resizePayload)

	resizeEvent := readOutboundEvent(t, clientAttached)
	if asString(resizeEvent["event"]) != protocol.EventPtyResized || asString(resizeEvent["id"]) != "remote-runtime-1" {
		t.Fatalf("unexpected pty_resized event: %+v", resizeEvent)
	}
	assertNoOutboundEvent(t, clientOther)
}

func TestDaemon_BroadcastRawWSMessage_RoutesPendingRemotePTYOutputBeforeAttachResult(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	clientPending := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
		attachedRemote:  make(map[string]struct{}),
		pendingRemote:   make(map[string]struct{}),
	}
	clientOther := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
		attachedRemote:  make(map[string]struct{}),
		pendingRemote:   make(map[string]struct{}),
	}
	d.wsHub.clients[clientPending] = true
	d.wsHub.clients[clientOther] = true

	clientPending.notePendingRemoteAttach("remote-runtime-1")

	outputPayload, err := json.Marshal(protocol.WebSocketEvent{
		Event: protocol.EventPtyOutput,
		ID:    protocol.Ptr("remote-runtime-1"),
		Data:  protocol.Ptr(base64.StdEncoding.EncodeToString([]byte("hello"))),
		Seq:   protocol.Ptr(7),
	})
	if err != nil {
		t.Fatalf("marshal pty_output: %v", err)
	}
	d.broadcastRawWSMessage(outputPayload)

	outputEvent := readOutboundEvent(t, clientPending)
	if asString(outputEvent["event"]) != protocol.EventPtyOutput || asString(outputEvent["id"]) != "remote-runtime-1" {
		t.Fatalf("unexpected pending-attach pty_output event: %+v", outputEvent)
	}
	assertNoOutboundEvent(t, clientOther)
	if clientPending.hasRemoteAttach("remote-runtime-1") {
		t.Fatal("pending attach should not mark remote runtime attached before attach_result")
	}
}

func TestDaemon_BroadcastRawWSMessage_RoutesRemoteTileContentToSubscribedClients(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	clientSubscribed := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	clientOther := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	d.wsHub.clients[clientSubscribed] = true
	d.wsHub.clients[clientOther] = true
	clientSubscribed.notePendingTileContent("remote-workspace", "tile-markdown")

	payload, err := json.Marshal(protocol.WorkspaceTileContentMessage{
		Event:       protocol.EventWorkspaceTileContent,
		WorkspaceID: "remote-workspace",
		TileID:      "tile-markdown",
		TileKind:    string(workspacelayout.TileKindMarkdown),
		Path:        "/srv/repo/README.md",
		Content:     "# Private",
	})
	if err != nil {
		t.Fatalf("marshal workspace_tile_content: %v", err)
	}
	d.broadcastRawWSMessage(payload)

	event := readOutboundEvent(t, clientSubscribed)
	if asString(event["event"]) != protocol.EventWorkspaceTileContent || asString(event["content"]) != "# Private" {
		t.Fatalf("unexpected tile content event: %+v", event)
	}
	assertNoOutboundEvent(t, clientOther)
	if !clientSubscribed.wantsTileContent("remote-workspace", "tile-markdown") {
		t.Fatal("successful relayed tile response should promote the pending request to a subscription")
	}
}

func TestDaemon_BroadcastRawWSMessage_PrunesRemoteTileSubscriptionsAfterLayoutUpdate(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	d.wsHub.clients[client] = true
	client.subscribeTileContent("remote-workspace", "tile-markdown")

	layoutJSON, err := workspacelayout.EncodeLayout(workspacelayout.DefaultLayout("pane-1"))
	if err != nil {
		t.Fatalf("encode layout: %v", err)
	}
	payload, err := json.Marshal(protocol.WorkspaceLayoutUpdatedMessage{
		Event: protocol.EventWorkspaceLayoutUpdated,
		WorkspaceLayout: protocol.WorkspaceLayout{
			WorkspaceID:  "remote-workspace",
			ActivePaneID: "pane-1",
			LayoutJson:   layoutJSON,
		},
	})
	if err != nil {
		t.Fatalf("marshal workspace_layout_updated: %v", err)
	}
	d.broadcastRawWSMessage(payload)

	if client.wantsTileContent("remote-workspace", "tile-markdown") {
		t.Fatal("removed remote tile subscription survived layout update")
	}
}

func TestDaemon_BroadcastRawWSMessage_RemoteSessionExitedClearsRemoteAttachState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[string]ptybackend.Stream),
		attachedRemote:  make(map[string]struct{}),
		pendingRemote:   make(map[string]struct{}),
	}
	d.wsHub.clients[client] = true
	client.attachedRemote["remote-runtime-1"] = struct{}{}

	exitPayload, err := json.Marshal(protocol.WebSocketEvent{
		Event: protocol.EventSessionExited,
		ID:    protocol.Ptr("remote-runtime-1"),
	})
	if err != nil {
		t.Fatalf("marshal session_exited: %v", err)
	}
	d.broadcastRawWSMessage(exitPayload)

	exitEvent := readOutboundEvent(t, client)
	if asString(exitEvent["event"]) != protocol.EventSessionExited || asString(exitEvent["id"]) != "remote-runtime-1" {
		t.Fatalf("unexpected session_exited event: %+v", exitEvent)
	}
	if client.hasRemoteAttach("remote-runtime-1") {
		t.Fatal("session_exited should clear remote attach state")
	}

	outputPayload, err := json.Marshal(protocol.WebSocketEvent{
		Event: protocol.EventPtyOutput,
		ID:    protocol.Ptr("remote-runtime-1"),
		Data:  protocol.Ptr(base64.StdEncoding.EncodeToString([]byte("late"))),
		Seq:   protocol.Ptr(8),
	})
	if err != nil {
		t.Fatalf("marshal late pty_output: %v", err)
	}
	d.broadcastRawWSMessage(outputPayload)
	assertNoOutboundEvent(t, client)
}

func TestDaemon_RecoveryBarrier_BlocksPTYCommands(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.setRecovering(true)

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.handleClientMessage(client, []byte(`{"cmd":"attach_session","id":"sess-1"}`))

	msg := <-client.send
	var event protocol.WebSocketEvent
	if err := json.Unmarshal(msg.payload, &event); err != nil {
		t.Fatalf("decode command_error: %v", err)
	}
	if event.Event != protocol.EventCommandError {
		t.Fatalf("event = %q, want %q", event.Event, protocol.EventCommandError)
	}
	if protocol.Deref(event.Cmd) != protocol.CmdAttachSession {
		t.Fatalf("cmd = %q, want %q", protocol.Deref(event.Cmd), protocol.CmdAttachSession)
	}
	if protocol.Deref(event.Error) != "daemon_recovering" {
		t.Fatalf("error = %q, want %q", protocol.Deref(event.Error), "daemon_recovering")
	}
}

func TestDaemon_RecoveryBarrier_BlocksClearSessions(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.setRecovering(true)

	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID:             "sess-1",
		Label:          "sess-1",
		Agent:          protocol.SessionAgentCodex,
		Directory:      "/tmp/sess-1",
		State:          protocol.SessionStateWorking,
		StateSince:     now,
		StateUpdatedAt: now,
		LastSeen:       now,
	})

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}
	client.setIdentity("daemon-test", "protocol-"+protocol.ProtocolVersion, []string{protocol.CapabilityWorkspaceSessions})

	d.handleClientMessage(client, []byte(`{"cmd":"clear_sessions"}`))

	msg := <-client.send
	var event protocol.WebSocketEvent
	if err := json.Unmarshal(msg.payload, &event); err != nil {
		t.Fatalf("decode command_error: %v", err)
	}
	if event.Event != protocol.EventCommandError {
		t.Fatalf("event = %q, want %q", event.Event, protocol.EventCommandError)
	}
	if protocol.Deref(event.Cmd) != protocol.CmdClearSessions {
		t.Fatalf("cmd = %q, want %q", protocol.Deref(event.Cmd), protocol.CmdClearSessions)
	}
	if protocol.Deref(event.Error) != "daemon_recovering" {
		t.Fatalf("error = %q, want %q", protocol.Deref(event.Error), "daemon_recovering")
	}
	if got := len(d.store.List("")); got != 1 {
		t.Fatalf("store sessions = %d, want 1 (clear should be blocked during recovery)", got)
	}
}

func TestDaemon_RecoveryBarrier_DefersInitialState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.daemonInstanceID = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	d.setRecovering(true)

	client := &wsClient{
		send:            make(chan outboundMessage, 2),
		attachedStreams: make(map[string]ptybackend.Stream),
	}

	d.scheduleInitialState(client)
	select {
	case <-client.send:
		t.Fatal("initial_state was sent while daemon was recovering")
	default:
	}

	d.setRecovering(false)
	select {
	case msg := <-client.send:
		var initial protocol.InitialStateMessage
		if err := json.Unmarshal(msg.payload, &initial); err != nil {
			t.Fatalf("decode deferred initial_state: %v", err)
		}
		if initial.Event != protocol.EventInitialState {
			t.Fatalf("event = %q, want %q", initial.Event, protocol.EventInitialState)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for deferred initial_state")
	}
}

func TestDaemon_HealthDoesNotReportReadyBeforeStartupCompletes(t *testing.T) {
	d := NewForTesting(filepath.Join(shortTempDir(t), "test.sock"))
	request := httptest.NewRequest(http.MethodGet, "/health", nil)

	readStatus := func() string {
		recorder := httptest.NewRecorder()
		d.handleHealth(recorder, request)
		var health struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(recorder.Result().Body).Decode(&health); err != nil {
			t.Fatalf("decode health: %v", err)
		}
		return health.Status
	}

	if status := readStatus(); status != "starting" {
		t.Fatalf("health status before startup = %q, want starting", status)
	}
	d.signalStarted()
	if status := readStatus(); status != "ok" {
		t.Fatalf("health status after startup = %q, want ok", status)
	}
}

func readOutboundEvent(t *testing.T, client *wsClient) map[string]interface{} {
	t.Helper()
	select {
	case outbound := <-client.send:
		var event map[string]interface{}
		if err := json.Unmarshal(outbound.payload, &event); err != nil {
			t.Fatalf("decode outbound event: %v", err)
		}
		return event
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for outbound event")
		return nil
	}
}

func assertNoOutboundEvent(t *testing.T, client *wsClient) {
	t.Helper()
	select {
	case outbound := <-client.send:
		t.Fatalf("unexpected outbound event: %s", string(outbound.payload))
	default:
	}
}

func TestHandleStop_SkipsClassificationForForcedStopSession(t *testing.T) {
	d := newBubbleDaemon(t)
	synctest.Test(t, func(t *testing.T) {
		stopDaemonBackground(t, d)
		mockClassifier := &countingClassifier{state: protocol.StateWaitingInput}
		d.classifier = mockClassifier

		now := time.Now()
		nowStr := string(protocol.NewTimestamp(now))
		d.store.Add(&protocol.Session{
			ID:             "sess-forced-stop",
			Agent:          protocol.SessionAgentCodex,
			Label:          "forced-stop",
			Directory:      "/tmp",
			State:          protocol.StateIdle,
			StateSince:     nowStr,
			StateUpdatedAt: nowStr,
			LastSeen:       nowStr,
		})
		d.markForcedStopClassification("sess-forced-stop")

		serverConn, clientConn := net.Pipe()
		defer clientConn.Close()

		done := make(chan struct{})
		go func() {
			defer close(done)
			d.handleStop(serverConn, &protocol.StopMessage{
				ID:             "sess-forced-stop",
				TranscriptPath: "",
			})
			_ = serverConn.Close()
		}()

		var resp protocol.Response
		if err := json.NewDecoder(clientConn).Decode(&resp); err != nil {
			t.Fatalf("decode stop response: %v", err)
		}
		if !resp.Ok {
			t.Fatalf("stop response ok=%v, want true", resp.Ok)
		}

		requireDone(t, done, "handleStop did not return")

		settleStopClassification(t)
		if got := mockClassifier.CallCount(); got != 0 {
			t.Fatalf("classifier calls=%d, want 0", got)
		}
		if d.consumeForcedStopClassification("sess-forced-stop") {
			t.Fatal("forced-stop suppression token should be consumed by handleStop")
		}
	})
}

type countingClassifier struct {
	state string
	mu    sync.Mutex
	calls int
}

func (c *countingClassifier) Classify(text string, timeout time.Duration) (string, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.state, nil
}

func (c *countingClassifier) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}
