package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASharedCodexSuccessorStartsWorkingOnItsOwnFirstPrompt(t *testing.T) {
	for _, command := range []string{"/new", "/clear"} {
		for _, old := range []struct {
			name   string
			fields string
			state  protocol.SessionState
		}{
			{"idle", "state=idle:app=codex", protocol.SessionStateIdle},
			{"working", "state=working:app=codex", protocol.SessionStateWorking},
			{"blocked", "state=blocked:app=codex", protocol.SessionStateWaitingInput},
		} {
			t.Run(command+"/"+old.name, func(t *testing.T) {
				w := newWorld(t, fakeagent.Codex)
				app := w.App()
				setSetting(t, app, "codex_shared_enabled", "true")
				original, first, terminal := sharedCodexWaiting(t, w, app)
				before := listedState(t, w, original)
				first.ProgramStatus("state=working:app=codex")
				working := testworld.AwaitStateAfter(app, before, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
				if old.state != protocol.SessionStateWorking {
					first.ProgramStatus(old.fields)
					testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == old.state })
				}
				moveOn(t, app, first, terminal, command, "the successor's first prompt")
				successor := sessionShownIn(t, w, app, terminal)
				if successor == original {
					t.Fatal("the terminal did not move to a successor")
				}
				next := testworld.AwaitSession(app, successor, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
				first.ProgramStatus("state=blocked:app=codex")
				testworld.AwaitStateAfter(app, next, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
			})
		}
	}
}
