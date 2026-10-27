package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAHeartbeatArmsNoAutoSettleButTheUsersAnswerInTheSameRunDoes(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, day := crewDayInBubble(t, w, map[string]string{"auto_settle_enabled": "true"})
		app.TypeLine(day.id, "pick a colour for the banner")
		day.reply("Red or blue? <!-- attn:state=waiting_input -->")
		if waiting := queriedSession(t, day.cli, day.id); !protocol.Deref(waiting.TurnOwed) {
			t.Fatalf("a day asking the user is %+v, want the turn owed", waiting)
		}

		w.advance(55*time.Minute + time.Second)
		if got := pastedContaining(day.term, crewHeartbeat); got != 1 {
			t.Fatalf("want one heartbeat, got %q", day.term.Pasted())
		}
		w.advance(40 * time.Second)
		if s := queriedSession(t, day.cli, day.id); s.State != protocol.SessionStateWorking || s.AutoSettleFiresAt != nil || !protocol.Deref(s.TurnOwed) {
			t.Fatalf("a day warmed by a heartbeat is %s with countdown %q and owed %v, want working, no countdown, still owed",
				s.State, protocol.Deref(s.AutoSettleFiresAt), protocol.Deref(s.TurnOwed))
		}

		answered := time.Now()
		app.TypeLine(day.id, "blue")
		w.advance(autoSettleDefaultArm)
		if s := queriedSession(t, day.cli, day.id); s.AutoSettleFiresAt == nil {
			t.Fatalf("the user's own answer in the heartbeat's run armed no countdown: %s owed %v", s.State, protocol.Deref(s.TurnOwed))
		} else if deadline := autoSettleFiresAt(t, s); !deadline.Equal(answered.Add(autoSettleDefaultArm + autoSettleDefaultCountdown)) {
			t.Fatalf("the user's answer countdown ends at %s, want %s", deadline, answered.Add(autoSettleDefaultArm+autoSettleDefaultCountdown))
		}
		w.advance(autoSettleDefaultCountdown)
		if s := queriedSession(t, day.cli, day.id); protocol.Deref(s.TurnOwed) || s.AutoSettleFiresAt != nil {
			t.Fatalf("after the user's answer countdown the day is owed %v with countdown %q, want settled", protocol.Deref(s.TurnOwed), protocol.Deref(s.AutoSettleFiresAt))
		}
	})
}

func TestAnApprovalKeypressEarnsNoCreditAndHoldsAttnsDoorbellForTheQuietWindow(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setSetting(t, app, "auto_approve_enabled", "false")
		setSetting(t, app, "auto_settle_enabled", "true")
		agent := w.bubbleClaude(t, app, "shop")
		registerSessions(t, w, cli, "sender")
		app.TypeLine(agent.id, "edit the config")
		agent.reply("Which file? <!-- attn:state=waiting_input -->")
		agent.term.OnSubmit(nil)
		if err := cli.RecordNotification(protocol.TerminalID(w.Terminal(agent.id)), "permission_prompt", "Allow edit?"); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		if s := queriedSession(t, cli, agent.id); s.State != protocol.SessionStatePendingApproval {
			t.Fatalf("the agent is %s, want pending_approval", s.State)
		}

		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(agent.self), Data: "y"})
		w.advance(0)
		answered := time.Now()
		if err := cli.UpdateStateFromHookEvidence(protocol.TerminalID(w.Terminal(agent.id)), protocol.StateWorking, "", "", ""); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		agent.reply("Edited. <!-- attn:state=idle -->")
		if s := queriedSession(t, cli, agent.id); s.AutoSettleFiresAt != nil {
			t.Fatalf("an approval keypress armed a countdown to %s", protocol.Deref(s.AutoSettleFiresAt))
		}
		agent.term.OnSubmit(agent.take)
		if sent := sendAgentMessage(t, cli, "sender", agent.id, "the build is green"); sent.Status != protocol.AgentMsgStatusQueued {
			t.Fatalf("mail right after an approval keypress = %+v, want it queued behind the quiet window", sent)
		}
		w.advance(time.Until(answered.Add(30*time.Second)) - time.Nanosecond)
		if got := pastedContaining(agent.term, inboxDoorbell); got != 0 {
			t.Fatalf("attn rang %v after the user's answer, inside the quiet window: %q", time.Since(answered), agent.term.Pasted())
		}
		w.advance(time.Nanosecond)
		if got := pastedContaining(agent.term, inboxDoorbell); got != 1 {
			t.Fatalf("30 s after the user's answer attn pasted %q, want the doorbell once", agent.term.Pasted())
		}
	})
}

func TestAnAnnotationArmsAutoSettleOnTheTurnThatTakesIt(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setSetting(t, app, "auto_settle_enabled", "true")
		agent := w.bubbleClaude(t, app, "shop")
		app.TypeLine(agent.id, "pick a colour for the banner")
		agent.reply("Red or blue? <!-- attn:state=waiting_input -->")
		if got := submitSessionAnnotationFeedback(app, agent.id, sessionAnnotationFeedback); got.status != "delivered" {
			t.Fatalf("submit = %+v, want delivered", got)
		}

		w.advance(autoSettleDefaultArm)
		s := queriedSession(t, cli, agent.id)
		if s.State != protocol.SessionStateWorking || !protocol.Deref(s.TurnOwed) {
			t.Fatalf("the agent is %s owed %v, want working on a still owed turn", s.State, protocol.Deref(s.TurnOwed))
		}
		if s.AutoSettleFiresAt == nil {
			t.Fatal("the turn that took the annotation armed no countdown")
		}
	})
}
