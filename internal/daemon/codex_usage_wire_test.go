package daemon_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
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
	app.TypeLine(session, "/new")
	if got := codex.Prompted(); got != "/new" {
		t.Fatalf("codex received %q, want /new", got)
	}
	app.TypeLine(session, "now fix it")
	codex.Prompted()
	next := awaitSuccessor(app, session)
	fixed := "Fixed with a lock. <!-- attn:state=idle -->"
	codex.Reply(fixed)
	if usage := awaitUsageTokens(app, next.ID, claudeTokens(fixed)); usage.MeasurementIncomplete != nil {
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
