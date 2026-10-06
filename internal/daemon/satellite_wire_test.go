package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAShellSplitFromAnAgentOnItsDesktopBecomesItsSatellite(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	cwd := w.Path("api")
	shell := fakeagent.Harness(protocol.SessionAgentShell)
	var shells []string
	spawnOn := func(h fakeagent.Harness, dir, base, desktop string) string {
		id := w.Spawn(app, h, dir, func(m *protocol.SpawnSessionMessage) {
			if base != "" {
				m.SpawnedFrom = protocol.Ptr(protocol.SessionID(base))
			}
			if desktop != "" {
				m.Placement = &protocol.SessionPlacement{DesktopID: protocol.Ptr(desktop)}
			}
		})
		if h == shell {
			shells = append(shells, id)
		} else {
			w.Launched(id)
		}
		return id
	}
	spawnFrom := func(h fakeagent.Harness, dir, base string) string { return spawnOn(h, dir, base, "") }
	agent := spawnFrom(fakeagent.Claude, cwd, "")
	satellite := spawnFrom(shell, cwd, agent)
	loneShell := spawnFrom(shell, cwd, "")

	cases := []struct {
		name       string
		session    string
		wantParent string
	}{
		{"a shell split from an agent", satellite, agent},
		{"a shell split from that agent's satellite", spawnFrom(shell, cwd, satellite), agent},
		{"a shell with no base", loneShell, ""},
		{"a shell split from a shell that has no agent", spawnFrom(shell, cwd, loneShell), ""},
		{"a shell split from a session that is gone", spawnFrom(shell, cwd, "session-long-gone"), ""},
		{"an agent split from an agent", spawnFrom(fakeagent.Claude, cwd, agent), ""},
	}
	created := testworld.Request(app, protocol.DesktopCreateMessage{Cmd: protocol.CmdDesktopCreate, ProfileID: app.SelectedProfile(), RequestID: "other-desktop"},
		protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "other-desktop" })
	if !created.Success || len(created.Desktops) == 0 {
		t.Fatalf("create another desktop: %+v", created)
	}
	otherDesktop := created.Desktops[len(created.Desktops)-1].ID
	spawnOn(shell, w.Path("other"), "", otherDesktop)

	cases = append(cases, struct{ name, session, wantParent string }{"a shell split from an agent onto another desktop", spawnOn(shell, w.Path("web"), agent, otherDesktop), ""})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			announced := testworld.AwaitSession(app, c.session, func(protocol.Session) bool { return true })
			if got := protocol.Deref(announced.ParentSessionID); string(got) != c.wantParent {
				t.Errorf("the app was told parent %q, want %q", got, c.wantParent)
			}
			queried := queriedSession(t, cli, c.session)
			if got := protocol.Deref(queried.ParentSessionID); string(got) != c.wantParent {
				t.Errorf("the CLI lists parent %q, want %q", got, c.wantParent)
			}
			if c.wantParent != "" && protocol.Deref(queried.TurnOwed) {
				t.Error("the satellite owes a turn")
			}
		})
	}
	for _, id := range shells {
		app.TypeLine(id, "exit")
		testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == id })
	}
}
