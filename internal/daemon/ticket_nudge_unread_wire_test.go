package daemon_test

import (
	"testing"
	"time"
)

func TestAnAgentThatHasNotReadItsTicketNudgeIsNudgedOnceNotAgain(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		w.finishStartupWork()
		app, cli := w.App(), w.Client()
		ticketNudgeReadySessions(t, w, cli, "worker")
		ticketReportTake(t, cli, "worker", "pricing")
		w.advance(ticketNudgeBundleWindow)

		for _, comment := range []string{"first look", "second look"} {
			commentOnTicket(t, cli, "chief", "pricing", comment)
			ticketNudgeAwaitDeadline(t, w, app, "worker", time.Now().Add(ticketNudgeCountdown))
			w.advance(ticketNudgeCountdown)
			w.advance(ticketNudgeBundleWindow)
		}
		if nudged := readInbox(t, cli, "worker", 0); len(nudged.Items) != 1 || nudged.Items[0].Content != ticketNudgePromptText {
			t.Errorf("an agent nudged twice before reading was sent %q, want the ticket nudge once", inboxContents(nudged.Items))
		}
		commentOnTicket(t, cli, "chief", "pricing", "after reading")
		ticketNudgeAwaitDeadline(t, w, app, "worker", time.Now().Add(ticketNudgeCountdown))
		w.advance(ticketNudgeCountdown)
		ticketNudgeAwaitDelivered(t, w, app, cli, "worker")
	})
}
