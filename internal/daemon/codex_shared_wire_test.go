package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASharedCodexLaunchIsOneSessionThatWorksWaitsAndCountsItsUsage(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	if !slices.Contains(codex.Argv, "--remote") {
		t.Fatalf("the shared launch ran %q, want Codex connected to an app-server", codex.Argv)
	}

	app.TypeLine(session, "find the flaky checkout test")
	if got := codex.Prompted(); got != "find the flaky checkout test" {
		t.Fatalf("codex received %q", got)
	}
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	question := "It races the tax lookup. Add a lock? <!-- attn:state=waiting_input -->"
	codex.Reply(question)
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
	if usage := awaitUsageTokens(app, session, claudeTokens(question)); usage.MeasurementIncomplete != nil {
		t.Errorf("usage = %+v, want it complete", usage)
	}

	var codexSessions []string
	for _, s := range w.App().Initial.Sessions {
		if s.Agent == protocol.SessionAgentCodex {
			codexSessions = append(codexSessions, string(s.ID))
		}
	}
	if !slices.Equal(codexSessions, []string{session}) {
		t.Errorf("Codex sessions = %v, want only %s", codexSessions, session)
	}
	if servers := w.CodexServers(); len(servers) != 1 {
		t.Errorf("app-servers launched = %v, want one", servers)
	}
}

func TestWithSharedCodexOffCodexRunsAloneInItsTerminal(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	app.TypeLine(session, "find the flaky checkout test")
	codex.Prompted()
	codex.Reply("Found it. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if slices.Contains(codex.Argv, "--remote") {
		t.Errorf("Codex ran %q, want it on its own", codex.Argv)
	}
	if servers := w.CodexServers(); len(servers) != 0 {
		t.Errorf("app-servers launched = %v, want none", servers)
	}
}

func TestASharedCodexAppServerThatExitsStartsAgainAndTheTerminalCarriesOn(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	conversation := codex.ConversationID

	codex.CrashAppServer()
	app.TypeLine(session, "add a discount field")
	if got := codex.Prompted(); got != "add a discount field" {
		t.Fatalf("codex received %q after its app-server exited", got)
	}
	if codex.ConversationID != conversation {
		t.Fatalf("the terminal shows conversation %s, want %s", codex.ConversationID, conversation)
	}
	codex.Reply("Added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if servers := w.CodexServers(); len(servers) != 2 {
		t.Errorf("app-servers launched = %v, want the first and the one that replaced it", servers)
	}
}

func TestASharedCodexTurnTheAppServerTookDownEnds(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	app.TypeLine(session, "add a discount field")
	codex.Prompted()
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	watcher := w.App()

	codex.CrashAppServer()
	testworld.AwaitSession(watcher, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
}
