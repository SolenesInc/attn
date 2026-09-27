package daemon_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestClearingSessionsEndsEveryAgentAndTerminal(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	agent := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(agent)
	shell := w.Spawn(app, workspaceShell, w.Path("docs"))
	app.TypeLine(shell, `printf 'mark%s\n' er-painted`)
	app.AwaitScreen(shell, "marker-painted")

	app.Send(protocol.ClearSessionsMessage{Cmd: protocol.CmdClearSessions})
	requestID := uuid.NewString()
	testworld.Request(app, protocol.TaskListMessage{Cmd: protocol.CmdTaskList, RequestID: protocol.Ptr(requestID)},
		protocol.EventTaskListResult, func(r protocol.TaskListResultMessage) bool { return r.RequestID == requestID })

	if sessions, err := cli.Query(""); err != nil || len(sessions) != 0 {
		t.Fatalf("after clearing, the daemon lists %+v (%v), want no sessions", sessions, err)
	}
	for _, session := range []string{agent, shell} {
		if got := attachWithPolicy(w.App(), session, protocol.AttachPolicySameAppRemount); got.Success {
			t.Errorf("after clearing, %s still has a terminal to attach to", session)
		}
	}
}
