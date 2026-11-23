package daemon_test

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
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

func TestRootAndSubagentUsageGrowingInOneTickTellTheAppOnce(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		agent := w.bubbleClaude(t, app, "shop")
		subagent := filepath.Join(strings.TrimSuffix(agent.transcript.Path, ".jsonl"), "subagents", "agent-research.jsonl")
		if err := os.MkdirAll(filepath.Dir(subagent), 0o755); err != nil {
			t.Fatal(err)
		}
		appendLegacyCostUsage(t, subagent, "research-1", "claude-opus-4-8", 3, 4)
		appendLegacyCostUsage(t, agent.transcript.Path, "root-1", "claude-opus-4-8", 5, 6)
		w.advance(time.Second)
		if usage := queriedSession(t, w.Client(), agent.id).Usage; usage == nil || usage.TotalTokens != 18 {
			t.Fatalf("usage = %+v, want 18 tokens from the root and its subagent", usage)
		}

		facts := producedBy(busStatus(t, app), "session.cost.changed")
		appendLegacyCostUsage(t, subagent, "research-2", "claude-opus-4-8", 1, 2)
		appendLegacyCostUsage(t, agent.transcript.Path, "root-2", "claude-opus-4-8", 7, 8)
		w.advance(time.Second)
		if usage := queriedSession(t, w.Client(), agent.id).Usage; usage == nil || usage.TotalTokens != 36 {
			t.Fatalf("usage = %+v, want 36 tokens after both transcripts grew", usage)
		}
		if again := producedBy(busStatus(t, app), "session.cost.changed"); again != facts+1 {
			t.Errorf("root and subagent usage growing together published %d session.cost.changed facts, want one", again-facts)
		}
	})
}

func TestAnAppServerSessionsUsageAndCostComeFromItsServerAndCountOnce(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		session, view, server := appServerSession(t, w, app, h)
		promptUntilWorking(app, session, view, server, "add a discount field")
		usage := awaitServerFeed(app, session, view.ConversationID, server, 1200)
		if len(usage.Models) != 1 || usage.Models[0].Purpose != "agent" || usage.CostUsd == nil {
			t.Errorf("usage = %+v, want one priced agent row", usage)
		}
		if usage := awaitServerFeed(app, session, view.ConversationID, server, 1500); len(usage.Models) != 1 {
			t.Errorf("usage after the server's total grew = %+v, want the one row replaced, not added to", usage)
		}
	})
}

func TestAnAppServerSessionsSubagentsReportFilesPullRequestsAndSpendOnceAcrossRestarts(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		session, view, server := appServerSession(t, w, app, h)
		conversation := view.ConversationID
		parentDoc, plan, notes := markdownFile(t, w, "parent.md"), markdownFile(t, w, "plan.md"), markdownFile(t, w, "notes.md")
		promptUntilWorking(app, session, view, server, "delegate the plan and pull request")
		server.ToolCall(conversation, writeMarkdownCall(parentDoc))
		awaitServerFeed(app, session, conversation, server, 1000)

		sub, nested := "sub-1", "sub-2"
		server.ForkedSubagent(conversation, sub, "write the plan")
		server.Subagent(sub, nested, "write notes")
		server.ToolCall(sub, writeMarkdownCall(plan))
		server.ToolCall(sub, fakeagent.ServerToolCall{Name: "bash", Input: map[string]any{"command": "gh pr create --fill"}, Output: shopPull(7) + "\n"})
		server.SetUsage(sub, "anthropic/claude-haiku-4-5", serverUsage(400))
		server.ToolCall(nested, writeMarkdownCall(notes))
		server.SetUsage(nested, "openai/gpt-5.5", serverUsage(200))

		usage := awaitUsageTokens(app, session, 1600)
		if usage.CostUsd == nil {
			t.Errorf("session usage with subagents = %+v, want priced cost", usage)
		}
		now := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return slices.Contains(pullNumbers(s), 7) })
		if got := pullNumbers(now); !slices.Contains(got, 7) {
			t.Fatalf("pull requests = %v, want 7 from subagent", got)
		}
		if got := recentCount(app, parentDoc); got != 1 {
			t.Fatalf("parent.md inherited by forked subagent counts %d, want 1", got)
		}
		if got := recentCount(app, plan); got != 1 {
			t.Fatalf("plan.md written by subagent counts %d, want 1", got)
		}
		if got := recentCount(app, notes); got != 1 {
			t.Fatalf("notes.md written by nested subagent counts %d, want 1", got)
		}

		launched := w.AppServers(h)
		w.restart()
		app = w.App()
		rejoinAppServer(t, w, h)
		server = w.AppServerAfter(h, launched)
		w.Launched(session)

		server.ToolCall(sub, writeMarkdownCall(plan))
		server.ToolCall(sub, fakeagent.ServerToolCall{Name: "bash", Input: map[string]any{"command": "gh pr create --fill"}, Output: shopPull(8) + "\n"})
		server.SetUsage(sub, "anthropic/claude-haiku-4-5", serverUsage(550))

		awaitUsageTokens(app, session, 1750)
		now = testworld.AwaitSession(app, session, func(s protocol.Session) bool { return slices.Contains(pullNumbers(s), 8) })
		if got := pullNumbers(now); !slices.Contains(got, 7) || !slices.Contains(got, 8) {
			t.Errorf("pull requests after restart = %v, want 7 and 8", got)
		}
		if got := recentCount(app, plan); got != 2 {
			t.Errorf("plan.md after daemon restart and second subagent edit counts %d, want 2", got)
		}
		if got := recentCount(app, notes); got != 1 {
			t.Errorf("notes.md after daemon restart counts %d, want 1", got)
		}
	})
}

func TestAResumedAppServerSessionSkipsPreLaunchSubagentHistoryAndCountsOnlyNewSubagentSpend(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app := w.App()
		first, _, server := appServerSession(t, w, app, h)
		oldFile, newFile := markdownFile(t, w, "old.md"), markdownFile(t, w, "new.md")
		server.StartedElsewhere("earlier-root", w.Path("shop"), "old work")
		server.Subagent("earlier-root", "earlier-sub", "old subagent")
		server.ToolCall("earlier-sub", writeMarkdownCall(oldFile))
		server.SetUsage("earlier-sub", "anthropic/claude-haiku-4-5", serverUsage(500))

		subBaselined := w.PauseAt(pausepoint.ServerSubagentFeedBaselined)
		resume := protocol.SpawnSessionMessage{
			Cmd: protocol.CmdSpawnSession, ID: protocol.SessionID(uuid.NewString()), Agent: string(h),
			Cwd: w.Path("shop"), ProfileID: app.SelectedProfile(), Placement: &protocol.SessionPlacement{}, Cols: 100, Rows: 30,
			ResumeSessionID: protocol.Ptr("earlier-root"),
		}
		app.Send(resume)
		resumed := testworld.Await(app, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == resume.ID })
		if !resumed.Success {
			t.Fatalf("resume earlier-root: %s", protocol.Deref(resumed.Error))
		}
		session := string(resumed.ID)
		subBaselined.Await()
		subBaselined.Release()
		promptUntilWorking(app, session, w.Launched(session), server, "continue")

		server.ForkedSubagent("earlier-root", "fresh-sub", "new subagent")
		server.ToolCall("fresh-sub", writeMarkdownCall(newFile))
		server.SetUsage("fresh-sub", "anthropic/claude-haiku-4-5", serverUsage(200))
		server.SetUsage("earlier-sub", "anthropic/claude-haiku-4-5", serverUsage(650))

		awaitUsageTokens(app, session, 350)
		if got := recentCount(app, newFile); got != 1 {
			t.Errorf("new.md written after resume counts %d, want 1", got)
		}
		if got := recentCount(app, oldFile); got != 0 {
			t.Errorf("old.md written before resume counts %d, want 0", got)
		}

		launched := w.AppServers(h)
		w.restart()
		app = w.App()
		rejoinAppServer(t, w, h)
		server = w.AppServerAfter(h, launched)
		w.Launched(first)
		w.Launched(session)

		server.SetUsage("earlier-sub", "anthropic/claude-haiku-4-5", serverUsage(700))
		awaitUsageTokens(app, session, 400)
	})
}
