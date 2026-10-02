package daemon_test

import (
	"os"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func profileRequest(p *testworld.Peer, cmd any, requestID string) protocol.ProfileActionResultMessage {
	p.T.Helper()
	return testworld.Request(p, cmd, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == requestID })
}

func mustProfileRequest(p *testworld.Peer, cmd any, requestID string) protocol.ProfileActionResultMessage {
	p.T.Helper()
	result := profileRequest(p, cmd, requestID)
	if !result.Success {
		p.T.Fatalf("%s failed: %s", result.Action, protocol.Deref(result.Error))
	}
	return result
}

func requestShowSession(p *testworld.Peer, sessionID string) protocol.ProfileActionResultMessage {
	p.T.Helper()
	id := uuid.NewString()
	return profileRequest(p, protocol.DesktopShowSessionMessage{Cmd: protocol.CmdDesktopShowSession, RequestID: id, SessionID: sessionID}, id)
}

func requestShowLeaf(p *testworld.Peer, desktopID, leafID string) protocol.ProfileActionResultMessage {
	p.T.Helper()
	id := uuid.NewString()
	return profileRequest(p, protocol.DesktopShowLeafMessage{Cmd: protocol.CmdDesktopShowLeaf, RequestID: id, DesktopID: desktopID, LeafID: leafID}, id)
}

func createDesktop(p *testworld.Peer, profileID string) protocol.Desktop {
	p.T.Helper()
	id := uuid.NewString()
	created := mustProfileRequest(p, protocol.DesktopCreateMessage{Cmd: protocol.CmdDesktopCreate, RequestID: id, ProfileID: profileID}, id)
	return created.Desktops[0]
}

func switchDesktop(p *testworld.Peer, profileID, desktopID string) {
	p.T.Helper()
	id := uuid.NewString()
	mustProfileRequest(p, protocol.DesktopSetCurrentMessage{Cmd: protocol.CmdDesktopSetCurrent, RequestID: id, ProfileID: profileID, DesktopID: desktopID}, id)
}

func createProfile(p *testworld.Peer, name string) protocol.Profile {
	p.T.Helper()
	id := uuid.NewString()
	return *mustProfileRequest(p, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: id, Name: name}, id).Profile
}

func selectProfile(p *testworld.Peer, profileID string) {
	p.T.Helper()
	id := uuid.NewString()
	mustProfileRequest(p, protocol.ProfileSelectMessage{Cmd: protocol.CmdProfileSelect, RequestID: id, ProfileID: profileID}, id)
}

func writeNotes(t *testing.T, w *world) string {
	t.Helper()
	notes := w.Path("notes.md")
	if err := os.MkdirAll(w.Path(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notes, []byte("# notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return notes
}

func injectAgent(t *testing.T, w *world, id string) {
	t.Helper()
	if err := w.InjectSession(id, id, w.Path(id), protocol.SessionAgentClaude); err != nil {
		t.Fatalf("inject %s: %v", id, err)
	}
	synctest.Wait()
}

type arrangementView struct {
	profile  protocol.Profile
	desktops map[string]protocol.Desktop
}

func viewProfile(t *testing.T, w *world, profileID string) arrangementView {
	t.Helper()
	p := w.AppOn(profileID)
	defer p.Close()
	view := arrangementView{desktops: map[string]protocol.Desktop{}}
	for _, profile := range p.Initial.Profiles {
		if profile.ID == profileID {
			view.profile = profile
		}
	}
	for _, desktop := range p.Initial.Desktops {
		view.desktops[desktop.ID] = desktop
	}
	if view.profile.ID == "" || protocol.Deref(p.Initial.SelectedProfileID) != profileID {
		t.Fatalf("a client remembering profile %s was scoped to %v", profileID, p.Initial.SelectedProfileID)
	}
	return view
}

func (v arrangementView) paneOf(t *testing.T, sessionID string) (protocol.Desktop, string) {
	t.Helper()
	for _, desktop := range v.desktops {
		for _, pane := range desktop.Panes {
			if pane.SessionID == sessionID {
				return desktop, pane.PaneID
			}
		}
	}
	t.Fatalf("session %s is placed on no desktop of profile %s", sessionID, v.profile.ID)
	return protocol.Desktop{}, ""
}

func arrangementsSeen(p *testworld.Peer) int {
	seen := 0
	for _, e := range p.Received() {
		if e.Event == protocol.EventProfileArrangementChanged {
			seen++
		}
	}
	return seen
}

type arrangementCounter struct {
	peers  []*testworld.Peer
	before []int
}

func countArrangements(peers ...*testworld.Peer) *arrangementCounter {
	synctest.Wait()
	c := &arrangementCounter{peers: peers}
	for _, p := range peers {
		c.before = append(c.before, arrangementsSeen(p))
	}
	return c
}

func (c *arrangementCounter) want(t *testing.T, what string, counts ...int) {
	t.Helper()
	synctest.Wait()
	for i, p := range c.peers {
		if got := arrangementsSeen(p) - c.before[i]; got != counts[i] {
			t.Fatalf("%s: client %d received %d arrangements, want %d", what, i, got, counts[i])
		}
		c.before[i] += counts[i]
	}
}

func shownIn(t *testing.T, result protocol.ProfileActionResultMessage, desktopID, leafID string) protocol.Desktop {
	t.Helper()
	if !result.Success {
		t.Fatalf("%s failed: %s (%s)", result.Action, protocol.Deref(result.Error), protocol.Deref(result.ErrorCode))
	}
	if protocol.Deref(result.PaneID) != leafID || result.Profile == nil || result.Profile.CurrentDesktopID != desktopID || len(result.Desktops) != 1 {
		t.Fatalf("%s answered pane %q, profile %+v, %d desktops; want leaf %s on current desktop %s", result.Action, protocol.Deref(result.PaneID), result.Profile, len(result.Desktops), leafID, desktopID)
	}
	shown := result.Desktops[0]
	if shown.ID != desktopID || shown.ActivePaneID != leafID {
		t.Fatalf("%s answered desktop %s with active leaf %s, want %s on %s", result.Action, shown.ID, shown.ActivePaneID, leafID, desktopID)
	}
	return shown
}

func awaitShown(p *testworld.Peer, profileID, desktopID, leafID string) protocol.ProfileArrangementChangedMessage {
	p.T.Helper()
	return testworld.Await(p, protocol.EventProfileArrangementChanged, func(e protocol.ProfileArrangementChangedMessage) bool {
		desktop, ok := desktopIn(e.Desktops, desktopID)
		return e.Profile.ID == profileID && e.Profile.CurrentDesktopID == desktopID && ok && desktop.ActivePaneID == leafID
	})
}

func desktopIn(desktops []protocol.Desktop, id string) (protocol.Desktop, bool) {
	for _, desktop := range desktops {
		if desktop.ID == id {
			return desktop, true
		}
	}
	return protocol.Desktop{}, false
}

func TestShowingAPlacedLeafSelectsItForEveryClientOfItsProfileAndSurvivesARestart(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		profileID := app.SelectedProfile()
		injectAgent(t, w, "a")
		second := createDesktop(app, profileID)
		switchDesktop(app, profileID, second.ID)
		injectAgent(t, w, "b")
		outsider := w.AppOn(createProfile(app, "elsewhere").ID)
		watcher := w.AppOn(profileID)
		before := viewProfile(t, w, profileID)
		first, paneA := before.paneOf(t, "a")
		_, paneB := before.paneOf(t, "b")
		counter := countArrangements(app, watcher, outsider)

		shown := shownIn(t, requestShowSession(app, "a"), first.ID, paneA)
		if shown.Revision != first.Revision || shown.TreeJson != first.TreeJson {
			t.Fatalf("showing a placed agent rewrote its desktop: revision %d -> %d, tree %s -> %s", first.Revision, shown.Revision, first.TreeJson, shown.TreeJson)
		}
		counter.want(t, "showing an agent on another desktop", 1, 1, 0)
		awaitShown(watcher, profileID, first.ID, paneA)

		again := shownIn(t, requestShowSession(watcher, "a"), first.ID, paneA)
		if again.Revision != first.Revision || again.TreeJson != first.TreeJson || len(again.Panes) != len(first.Panes) {
			t.Fatalf("showing the shown agent again changed its desktop: %+v, was %+v", again, first)
		}
		counter.want(t, "showing the shown agent again answers only its requester", 0, 1, 0)

		shownIn(t, requestShowLeaf(app, second.ID, paneB), second.ID, paneB)
		counter.want(t, "showing an agent pane on another desktop", 1, 1, 0)
		awaitShown(watcher, profileID, second.ID, paneB)

		if docked := dockOnDesktop(app, viewProfile(t, w, profileID).desktops[first.ID], protocol.DesktopDockTileMessage{
			TileID: "tile-notes", TileKind: "markdown", TileParams: protocol.Ptr(writeNotes(t, w)), Edge: protocol.LayoutDockEdgeRight,
		}); !docked.Success {
			t.Fatalf("docking a tile: %s", protocol.Deref(docked.Error))
		}
		shownIn(t, requestShowLeaf(app, second.ID, paneB), second.ID, paneB)
		counter = countArrangements(app, watcher, outsider)
		shownIn(t, requestShowLeaf(app, first.ID, "tile-notes"), first.ID, "tile-notes")
		counter.want(t, "showing a tile on another desktop", 1, 1, 0)
		awaitShown(watcher, profileID, first.ID, "tile-notes")

		w.restart()
		after := viewProfile(t, w, profileID)
		if after.profile.CurrentDesktopID != first.ID || after.desktops[first.ID].ActivePaneID != "tile-notes" {
			t.Fatalf("after a restart profile %s shows desktop %s leaf %s, want %s leaf tile-notes",
				profileID, after.profile.CurrentDesktopID, after.desktops[first.ID].ActivePaneID, first.ID)
		}
	})
}

func TestShowingAnUnplacedAgentPlacesItOnTheCurrentDesktop(t *testing.T) {
	for _, tc := range []struct {
		name string
		// arrange returns the current desktop and the leaf the agent should dock beside, or "" when it is empty.
		arrange func(t *testing.T, w *world, app *testworld.Peer, profileID, paneA string) (string, string)
	}{
		{
			name: "beside the active agent",
			arrange: func(t *testing.T, w *world, app *testworld.Peer, profileID, paneA string) (string, string) {
				desktop, _ := viewProfile(t, w, profileID).paneOf(t, "a")
				id := uuid.NewString()
				mustProfileRequest(app, protocol.DesktopSetActivePaneMessage{Cmd: protocol.CmdDesktopSetActivePane, RequestID: id, DesktopID: desktop.ID, PaneID: paneA}, id)
				return desktop.ID, paneA
			},
		},
		{
			name: "beside the active tile",
			arrange: func(t *testing.T, w *world, app *testworld.Peer, profileID, paneA string) (string, string) {
				desktop, _ := viewProfile(t, w, profileID).paneOf(t, "a")
				notes := writeNotes(t, w)
				if docked := dockOnDesktop(app, desktop, protocol.DesktopDockTileMessage{
					TileID: "tile-notes", TileKind: "markdown", TileParams: protocol.Ptr(notes), Edge: protocol.LayoutDockEdgeRight,
				}); !docked.Success {
					t.Fatalf("docking a tile: %s", protocol.Deref(docked.Error))
				}
				return desktop.ID, "tile-notes"
			},
		},
		{
			name: "as the first leaf of an empty desktop",
			arrange: func(t *testing.T, w *world, app *testworld.Peer, profileID, _ string) (string, string) {
				empty := createDesktop(app, profileID)
				switchDesktop(app, profileID, empty.ID)
				return empty.ID, ""
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubble(t, func(t *testing.T, w *world) {
				app := w.App()
				profileID := app.SelectedProfile()
				injectAgent(t, w, "a")
				injectAgent(t, w, "b")
				view := viewProfile(t, w, profileID)
				_, paneA := view.paneOf(t, "a")
				desktop, paneB := view.paneOf(t, "b")
				id := uuid.NewString()
				mustProfileRequest(app, protocol.DesktopRemoveLeafMessage{Cmd: protocol.CmdDesktopRemoveLeaf, RequestID: id, DesktopID: desktop.ID, LeafID: paneB, ExpectedRevision: desktop.Revision}, id)
				current, anchor := tc.arrange(t, w, app, profileID, paneA)
				revision := viewProfile(t, w, profileID).desktops[current].Revision
				watcher := w.AppOn(profileID)
				counter := countArrangements(app, watcher)

				result := requestShowSession(app, "b")
				placed := protocol.Deref(result.PaneID)
				if placed == "" || placed == paneB {
					t.Fatalf("showing the unplaced agent answered pane %q, want a new pane", placed)
				}
				shown := shownIn(t, result, current, placed)
				if shown.Revision <= revision {
					t.Fatalf("placing the agent left desktop revision %d at %d", revision, shown.Revision)
				}
				if count := slices.IndexFunc(shown.Panes, func(p protocol.DesktopPane) bool { return p.SessionID == "b" }); count < 0 || shown.Panes[count].PaneID != placed {
					t.Fatalf("desktop %s panes %+v do not hold agent b in %s", shown.ID, shown.Panes, placed)
				}
				tree := desktopTree(t, shown)
				if anchor == "" {
					if !slices.Equal(tree.leafIDs(), []string{placed}) {
						t.Fatalf("the empty desktop now holds %v, want only %s", tree.leafIDs(), placed)
					}
				} else if split, index, ok := tree.splitHolding(placed); !ok || !slices.Equal(split.Children[1-index].leafIDs(), []string{anchor}) {
					t.Fatalf("the new pane %s is not split beside %s: %s", placed, anchor, shown.TreeJson)
				}
				counter.want(t, "showing an unplaced agent", 1, 1)
				awaitShown(watcher, profileID, current, placed)
			})
		})
	}
}

func TestShowingAnAgentOfAnotherProfileMovesOnlyTheRequester(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		home := app.SelectedProfile()
		injectAgent(t, w, "a")
		away := createProfile(app, "away")
		selectProfile(app, away.ID)
		injectAgent(t, w, "x")
		selectProfile(app, home)
		destination, stay := w.AppOn(away.ID), w.AppOn(home)
		desktop, paneX := viewProfile(t, w, away.ID).paneOf(t, "x")
		counter := countArrangements(app, destination, stay)

		result := requestShowSession(app, "x")
		shownIn(t, result, desktop.ID, paneX)
		if result.Profile.ID != away.ID {
			t.Fatalf("showing x answered profile %s, want %s", result.Profile.ID, away.ID)
		}
		counter.want(t, "showing an agent of another profile", 1, 1, 0)
		awaitShown(app, away.ID, desktop.ID, paneX)

		shownIn(t, requestShowSession(destination, "x"), desktop.ID, paneX)
		counter.want(t, "showing the shown agent again answers only its requester", 0, 1, 0)
	})
}

func TestAShowThatFailsChangesNothing(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		home := app.SelectedProfile()
		injectAgent(t, w, "a")
		other := createDesktop(app, home)
		injectAgent(t, w, "other-anchor")
		requestShowSession(app, "a")
		away := createProfile(app, "away")
		selectProfile(app, away.ID)
		injectAgent(t, w, "gone")
		selectProfile(app, home)
		if closed := closeFromApp(app, "gone"); closed.Error != nil {
			t.Fatalf("closing gone: %s", *closed.Error)
		}
		watcher := w.AppOn(home)
		before := viewProfile(t, w, home)
		desktop, paneA := before.paneOf(t, "a")
		counter := countArrangements(app, watcher)

		for _, tc := range []struct {
			name   string
			result protocol.ProfileActionResultMessage
			code   protocol.ProfileErrorCode
		}{
			{"a closed agent", requestShowSession(app, "gone"), protocol.ProfileErrorCodeSessionClosed},
			{"an unknown agent", requestShowSession(app, "nobody"), protocol.ProfileErrorCodeNotFound},
			{"no agent", requestShowSession(app, " "), protocol.ProfileErrorCodeInvalid},
			{"a leaf of another desktop", requestShowLeaf(app, other.ID, paneA), protocol.ProfileErrorCodeNotFound},
			{"a missing desktop", requestShowLeaf(app, "desktop-missing", paneA), protocol.ProfileErrorCodeNotFound},
			{"no leaf", requestShowLeaf(app, desktop.ID, ""), protocol.ProfileErrorCodeInvalid},
		} {
			code := protocol.Deref(tc.result.ErrorCode)
			if tc.result.Success || code != tc.code || protocol.Deref(tc.result.Error) == "" {
				t.Errorf("showing %s: success=%v code=%q error=%q, want %s with a message", tc.name, tc.result.Success, code, protocol.Deref(tc.result.Error), tc.code)
			}
		}
		counter.want(t, "failed shows", 0, 0)
		after := viewProfile(t, w, home)
		if after.profile.CurrentDesktopID != before.profile.CurrentDesktopID || after.desktops[desktop.ID].ActivePaneID != desktop.ActivePaneID {
			t.Fatalf("failed shows moved the selection from %s/%s to %s/%s", before.profile.CurrentDesktopID, desktop.ActivePaneID,
				after.profile.CurrentDesktopID, after.desktops[desktop.ID].ActivePaneID)
		}

		switchDesktop(watcher, home, other.ID)
		counter.want(t, "a later selection on the requester's own profile", 1, 1)
	})
}

func TestAMoveTellsClientsWhereTheLeafWentEvenWhenItIsRenamed(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		profileID := app.SelectedProfile()
		injectAgent(t, w, "a")
		source, paneA := viewProfile(t, w, profileID).paneOf(t, "a")
		target := createDesktop(app, profileID)
		notes := writeNotes(t, w)
		for _, desktop := range []protocol.Desktop{source, target} {
			if docked := dockOnDesktop(app, viewProfile(t, w, profileID).desktops[desktop.ID], protocol.DesktopDockTileMessage{
				TileID: "tile-notes", TileKind: "markdown", TileParams: protocol.Ptr(notes), Edge: protocol.LayoutDockEdgeRight,
			}); !docked.Success {
				t.Fatalf("docking a tile on %s: %s", desktop.ID, protocol.Deref(docked.Error))
			}
		}
		watcher := w.AppOn(profileID)

		move := func(leafID string) (string, protocol.LeafMoved) {
			t.Helper()
			view := viewProfile(t, w, profileID)
			counter := countArrangements(watcher)
			id := uuid.NewString()
			moved := mustProfileRequest(app, protocol.DesktopMoveLeafMessage{
				Cmd: protocol.CmdDesktopMoveLeaf, RequestID: id, SourceDesktopID: source.ID, TargetDesktopID: target.ID, LeafID: leafID,
				Edge: protocol.LayoutDockEdgeRight, ExpectedSourceRevision: view.desktops[source.ID].Revision, ExpectedTargetRevision: view.desktops[target.ID].Revision,
			}, id)
			change := testworld.Await(watcher, protocol.EventProfileArrangementChanged, func(e protocol.ProfileArrangementChangedMessage) bool {
				return e.MovedLeaf != nil && e.MovedLeaf.FromLeafID == leafID
			})
			counter.want(t, "moving "+leafID, 1)
			return protocol.Deref(moved.PaneID), *change.MovedLeaf
		}

		final, moved := move("tile-notes")
		if final == "tile-notes" || !strings.HasPrefix(final, "tile-notes-") {
			t.Fatalf("moving tile-notes onto a desktop that holds one answered %q, want a renamed leaf", final)
		}
		if want := (protocol.LeafMoved{FromDesktopID: source.ID, FromLeafID: "tile-notes", ToDesktopID: target.ID, ToLeafID: final}); moved != want {
			t.Fatalf("the renamed move arrived as %+v, want %+v", moved, want)
		}
		leaves := desktopTree(t, viewProfile(t, w, profileID).desktops[target.ID]).leafIDs()
		if !slices.Contains(leaves, "tile-notes") || !slices.Contains(leaves, final) {
			t.Fatalf("the target desktop holds %v, want its own tile-notes and the moved %s", leaves, final)
		}

		final, moved = move(paneA)
		if want := (protocol.LeafMoved{FromDesktopID: source.ID, FromLeafID: paneA, ToDesktopID: target.ID, ToLeafID: paneA}); final != paneA || moved != want {
			t.Fatalf("moving agent pane %s answered %q and arrived as %+v, want %+v", paneA, final, moved, want)
		}

		shownIn(t, requestShowLeaf(app, target.ID, "tile-notes"), target.ID, "tile-notes")
		plain := awaitShown(watcher, profileID, target.ID, "tile-notes")
		if plain.MovedLeaf != nil {
			t.Fatalf("an arrangement no move produced carries moved_leaf %+v", plain.MovedLeaf)
		}
	})
}

func TestClosingTheShownAgentHandsTheSelectionOnWithoutAClientCommand(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		profileID := app.SelectedProfile()
		injectAgent(t, w, "a")
		injectAgent(t, w, "b")
		view := viewProfile(t, w, profileID)
		desktop, paneA := view.paneOf(t, "a")
		_, paneB := view.paneOf(t, "b")
		shownIn(t, requestShowSession(app, "b"), desktop.ID, paneB)
		watcher := w.AppOn(profileID)

		if closed := closeFromApp(app, "b"); closed.Error != nil {
			t.Fatalf("closing b: %s", *closed.Error)
		}
		change := testworld.Await(watcher, protocol.EventProfileArrangementChanged, func(e protocol.ProfileArrangementChangedMessage) bool {
			shown, ok := desktopIn(e.Desktops, desktop.ID)
			return ok && !slices.ContainsFunc(shown.Panes, func(p protocol.DesktopPane) bool { return p.PaneID == paneB })
		})
		shown, _ := desktopIn(change.Desktops, desktop.ID)
		if change.Profile.CurrentDesktopID != desktop.ID || shown.ActivePaneID != paneA {
			t.Fatalf("after closing the shown agent profile %s shows %s leaf %q, want %s leaf %s", profileID, change.Profile.CurrentDesktopID, shown.ActivePaneID, desktop.ID, paneA)
		}
	})
}
