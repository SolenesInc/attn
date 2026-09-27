package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestOnlyAPromptTheHarnessMarksSubmittedCountsAsTheUsersTurn(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := autoSettleOwingSession(t, w)
		w.advance(time.Minute)
		requested := protocol.Deref(sessionStateLastShown(t, app).LastModelRequestAt)
		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: "s1", Data: "the user's answer\r"})
		w.advance(0)
		if err := cli.UpdateStateFromHookEvidence("s1", protocol.StateWorking, "", "", "the user's answer"); err != nil {
			t.Fatalf("report working from an ordinary hook: %v", err)
		}
		w.advance(autoSettleDefaultArm + autoSettleDefaultCountdown)
		for _, s := range sessionUpdatesOf(app, "s1") {
			if s.AutoSettleFiresAt != nil || protocol.Deref(s.LastModelRequestAt) != requested {
				t.Fatalf("an unmarked hook carrying prompt text was taken as the user's prompt: fires_at=%q last_model_request_at=%q",
					protocol.Deref(s.AutoSettleFiresAt), protocol.Deref(s.LastModelRequestAt))
			}
		}

		if err := cli.UpdateStateFromHookEvidence("s1", protocol.StateWorking, "", "user_prompt_submit", "the user's answer"); err != nil {
			t.Fatalf("report the prompt taken: %v", err)
		}
		submitted := time.Now()
		w.advance(autoSettleDefaultArm)
		shown := sessionStateLastShown(t, app)
		if !autoSettleFiresAt(t, shown).Equal(submitted.Add(autoSettleDefaultArm+autoSettleDefaultCountdown)) || protocol.Deref(shown.LastModelRequestAt) == requested {
			t.Fatalf("after the marked submit the app shows fires_at=%q last_model_request_at=%q, want the countdown armed from the submit and the request dated",
				protocol.Deref(shown.AutoSettleFiresAt), protocol.Deref(shown.LastModelRequestAt))
		}
	})
}

func TestTheUsersPromptArmsAutoSettleWhenTheHarnessReportsWorkingBeforeItsSubmitHook(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := autoSettleOwingSession(t, w)
		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: "s1", Data: "fix the flaky test\r"})
		w.advance(0)
		if err := cli.UpdateState("s1", protocol.StateWorking); err != nil {
			t.Fatalf("report working: %v", err)
		}
		w.advance(time.Second)
		if err := cli.UpdateStateFromHookEvidence("s1", protocol.StateWorking, "", "user_prompt_submit", "fix the flaky test"); err != nil {
			t.Fatalf("report the prompt taken: %v", err)
		}
		submitted := time.Now()
		w.advance(autoSettleDefaultArm)
		if shown := sessionStateLastShown(t, app); !autoSettleFiresAt(t, shown).Equal(submitted.Add(autoSettleDefaultArm + autoSettleDefaultCountdown)) {
			t.Fatalf("the app shows fires_at=%q, want the countdown armed from the submit", protocol.Deref(shown.AutoSettleFiresAt))
		}
	})
}
