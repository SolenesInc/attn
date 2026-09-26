package main_test

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestMain(m *testing.M) {
	if os.Getenv("ATTN_APP_STOP_HELPER_READY") != "" {
		fakeagent.Main()
		config.ScopeTestEnvironment(os.Getenv("ATTN_DATA_DIR"))
		os.Exit(m.Run())
	}
	os.Exit(testworld.Main(m))
}

func labelled(name string) func(*protocol.SpawnSessionMessage) {
	return func(m *protocol.SpawnSessionMessage) { m.Label = protocol.Ptr(name) }
}

func settle(app *testworld.Peer, id string) {
	app.T.Helper()
	testworld.AwaitSession(app, id, func(x protocol.Session) bool { return protocol.Deref(x.TurnOwed) })
	app.Send(protocol.SettleTurnMessage{Cmd: protocol.CmdSettleTurn, SessionID: id})
	testworld.AwaitSession(app, id, func(x protocol.Session) bool {
		return x.State == protocol.SessionStateIdle && !protocol.Deref(x.TurnOwed)
	})
}

func TestAgentListShowsSessionsByWorkspaceThenLabelWithTheOwedTurn(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, fakeagent.Codex))
	s.Start()

	if got := s.Attn("agent", "list").Stdout; !strings.Contains(got, "No sessions on this daemon.") {
		t.Fatalf("agent list on an empty daemon printed %q", got)
	}

	app := s.App()
	zeta := s.Spawn(app, fakeagent.Claude, s.Path("shop"), labelled("zeta"))
	alpha := s.Spawn(app, fakeagent.Codex, s.Path("shop"), labelled("alpha"))
	notes := s.Spawn(app, fakeagent.Claude, s.Path("blog"), labelled("notes"))
	s.Launched(alpha)
	s.Launched(notes)
	settle(app, alpha)
	settle(app, notes)
	asking := s.Launched(zeta)
	app.TypeLine(zeta, "add a discount field")
	asking.Prompted()
	asking.Reply("Before or after tax? <!-- attn:state=waiting_input -->")
	testworld.AwaitSession(app, zeta, func(x protocol.Session) bool {
		return x.State == protocol.SessionStateWaitingInput && protocol.Deref(x.TurnOwed)
	})

	var rows []struct {
		ID        string  `json:"id"`
		Label     string  `json:"label"`
		Agent     string  `json:"agent"`
		Workspace string  `json:"workspace"`
		TurnOwed  bool    `json:"turn_owed"`
		Member    *string `json:"member"`
	}
	s.Attn("agent", "list", "--json").JSON(t, &rows)
	var got []string
	for _, r := range rows {
		if r.Member == nil {
			t.Fatalf("row %s has no member key", r.ID)
		}
		got = append(got, fmt.Sprintf("%s/%s/%s/%t", r.Workspace, r.Label, r.Agent, r.TurnOwed))
	}
	want := []string{"blog/notes/claude/false", "shop/alpha/codex/false", "shop/zeta/claude/true"}
	if !slices.Equal(got, want) {
		t.Fatalf("agent list --json = %q, want %q", got, want)
	}

	table := s.Attn("agent", "list").Stdout
	for _, id := range []string{zeta, alpha, notes} {
		if !strings.Contains(table, id[:8]) || strings.Contains(table, id) {
			t.Fatalf("table should show %s by its short id:\n%s", id, table)
		}
	}
	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(line, zeta[:8]) != strings.HasSuffix(line, "owed") {
			t.Fatalf("table should mark only zeta's turn as owed:\n%s", table)
		}
	}
}

func TestASessionKeepsItsAgentAcrossADaemonRestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	id := s.Spawn(s.App(), fakeagent.Claude, s.Path("shop"))
	claude := s.Launched(id)

	watch := s.Launch(testworld.Invocation{Args: []string{"ticket", "inbox", "--watch", "--interval", "20ms"}, Session: id})
	s.Stop()
	watch.AwaitStderr("ticket inbox --watch:")
	s.Start()

	app := s.App()
	app.TypeLine(id, "still there?")
	if got := claude.Prompted(); got != "still there?" {
		t.Fatalf("claude received %q", got)
	}
}
