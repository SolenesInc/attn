package daemon_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
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
	unnamed := createDesktop(app, profile)
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
		{name: "by shortcut digit onto an empty desktop", ref: fmt.Sprint(protocol.Deref(ops.ShortcutSlot)), want: ops.ID, wantPanes: 1},
		{name: "by name, in any case, beside the active pane", ref: "oPS", want: ops.ID, wantPanes: 2},
		{name: "by id", ref: unnamed.ID, want: unnamed.ID, wantPanes: 1},
		{name: "by the label an unnamed desktop shows", ref: fmt.Sprintf("desktop %d", protocol.Deref(unnamed.ShortcutSlot)), want: unnamed.ID, wantPanes: 2},
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
		{name: "a digit no desktop holds", ref: "8", want: []string{"no desktop holds shortcut 8", "2 Ops"}},
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
