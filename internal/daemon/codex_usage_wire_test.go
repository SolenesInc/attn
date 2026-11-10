package daemon_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
)

func TestCodexUsageCountsNestedSubagentsAndGuardianReviewsAndANewConversationCountsItsOwn(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	app.TypeLine(session, "find the flaky checkout test")
	codex.Prompted()
	research, deeper := "The flaky test races the tax lookup.", "The lookup has no lock."
	codex.Subagent(research)
	codex.Subagent(deeper)
	review := "Approve the lock change."
	codex.GuardianReview(review)
	found := "It races the tax lookup. <!-- attn:state=idle -->"
	codex.Reply(found)
	counted := claudeTokens(research) + claudeTokens(deeper) + claudeTokens(review) + claudeTokens(found)
	awaitUsageTokens(app, session, counted)

	rollout := codexRollout(t, w, codex.ConversationID)
	before, err := os.ReadFile(rollout)
	if err != nil {
		t.Fatal(err)
	}
	terminal := app.Terminal(session)
	app.TypeLine(session, "/new")
	if got := codex.Prompted(); got != "/new" {
		t.Fatalf("codex received %q, want /new", got)
	}
	typeInto(app, terminal, "now fix it\r")
	codex.Prompted()
	next := awaitSuccessor(app, session)
	fixed := "Fixed with a lock. <!-- attn:state=idle -->"
	codex.Reply(fixed)
	if usage := awaitUsageTokens(app, string(next.ID), claudeTokens(fixed)); usage.MeasurementIncomplete != nil {
		t.Errorf("usage of the session /new opened = %+v, want it complete", usage)
	}
	if after, err := os.ReadFile(rollout); err != nil || !bytes.Equal(after, before) {
		t.Errorf("the previous conversation's rollout after /new = %q (%v), want it untouched", after, err)
	}
}

func codexRollout(t *testing.T, w *world, conversation string) string {
	t.Helper()
	rollouts, err := filepath.Glob(filepath.Join(w.Dir, "toolhome", ".codex", "sessions", "*", "*", "*", "rollout-*-"+conversation+".jsonl"))
	if err != nil || len(rollouts) != 1 {
		t.Fatalf("rollout of %s = %v (%v), want exactly one", conversation, rollouts, err)
	}
	return rollouts[0]
}

func TestAResumedCodexSessionCountsItsFirstReplyOnceWhenItsWatcherIsReplaced(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	cwd := w.Path("shop")
	session := w.Spawn(app, fakeagent.Codex, cwd)
	codex := w.Launched(session)
	app.TypeLine(session, "add a discount field to checkout")
	codex.Prompted()
	question := "Before or after tax? <!-- attn:state=waiting_input -->"
	codex.Reply(question)
	counted := claudeTokens(question)
	awaitUsageTokens(app, session, counted)

	resumed := respawn(w, app, fakeagent.Codex, session, cwd)
	app.TypeLine(session, "after tax")
	resumed.Prompted()
	read := w.PauseAt(pausepoint.TranscriptUsageRead)
	w.OnTranscriptWrite(func() {})
	applied := "Applied after tax. <!-- attn:state=idle -->"
	resumed.Reply(applied)
	counted += claudeTokens(applied)
	read.Await()
	read.Release()
	w.OnTranscriptWrite(w.readTranscripts)

	app.TypeLine(session, "show the diff")
	resumed.Prompted()
	shown := "Here is the diff. <!-- attn:state=idle -->"
	resumed.Reply(shown)
	counted += claudeTokens(shown)
	if usage := awaitUsageTokens(app, session, counted); usage.MeasurementIncomplete != nil {
		t.Errorf("usage after resuming = %+v, want it complete", usage)
	}
}

func TestCodexUsageCountsEveryRequestOnceAcrossARestart(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	cwd := w.Path("shop")
	session := w.Spawn(app, fakeagent.Codex, cwd)
	codex := w.Launched(session)
	app.TypeLine(session, "add a discount field to checkout")
	codex.Prompted()
	research := "Checkout applies tax after discounts."
	codex.Subagent(research)
	question := "Before or after tax? <!-- attn:state=waiting_input -->"
	codex.Reply(question)
	counted := claudeTokens(research) + claudeTokens(question)
	awaitUsageTokens(app, session, counted)

	w.restart()
	app = w.App()
	if restored := queriedSession(t, w.Client(), session).Usage; restored == nil || restored.TotalTokens != counted {
		t.Fatalf("usage after the restart = %+v, want %d tokens", restored, counted)
	}

	w.Spawn(app, fakeagent.Codex, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ID = protocol.SessionID(session)
		m.ResumeSessionID = protocol.Ptr(codex.ConversationID)
	})
	resumed := w.Launched(session)
	app.TypeLine(session, "after tax")
	resumed.Prompted()
	check := "The discount stays before tax."
	resumed.Subagent(check)
	applied := "Applied after tax. <!-- attn:state=idle -->"
	resumed.Reply(applied)
	counted += claudeTokens(check) + claudeTokens(applied)
	if usage := awaitUsageTokens(app, session, counted); usage.MeasurementIncomplete != nil {
		t.Errorf("usage after resuming = %+v, want it still complete", usage)
	}
}
