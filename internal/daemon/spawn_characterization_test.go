package daemon

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
)

func newSpawnCharacterizationDaemon(t *testing.T) (*Daemon, *fakeSpawnBackend, *wsClient, string) {
	t.Helper()
	return newSpawnCharacterizationDaemonOn(t, NewForTesting(filepath.Join(t.TempDir(), "test.sock")))
}

func newSpawnCharacterizationDaemonOn(t *testing.T, d *Daemon) (*Daemon, *fakeSpawnBackend, *wsClient, string) {
	t.Helper()
	backend := &fakeSpawnBackend{}
	d.ptyBackend = backend
	client := newProtocolTestClient()
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

}
