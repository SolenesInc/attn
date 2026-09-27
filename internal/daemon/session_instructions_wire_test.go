package daemon_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type instructionsEvidence struct {
	TurnID string `json:"turn_id"`
	Quote  string `json:"quote"`
}

func instructionsAnswer(answer string, evidence ...instructionsEvidence) string {
	encoded, _ := json.Marshal(map[string]any{"answer": answer, "evidence": evidence})
	return string(encoded)
}

func TestSessionInstructionsAnswerOnlyFromQuotesTheTranscriptHolds(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Codex)
	asked := make(chan *fakeagent.HeadlessTask)
	w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
		switch {
		case strings.Contains(task.Prompt, "You generate short titles"):
			task.Answer("Merge PR 571")
		case strings.Contains(task.Prompt, "Classify how this assistant message ends"):
			task.Answer(`{"verdict":"DONE"}`)
		default:
			asked <- task
		}
	})
	nextQuestion := func() *fakeagent.HeadlessTask {
		t.Helper()
		select {
		case task := <-asked:
			return task
		case <-time.After(fakeagent.HangGuard):
			t.Fatalf("the daemon asked the model nothing within %s", fakeagent.HangGuard)
			return nil
		}
	}
	app, cli := w.App(), w.Client()
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	agent := w.Launched(session)
	converse := func(prompt, reply string) {
		t.Helper()
		app.TypeLine(session, prompt)
		agent.Prompted()
		working := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		agent.Reply(reply)
		testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	}
	converse("Please create PR #571. Do not merge it yet.", "The PR is ready. Should I merge it?")
	converse("Yes, do it.", "I merged PR #571.")

	userSaid := instructionsEvidence{"turn-3", "Yes, do it."}
	agentAsked := instructionsEvidence{"turn-2", "Should I merge it?"}
	agentSaid := instructionsEvidence{"turn-4", "merged PR #571"}
	for _, tc := range []struct {
		name     string
		question string
		answers  []string
		answer   string
		effort   string
		evidence []string
		refusal  string
	}{
		{
			name: "the user's own words authorize", question: "Was merging PR #571 authorized?",
			answers: []string{instructionsAnswer("Yes. The user said to merge.", userSaid, agentAsked)},
			answer:  "Yes. The user said to merge.", effort: "low",
			evidence: []string{"turn-2 assistant Should I merge it?", "turn-3 user Yes, do it."},
		},
		{
			name: "a one-word answer reads as a sentence", question: "Did the agent say it merged PR #571?",
			answers: []string{instructionsAnswer("yes", agentSaid)},
			answer:  "Yes.", effort: "low", evidence: []string{"turn-4 assistant merged PR #571"},
		},
		{
			name: "a quote the transcript lacks is retried at medium effort", question: "Was merging PR #571 authorized?",
			answers: []string{
				instructionsAnswer("Yes.", userSaid, instructionsEvidence{"turn-9", "merge away"}),
				instructionsAnswer("Yes.", userSaid),
			},
			answer: "Yes.", effort: "medium", evidence: []string{"turn-3 user Yes, do it."},
		},
		{
			name: "the agent cannot authorize itself", question: "Was the agent authorized to merge PR #571?",
			answers: []string{instructionsAnswer("Yes.", agentSaid), instructionsAnswer("Yes.", agentSaid)},
			refusal: "invalid_evidence",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type asked struct {
				result *protocol.SessionInstructionsResult
				err    error
			}
			done := make(chan asked, 1)
			go func() {
				result, err := cli.SessionInstructions(session, tc.question)
				done <- asked{result, err}
			}()
			for i, answer := range tc.answers {
				task := nextQuestion()
				if want := []string{"low", "medium"}[i]; task.Harness != fakeagent.Codex || task.Effort != want {
					t.Errorf("attempt %d ran %s at %q, want codex at %s", i+1, task.Harness, task.Effort, want)
				}
				if !strings.Contains(task.Prompt, tc.question) || !strings.Contains(task.Prompt, "Do not merge it yet.") {
					t.Errorf("attempt %d asked %q without the question and the conversation", i+1, task.Prompt)
				}
				task.Answer(answer)
			}
			got := <-done
			if tc.refusal != "" {
				if got.err == nil || !strings.Contains(got.err.Error(), tc.refusal) {
					t.Fatalf("asking %q = %+v, %v; want %s", tc.question, got.result, got.err, tc.refusal)
				}
				return
			}
			if got.err != nil {
				t.Fatalf("asking %q: %v", tc.question, got.err)
			}
			var evidence []string
			for _, e := range got.result.Evidence {
				evidence = append(evidence, e.TurnID+" "+e.Author+" "+e.Quote)
			}
			if got.result.Answer != tc.answer || got.result.ReasoningEffort != tc.effort || strings.Join(evidence, "\n") != strings.Join(tc.evidence, "\n") {
				t.Errorf("asking %q answered %q at %s with %q; want %q at %s with %q",
					tc.question, got.result.Answer, got.result.ReasoningEffort, evidence, tc.answer, tc.effort, tc.evidence)
			}
			if got.result.TranscriptPath == "" || !strings.HasPrefix(got.result.TranscriptFingerprint, "sha256:") {
				t.Errorf("the answer names transcript %q with fingerprint %q", got.result.TranscriptPath, got.result.TranscriptFingerprint)
			}
		})
	}

	converse("paste the whole build log", strings.Repeat("build output line\n", 7000))
	if _, err := cli.SessionInstructions(session, "Was merging PR #571 authorized?"); err == nil || !strings.Contains(err.Error(), "conversation_too_large") {
		t.Errorf("asking about a conversation too large to inspect = %v, want conversation_too_large", err)
	}
}
