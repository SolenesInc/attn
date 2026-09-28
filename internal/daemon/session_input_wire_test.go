package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAHalfTypedDraftSurvivesTheTurnEndingAndAttnsMail(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	recipient, agent := mailIdleAgent(w, app, "shop")
	sender := spawnPanes(w, app, w.Path("sender"))[0].session
	app.TypeLine(recipient, "keep going")
	agent.Prompted()

	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: recipient, Data: "half a thought"})
	app.AwaitScreen(recipient, "half a thought")
	agent.Reply("Done for now. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, recipient, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	held := sendAgentMessage(t, cli, sender, recipient, "the build is green")
	if held.Status != protocol.AgentMsgStatusQueued {
		t.Errorf("mail for an agent under a fresh draft = %+v, want it held back", held)
	}
	app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: recipient, Data: " about checkout\r"})
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
		session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
		term := w.terms.Terminal(session)
		term.Heartbeat("not_busy", "Claude Code")
		submit := protocol.SessionAnnotationsSubmitMessage{
			Cmd: protocol.CmdSessionAnnotationsSubmit, RequestID: "feedback-1", SessionID: session, Text: sessionAnnotationFeedback,
		}
		send := func() protocol.SessionAnnotationsSubmitResultMessage {
			return testworld.Request(app, submit, protocol.EventSessionAnnotationsSubmitResult,
				func(r protocol.SessionAnnotationsSubmitResultMessage) bool { return r.RequestID == submit.RequestID })
		}
		if first := send(); first.Status != "delivered" {
			t.Fatalf("the first send = %+v, want delivered", first)
		}
		typed := len(term.Inputs())

		if err := cli.RecordNotification(session, "permission_prompt", "Allow rm -rf build?"); err != nil {
			t.Fatal(err)
		}
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
		resent := send()
		for _, input := range term.Inputs()[typed:] {
			t.Errorf("resending the batch (answered %s) typed %q into the approval prompt, want nothing", resent.Status, input.Data)
		}
	})
}
