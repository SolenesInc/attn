package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSessionUsageCountsEveryMessageOnceAcrossARestart(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	session := w.Spawn(app, fakeagent.Claude, cwd)
	claude := w.Launched(session)
	app.TypeLine(session, "add a discount field to checkout")
	claude.Prompted()
	research := "Checkout applies tax after discounts."
	claude.Subagent(research)
	draft := "Checking"
	claude.Stream(draft)
	awaitUsageTokens(app, session, claudeTokens(research)+claudeTokens(draft))

	question := "Before or after tax? <!-- attn:state=waiting_input -->"
	claude.Reply(question)
	counted := claudeTokens(research) + claudeTokens(question)
	usage := awaitUsageTokens(app, session, counted)
	if len(usage.Models) != 1 || usage.Models[0].Purpose != "agent" || usage.MeasurementIncomplete != nil {
		t.Errorf("usage = %+v, want one complete agent row", usage)
	}

	w.restart()
	app = w.App()
	if restored := queriedSession(t, w.Client(), session).Usage; restored == nil || restored.TotalTokens != counted {
		t.Fatalf("usage after the restart = %+v, want %d tokens", restored, counted)
	}

	w.Spawn(app, fakeagent.Claude, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ID = session
		m.ResumeSessionID = protocol.Ptr(session)
	})
	resumed := w.Launched(session)
	app.TypeLine(session, "after tax")
	resumed.Prompted()
	applied := "Applied after tax. <!-- attn:state=idle -->"
	resumed.Reply(applied)
	counted += claudeTokens(applied)
	if usage := awaitUsageTokens(app, session, counted); usage.MeasurementIncomplete != nil {
		t.Errorf("usage after resuming = %+v, want it still complete", usage)
	}
}

func TestSessionUsageStaysFlaggedIncompleteAcrossARestartOnceATranscriptIsLost(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	claude := w.Launched(session)
	app.TypeLine(session, "find the flaky checkout test")
	claude.Prompted()
	research := "The flaky test races the tax lookup."
	found := "It races the tax lookup. <!-- attn:state=idle -->"
	claude.Subagent(research)
	claude.Reply(found)
	awaitUsageTokens(app, session, claudeTokens(research)+claudeTokens(found))

	claude.DeleteSubagentTranscripts()
	app.TypeLine(session, "fix it")
	claude.Prompted()
	claude.Reply("Fixed. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.Usage != nil && protocol.Deref(s.Usage.MeasurementIncomplete)
	})

	w.restart()
	if usage := queriedSession(t, w.Client(), session).Usage; usage == nil || !protocol.Deref(usage.MeasurementIncomplete) {
		t.Fatalf("usage after the restart = %+v, want it still flagged incomplete", usage)
	}
}

func awaitUsageTokens(app *testworld.Peer, session string, tokens int) *protocol.SessionUsage {
	return testworld.AwaitSession(app, session, func(s protocol.Session) bool {
		return s.Usage != nil && s.Usage.TotalTokens == tokens
	}).Usage
}

func claudeTokens(text string) int {
	return 2 * len(text)
}
