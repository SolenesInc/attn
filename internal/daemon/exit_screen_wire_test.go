package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestPeekShowsTheLastExitAndALaterExitReplacesIt(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()
	cwd := w.Path("deploy")
	session := w.Spawn(app, fakeagent.Claude, cwd)

	first := w.Launched(session)
	app.TypeLine(session, "deploy it")
	first.Prompted()
	first.Reply("Error: the registry refused the push <!-- attn:state=idle -->")
	first.Exit(1)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })
	earlier, _ := peekExit(t, cli, session, "the registry refused the push")
	if earlier.Code != 1 || earlier.At == "" {
		t.Errorf("first exit = %+v, want code 1 with its time", earlier)
	}

	w.Spawn(app, fakeagent.Claude, cwd, func(m *protocol.SpawnSessionMessage) {
		m.ID = session
		m.ResumeSessionID = protocol.Ptr(session)
	})
	second := w.Launched(session)
	app.TypeLine(session, "try again")
	second.Prompted()
	second.Reply("Error: the disk is full <!-- attn:state=idle -->")
	second.Exit(2)
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == session })
	later, screen := peekExit(t, cli, session, "the disk is full")
	if later.Code != 2 || later.At < earlier.At {
		t.Errorf("second exit = %+v after %+v, want code 2 replacing the first", later, earlier)
	}
	if strings.Contains(screen, "the registry refused the push") {
		t.Errorf("peek after the second exit still shows the first one's screen: %q", screen)
	}
}

func peekExit(t *testing.T, cli *client.Client, session, showing string) (protocol.AgentPeekExit, string) {
	t.Helper()
	peek, err := cli.AgentPeek(session)
	if err != nil {
		t.Fatalf("peek %s: %v", session, err)
	}
	if peek.Exit == nil {
		t.Fatalf("peek of the exited %s = %+v, want its exit", session, peek)
	}
	if peek.Screen == nil || !strings.Contains(peek.Screen.Text, showing) {
		t.Fatalf("peek screen = %+v, want the final screen showing %q", peek.Screen, showing)
	}
	return *peek.Exit, peek.Screen.Text
}
