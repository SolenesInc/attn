package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAHalfTypedDraftSurvivesTheTurnEndingAndAttnsMail(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	recipient, agent := mailIdleAgent(w, app, "shop")
	sender := spawnPanes(w, app, w.Path("sender"))[0].session
	app.TypeLine(recipient, "keep going")
	agent.Prompted()

	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(app.Terminal(recipient)), Data: "half a thought"})
	app.AwaitScreen(recipient, "half a thought")
	agent.Reply("Done for now. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, recipient, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	held := sendAgentMessage(t, cli, sender, recipient, "the build is green")
	if held.Status != protocol.AgentMsgStatusQueued {
		t.Errorf("mail for an agent under a fresh draft = %+v, want it held back", held)
	}
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(app.Terminal(recipient)), Data: " about checkout\r"})
	if got := agent.Prompted(); got != "half a thought about checkout" {
		t.Fatalf("the agent received %q, want the user's draft exactly as typed", got)
	}
}

func TestAttnPressesEnterOnItsPasteOnlyAfterTheHarnessHadTimeToTakeIt(t *testing.T) {
	daemon.UseShippedPasteGap(t)
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		recipient := w.bubbleClaude(t, app, "shop")
		registerSessions(t, w, cli, "sender")
		sendAgentMessage(t, cli, "sender", recipient.id, "the migration landed")

		inputs := recipient.term.Inputs()
		if len(inputs) != 2 || !strings.Contains(inputs[0].Data, inboxDoorbell) || inputs[1].Data != "\r" {
			t.Fatalf("attn typed %+v, want the doorbell pasted then Enter", inputs)
		}
		if gap := inputs[1].At.Sub(inputs[0].At); gap < 150*time.Millisecond {
			t.Errorf("attn pressed Enter %s after its paste, want at least 150ms so the harness takes the paste first", gap)
		}
	})
}

func TestResendingAnAnnotationBatchNeverPressesEnterOnAnApprovalThatOpenedSince(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		setSetting(t, app, "auto_approve_enabled", "false")
		session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
		term := w.terminal(session)
		term.Heartbeat("not_busy", "Claude Code")
		submit := protocol.SessionAnnotationsSubmitMessage{
			Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: "feedback-1", SessionID: protocol.SessionID(session), Text: sessionAnnotationFeedback,
		}
		send := func() protocol.SessionAnnotationsSubmitResultMessage {
			return testworld.Request(app, submit, protocol.EventSessionAnnotationsSubmitResult,
				func(r protocol.SessionAnnotationsSubmitResultMessage) bool { return r.RequestID == submit.RequestID })
		}
		if first := send(); first.Status != "delivered" {
			t.Fatalf("the first send = %+v, want delivered", first)
		}
		typed := len(term.Inputs())

		if err := cli.RecordNotification(protocol.TerminalID(w.Terminal(session)), "permission_prompt", "Allow rm -rf build?"); err != nil {
			t.Fatal(err)
		}
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
		resent := send()
		for _, input := range term.Inputs()[typed:] {
			t.Errorf("resending the batch (answered %s) typed %q into the approval prompt, want nothing", resent.Status, input.Data)
		}
	})
}

func TestAnAskUserQuestionHoldsAttnsRingUntilItIsAnswered(t *testing.T) {
	for _, tc := range []struct {
		name      string
		answer    func(w *world, app *testworld.Peer, claude *bubbleClaude)
		ringAfter time.Duration
	}{
		{"by a keypress", func(w *world, app *testworld.Peer, claude *bubbleClaude) {
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: protocol.TerminalID(claude.self), Data: "1"})
		}, 30 * time.Second},
		{"over the hook wire", func(*world, *testworld.Peer, *bubbleClaude) {}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				claude := w.bubbleClaude(t, app, "shop")
				registerSessions(t, w, cli, "sender")
				terminal := protocol.TerminalID(claude.self)
				app.TypeLine(claude.id, "ask me which colour to use")
				if err := cli.UpdateStateFromHook(terminal, protocol.StateWorking, "auto"); err != nil {
					t.Fatal(err)
				}
				w.advance(time.Minute)

				asked := time.Now()
				claude.term.ProgramStatus(pty.ProgramBlockedQuestion)
				if err := cli.UpdateState(terminal, protocol.StatePendingApproval); err != nil {
					t.Fatal(err)
				}
				w.advance(0)
				if s := queriedSession(t, cli, claude.id); s.State != protocol.SessionStatePendingApproval || !stateSince(t, s).Equal(asked) {
					t.Fatalf("an open AskUserQuestion reads %s since %s, want pending_approval since it was asked, %s", s.State, s.StateSince, asked)
				}
				sent := sendAgentMessage(t, cli, "sender", claude.id, "the build is green")
				if sent.Status != protocol.AgentMsgStatusQueued || !strings.Contains(sent.Detail, "approval") {
					t.Fatalf("mail for an agent asking the user = %+v, want it queued behind the approval", sent)
				}
				w.advance(10 * time.Minute)
				if got := pastedContaining(claude.term, inboxDoorbell); got != 0 {
					t.Fatalf("attn typed into an open AskUserQuestion: %q", claude.term.Pasted())
				}

				answered := time.Now()
				tc.answer(w, app, claude)
				claude.term.ProgramStatus(pty.ProgramWorking)
				w.advance(0)
				claude.reply("Blue it is. <!-- attn:state=idle -->")
				claude.term.ProgramStatus(pty.ProgramDone)
				w.advance(0)
				if tc.ringAfter > 0 {
					w.advance(time.Until(answered.Add(tc.ringAfter)) - time.Nanosecond)
					if got := pastedContaining(claude.term, inboxDoorbell); got != 0 {
						t.Fatalf("attn rang %s after the user answered, inside the quiet window", time.Since(answered))
					}
					w.advance(time.Nanosecond)
				}
				if got := pastedContaining(claude.term, inboxDoorbell); got != 1 {
					t.Fatalf("%s after the answer attn pasted %q, want the doorbell once", time.Since(answered), claude.term.Pasted())
				}
			})
		})
	}
}

func TestARingWaitsOutTheQuietWindowEvenAfterTheUserSubmitsTheirPrompt(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		claude := w.bubbleClaude(t, app, "shop")
		registerSessions(t, w, cli, "sender")
		typed := time.Now()
		app.TypeLine(claude.id, "rename the checkout module")
		w.advance(0)
		claude.reply("Renamed. <!-- attn:state=idle -->")

		if sent := sendAgentMessage(t, cli, "sender", claude.id, "the build is green"); sent.Status != protocol.AgentMsgStatusQueued {
			t.Fatalf("mail moments after the user's prompt = %+v, want it queued", sent)
		}
		w.advance(time.Until(typed.Add(30*time.Second)) - time.Nanosecond)
		if got := pastedContaining(claude.term, inboxDoorbell); got != 0 {
			t.Fatalf("attn rang %s after the user's last keystroke", time.Since(typed))
		}
		w.advance(time.Nanosecond)
		if got := pastedContaining(claude.term, inboxDoorbell); got != 1 {
			t.Fatalf("30 s after the user's last keystroke attn pasted %q, want the doorbell once", claude.term.Pasted())
		}
	})
}
