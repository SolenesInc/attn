package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
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

func TestAnApprovalKeypressEarnsNoCreditAndDoesNotHoldAttnsDoorbell(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setSetting(t, app, "auto_settle_enabled", "true")
		agent := w.bubbleClaude(t, app, "shop")
		registerSessions(t, w, cli, "sender")
		app.TypeLine(agent.id, "edit the config")
		agent.reply("Which file? <!-- attn:state=waiting_input -->")
		agent.term.OnSubmit(nil)
		if err := cli.RecordNotification(agent.id, "permission_prompt", "Allow edit?"); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		if s := queriedSession(t, cli, agent.id); s.State != protocol.SessionStatePendingApproval {
			t.Fatalf("the agent is %s, want pending_approval", s.State)
		}

		app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: agent.self, Data: "y"})
		w.advance(0)
		if err := cli.UpdateStateFromHookEvidence(agent.id, protocol.StateWorking, "", "", ""); err != nil {
			t.Fatal(err)
		}
		w.advance(0)
		agent.reply("Edited. <!-- attn:state=idle -->")
		if s := queriedSession(t, cli, agent.id); s.AutoSettleFiresAt != nil {
			t.Fatalf("an approval keypress armed a countdown to %s", protocol.Deref(s.AutoSettleFiresAt))
		}
		if sent := sendAgentMessage(t, cli, "sender", agent.id, "the build is green"); sent.Status != protocol.AgentMsgStatusNotified {
			t.Fatalf("mail right after an approval keypress = %+v, want the doorbell to ring", sent)
		}
	})
}

func TestAnAnnotationArmsAutoSettleOnTheTurnThatTakesItButNotOnAttnsTurnAfterTheUserTypedOverIt(t *testing.T) {
	for _, tc := range []struct {
		name      string
		after     func(t *testing.T, w *world, app *testworld.Peer, cli *client.Client, agent *bubbleClaude)
		wantArmed bool
	}{
		{"the agent takes the annotation", func(*testing.T, *world, *testworld.Peer, *client.Client, *bubbleClaude) {}, true},
		{"the user types over it and the doorbell's turn follows", func(t *testing.T, w *world, app *testworld.Peer, cli *client.Client, agent *bubbleClaude) {
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: agent.self, Data: "x"})
			w.advance(0)
			agent.term.OnSubmit(agent.take)
			registerSessions(t, w, cli, "sender")
			sendAgentMessage(t, cli, "sender", agent.id, "the build is green")
			w.advance(30 * time.Second)
			if got := pastedContaining(agent.term, inboxDoorbell); got != 1 {
				t.Fatalf("after the user's keystroke went quiet attn pasted %q, want the doorbell once", agent.term.Pasted())
			}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				setSetting(t, app, "auto_settle_enabled", "true")
				agent := w.bubbleClaude(t, app, "shop")
				app.TypeLine(agent.id, "pick a colour for the banner")
				agent.reply("Red or blue? <!-- attn:state=waiting_input -->")
				if !tc.wantArmed {
					agent.term.OnSubmit(nil)
				}
				if got := submitSessionAnnotationFeedback(app, agent.id, sessionAnnotationFeedback); got.status != "delivered" {
					t.Fatalf("submit = %+v, want delivered", got)
				}
				tc.after(t, w, app, cli, agent)

				w.advance(autoSettleDefaultArm)
				s := queriedSession(t, cli, agent.id)
				if s.State != protocol.SessionStateWorking || !protocol.Deref(s.TurnOwed) {
					t.Fatalf("the agent is %s owed %v, want working on a still owed turn", s.State, protocol.Deref(s.TurnOwed))
				}
				if armed := s.AutoSettleFiresAt != nil; armed != tc.wantArmed {
					t.Fatalf("once %s the countdown is %q, want armed %v", tc.name, protocol.Deref(s.AutoSettleFiresAt), tc.wantArmed)
				}
			})
		})
	}
}
