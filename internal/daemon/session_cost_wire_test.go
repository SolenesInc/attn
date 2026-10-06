package daemon_test

import (
	"math"
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
		m.ID = protocol.SessionID(session)
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

func TestCodexFastPricingSurvivesTogglesAndRestart(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	reply := "Ready. <!-- attn:state=idle -->"
	var tokens int
	var want float64
	for _, tier := range []string{"priority", "default", "fast"} {
		codex.CodexSettings("gpt-6.1-sol", tier)
		app.TypeLine(session, "continue")
		codex.Prompted()
		codex.Subagent(reply)
		codex.Reply(reply)
		tokens += 4 * len(reply)
		multiplier := 1.0
		if tier != "default" {
			multiplier = 2
		}
		want += float64(len(reply)) * 24 * multiplier / 1e6
		usage := awaitUsageTokens(app, session, tokens)
		if usage.CostUsd == nil || math.Abs(*usage.CostUsd-want) > 1e-12 || len(usage.Models) != 1 || usage.HasUnpricedUsage {
			t.Fatalf("usage = %+v, want one fully priced row costing %.9f", usage, want)
		}
	}
	w.restart()
	usage := queriedSession(t, w.Client(), session).Usage
	if usage == nil || usage.TotalTokens != tokens || usage.CostUsd == nil || math.Abs(*usage.CostUsd-want) > 1e-12 {
		t.Fatalf("restored usage = %+v, want %d tokens costing %.9f", usage, tokens, want)
	}
}
