package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

func TestDaemon_BroadcastRawWSMessage_RoutesRemotePTYTrafficToInterestedClients(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	clientAttached := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[protocol.TerminalID]ptybackend.Stream),
		attachedRemote:  make(map[protocol.TerminalID]struct{}),
		pendingRemote:   make(map[protocol.TerminalID]struct{}),
	}
	clientOther := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[protocol.TerminalID]ptybackend.Stream),
		attachedRemote:  make(map[protocol.TerminalID]struct{}),
		pendingRemote:   make(map[protocol.TerminalID]struct{}),
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
		attachedStreams: make(map[protocol.TerminalID]ptybackend.Stream),
		attachedRemote:  make(map[protocol.TerminalID]struct{}),
		pendingRemote:   make(map[protocol.TerminalID]struct{}),
	}
	clientOther := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[protocol.TerminalID]ptybackend.Stream),
		attachedRemote:  make(map[protocol.TerminalID]struct{}),
		pendingRemote:   make(map[protocol.TerminalID]struct{}),
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

func TestDaemon_BroadcastRawWSMessage_RemoteSessionExitedClearsRemoteAttachState(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	client := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[protocol.TerminalID]ptybackend.Stream),
		attachedRemote:  make(map[protocol.TerminalID]struct{}),
		pendingRemote:   make(map[protocol.TerminalID]struct{}),
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

func TestDaemon_LateSpawnCannotRecreateClosingSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	d.ptyBackend = &fakeSpawnBackend{}
	now := protocol.TimestampNow().String()
	d.store.Add(&protocol.Session{
		ID: "late-spawn", Label: "closing", Agent: protocol.SessionAgentCodex, ProfileID: defaultProfileID(t, d.store),
		Directory: t.TempDir(), State: protocol.SessionStateWorking,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	if _, err := d.prepareSessionTeardown("late-spawn"); err != nil {
		t.Fatalf("prepare close: %v", err)
	}
	d.commitSessionUnregister("late-spawn", store.SessionClose{By: who.User()})
	client := spawnTestClient()
	d.handleSpawnSession(client, &protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: "late-spawn", Cwd: t.TempDir(), Agent: protocol.AgentShellValue,
		ProfileID: defaultProfileID(t, d.store), Cols: 80, Rows: 24,
	})
	expectSpawnResult(t, client, "late-spawn", false)
	if d.store.Get("late-spawn") != nil || !d.store.SessionCloseIntentional("late-spawn") {
		t.Fatal("late spawn recreated the session or cleared its tombstone")
	}
}

func TestDaemon_ReconcileSessionsWithWorkerBackend_ReapUnplacesTheSession(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: "stale-session", Label: "stale", Agent: protocol.SessionAgentCodex, Directory: "/tmp/stale",
		ProfileID: defaultProfileID(t, d.store),
		State:     protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	profile, err := d.store.MostRecentlyUsedProfile()
	if err != nil {
		t.Fatal(err)
	}
	placeTestSession(t, d, "stale-session", profile.CurrentDesktopID)
	d.ptyBackend = &fakeWorkerReconcileBackend{
		liveIDs: nil,
		info:    map[string]ptybackend.SessionInfo{},
	}

	report := d.reconcileSessionsWithWorkerBackend(context.Background(), true, d.storedSessionIDs(), time.Time{})

	if report.Reaped != 1 {
		t.Fatalf("reaped = %d, want 1", report.Reaped)
	}
	desktop, err := d.store.GetDesktop(profile.CurrentDesktopID)
	if err != nil || len(desktop.Panes) != 0 {
		t.Fatalf("desktop after reap = %+v err=%v, want the pane gone and the desktop kept", desktop, err)
	}
}

func TestDaemon_PruneSessionsWithoutPTY_RemovesTheReapedPane(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := "codex-stale"
	desktopID := addStalePlacedSession(t, d, sessionID)

	if removed := d.pruneSessionsWithoutPTY(d.storedSessionIDs(), time.Time{}); removed != 1 {
		t.Fatalf("pruneSessionsWithoutPTY removed = %d, want 1", removed)
	}
	if got := d.store.Get(protocol.SessionID(sessionID)); got != nil {
		t.Fatalf("store.Get(%q) = %+v, want nil", sessionID, got)
	}
	if desktop, err := d.store.GetDesktop(desktopID); err != nil || len(desktop.Panes) != 0 {
		t.Fatalf("desktop after prune = %+v err=%v, want no panes", desktop, err)
	}
}

func addStalePlacedSession(t *testing.T, d *Daemon, sessionID string) string {
	t.Helper()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: protocol.SessionID(sessionID), Label: sessionID, Agent: protocol.SessionAgentCodex, Directory: "/tmp/stale",
		ProfileID: defaultProfileID(t, d.store),
		State:     protocol.SessionStateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	profile, err := d.store.MostRecentlyUsedProfile()
	if err != nil {
		t.Fatal(err)
	}
	placeTestSession(t, d, sessionID, profile.CurrentDesktopID)
	return profile.CurrentDesktopID
}

func TestDaemon_PruneSessionsWithoutPTY_KeepsTheTilesOfItsDesktop(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	sessionID := "codex-stale-tile"
	desktopID := addStalePlacedSession(t, d, sessionID)
	desktop, err := d.store.GetDesktop(desktopID)
	if err != nil {
		t.Fatal(err)
	}
	tileID := markdownTileIDForPath("/tmp/notes.md")
	if _, err := d.store.UpdateDesktopArrangement(desktopID, desktop.Revision, func(desktop profiles.Desktop) (profiles.Desktop, error) {
		next, ok := layouttree.DockTile(desktop.Tree, desktop.Panes[0].PaneID, layouttree.DirectionVertical, false, "split-tile", tileID, string(layouttree.TileKindMarkdown), "/tmp/notes.md", "", layouttree.DefaultSplitRatio)
		if !ok {
			t.Fatal("dock the markdown tile")
		}
		desktop.Tree = next
		return desktop, nil
	}); err != nil {
		t.Fatal(err)
	}

	if removed := d.pruneSessionsWithoutPTY(d.storedSessionIDs(), time.Time{}); removed != 1 {
		t.Fatalf("pruneSessionsWithoutPTY removed = %d, want 1", removed)
	}
	desktop, err = d.store.GetDesktop(desktopID)
	if err != nil {
		t.Fatal(err)
	}
	if tiles := layouttree.TileIDs(desktop.Tree); len(desktop.Panes) != 0 || len(tiles) != 1 || tiles[0] != tileID {
		t.Fatalf("desktop after prune = panes %+v tiles %v, want only the markdown tile", desktop.Panes, tiles)
	}
}

func TestDaemon_HandleUnregisterWS_KeepsTheOtherAgentOnItsDesktop(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	now := time.Now().UTC().Format(time.RFC3339)
	profile, err := d.store.MostRecentlyUsedProfile()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"sess-primary", "sess-next"} {
		d.store.Add(&protocol.Session{
			ID: protocol.SessionID(id), Label: id, Directory: t.TempDir(), ProfileID: profile.ID,
			State: protocol.StateWorking, StateSince: now, StateUpdatedAt: now, LastSeen: now,
		})
		placeTestSession(t, d, id, profile.CurrentDesktopID)
	}

	client := &wsClient{
		send:            make(chan outboundMessage, 8),
		attachedStreams: make(map[protocol.TerminalID]ptybackend.Stream),
	}
	d.wsHub.clients[client] = true

	d.handleUnregisterWS(client, &protocol.UnregisterMessage{ID: "sess-primary"})

	if got := d.store.Get("sess-primary"); got != nil {
		t.Fatalf("closed session still exists: %+v", got)
	}
	if got := d.store.Get("sess-next"); got == nil {
		t.Fatal("the other session was removed")
	}
	desktop, err := d.store.GetDesktop(profile.CurrentDesktopID)
	if err != nil {
		t.Fatal(err)
	}
	if len(desktop.Panes) != 1 || desktop.Panes[0].SessionID != "sess-next" || desktop.ActivePaneID != desktop.Panes[0].PaneID {
		t.Fatalf("desktop after close = %+v, want the other agent kept and active", desktop)
	}
}
