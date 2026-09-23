package daemon

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type profilesTestDaemon struct {
	t      *testing.T
	d      *Daemon
	dbPath string

	homeDaemonID string
}

func newProfilesTestDaemon(t *testing.T) *profilesTestDaemon {
	t.Helper()
	return newProfilesTestDaemonEnrolledAt(t, "")
}

func newProfilesTestDaemonEnrolledAt(t *testing.T, homeDaemonID string) *profilesTestDaemon {
	t.Helper()
	t.Setenv("ATTN_DATA_DIR", t.TempDir())
	world := &profilesTestDaemon{t: t, dbPath: filepath.Join(t.TempDir(), "attn.db"), homeDaemonID: homeDaemonID}
	world.start()
	return world
}

func (w *profilesTestDaemon) start() {
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

func (w *profilesTestDaemon) restart() {
	w.t.Helper()
	if err := w.d.store.Close(); err != nil {
		w.t.Fatalf("close store: %v", err)
	}
	w.start()
}

func (w *profilesTestDaemon) connect(rememberedProfileID string) (*wsClient, protocol.InitialStateMessage) {
	w.t.Helper()
	client := newWorkspaceProtocolTestClient()
	hello := &protocol.ClientHelloMessage{
		ClientKind:   "tauri-app",
		Version:      "test",
		Capabilities: []string{protocol.CapabilityWorkspaceSessions},
		ClientToken:  protocol.Ptr("the-token"),
	}
	if rememberedProfileID != "" {
		hello.ProfileID = protocol.Ptr(rememberedProfileID)
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

func (w *profilesTestDaemon) send(client *wsClient, command map[string]any) protocol.ProfileActionResultMessage {
	w.t.Helper()
	if _, ok := command["request_id"]; !ok {
		command["request_id"] = "req-" + command["cmd"].(string)
	}
	data, err := json.Marshal(command)
	if err != nil {
		w.t.Fatalf("marshal command: %v", err)
	}
	w.d.handleClientMessage(client, data)
	var result protocol.ProfileActionResultMessage
	var others [][]byte
	found := false
	for _, payload := range drainClientPayloads(w.t, client) {
		if !found && eventName(w.t, payload) == protocol.EventProfileActionResult {
			decodeInto(w.t, payload, &result)
			found = true
			continue
		}
		others = append(others, payload)
	}
	if !found {
		w.t.Fatalf("%s was not answered with profile_action_result", command["cmd"])
	}
	if result.RequestID != command["request_id"] {
		w.t.Fatalf("result answers request %q, want %q", result.RequestID, command["request_id"])
	}
	for _, payload := range others {
		client.send <- outboundMessage{kind: messageKindText, payload: payload}
	}
	return result
}

func (w *profilesTestDaemon) mustSend(client *wsClient, command map[string]any) protocol.ProfileActionResultMessage {
	w.t.Helper()
	result := w.send(client, command)
	if !result.Success {
		w.t.Fatalf("%s failed: %s", command["cmd"], protocol.Deref(result.Error))
	}
	return result
}

func (w *profilesTestDaemon) agent(sessionID, profileID string) {
	w.t.Helper()
	w.d.store.Add(&protocol.Session{
		ID:        sessionID,
		Label:     sessionID,
		Directory: w.t.TempDir(),
		State:     protocol.SessionStateIdle,
		Agent:     protocol.SessionAgentClaude,
	})
	if err := w.d.store.AssignSessionProfile(sessionID, profileID); err != nil {
		w.t.Fatalf("assign %s to profile %s: %v", sessionID, profileID, err)
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

func arrangementChanges(t *testing.T, client *wsClient) []protocol.ProfileArrangementChangedMessage {
	t.Helper()
	var changes []protocol.ProfileArrangementChangedMessage
	for _, payload := range drainClientPayloads(t, client) {
		if eventName(t, payload) != protocol.EventProfileArrangementChanged {
			continue
		}
		var change protocol.ProfileArrangementChangedMessage
		decodeInto(t, payload, &change)
		changes = append(changes, change)
	}
	return changes
}

func profilesChanges(t *testing.T, client *wsClient) []protocol.ProfilesChangedMessage {
	t.Helper()
	var changes []protocol.ProfilesChangedMessage
	for _, payload := range drainClientPayloads(t, client) {
		if eventName(t, payload) != protocol.EventProfilesChanged {
			continue
		}
		var change protocol.ProfilesChangedMessage
		decodeInto(t, payload, &change)
		changes = append(changes, change)
	}
	return changes
}

func wantErrorCode(t *testing.T, result protocol.ProfileActionResultMessage, want protocol.ProfileErrorCode) {
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

func TestFirstClientOnAnEmptyDaemonCreatesAndSelectsAProfile(t *testing.T) {
	w := newProfilesTestDaemon(t)
	client, initial := w.connect("")
	if len(initial.Profiles) != 0 || initial.SelectedProfileID != nil || len(initial.Desktops) != 0 {
		t.Fatalf("an empty daemon announced profiles=%v selected=%v desktops=%v", initial.Profiles, initial.SelectedProfileID, initial.Desktops)
	}

	created := w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "attn"})
	if created.Profile == nil || len(created.Desktops) != 1 {
		t.Fatalf("profile_create returned profile=%v desktops=%v, want the profile and its first desktop", created.Profile, created.Desktops)
	}
	first := created.Desktops[0]
	if created.Profile.CurrentDesktopID != first.ID || protocol.Deref(first.ShortcutSlot) != 1 {
		t.Fatalf("first desktop %+v is not current in slot 1 of %+v", first, created.Profile)
	}
	if got := profilesChanges(t, client); len(got) != 1 || len(got[0].Profiles) != 1 {
		t.Fatalf("profile_create broadcast %+v, want one profiles_changed with one profile", got)
	}

	selected := w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileSelect, "profile_id": created.Profile.ID})
	if len(selected.Desktops) != 1 || selected.Profile.LastUsedAt == nil {
		t.Fatalf("profile_select returned %+v, want the arrangement and a last_used_at", selected)
	}

	_, again := w.connect("")
	if protocol.Deref(again.SelectedProfileID) != created.Profile.ID || len(again.Desktops) != 1 {
		t.Fatalf("a client with no remembered profile got selected=%v desktops=%d, want the most recently used profile", again.SelectedProfileID, len(again.Desktops))
	}
}

func TestHelloScopesTheClientToItsRememberedProfile(t *testing.T) {
	w := newProfilesTestDaemon(t)
	bootstrap, _ := w.connect("")
	work := w.mustSend(bootstrap, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "work"}).Profile
	home := w.mustSend(bootstrap, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "home"}).Profile
	w.mustSend(bootstrap, map[string]any{"cmd": protocol.CmdProfileSelect, "profile_id": home.ID})
	before, err := w.d.store.GetProfile(work.ID)
	if err != nil {
		t.Fatal(err)
	}

	_, remembered := w.connect(work.ID)
	if protocol.Deref(remembered.SelectedProfileID) != work.ID {
		t.Fatalf("hello remembering %s was scoped to %v", work.ID, remembered.SelectedProfileID)
	}
	if len(remembered.Profiles) != 2 {
		t.Fatalf("initial_state lists %d profiles, want 2", len(remembered.Profiles))
	}
	after, err := w.d.store.GetProfile(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.LastUsedAt != before.LastUsedAt {
		t.Fatalf("connecting changed last_used_at from %q to %q; only a selection may", before.LastUsedAt, after.LastUsedAt)
	}

	_, unknown := w.connect("profile-that-never-existed")
	if protocol.Deref(unknown.SelectedProfileID) != home.ID {
		t.Fatalf("hello remembering an unknown profile was scoped to %v, want the most recently used %s", unknown.SelectedProfileID, home.ID)
	}

	w.mustSend(bootstrap, map[string]any{
		"cmd": protocol.CmdProfileDelete, "profile_id": work.ID, "expected_revision": work.Revision, "destination_profile_id": home.ID,
	})
	_, deleted := w.connect(work.ID)
	if protocol.Deref(deleted.SelectedProfileID) != home.ID || len(deleted.Profiles) != 1 {
		t.Fatalf("hello remembering a deleted profile got selected=%v profiles=%d, want %s and 1", deleted.SelectedProfileID, len(deleted.Profiles), home.ID)
	}
}

func TestSelectionReachesTheOtherConnectionAndSurvivesARestart(t *testing.T) {
	w := newProfilesTestDaemon(t)
	first, _ := w.connect("")
	profile := w.mustSend(first, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "attn"})
	profileID, desktopOne := profile.Profile.ID, profile.Desktops[0]
	desktopTwo := w.mustSend(first, map[string]any{"cmd": protocol.CmdDesktopCreate, "profile_id": profileID}).Desktops[0]
	other := w.mustSend(first, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "elsewhere"}).Profile
	w.agent("agent-a", profileID)
	w.agent("agent-b", profileID)
	placedA := w.mustSend(first, map[string]any{
		"cmd": protocol.CmdDesktopPlaceSession, "desktop_id": desktopTwo.ID, "expected_revision": desktopTwo.Revision, "session_id": "agent-a",
	})
	placedB := w.mustSend(first, map[string]any{
		"cmd": protocol.CmdDesktopPlaceSession, "desktop_id": desktopTwo.ID, "expected_revision": placedA.Desktops[0].Revision,
		"session_id": "agent-b", "anchor_pane_id": protocol.Deref(placedA.PaneID),
	})
	paneB := protocol.Deref(placedB.PaneID)
	revisionBeforeSelection := placedB.Desktops[0].Revision

	w.mustSend(first, map[string]any{"cmd": protocol.CmdProfileSelect, "profile_id": profileID})
	second, _ := w.connect(profileID)
	outsider, _ := w.connect(other.ID)
	drainClientPayloads(t, first)

	w.mustSend(first, map[string]any{"cmd": protocol.CmdDesktopSetCurrent, "profile_id": profileID, "desktop_id": desktopTwo.ID})
	w.mustSend(first, map[string]any{"cmd": protocol.CmdDesktopSetActivePane, "desktop_id": desktopTwo.ID, "pane_id": paneB})

	seen := arrangementChanges(t, second)
	if len(seen) != 2 {
		t.Fatalf("the second connection saw %d arrangement changes, want 2", len(seen))
	}
	if seen[0].Profile.CurrentDesktopID != desktopTwo.ID {
		t.Fatalf("the second connection saw current desktop %s, want %s", seen[0].Profile.CurrentDesktopID, desktopTwo.ID)
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
		t.Fatalf("a connection on another profile saw %d arrangement changes", len(leaked))
	}

	wrong := w.send(first, map[string]any{"cmd": protocol.CmdDesktopSetActivePane, "desktop_id": desktopOne.ID, "pane_id": paneB})
	wantErrorCode(t, wrong, protocol.ProfileErrorCodeNotFound)

	w.restart()
	_, initial := w.connect(profileID)
	if protocol.Deref(initial.SelectedProfileID) != profileID {
		t.Fatalf("after a restart the client was scoped to %v", initial.SelectedProfileID)
	}
	var current string
	for _, s := range initial.Profiles {
		if s.ID == profileID {
			current = s.CurrentDesktopID
		}
	}
	if current != desktopTwo.ID {
		t.Fatalf("after a restart the current desktop is %s, want %s", current, desktopTwo.ID)
	}
	restored := ""
	for _, desktop := range initial.Desktops {
		if desktop.ID == desktopTwo.ID {
			restored = desktop.ActivePaneID
		}
	}
	if restored != paneB {
		t.Fatalf("after a restart desktop %s has active pane %q among %d desktops, want %s", desktopTwo.ID, restored, len(initial.Desktops), paneB)
	}
}

func TestSecondClientWithAStaleRevisionRereadsAndRetries(t *testing.T) {
	w := newProfilesTestDaemon(t)
	first, _ := w.connect("")
	created := w.mustSend(first, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "attn"})
	desktop := created.Desktops[0]
	w.mustSend(first, map[string]any{"cmd": protocol.CmdProfileSelect, "profile_id": created.Profile.ID})
	second, initial := w.connect(created.Profile.ID)
	secondsRevision := initial.Desktops[0].Revision
	drainClientPayloads(t, first)

	w.mustSend(first, map[string]any{
		"cmd": protocol.CmdDesktopRename, "desktop_id": desktop.ID, "name": "review", "expected_revision": desktop.Revision,
	})
	stale := w.send(second, map[string]any{
		"cmd": protocol.CmdDesktopRename, "desktop_id": desktop.ID, "name": "scratch", "expected_revision": secondsRevision,
	})
	wantErrorCode(t, stale, protocol.ProfileErrorCodeStaleRevision)

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
	w := newProfilesTestDaemon(t)
	client, _ := w.connect("")
	created := w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "attn"})
	profileID, source := created.Profile.ID, created.Desktops[0]
	target := w.mustSend(client, map[string]any{"cmd": protocol.CmdDesktopCreate, "profile_id": profileID}).Desktops[0]
	w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileSelect, "profile_id": profileID})
	watcher, _ := w.connect(profileID)
	w.agent("agent-a", profileID)
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
	wantErrorCode(t, stale, protocol.ProfileErrorCodeStaleRevision)
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

func TestLayoutCommandsNeverChangeWhichProfileAnAgentBelongsTo(t *testing.T) {
	w := newProfilesTestDaemon(t)
	client, _ := w.connect("")
	mine := w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "mine"})
	theirs := w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "theirs"})
	w.agent("their-agent", theirs.Profile.ID)
	w.agent("my-agent", mine.Profile.ID)

	foreign := w.send(client, map[string]any{
		"cmd": protocol.CmdDesktopPlaceSession, "desktop_id": mine.Desktops[0].ID, "expected_revision": mine.Desktops[0].Revision, "session_id": "their-agent",
	})
	wantErrorCode(t, foreign, protocol.ProfileErrorCodeCrossProfile)

	placed := w.mustSend(client, map[string]any{
		"cmd": protocol.CmdDesktopPlaceSession, "desktop_id": mine.Desktops[0].ID, "expected_revision": mine.Desktops[0].Revision, "session_id": "my-agent",
	})
	across := w.send(client, map[string]any{
		"cmd": protocol.CmdDesktopMoveLeaf, "source_desktop_id": mine.Desktops[0].ID, "target_desktop_id": theirs.Desktops[0].ID,
		"leaf_id": protocol.Deref(placed.PaneID), "edge": "left",
		"expected_source_revision": placed.Desktops[0].Revision, "expected_target_revision": theirs.Desktops[0].Revision,
	})
	wantErrorCode(t, across, protocol.ProfileErrorCodeCrossProfile)

	twice := w.send(client, map[string]any{
		"cmd": protocol.CmdDesktopPlaceSession, "desktop_id": mine.Desktops[0].ID, "expected_revision": placed.Desktops[0].Revision, "session_id": "my-agent",
	})
	wantErrorCode(t, twice, protocol.ProfileErrorCodeAlreadyPlaced)
	if got, err := w.d.store.SessionProfileID("my-agent"); err != nil || got != mine.Profile.ID {
		t.Fatalf("my-agent belongs to %q (err %v), want %s", got, err, mine.Profile.ID)
	}
}

func TestProfileNamesAreUniqueWhileLiveAndReusableAfterDelete(t *testing.T) {
	w := newProfilesTestDaemon(t)
	client, _ := w.connect("")
	attn := w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "attn"}).Profile
	side := w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "side"}).Profile

	wantErrorCode(t, w.send(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "attn"}), protocol.ProfileErrorCodeNameTaken)
	wantErrorCode(t, w.send(client, map[string]any{
		"cmd": protocol.CmdProfileRename, "profile_id": side.ID, "name": "attn", "expected_revision": side.Revision,
	}), protocol.ProfileErrorCodeNameTaken)

	renamed := w.mustSend(client, map[string]any{
		"cmd": protocol.CmdProfileRename, "profile_id": attn.ID, "name": "attention", "expected_revision": attn.Revision,
	}).Profile
	if renamed.ID != attn.ID {
		t.Fatalf("renaming changed the profile id from %s to %s", attn.ID, renamed.ID)
	}
	w.mustSend(client, map[string]any{
		"cmd": protocol.CmdProfileDelete, "profile_id": renamed.ID, "expected_revision": renamed.Revision, "destination_profile_id": side.ID,
	})
	reborn := w.mustSend(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "attention"}).Profile
	if reborn.ID == attn.ID {
		t.Fatalf("a new profile reused the deleted profile's id %s", attn.ID)
	}
	live, err := w.d.store.ListProfiles(false)
	if err != nil || len(live) != 2 {
		t.Fatalf("%d live profiles (err %v), want 2", len(live), err)
	}
	wantErrorCode(t, w.send(client, map[string]any{
		"cmd": protocol.CmdProfileDelete, "profile_id": reborn.ID, "expected_revision": reborn.Revision, "destination_profile_id": reborn.ID,
	}), protocol.ProfileErrorCodeDestinationSame)
}

func TestProfileCommandWithoutARequestIDIsRefused(t *testing.T) {
	w := newProfilesTestDaemon(t)
	client, _ := w.connect("")
	result := w.send(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "attn", "request_id": ""})
	wantErrorCode(t, result, protocol.ProfileErrorCodeInvalid)
}

func TestOutpostRefusesProfileCommandsByName(t *testing.T) {
	w := newProfilesTestDaemonEnrolledAt(t, "d-0123456789abcdef0123456789abcdef")
	client, _ := w.connect("")
	result := w.send(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "attn"})
	wantErrorCode(t, result, protocol.ProfileErrorCodeUnavailable)
	if live, err := w.d.store.ListProfiles(false); err != nil || len(live) != 0 {
		t.Fatalf("a refused create left %d profiles (err %v)", len(live), err)
	}
}

func TestSelectingAProfileTellsEveryClientItWasUsed(t *testing.T) {
	w := newProfilesTestDaemon(t)
	first, _ := w.connect("")
	work := w.mustSend(first, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "work"}).Profile
	w.mustSend(first, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "home"})
	second, _ := w.connect("")
	drainClientPayloads(t, second)

	w.mustSend(first, map[string]any{"cmd": protocol.CmdProfileSelect, "profile_id": work.ID})

	seen := profilesChanges(t, second)
	if len(seen) != 1 {
		t.Fatalf("the other client saw %d profiles_changed, want 1", len(seen))
	}
	for _, profile := range seen[0].Profiles {
		if profile.ID == work.ID && profile.LastUsedAt == nil {
			t.Fatal("the other client was told of the selection without its last_used_at")
		}
	}
}

func TestDeletingAProfileLandsItsClientsOnTheDestination(t *testing.T) {
	w := newProfilesTestDaemon(t)
	deleter, _ := w.connect("")
	doomed := w.mustSend(deleter, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "doomed"}).Profile
	kept := w.mustSend(deleter, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "kept"})
	w.mustSend(deleter, map[string]any{"cmd": protocol.CmdProfileSelect, "profile_id": doomed.ID})
	bystander, _ := w.connect(doomed.ID)
	drainClientPayloads(t, deleter)

	deleted := w.mustSend(deleter, map[string]any{
		"cmd": protocol.CmdProfileDelete, "profile_id": doomed.ID, "expected_revision": doomed.Revision, "destination_profile_id": kept.Profile.ID,
	})
	if deleted.Profile.ID != kept.Profile.ID || len(deleted.Desktops) != 1 {
		t.Fatalf("profile_delete answered %+v, want the destination and its arrangement", deleted)
	}
	landed := arrangementChanges(t, bystander)
	if len(landed) != 1 || landed[0].Profile.ID != kept.Profile.ID || len(landed[0].Desktops) != 1 {
		t.Fatalf("a client on the deleted profile was sent %+v, want the destination's whole arrangement", landed)
	}

	w.mustSend(deleter, map[string]any{
		"cmd": protocol.CmdDesktopRename, "desktop_id": kept.Desktops[0].ID, "name": "after", "expected_revision": kept.Desktops[0].Revision,
	})
	if followed := arrangementChanges(t, bystander); len(followed) != 1 || followed[0].Desktops[0].Name != "after" {
		t.Fatalf("after the delete the client was sent %+v, want changes to the destination", followed)
	}
}

func TestTheResultReachesItsSenderBeforeTheBroadcast(t *testing.T) {
	w := newProfilesTestDaemon(t)
	client, _ := w.connect("")
	data, err := json.Marshal(map[string]any{"cmd": protocol.CmdProfileCreate, "name": "attn", "request_id": "r1"})
	if err != nil {
		t.Fatal(err)
	}
	w.d.handleClientMessage(client, data)

	payloads := drainClientPayloads(t, client)
	if len(payloads) != 2 || eventName(t, payloads[0]) != protocol.EventProfileActionResult || eventName(t, payloads[1]) != protocol.EventProfilesChanged {
		names := make([]string, 0, len(payloads))
		for _, payload := range payloads {
			names = append(names, eventName(t, payload))
		}
		t.Fatalf("profile_create sent %v, want profile_action_result then profiles_changed", names)
	}
}

func TestAStorageFailureIsNotReportedAsUnavailable(t *testing.T) {
	w := newProfilesTestDaemon(t)
	client, _ := w.connect("")
	if err := w.d.store.Close(); err != nil {
		t.Fatal(err)
	}
	result := w.send(client, map[string]any{"cmd": protocol.CmdProfileCreate, "name": "attn"})
	wantErrorCode(t, result, protocol.ProfileErrorCodeInternal)
}
