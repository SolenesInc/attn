package daemon_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestADelegateLandsOnTheDesktopItsCallerNames(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	cwd := w.Path("notes")
	sourceResult, sourceDesktop, sourcePane := w.RequestSpawn(app, fakeagent.Codex, cwd)
	source := sourceResult.ID
	profile := app.SelectedProfile()
	id := "create-ops"
	ops := mustProfileRequest(app, protocol.DesktopCreateMessage{Cmd: protocol.CmdDesktopCreate, RequestID: id, ProfileID: profile, Name: protocol.Ptr("Ops")}, id).Desktops[0]
	w.Spawn(app, fakeagent.Codex, w.Path("ops-anchor"))
	unnamed := createDesktop(app, profile)
	w.Spawn(app, fakeagent.Codex, w.Path("unnamed-anchor"))
	focusAgent(t, w, app, string(source))
	side := createProfile(app, "Side")

	delegate := func(name, desktop string) (*protocol.DelegateResult, error) {
		request := brief(cwd, "Watch the deploy")
		request.Agent = protocol.Ptr("codex")
		request.SourceSessionID = protocol.Ptr(source)
		request.Label = protocol.Ptr(name)
		request.Desktop = protocol.Ptr(desktop)
		return cli.Delegate(request)
	}

	for i, row := range []struct {
		name, ref, want string
		wantPanes       int
	}{
		{name: "by shortcut digit onto another desktop", ref: fmt.Sprint(protocol.Deref(ops.ShortcutSlot)), want: ops.ID, wantPanes: 2},
		{name: "by name, in any case, beside the active pane", ref: "oPS", want: ops.ID, wantPanes: 3},
		{name: "by id", ref: unnamed.ID, want: unnamed.ID, wantPanes: 2},
		{name: "by the label an unnamed desktop shows", ref: fmt.Sprintf("desktop %d", protocol.Deref(unnamed.ShortcutSlot)), want: unnamed.ID, wantPanes: 3},
		{name: "by the caller's own desktop, beside the caller", ref: "1", want: sourceDesktop, wantPanes: 2},
	} {
		result, err := delegate(fmt.Sprintf("d%d", i), row.ref)
		if err != nil {
			t.Fatalf("%s: %v", row.name, err)
		}
		if got := protocol.Deref(result.DesktopID); got != row.want {
			t.Fatalf("%s: the delegate landed on desktop %s; want %s", row.name, got, row.want)
		}
		pane := protocol.Deref(result.PaneID)
		arrangement := testworld.Await(app, protocol.EventProfileArrangementChanged, func(e protocol.ProfileArrangementChangedMessage) bool {
			for _, desktop := range e.Desktops {
				if desktop.ID == row.want && strings.Contains(desktop.TreeJson, pane) {
					return true
				}
			}
			return false
		})
		for _, desktop := range arrangement.Desktops {
			if desktop.ID == row.want && len(desktop.Panes) != row.wantPanes {
				t.Errorf("%s: desktop %s holds %d panes; want %d", row.name, desktop.ID, len(desktop.Panes), row.wantPanes)
			}
			if desktop.ID == sourceDesktop && desktop.ActivePaneID != sourcePane {
				t.Errorf("%s: the caller's desktop now shows pane %s; want the caller's %s", row.name, desktop.ActivePaneID, sourcePane)
			}
		}
		if arrangement.Profile.CurrentDesktopID != sourceDesktop {
			t.Errorf("%s: the profile now shows desktop %s; want the caller's %s", row.name, arrangement.Profile.CurrentDesktopID, sourceDesktop)
		}
	}

	for _, row := range []struct {
		name, ref string
		want      []string
	}{
		{name: "an unknown name", ref: "nope", want: []string{`unknown desktop "nope"`, "1 Desktop 1", "2 Ops (" + ops.ID + ")"}},
		{name: "a desktop of another profile", ref: side.CurrentDesktopID, want: []string{`belongs to profile "Side"`, "2 Ops"}},
	} {
		_, err := delegate("refused", row.ref)
		if err == nil {
			t.Errorf("%s: the delegation was accepted; want it refused", row.name)
			continue
		}
		for _, want := range row.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the refusal reads %q; want it to name %q", row.name, err, want)
			}
		}
	}
}

func TestALaunchOntoAMissingDesktopDropsTheAnchorAndRecreatesOnlyNumberedTargets(t *testing.T) {
	for _, numbered := range []bool{true, false} {
		t.Run(fmt.Sprintf("numbered=%t", numbered), func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app := w.App()
			_, current, sourcePane := w.RequestSpawn(app, fakeagent.Codex, w.Path("source"))
			target, want := "desktop-missing", current
			if numbered {
				target = profiles.NumberedDesktopID(app.SelectedProfile(), 7)
				want = target
			}
			spawned, desktopID, _ := w.RequestSpawn(app, fakeagent.Codex, w.Path("launched"), func(m *protocol.SpawnSessionMessage) {
				m.Placement = &protocol.SessionPlacement{DesktopID: protocol.Ptr(target), AnchorPaneID: protocol.Ptr("missing-anchor")}
			})
			if !spawned.Success || desktopID != want {
				t.Fatalf("spawn = %+v on %s; want success on %s", spawned, desktopID, want)
			}
			arrangement := viewProfile(t, w, app.SelectedProfile())
			if arrangement.profile.CurrentDesktopID != current {
				t.Errorf("current desktop = %s; want %s", arrangement.profile.CurrentDesktopID, current)
			}
			for _, desktop := range arrangement.desktops {
				if desktop.ID == current && desktop.ActivePaneID != sourcePane {
					t.Errorf("current desktop shows %s; want caller %s", desktop.ActivePaneID, sourcePane)
				}
			}
		})
	}
}

func TestADelegateRecreatesAMissingNumberedDesktop(t *testing.T) {
	for _, byID := range []bool{false, true} {
		t.Run(fmt.Sprintf("by-id=%t", byID), func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app := w.App()
			cwd := w.Path("notes")
			source, current, sourcePane := w.RequestSpawn(app, fakeagent.Codex, cwd)
			target := profiles.NumberedDesktopID(app.SelectedProfile(), 7)
			for _, desktop := range w.App().Initial.Desktops {
				if desktop.ID == target {
					t.Fatal("desktop 7 already exists before delegation")
				}
			}
			request := brief(cwd, "Watch the deploy")
			request.Agent = protocol.Ptr("codex")
			request.SourceSessionID = protocol.Ptr(source.ID)
			request.Desktop = protocol.Ptr("7")
			if byID {
				request.Desktop = protocol.Ptr(target)
				request.Label = protocol.Ptr("Watcher")
			}
			result, err := w.Client().Delegate(request)
			if err != nil {
				t.Fatal(err)
			}
			if protocol.Deref(result.DesktopID) != target {
				t.Fatalf("delegate landed on %s; want recreated %s", protocol.Deref(result.DesktopID), target)
			}
			arrangement := viewProfile(t, w, app.SelectedProfile())
			if arrangement.profile.CurrentDesktopID != current {
				t.Fatalf("current desktop = %s; want %s", arrangement.profile.CurrentDesktopID, current)
			}
			desktop := arrangement.desktops[target]
			if protocol.Deref(desktop.ShortcutSlot) != 7 || len(desktop.Panes) != 1 || desktop.Panes[0].SessionID != result.SessionID {
				t.Errorf("recreated desktop = %+v; want slot 7 holding the delegate", desktop)
			}
			caller := arrangement.desktops[current]
			if len(caller.Panes) != 1 || caller.ActivePaneID != sourcePane {
				t.Errorf("caller desktop = %+v; want only the caller, still active", caller)
			}
		})
	}
}
