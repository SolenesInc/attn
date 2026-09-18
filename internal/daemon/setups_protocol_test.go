package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type setupsTestDaemon struct {
	t      *testing.T
	d      *Daemon
	dbPath string

	homeDaemonID string
}

func newSetupsTestDaemon(t *testing.T) *setupsTestDaemon {
	t.Helper()
	return newSetupsTestDaemonEnrolledAt(t, "")
}

func newSetupsTestDaemonEnrolledAt(t *testing.T, homeDaemonID string) *setupsTestDaemon {
	t.Helper()
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	world := &setupsTestDaemon{t: t, dbPath: filepath.Join(t.TempDir(), "attn.db"), homeDaemonID: homeDaemonID}
	world.start()
	return world
}

func (w *setupsTestDaemon) start() {
	w.t.Helper()
	persistent, err := store.NewWithDB(w.dbPath)
	if err != nil {
		w.t.Fatalf("open store: %v", err)
	}
	w.d = newEnrolledDaemon(w.t, w.homeDaemonID)
	w.d.clientToken = "the-token"
	w.d.store = persistent
	w.t.Cleanup(func() { persistent.Close() })
}

func (w *setupsTestDaemon) restart() {
	w.t.Helper()
	if err := w.d.store.Close(); err != nil {
		w.t.Fatalf("close store: %v", err)
	}
	w.start()
}

func (w *setupsTestDaemon) connect(rememberedSetupID string) (*wsClient, protocol.InitialStateMessage) {
	w.t.Helper()
	client := newWorkspaceProtocolTestClient()
	hello := &protocol.ClientHelloMessage{
		ClientKind:   "tauri-app",
		Version:      "test",
		Capabilities: []string{protocol.CapabilityWorkspaceSessions},
		ClientToken:  protocol.Ptr("the-token"),
	}
	if rememberedSetupID != "" {
		hello.SetupID = protocol.Ptr(rememberedSetupID)
	}
	w.d.handleClientHello(client, hello)
	var initial protocol.InitialStateMessage
	found := false
	for _, payload := range drainClientPayloads(w.t, client) {
		if eventName(w.t, payload) == protocol.EventInitialState {
			decodeInto(w.t, payload, &initial)
			found = true
		}
	}
	if !found {
		w.t.Fatal("hello was not answered with initial_state")
	}
	return client, initial
}

func (w *setupsTestDaemon) send(client *wsClient, command map[string]any) protocol.SetupActionResultMessage {
	w.t.Helper()
	if _, ok := command["request_id"]; !ok {
		command["request_id"] = "req-" + command["cmd"].(string)
	}
	data, err := json.Marshal(command)
	if err != nil {
		w.t.Fatalf("marshal command: %v", err)
	}
	w.d.handleClientMessage(client, data)
	var result protocol.SetupActionResultMessage
	var others [][]byte
	found := false
	for _, payload := range drainClientPayloads(w.t, client) {
		if !found && eventName(w.t, payload) == protocol.EventSetupActionResult {
			decodeInto(w.t, payload, &result)
			found = true
			continue
		}
		others = append(others, payload)
	}
	if !found {
		w.t.Fatalf("%s was not answered with setup_action_result", command["cmd"])
	}
	if result.RequestID != command["request_id"] {
		w.t.Fatalf("result answers request %q, want %q", result.RequestID, command["request_id"])
	}
	for _, payload := range others {
		client.send <- outboundMessage{kind: messageKindText, payload: payload}
	}
	return result
}

func (w *setupsTestDaemon) mustSend(client *wsClient, command map[string]any) protocol.SetupActionResultMessage {
	w.t.Helper()
	result := w.send(client, command)
	if !result.Success {
		w.t.Fatalf("%s failed: %s", command["cmd"], protocol.Deref(result.Error))
	}
	return result
}

func (w *setupsTestDaemon) agent(sessionID, setupID string) {
	w.t.Helper()
	w.d.store.Add(&protocol.Session{
		ID:        sessionID,
		Label:     sessionID,
		Directory: w.t.TempDir(),
		State:     protocol.SessionStateIdle,
		Agent:     protocol.SessionAgentClaude,
	})
	if err := w.d.store.AssignSessionSetup(sessionID, setupID); err != nil {
		w.t.Fatalf("assign %s to setup %s: %v", sessionID, setupID, err)
	}
}

func eventName(t *testing.T, payload []byte) string {
	t.Helper()
	var envelope struct {
		Event string `json:"event"`
	}
	decodeInto(t, payload, &envelope)
	return envelope.Event
}

func decodeInto(t *testing.T, payload []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(payload, target); err != nil {
		t.Fatalf("decode %s: %v", payload, err)
	}
}

func arrangementChanges(t *testing.T, client *wsClient) []protocol.SetupArrangementChangedMessage {
	t.Helper()
	var changes []protocol.SetupArrangementChangedMessage
	for _, payload := range drainClientPayloads(t, client) {
		if eventName(t, payload) != protocol.EventSetupArrangementChanged {
			continue
		}
		var change protocol.SetupArrangementChangedMessage
		decodeInto(t, payload, &change)
		changes = append(changes, change)
	}
	return changes
}

func setupsChanges(t *testing.T, client *wsClient) []protocol.SetupsChangedMessage {
	t.Helper()
	var changes []protocol.SetupsChangedMessage
	for _, payload := range drainClientPayloads(t, client) {
		if eventName(t, payload) != protocol.EventSetupsChanged {
			continue
		}
		var change protocol.SetupsChangedMessage
		decodeInto(t, payload, &change)
		changes = append(changes, change)
	}
	return changes
}

func wantErrorCode(t *testing.T, result protocol.SetupActionResultMessage, want protocol.SetupErrorCode) {
	t.Helper()
	if result.Success {
		t.Fatalf("%s succeeded, want error code %s", result.Action, want)
	}
	if result.ErrorCode == nil || *result.ErrorCode != want {
		t.Fatalf("%s failed with code %q (%s), want %s", result.Action, protocol.Deref(result.ErrorCode), protocol.Deref(result.Error), want)
	}
	if protocol.Deref(result.Error) == "" {
		t.Fatalf("%s failed without a message", result.Action)
	}
}

func TestFirstClientOnAnEmptyDaemonCreatesAndSelectsASetup(t *testing.T) {
	w := newSetupsTestDaemon(t)
	client, initial := w.connect("")
	if len(initial.Setups) != 0 || initial.SelectedSetupID != nil || len(initial.Desktops) != 0 {
		t.Fatalf("an empty daemon announced setups=%v selected=%v desktops=%v", initial.Setups, initial.SelectedSetupID, initial.Desktops)
	}

	created := w.mustSend(client, map[string]any{"cmd": protocol.CmdSetupCreate, "name": "attn"})
	if created.Setup == nil || len(created.Desktops) != 1 {
		t.Fatalf("setup_create returned setup=%v desktops=%v, want the setup and its first desktop", created.Setup, created.Desktops)
	}
	first := created.Desktops[0]
	if created.Setup.CurrentDesktopID != first.ID || protocol.Deref(first.ShortcutSlot) != 1 {
		t.Fatalf("first desktop %+v is not current in slot 1 of %+v", first, created.Setup)
	}
	if got := setupsChanges(t, client); len(got) != 1 || len(got[0].Setups) != 1 {
		t.Fatalf("setup_create broadcast %+v, want one setups_changed with one setup", got)
	}

	selected := w.mustSend(client, map[string]any{"cmd": protocol.CmdSetupSelect, "setup_id": created.Setup.ID})
	if len(selected.Desktops) != 1 || selected.Setup.LastUsedAt == nil {
		t.Fatalf("setup_select returned %+v, want the arrangement and a last_used_at", selected)
	}

	_, again := w.connect("")
	if protocol.Deref(again.SelectedSetupID) != created.Setup.ID || len(again.Desktops) != 1 {
		t.Fatalf("a client with no remembered setup got selected=%v desktops=%d, want the most recently used setup", again.SelectedSetupID, len(again.Desktops))
	}
}

func TestHelloScopesTheClientToItsRememberedSetup(t *testing.T) {
	w := newSetupsTestDaemon(t)
	bootstrap, _ := w.connect("")
	work := w.mustSend(bootstrap, map[string]any{"cmd": protocol.CmdSetupCreate, "name": "work"}).Setup
	home := w.mustSend(bootstrap, map[string]any{"cmd": protocol.CmdSetupCreate, "name": "home"}).Setup
	w.mustSend(bootstrap, map[string]any{"cmd": protocol.CmdSetupSelect, "setup_id": home.ID})
	before, err := w.d.store.GetSetup(work.ID)
	if err != nil {
		t.Fatal(err)
	}

	_, remembered := w.connect(work.ID)
	if protocol.Deref(remembered.SelectedSetupID) != work.ID {
		t.Fatalf("hello remembering %s was scoped to %v", work.ID, remembered.SelectedSetupID)
	}
	if len(remembered.Setups) != 2 {
		t.Fatalf("initial_state lists %d setups, want 2", len(remembered.Setups))
	}
	after, err := w.d.store.GetSetup(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.LastUsedAt != before.LastUsedAt {
		t.Fatalf("connecting changed last_used_at from %q to %q; only a selection may", before.LastUsedAt, after.LastUsedAt)
	}

	_, unknown := w.connect("setup-that-never-existed")
	if protocol.Deref(unknown.SelectedSetupID) != home.ID {
		t.Fatalf("hello remembering an unknown setup was scoped to %v, want the most recently used %s", unknown.SelectedSetupID, home.ID)
	}

	w.mustSend(bootstrap, map[string]any{
		"cmd": protocol.CmdSetupDelete, "setup_id": work.ID, "expected_revision": work.Revision, "destination_setup_id": home.ID,
	})
	_, deleted := w.connect(work.ID)
	if protocol.Deref(deleted.SelectedSetupID) != home.ID || len(deleted.Setups) != 1 {
		t.Fatalf("hello remembering a deleted setup got selected=%v setups=%d, want %s and 1", deleted.SelectedSetupID, len(deleted.Setups), home.ID)
	}
}

func TestSelectionReachesTheOtherConnectionAndSurvivesARestart(t *testing.T) {
	w := newSetupsTestDaemon(t)
	first, _ := w.connect("")
	setup := w.mustSend(first, map[string]any{"cmd": protocol.CmdSetupCreate, "name": "attn"})
	setupID, desktopOne := setup.Setup.ID, setup.Desktops[0]
	desktopTwo := w.mustSend(first, map[string]any{"cmd": protocol.CmdDesktopCreate, "setup_id": setupID}).Desktops[0]
	other := w.mustSend(first, map[string]any{"cmd": protocol.CmdSetupCreate, "name": "elsewhere"}).Setup
	w.agent("agent-a", setupID)
	w.agent("agent-b", setupID)
	placedA := w.mustSend(first, map[string]any{
		"cmd": protocol.CmdDesktopPlaceSession, "desktop_id": desktopTwo.ID, "expected_revision": desktopTwo.Revision, "session_id": "agent-a",
	})
	placedB := w.mustSend(first, map[string]any{
		"cmd": protocol.CmdDesktopPlaceSession, "desktop_id": desktopTwo.ID, "expected_revision": placedA.Desktops[0].Revision,
		"session_id": "agent-b", "anchor_pane_id": protocol.Deref(placedA.PaneID),
	})
	paneB := protocol.Deref(placedB.PaneID)
	revisionBeforeSelection := placedB.Desktops[0].Revision

	w.mustSend(first, map[string]any{"cmd": protocol.CmdSetupSelect, "setup_id": setupID})
	second, _ := w.connect(setupID)
	outsider, _ := w.connect(other.ID)
	drainClientPayloads(t, first)

	w.mustSend(first, map[string]any{"cmd": protocol.CmdDesktopSetCurrent, "setup_id": setupID, "desktop_id": desktopTwo.ID})
	w.mustSend(first, map[string]any{"cmd": protocol.CmdDesktopSetActivePane, "desktop_id": desktopTwo.ID, "pane_id": paneB})

	seen := arrangementChanges(t, second)
	if len(seen) != 2 {
		t.Fatalf("the second connection saw %d arrangement changes, want 2", len(seen))
	}
	if seen[0].Setup.CurrentDesktopID != desktopTwo.ID {
		t.Fatalf("the second connection saw current desktop %s, want %s", seen[0].Setup.CurrentDesktopID, desktopTwo.ID)
	}
	if len(seen[1].Desktops) != 1 || seen[1].Desktops[0].ActivePaneID != paneB {
		t.Fatalf("the second connection saw %+v, want desktop %s with active pane %s", seen[1].Desktops, desktopTwo.ID, paneB)
	}
	if seen[1].Desktops[0].Revision != revisionBeforeSelection {
		t.Fatalf("selecting a pane moved the desktop revision from %d to %d", revisionBeforeSelection, seen[1].Desktops[0].Revision)
	}
	if own := arrangementChanges(t, first); len(own) != 2 {
		t.Fatalf("the selecting connection saw %d arrangement changes, want 2", len(own))
	}
	if leaked := arrangementChanges(t, outsider); len(leaked) != 0 {
		t.Fatalf("a connection on another setup saw %d arrangement changes", len(leaked))
	}

	wrong := w.send(first, map[string]any{"cmd": protocol.CmdDesktopSetActivePane, "desktop_id": desktopOne.ID, "pane_id": paneB})
	wantErrorCode(t, wrong, protocol.SetupErrorCodeNotFound)

	w.restart()
	_, initial := w.connect(setupID)
	if protocol.Deref(initial.SelectedSetupID) != setupID {
		t.Fatalf("after a restart the client was scoped to %v", initial.SelectedSetupID)
	}
	var current string
	for _, s := range initial.Setups {
		if s.ID == setupID {
			current = s.CurrentDesktopID
		}
	}
	if current != desktopTwo.ID {
		t.Fatalf("after a restart the current desktop is %s, want %s", current, desktopTwo.ID)
	}
	for _, desktop := range initial.Desktops {
		if desktop.ID == desktopTwo.ID && desktop.ActivePaneID != paneB {
			t.Fatalf("after a restart the active pane is %s, want %s", desktop.ActivePaneID, paneB)
		}
	}
}

func TestSecondClientWithAStaleRevisionRereadsAndRetries(t *testing.T) {
	w := newSetupsTestDaemon(t)
	first, _ := w.connect("")
	created := w.mustSend(first, map[string]any{"cmd": protocol.CmdSetupCreate, "name": "attn"})
	desktop := created.Desktops[0]
	w.mustSend(first, map[string]any{"cmd": protocol.CmdSetupSelect, "setup_id": created.Setup.ID})
	second, initial := w.connect(created.Setup.ID)
	secondsRevision := initial.Desktops[0].Revision
	drainClientPayloads(t, first)

	w.mustSend(first, map[string]any{
		"cmd": protocol.CmdDesktopRename, "desktop_id": desktop.ID, "name": "review", "expected_revision": desktop.Revision,
	})
	stale := w.send(second, map[string]any{
		"cmd": protocol.CmdDesktopRename, "desktop_id": desktop.ID, "name": "scratch", "expected_revision": secondsRevision,
	})
	wantErrorCode(t, stale, protocol.SetupErrorCodeStaleRevision)

	seen := arrangementChanges(t, second)
	if len(seen) != 1 || seen[0].Desktops[0].Name != "review" {
		t.Fatalf("the stale client was sent %+v, want the winning rename", seen)
	}
	retried := w.mustSend(second, map[string]any{
		"cmd": protocol.CmdDesktopRename, "desktop_id": desktop.ID, "name": "scratch", "expected_revision": seen[0].Desktops[0].Revision,
	})
	if retried.Desktops[0].Name != "scratch" {
		t.Fatalf("the retry left the desktop named %q", retried.Desktops[0].Name)
	}
}

func TestMoveBetweenDesktopsArrivesAsOneMessageAndFailsWhole(t *testing.T) {
	w := newSetupsTestDaemon(t)
	client, _ := w.connect("")
	created := w.mustSend(client, map[string]any{"cmd": protocol.CmdSetupCreate, "name": "attn"})
	setupID, source := created.Setup.ID, created.Desktops[0]
	target := w.mustSend(client, map[string]any{"cmd": protocol.CmdDesktopCreate, "setup_id": setupID}).Desktops[0]
	w.mustSend(client, map[string]any{"cmd": protocol.CmdSetupSelect, "setup_id": setupID})
	watcher, _ := w.connect(setupID)
	w.agent("agent-a", setupID)
	placed := w.mustSend(client, map[string]any{
		"cmd": protocol.CmdDesktopPlaceSession, "desktop_id": source.ID, "expected_revision": source.Revision, "session_id": "agent-a",
	})
	paneID := protocol.Deref(placed.PaneID)
	source = placed.Desktops[0]
	drainClientPayloads(t, watcher)

	stale := w.send(client, map[string]any{
		"cmd": protocol.CmdDesktopMoveLeaf, "source_desktop_id": source.ID, "target_desktop_id": target.ID, "leaf_id": paneID,
		"edge": "right", "expected_source_revision": source.Revision, "expected_target_revision": target.Revision + 7,
	})
	wantErrorCode(t, stale, protocol.SetupErrorCodeStaleRevision)
	if seen := arrangementChanges(t, watcher); len(seen) != 0 {
		t.Fatalf("a refused move still sent %d arrangement changes", len(seen))
	}
	if placement, ok, err := w.d.store.SessionPlacement("agent-a"); err != nil || !ok || placement.DesktopID != source.ID {
		t.Fatalf("a refused move left agent-a at %+v (placed=%v, err=%v), want desktop %s", placement, ok, err, source.ID)
	}

	moved := w.mustSend(client, map[string]any{
		"cmd": protocol.CmdDesktopMoveLeaf, "source_desktop_id": source.ID, "target_desktop_id": target.ID, "leaf_id": paneID,
		"edge": "right", "expected_source_revision": source.Revision, "expected_target_revision": target.Revision,
	})
	if len(moved.Desktops) != 2 {
		t.Fatalf("the move result carries %d desktops, want source and target", len(moved.Desktops))
	}
	seen := arrangementChanges(t, watcher)
	if len(seen) != 1 || len(seen[0].Desktops) != 2 {
		t.Fatalf("the move arrived as %d messages, want one carrying both desktops", len(seen))
	}
	for _, desktop := range seen[0].Desktops {
		tree, err := layouttree.DecodeLayout(desktop.TreeJson)
		if desktop.ID == source.ID {
			if desktop.TreeJson != "" || len(desktop.Panes) != 0 || desktop.ActivePaneID != "" {
				t.Fatalf("the source desktop still holds %+v", desktop)
			}
			continue
		}
		if err != nil || len(desktop.Panes) != 1 || desktop.Panes[0].PaneID != paneID || desktop.ActivePaneID != paneID {
			t.Fatalf("the target desktop is %+v (tree %v, err %v), want pane %s active", desktop, tree, err, paneID)
		}
	}
}

func TestLayoutCommandsNeverChangeWhichSetupAnAgentBelongsTo(t *testing.T) {
	w := newSetupsTestDaemon(t)
	client, _ := w.connect("")
	mine := w.mustSend(client, map[string]any{"cmd": protocol.CmdSetupCreate, "name": "mine"})
	theirs := w.mustSend(client, map[string]any{"cmd": protocol.CmdSetupCreate, "name": "theirs"})
	w.agent("their-agent", theirs.Setup.ID)
	w.agent("my-agent", mine.Setup.ID)

	foreign := w.send(client, map[string]any{
		"cmd": protocol.CmdDesktopPlaceSession, "desktop_id": mine.Desktops[0].ID, "expected_revision": mine.Desktops[0].Revision, "session_id": "their-agent",
	})
	wantErrorCode(t, foreign, protocol.SetupErrorCodeCrossSetup)

	placed := w.mustSend(client, map[string]any{
		"cmd": protocol.CmdDesktopPlaceSession, "desktop_id": mine.Desktops[0].ID, "expected_revision": mine.Desktops[0].Revision, "session_id": "my-agent",
	})
	across := w.send(client, map[string]any{
		"cmd": protocol.CmdDesktopMoveLeaf, "source_desktop_id": mine.Desktops[0].ID, "target_desktop_id": theirs.Desktops[0].ID,
		"leaf_id": protocol.Deref(placed.PaneID), "edge": "left",
		"expected_source_revision": placed.Desktops[0].Revision, "expected_target_revision": theirs.Desktops[0].Revision,
	})
	wantErrorCode(t, across, protocol.SetupErrorCodeCrossSetup)

	twice := w.send(client, map[string]any{
		"cmd": protocol.CmdDesktopPlaceSession, "desktop_id": mine.Desktops[0].ID, "expected_revision": placed.Desktops[0].Revision, "session_id": "my-agent",
	})
	wantErrorCode(t, twice, protocol.SetupErrorCodeAlreadyPlaced)
	if got, err := w.d.store.SessionSetupID("my-agent"); err != nil || got != mine.Setup.ID {
		t.Fatalf("my-agent belongs to %q (err %v), want %s", got, err, mine.Setup.ID)
	}
}

func TestSetupNamesAreUniqueWhileLiveAndReusableAfterDelete(t *testing.T) {
	w := newSetupsTestDaemon(t)
	client, _ := w.connect("")
	attn := w.mustSend(client, map[string]any{"cmd": protocol.CmdSetupCreate, "name": "attn"}).Setup
	side := w.mustSend(client, map[string]any{"cmd": protocol.CmdSetupCreate, "name": "side"}).Setup

	wantErrorCode(t, w.send(client, map[string]any{"cmd": protocol.CmdSetupCreate, "name": "attn"}), protocol.SetupErrorCodeNameTaken)
	wantErrorCode(t, w.send(client, map[string]any{
		"cmd": protocol.CmdSetupRename, "setup_id": side.ID, "name": "attn", "expected_revision": side.Revision,
	}), protocol.SetupErrorCodeNameTaken)

	renamed := w.mustSend(client, map[string]any{
		"cmd": protocol.CmdSetupRename, "setup_id": attn.ID, "name": "attention", "expected_revision": attn.Revision,
	}).Setup
	if renamed.ID != attn.ID {
		t.Fatalf("renaming changed the setup id from %s to %s", attn.ID, renamed.ID)
	}
	w.mustSend(client, map[string]any{
		"cmd": protocol.CmdSetupDelete, "setup_id": renamed.ID, "expected_revision": renamed.Revision, "destination_setup_id": side.ID,
	})
	reborn := w.mustSend(client, map[string]any{"cmd": protocol.CmdSetupCreate, "name": "attention"}).Setup
	if reborn.ID == attn.ID {
		t.Fatalf("a new setup reused the deleted setup's id %s", attn.ID)
	}
	live, err := w.d.store.ListSetups(false)
	if err != nil || len(live) != 2 {
		t.Fatalf("%d live setups (err %v), want 2", len(live), err)
	}
	wantErrorCode(t, w.send(client, map[string]any{
		"cmd": protocol.CmdSetupDelete, "setup_id": reborn.ID, "expected_revision": reborn.Revision, "destination_setup_id": reborn.ID,
	}), protocol.SetupErrorCodeDestinationSame)
}

func TestSetupCommandWithoutARequestIDIsRefused(t *testing.T) {
	w := newSetupsTestDaemon(t)
	client, _ := w.connect("")
	result := w.send(client, map[string]any{"cmd": protocol.CmdSetupCreate, "name": "attn", "request_id": ""})
	wantErrorCode(t, result, protocol.SetupErrorCodeInvalid)
}

func TestOutpostRefusesSetupCommandsByName(t *testing.T) {
	w := newSetupsTestDaemonEnrolledAt(t, "d-0123456789abcdef0123456789abcdef")
	client, _ := w.connect("")
	result := w.send(client, map[string]any{"cmd": protocol.CmdSetupCreate, "name": "attn"})
	wantErrorCode(t, result, protocol.SetupErrorCodeUnavailable)
	if live, err := w.d.store.ListSetups(false); err != nil || len(live) != 0 {
		t.Fatalf("a refused create left %d setups (err %v)", len(live), err)
	}
}
