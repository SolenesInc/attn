package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestARespawnedSessionsTicketIsJudgedAgainWhenItEndsAgain(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	session := reconcilingSession(t, w, app, "migrate-store", "Move the store onto the new backend.")
	w.HeadlessTask().Answer(`{"assessment":"partial","confidence":"medium","whats_left":"e2e spec never ran","evidence":"first run"}`)
	awaitReconcileTask(app, "migrate-store", func(task protocol.Task) bool { return task.State == "done" })

	w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.ID = session
		m.ResumeSessionID = protocol.Ptr(session)
		m.Label = protocol.Ptr("store")
	})
	agent := w.Launched(session)
	app.TypeLine(session, "run the e2e spec")
	agent.Prompted()
	replied := time.Now()
	agent.Reply("The e2e spec passes. <!-- attn:state=idle -->")
	answerTurnVerdict(t, w, "DONE")
	awaitIdleSince(t, app, session, replied)
	agent.Exit(0)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })

	task := w.HeadlessTask()
	if !strings.Contains(task.Prompt, "The e2e spec passes.") {
		t.Errorf("the second reconciliation judged %q, want the respawned run's conversation", task.Prompt)
	}
	task.Answer(`{"assessment":"complete","confidence":"high","whats_left":"nothing","evidence":"second run"}`)
	awaitReconcileTask(app, "migrate-store", func(task protocol.Task) bool {
		return task.State == "done" && len(ticketReconciliationNotes(showTicket(t, cli, "migrate-store"))) == 2
	})
}
