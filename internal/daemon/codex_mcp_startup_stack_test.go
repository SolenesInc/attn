package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASharedCodexTileThatRejoinsItsLoadedConversationAfterARestartSettlesThoughItMissedPartOfAnMCPStartup(t *testing.T) {
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	session := s.Spawn(app, fakeagent.Codex, s.Path("shop"))
	codex := s.Launched(session)
	app.TypeLine(session, "find the flaky checkout test")
	codex.Prompted()
	codex.Reply("It races the tax lookup. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })

	server := s.CodexServer()
	server.ReportMCPStartup(codex.ConversationID, "oven", "starting")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	server.ReportMCPStartup(codex.ConversationID, "oven", "ready")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })

	codex.HoldRejoin()
	s.Stop()
	s.Start()
	app = s.App()
	server = s.CodexServer()
	if sent := submitSessionAnnotationFeedback(app, session, "add a discount field"); sent.status != "delivered" {
		t.Fatalf("a message before the tile rejoined: %+v", sent)
	}
	if got := server.Prompted(codex.ConversationID); got != "add a discount field" {
		t.Fatalf("codex received %q", got)
	}
	server.ReportMCPStartup(codex.ConversationID, "oven", "ready")
	codex.ReleaseRejoin()
	server.ReportMCPStartup(codex.ConversationID, "codex_apps", "ready")
	codex.Reply("Added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })

	app.TypeLine(session, "add a tax field")
	if got := codex.Prompted(); got != "add a tax field" {
		t.Fatalf("codex received %q", got)
	}
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateWorking })
	codex.Reply("Added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(x protocol.Session) bool { return x.State == protocol.SessionStateIdle })
}
