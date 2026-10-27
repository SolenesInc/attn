package daemon_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTerminalMovePreservesPlacementAndDelegationAuthority(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	cwd := w.Path("notes")
	profile := app.SelectedProfile()
	s, home, _ := w.RequestSpawn(app, fakeagent.Codex, cwd)
	tt, _, tPane := w.RequestSpawn(app, fakeagent.Codex, cwd, func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("second")
		m.Placement = &protocol.SessionPlacement{DesktopID: protocol.Ptr(home)}
	})
	id := uuid.NewString()
	ops := mustProfileRequest(app, protocol.DesktopCreateMessage{Cmd: protocol.CmdDesktopCreate, RequestID: id, ProfileID: profile, Name: protocol.Ptr("Ops")}, id).Desktops[0]
	w.Spawn(app, fakeagent.Codex, w.Path("ops-anchor"))
	id = uuid.NewString()
	web := mustProfileRequest(app, protocol.DesktopCreateMessage{Cmd: protocol.CmdDesktopCreate, RequestID: id, ProfileID: profile, Name: protocol.Ptr("Web")}, id).Desktops[0]
	w.Spawn(app, fakeagent.Codex, w.Path("web-anchor"))
	focusAgent(t, w, app, string(s.ID))

	arrangement := func(when string, holds func(map[string]protocol.Desktop) bool) (protocol.ProfileArrangementChangedMessage, map[string]protocol.Desktop) {
		t.Helper()
		var byID map[string]protocol.Desktop
		got := testworld.Await(app, protocol.EventProfileArrangementChanged, func(e protocol.ProfileArrangementChangedMessage) bool {
			byID = map[string]protocol.Desktop{}
			for _, desktop := range e.Desktops {
				byID[desktop.ID] = desktop
			}
			return holds(byID)
		})
		if got.Profile.CurrentDesktopID != home {
			t.Errorf("%s: the profile now shows desktop %s; want %s, where the user was", when, got.Profile.CurrentDesktopID, home)
		}
		return got, byID
	}
	holding := func(desktop protocol.Desktop, session string) bool {
		for _, pane := range desktop.Panes {
			if string(pane.SessionID) == session {
				return true
			}
		}
		return false
	}
	move := func(caller, session, ref string) *protocol.TerminalMoveResult {
		t.Helper()
		response, err := cli.TerminalCommand(context.Background(), protocol.TerminalMoveMessage{Cmd: protocol.CmdTerminalMove, CallerSessionID: protocol.SessionID(caller), Target: session, Desktop: ref})
		if err != nil {
			t.Fatalf("moving %s to %q as %q: %v", session, ref, caller, err)
		}
		return response.TerminalMoveResult
	}

	moved := move(string(s.ID), string(s.ID), fmt.Sprint(protocol.Deref(ops.ShortcutSlot)))
	if moved.DesktopID != ops.ID || protocol.Deref(moved.FromDesktopID) != home {
		t.Errorf("by digit, the move reports %+v; want from %s to Ops %s", moved, home, ops.ID)
	}
	event, desktops := arrangement("a session moving itself onto an empty desktop", func(d map[string]protocol.Desktop) bool { return holding(d[ops.ID], string(s.ID)) })
	if event.MovedLeaf == nil {
		event = testworld.Await(app, protocol.EventProfileArrangementChanged, func(e protocol.ProfileArrangementChangedMessage) bool { return e.MovedLeaf != nil })
	}
	if desktops[ops.ID].ActivePaneID != moved.PaneID {
		t.Errorf("the empty desktop's active pane is %q; want its first pane %s", desktops[ops.ID].ActivePaneID, moved.PaneID)
	}
	if desktops[home].ActivePaneID != tPane {
		t.Errorf("the desktop the user sees now shows %q; want %s, unchanged", desktops[home].ActivePaneID, tPane)
	}
	if m := event.MovedLeaf; m == nil || m.FromDesktopID != home || m.ToDesktopID != ops.ID || m.ToLeafID != moved.PaneID {
		t.Errorf("the broadcast names the moved leaf %+v; want %s to %s as %s", m, home, ops.ID, moved.PaneID)
	}

	moved = move(string(tt.ID), string(tt.ID), "oPs")
	_, desktops = arrangement("the shown pane moving itself by name", func(d map[string]protocol.Desktop) bool { return holding(d[ops.ID], string(tt.ID)) })
	if desktops[ops.ID].ActivePaneID != moved.PaneID {
		t.Errorf("the pane that was shown moved, but Ops shows %q; want it, %s", desktops[ops.ID].ActivePaneID, moved.PaneID)
	}

	move(string(s.ID), string(s.ID), web.ID)
	arrangement("a session moving itself by id", func(d map[string]protocol.Desktop) bool { return holding(d[web.ID], string(s.ID)) })

	request := brief(cwd, "Watch the deploy")
	request.Agent, request.SourceSessionID, request.Label, request.Desktop = protocol.Ptr("codex"), protocol.Ptr(s.ID), protocol.Ptr("watcher"), protocol.Ptr("web")
	delegate, err := cli.Delegate(request)
	if err != nil {
		t.Fatal(err)
	}
	if got := move(string(s.ID), string(delegate.SessionID), "web"); !protocol.Deref(got.Unchanged) {
		t.Errorf("moving a delegate onto the desktop it is on reports %+v; want unchanged", got)
	}
	_, err = cli.TerminalCommand(context.Background(), protocol.TerminalMoveMessage{Cmd: protocol.CmdTerminalMove, CallerSessionID: tt.ID, Target: string(delegate.SessionID), Desktop: "ops"})
	if err == nil || !strings.Contains(err.Error(), "may move itself or an agent in its delegation chain") {
		t.Errorf("a session moving another's delegate was answered %v; want a refusal naming who may move it", err)
	}
	move(string(s.ID), string(delegate.SessionID), "ops")
	_, desktops = arrangement("a dispatcher moving its delegate", func(d map[string]protocol.Desktop) bool { return holding(d[ops.ID], string(delegate.SessionID)) })
	if desktops[ops.ID].ActivePaneID == protocol.Deref(delegate.PaneID) || !holding(desktops[ops.ID], string(tt.ID)) {
		t.Errorf("a pane that was not shown moved onto Ops and took its active pane %q", desktops[ops.ID].ActivePaneID)
	}
	request = brief(cwd, "Check the deploy")
	request.Agent, request.SourceSessionID, request.Label = protocol.Ptr("codex"), protocol.Ptr(delegate.SessionID), protocol.Ptr("checker")
	grandchild, err := cli.Delegate(request)
	if err != nil {
		t.Fatal(err)
	}
	move(string(s.ID), string(grandchild.SessionID), "web")
	arrangement("a dispatcher moving its delegate's delegate", func(d map[string]protocol.Desktop) bool { return holding(d[web.ID], string(grandchild.SessionID)) })

	unplaced := brief(cwd, "Tidy the notes")
	unplaced.Agent, unplaced.Label = protocol.Ptr("codex"), protocol.Ptr("tidier")
	loose, err := cli.Delegate(unplaced)
	if err != nil {
		t.Fatal(err)
	}
	move(string(loose.SessionID), "", "3")
	arrangement("the user placing an unplaced session", func(d map[string]protocol.Desktop) bool { return holding(d[web.ID], string(loose.SessionID)) })

	residentSession, _, resident := w.RequestSpawn(app, fakeagent.Codex, cwd, func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("resident")
		m.Placement = &protocol.SessionPlacement{DesktopID: protocol.Ptr(home)}
	})
	_, sourcePane := placedPane(t, w, string(s.ID))
	id = uuid.NewString()
	mustProfileRequest(app, protocol.DesktopSetActivePaneMessage{Cmd: protocol.CmdDesktopSetActivePane, RequestID: id, DesktopID: web.ID, PaneID: sourcePane}, id)
	move(string(s.ID), string(s.ID), "1")
	_, desktops = arrangement("the shown pane of a background desktop moving onto the one the user sees", func(d map[string]protocol.Desktop) bool {
		return holding(d[home], string(s.ID)) && holding(d[home], string(residentSession.ID))
	})
	if desktops[home].ActivePaneID != resident {
		t.Errorf("a pane arriving on the desktop the user sees took its active pane %q; want %s, unchanged", desktops[home].ActivePaneID, resident)
	}

	_, err = cli.TerminalCommand(context.Background(), protocol.TerminalMoveMessage{Cmd: protocol.CmdTerminalMove, CallerSessionID: s.ID, Target: string(s.ID), Desktop: "nope"})
	if err == nil || !strings.Contains(err.Error(), `unknown desktop "nope"`) || !strings.Contains(err.Error(), "2 Ops") {
		t.Errorf("an unknown desktop was answered %v; want a refusal listing the desktops", err)
	}

	sessions := []string{string(s.ID), string(tt.ID), string(delegate.SessionID), string(grandchild.SessionID), string(loose.SessionID), string(residentSession.ID)}
	for _, id := range sessions {
		closeSessionPane(app, id)
	}
	arrangement("every moved session closed", func(d map[string]protocol.Desktop) bool {
		for _, desktop := range d {
			for _, id := range sessions {
				if holding(desktop, id) {
					return false
				}
			}
		}
		return true
	})
}

func TestTerminalMoveCreatesTheMissingNumberedDesktop(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	profile := app.SelectedProfile()
	cwd := w.Path("notes")
	s, home, _ := w.RequestSpawn(app, fakeagent.Codex, cwd)
	other, _, _ := w.RequestSpawn(app, fakeagent.Codex, cwd, func(m *protocol.SpawnSessionMessage) {
		m.Label = protocol.Ptr("second")
		m.Placement = &protocol.SessionPlacement{DesktopID: protocol.Ptr(home)}
	})

	for _, c := range []struct {
		name    string
		session protocol.SessionID
		ref     string
		slot    int
		created bool
	}{
		{name: "by a missing shortcut digit", session: s.ID, ref: "5", slot: 5, created: true},
		{name: "by a missing numbered desktop id", session: other.ID, ref: profiles.NumberedDesktopID(profile, 6), slot: 6, created: true},
		{name: "by the digit of the desktop it just created", session: other.ID, ref: "5", slot: 5},
	} {
		t.Run(c.name, func(t *testing.T) {
			response, err := cli.TerminalCommand(context.Background(), protocol.TerminalMoveMessage{Cmd: protocol.CmdTerminalMove, CallerSessionID: c.session, Target: string(c.session), Desktop: c.ref})
			if err != nil {
				t.Fatalf("moving %s to %q: %v", c.session, c.ref, err)
			}
			result := response.TerminalMoveResult
			want := profiles.NumberedDesktopID(profile, c.slot)
			if result.DesktopID != want || protocol.Deref(result.DesktopCreated) != c.created {
				t.Errorf("the move reports desktop %s, created %v; want %s, created %v", result.DesktopID, protocol.Deref(result.DesktopCreated), want, c.created)
			}
			got := testworld.Await(app, protocol.EventProfileArrangementChanged, func(e protocol.ProfileArrangementChangedMessage) bool {
				for _, desktop := range e.Desktops {
					if desktop.ID == want && protocol.Deref(desktop.ShortcutSlot) == c.slot && slices.ContainsFunc(desktop.Panes, func(p protocol.DesktopPane) bool { return p.SessionID == c.session }) {
						return true
					}
				}
				return false
			})
			if got.Profile.CurrentDesktopID != home {
				t.Errorf("the profile now shows desktop %s; want %s, where the user was", got.Profile.CurrentDesktopID, home)
			}
		})
	}
}

func TestTerminalMoveReachesShellsAcrossDesktopsAndHonorsProfiles(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	caller := protocol.SessionID(w.Spawn(app, fakeagent.Claude, w.Path("caller")))
	opener := protocol.SessionID(w.Spawn(app, fakeagent.Claude, w.Path("opener")))
	call := func(message any) *protocol.Response {
		t.Helper()
		r, err := cli.TerminalCommand(context.Background(), message)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	shell := call(protocol.TerminalOpenMessage{Cmd: protocol.CmdTerminalOpen, CallerSessionID: opener}).TerminalOpenResult
	moved := call(protocol.TerminalMoveMessage{Cmd: protocol.CmdTerminalMove, CallerSessionID: caller, Target: string(shell.TerminalID), Desktop: "8"}).TerminalMoveResult
	if moved.TerminalID != shell.TerminalID || moved.DesktopID != profiles.NumberedDesktopID(app.SelectedProfile(), 8) {
		t.Fatalf("another agent's shell move: %+v", moved)
	}
	for _, message := range []any{
		protocol.TerminalInputMessage{Cmd: protocol.CmdTerminalInput, CallerSessionID: caller, Target: string(shell.TerminalID), Data: "never typed"},
		protocol.TerminalCloseMessage{Cmd: protocol.CmdTerminalClose, CallerSessionID: caller, Target: string(shell.TerminalID)},
	} {
		_, err := cli.TerminalCommand(context.Background(), message)
		if err == nil || !strings.Contains(err.Error(), "--desktop "+moved.DesktopID) {
			t.Fatalf("typing and closing still need the desktop: %v", err)
		}
	}
	call(protocol.TerminalMoveMessage{Cmd: protocol.CmdTerminalMove, CallerSessionID: caller, Target: string(shell.TerminalID), Desktop: "1"})
	chief := protocol.SessionID(w.Spawn(app, fakeagent.Claude, w.Path("chief"), func(m *protocol.SpawnSessionMessage) { m.ChiefOfStaff = protocol.Ptr(true) }))
	call(protocol.TerminalMoveMessage{Cmd: protocol.CmdTerminalMove, CallerSessionID: chief, Target: string(caller), Desktop: "8"})
	id := uuid.NewString()
	profile := mustProfileRequest(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: id, Name: "Other"}, id).Profile
	otherApp := w.AppOn(profile.ID)
	foreign := w.Spawn(otherApp, fakeagent.Claude, w.Path("foreign"))
	_, err := cli.TerminalCommand(context.Background(), protocol.TerminalMoveMessage{Cmd: protocol.CmdTerminalMove, CallerSessionID: caller, Target: otherApp.Terminal(foreign), Desktop: "8"})
	if err == nil || !strings.Contains(err.Error(), "not found in the caller's profile") {
		t.Fatalf("foreign terminal must not be found: %v", err)
	}
}
