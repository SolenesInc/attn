package daemon_test

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func homeOf(t *testing.T, desktops []protocol.Desktop) protocol.Desktop {
	t.Helper()
	for _, desktop := range desktops {
		if desktop.Kind == protocol.DesktopKindHome {
			return desktop
		}
	}
	t.Fatalf("no desktop of kind home among %d desktops", len(desktops))
	return protocol.Desktop{}
}

func listsHome(desktops []protocol.Desktop) bool {
	return slices.ContainsFunc(desktops, func(d protocol.Desktop) bool { return d.Kind == protocol.DesktopKindHome })
}

func orderedDesktops(view arrangementView) []protocol.Desktop {
	desktops := make([]protocol.Desktop, 0, len(view.desktops))
	for _, desktop := range view.desktops {
		desktops = append(desktops, desktop)
	}
	slices.SortFunc(desktops, func(a, b protocol.Desktop) int {
		if a.OrderKey < b.OrderKey {
			return -1
		}
		return 1
	})
	return desktops
}

func renameDesktop(app *testworld.Peer, desktopID, name string) protocol.ProfileActionResultMessage {
	id := uuid.NewString()
	return profileRequest(app, protocol.DesktopRenameMessage{Cmd: protocol.CmdDesktopRename, RequestID: id, DesktopID: desktopID, Name: name}, id)
}

func requestSwitchDesktop(app *testworld.Peer, profileID, desktopID string) protocol.ProfileActionResultMessage {
	id := uuid.NewString()
	return profileRequest(app, protocol.DesktopSetCurrentMessage{Cmd: protocol.CmdDesktopSetCurrent, RequestID: id, ProfileID: profileID, DesktopID: desktopID}, id)
}

func TestHomeIsAKeptDesktopAtTheTopThatEveryAppSharesWhileViewsAreOn(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		profile := app.SelectedProfile()
		first := app.Initial.Desktops[0]
		if listsHome(app.Initial.Desktops) {
			t.Fatal("with views off the app was sent a Home desktop")
		}

		setSetting(t, app, "views_enabled", "true")
		desktops := orderedDesktops(viewProfile(t, w, profile))
		home := homeOf(t, desktops)
		if desktops[0].ID != home.ID {
			t.Fatalf("desktops are ordered %v, want Home at the top", desktops)
		}
		if home.ShortcutSlot != nil || home.Name != "" {
			t.Errorf("Home has slot %v and name %q, want no digit and no name", home.ShortcutSlot, home.Name)
		}
		tree := desktopTree(t, home)
		if len(tree.Children) != 2 || !slices.Equal(tree.Children[0].leafIDs(), []string{"tile-view-attn", "tile-view-sessions"}) || !slices.Equal(tree.Children[1].leafIDs(), []string{"tile-view-pull-requests"}) {
			t.Fatalf("Home holds %s, want attn above Sessions, then Pull requests", home.TreeJson)
		}

		if closed := requestCloseDesktop(app, home); closed.Success {
			t.Error("closing Home succeeded")
		}
		if renamed := renameDesktop(app, home.ID, "Mine"); renamed.Success {
			t.Error("renaming Home succeeded")
		}

		if named := renameDesktop(app, first.ID, "Main"); !named.Success {
			t.Fatalf("naming Desktop 1: %s", protocol.Deref(named.Error))
		}
		other := w.AppOn(profile)
		defer other.Close()
		switchDesktop(app, profile, home.ID)
		testworld.Await(other, protocol.EventProfileArrangementChanged, func(m protocol.ProfileArrangementChangedMessage) bool {
			return m.Profile.CurrentDesktopID == home.ID
		})

		scratch := createDesktop(app, profile)
		switchDesktop(app, profile, home.ID)
		w.advance(29 * time.Second)
		if _, kept := viewProfile(t, w, profile).desktops[scratch.ID]; !kept {
			t.Fatal("an empty desktop left for Home was removed before 30 s")
		}
		w.advance(time.Second)
		awaitDesktopRemoved(t, w, profile, scratch.ID)

		for _, tile := range []string{"tile-view-attn", "tile-view-sessions", "tile-view-pull-requests"} {
			if closed := desktopAction(app, protocol.DesktopRemoveLeafMessage{Cmd: protocol.CmdDesktopRemoveLeaf, DesktopID: home.ID, LeafID: tile}); !closed.Success {
				t.Fatalf("closing %s on Home: %s", tile, protocol.Deref(closed.Error))
			}
		}
		switchDesktop(app, profile, first.ID)
		w.advance(time.Hour)
		if !listsHome(orderedDesktops(viewProfile(t, w, profile))) {
			t.Fatal("an empty Home was removed")
		}

		if closed := requestCloseDesktop(app, viewProfile(t, w, profile).desktops[first.ID]); !closed.Success || closed.Profile.CurrentDesktopID != home.ID {
			t.Fatalf("closing the last ordinary desktop beside Home answered success=%v current=%v: %s", closed.Success, closed.Profile, protocol.Deref(closed.Error))
		}
		w.restart()
		if view := viewProfile(t, w, profile); view.profile.CurrentDesktopID != home.ID || len(view.desktops) != 1 {
			t.Fatalf("after a restart the profile shows %s of %d desktops, want Home alone", view.profile.CurrentDesktopID, len(view.desktops))
		}
	})
}

func TestWorkStartedOnHomeLandsThereAndMovesOffWhenViewsTurnOff(t *testing.T) {
	w := newWorld(t, shellHarness)
	app := w.App()
	profile := app.SelectedProfile()
	first := app.Initial.Desktops[0]
	setSetting(t, app, "views_enabled", "true")
	home := homeOf(t, orderedDesktops(viewProfile(t, w, profile)))
	switchDesktop(app, profile, home.ID)

	shell := w.Spawn(app, shellHarness, w.Path("shell"))
	if landed, _ := viewProfile(t, w, profile).paneOf(t, shell); landed.ID != home.ID {
		t.Fatalf("a shell started while Home is current landed on %s, want Home", landed.ID)
	}
	if closed := requestCloseDesktop(app, viewProfile(t, w, profile).desktops[first.ID]); !closed.Success {
		t.Fatalf("closing the only ordinary desktop: %s", protocol.Deref(closed.Error))
	}

	setSetting(t, app, "views_enabled", "false")
	view := viewProfile(t, w, profile)
	if listsHome(orderedDesktops(view)) {
		t.Fatal("with views off Home is still listed")
	}
	landed, _ := view.paneOf(t, shell)
	if view.profile.CurrentDesktopID != landed.ID || landed.ShortcutSlot == nil {
		t.Fatalf("with views off the shell sits on %s and %s is current, want both on a new numbered desktop", landed.ID, view.profile.CurrentDesktopID)
	}
	if refused := requestSwitchDesktop(app, profile, home.ID); refused.Success {
		t.Fatal("with views off, switching to Home succeeded")
	}

	setSetting(t, app, "views_enabled", "true")
	view = viewProfile(t, w, profile)
	back := homeOf(t, orderedDesktops(view))
	if len(desktopTree(t, back).tiles()) != 3 || len(back.Panes) != 0 {
		t.Fatalf("Home came back holding %s, want only its three views", back.TreeJson)
	}
	if view.profile.CurrentDesktopID != landed.ID {
		t.Fatalf("turning views on made %s current, want %s kept", view.profile.CurrentDesktopID, landed.ID)
	}
}

func TestNothingReopensOnHomeOrShowsItWhileViewsAreOff(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	profile := app.SelectedProfile()
	setSetting(t, app, "views_enabled", "true")
	home := homeOf(t, orderedDesktops(viewProfile(t, w, profile)))
	switchDesktop(app, profile, home.ID)
	session := w.Spawn(app, fakeagent.Codex, w.Path("on-home"))
	agent := w.Launched(session)
	app.TypeLine(session, "hello")
	agent.Prompted()
	agent.Reply("Ready. <!-- attn:state=idle -->")
	if landed, _ := viewProfile(t, w, profile).paneOf(t, session); landed.ID != home.ID {
		t.Fatalf("an agent started while Home is current landed on %s, want Home", landed.ID)
	}
	closeSession(t, cli, session, "done")
	awaitClosed(app, session)

	setSetting(t, app, "views_enabled", "false")
	if shown := requestShowLeaf(app, home.ID, "tile-view-attn"); shown.Success {
		t.Fatal("with views off, showing a tile on Home succeeded")
	}
	if _, err := cli.SessionReopen(client.SessionReopenOptions{SessionID: protocol.SessionID(session)}); err != nil {
		t.Fatal(err)
	}
	w.Launched(session)
	view := viewProfile(t, w, profile)
	landed, _ := view.paneOf(t, session)
	if landed.ID == home.ID || !slices.ContainsFunc(orderedDesktops(view), func(d protocol.Desktop) bool { return d.ID == landed.ID }) {
		t.Fatalf("with views off, the agent last on Home reopened on %s, want a listed desktop", landed.ID)
	}
}

func TestPuttingHomeBackReturnsItToTheTopWithItsViewsAndKeepsItsWork(t *testing.T) {
	w := newWorld(t, shellHarness)
	app := w.App()
	profile := app.SelectedProfile()
	first := app.Initial.Desktops[0]
	setSetting(t, app, "views_enabled", "true")
	home := homeOf(t, orderedDesktops(viewProfile(t, w, profile)))
	switchDesktop(app, profile, home.ID)
	shell := w.Spawn(app, shellHarness, w.Path("shell"))
	_, pane := viewProfile(t, w, profile).paneOf(t, shell)
	if closed := desktopAction(app, protocol.DesktopRemoveLeafMessage{Cmd: protocol.CmdDesktopRemoveLeaf, DesktopID: home.ID, LeafID: "tile-view-pull-requests"}); !closed.Success {
		t.Fatalf("closing Pull requests on Home: %s", protocol.Deref(closed.Error))
	}
	id := uuid.NewString()
	if moved := profileRequest(app, protocol.DesktopSetOrderMessage{Cmd: protocol.CmdDesktopSetOrder, RequestID: id, ProfileID: profile, DesktopIds: []string{first.ID, home.ID}}, id); !moved.Success {
		t.Fatalf("moving Home below Desktop 1: %s", protocol.Deref(moved.Error))
	}

	id = uuid.NewString()
	if reset := profileRequest(app, protocol.DesktopResetHomeMessage{Cmd: protocol.CmdDesktopResetHome, RequestID: id, ProfileID: profile}, id); !reset.Success {
		t.Fatalf("putting Home back: %s", protocol.Deref(reset.Error))
	}
	desktops := orderedDesktops(viewProfile(t, w, profile))
	if desktops[0].ID != home.ID {
		t.Fatalf("after putting Home back the desktops are ordered %v, want Home at the top", desktops)
	}
	tree := desktopTree(t, homeOf(t, desktops))
	if want := []string{"tile-view-attn", "tile-view-sessions", "tile-view-pull-requests", pane}; !slices.Equal(tree.leafIDs(), want) {
		t.Fatalf("Home holds %v after it was put back, want its views as it starts and the shell after them: %v", tree.leafIDs(), want)
	}

	setSetting(t, app, "views_enabled", "false")
	id = uuid.NewString()
	if refused := profileRequest(app, protocol.DesktopResetHomeMessage{Cmd: protocol.CmdDesktopResetHome, RequestID: id, ProfileID: profile}, id); refused.Success {
		t.Fatal("with views off, putting Home back succeeded")
	}
}
