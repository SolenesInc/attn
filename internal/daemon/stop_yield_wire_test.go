package daemon_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAParkedVerdictHoldsAYieldedTurnWorkingPastTheIdlePromptUntilItExpires(t *testing.T) {
	inBubbleAnsweringHeadlessTasks(t, []fakeagent.Harness{fakeagent.Claude}, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		transcript, _ := hookedClaudeAtWork(t, w, app, cli, "deploy to staging")
		transcript.Answer("The deploy is running in the background; I'll continue when it completes.")
		yieldWithBackgroundWork(t, cli, transcript)
		task := w.HeadlessTask()
		if !strings.Contains(task.Prompt, "yielded with 1 background process") {
			t.Errorf("the verdict prompt %q does not say the turn yielded with work running", task.Prompt)
		}
		task.Answer(`{"verdict":"PARKED"}`)
		parked := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
			return protocol.Deref(s.StateReason) == "background_parked"
		})
		if parked.State != protocol.SessionStateWorking {
			t.Fatalf("the parked turn is %s, want working", parked.State)
		}

		if err := cli.RecordNotification("s1", "idle_prompt", "Claude is waiting for your input"); err != nil {
			t.Fatalf("notify idle_prompt: %v", err)
		}
		w.advance(30*time.Minute - time.Second)
		if got := queriedSession(t, cli, "s1"); got.State != protocol.SessionStateWorking {
			t.Fatalf("inside the parked window the idle prompt settled the session %s (%s)", got.State, protocol.Deref(got.StateReason))
		}
		w.advance(2 * time.Second)
		expired := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
			return s.State != protocol.SessionStateWorking && s.State != protocol.SessionStateLaunching
		})
		if expired.State != protocol.SessionStateIdle || protocol.Deref(expired.StateReason) != "parked_expired" {
			t.Fatalf("past the parked window the session is %s (%s), want idle (parked_expired)", expired.State, protocol.Deref(expired.StateReason))
		}
	})
}

func TestADoneVerdictOnAYieldedTurnSettlesItDespiteTheWorkStillRunning(t *testing.T) {
	inBubbleAnsweringHeadlessTasks(t, []fakeagent.Harness{fakeagent.Claude}, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		transcript, _ := hookedClaudeAtWork(t, w, app, cli, "deploy to staging")
		transcript.Answer("Staging is deployed. The log tail left running is not mine to wait on.")
		yieldWithBackgroundWork(t, cli, transcript)
		answerTurnVerdict(t, w, "DONE")
		settled := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
			return s.State != protocol.SessionStateWorking && s.State != protocol.SessionStateLaunching
		})
		if settled.State != protocol.SessionStateIdle {
			t.Fatalf("after a done verdict the yielded turn is %s (%s), want idle", settled.State, protocol.Deref(settled.StateReason))
		}
	})
}

func hookedClaudeAtWork(t *testing.T, w *world, app *testworld.Peer, cli *client.Client, prompt string) (*fakeagent.ClaudeTranscript, protocol.Session) {
	t.Helper()
	cwd := w.Path("s1")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := fakeagent.WriteClaudeTranscript(t, cwd, "")
	if err := cli.RegisterWithAgent("s1", "checkout work", cwd, string(protocol.SessionAgentClaude)); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := cli.ObserveAgentConversation("s1", transcript.ConversationID, transcript.Path); err != nil {
		t.Fatalf("bind the conversation: %v", err)
	}
	transcript.Prompt(prompt)
	if err := cli.UpdateStateFromHookEvidence("s1", protocol.StateWorking, "", "user_prompt_submit", prompt); err != nil {
		t.Fatalf("report the prompt taken: %v", err)
	}
	working := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	return transcript, working
}

func yieldWithBackgroundWork(t *testing.T, cli *client.Client, transcript *fakeagent.ClaudeTranscript) {
	t.Helper()
	if err := cli.SendStop("s1", transcript.Path, client.StopFacts{BackgroundTasks: []protocol.StopBackgroundTask{
		{Type: "background_session", Status: "running", Name: protocol.Ptr("./deploy.sh staging")},
	}}); err != nil {
		t.Fatalf("stop with background work: %v", err)
	}
}
