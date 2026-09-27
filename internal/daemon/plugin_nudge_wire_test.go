package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestANudgeHeldForApprovalRingsOnceTheDriverReportsItsAgentIdle(t *testing.T) {
	w := newWorld(t)
	app, cli := w.App(), w.Client()
	driver := connectDriver(t, w, "snipe-plugin", "snipe", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "snipe")
	author, _ := spawnDriven(w, app, driver, w.Path("author"))
	asking, run := spawnDriven(w, app, driver, w.Path("asking"))
	if err := driver.state(run, 1, protocol.StatePendingApproval); err != nil {
		t.Fatal(err)
	}
	testworld.AwaitSession(app, asking, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
	app.TypeLine(asking, "yes, allow it")
	app.AwaitScreen(asking, "yes, allow it")

	createTicket(t, cli, asking, "fix the build", "build")
	commentOnTicket(t, cli, author, "build", "take a look")
	testworld.AwaitSession(app, asking, func(s protocol.Session) bool { return protocol.Deref(s.TicketUnread) })
	app.Send(protocol.TriggerNudgeMessage{Cmd: protocol.CmdTriggerNudge, SessionID: asking})
	for seq, state := range []string{protocol.StateWorking, protocol.StateIdle} {
		if err := driver.state(run, uint64(seq+2), state); err != nil {
			t.Fatal(err)
		}
	}

	app.AwaitScreen(asking, "attn agent inbox")
}
