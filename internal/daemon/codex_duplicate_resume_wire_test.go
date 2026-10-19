package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestBothExplicitSharedResumesFollowTheirProgramsStatus(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	original, first, _ := sharedCodexWaiting(t, w, app)
	duplicate := w.Spawn(app, fakeagent.Codex, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.ResumeSessionID = protocol.Ptr(first.ConversationID)
	})
	second := w.Launched(duplicate)
	states := map[string]protocol.Session{
		original: listedState(t, w, original),
		duplicate: testworld.AwaitSession(app, duplicate, func(s protocol.Session) bool {
			return s.State == protocol.SessionStateIdle
		}),
	}
	for i, status := range []struct {
		fields string
		state  protocol.SessionState
	}{
		{"state=working", protocol.SessionStateWorking},
		{"state=blocked:kind=question", protocol.SessionStatePendingApproval},
		{"state=working", protocol.SessionStateWorking},
		{"state=idle", protocol.SessionStateIdle},
	} {
		views := []*fakeagent.Run{first, second}
		ids := []string{original, duplicate}
		for j := range views {
			index := (i + j) % len(views)
			views[index].ProgramStatus(status.fields)
			id := ids[index]
			states[id] = testworld.AwaitStateAfter(app, states[id], func(s protocol.Session) bool { return s.State == status.state })
		}
	}
}

func TestStartingAnotherConversationInASharedViewKeepsItsDuplicateBlocked(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "codex_shared_enabled", "true")
	_, first, terminal := sharedCodexWaiting(t, w, app)
	duplicate := w.Spawn(app, fakeagent.Codex, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.ResumeSessionID = protocol.Ptr(first.ConversationID)
	})
	second := w.Launched(duplicate)
	before := listedState(t, w, duplicate)
	app.TypeLine(duplicate, "continue checkout")
	second.Prompted()
	working := testworld.AwaitStateAfter(app, before, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	second.Reply("Added it. <!-- attn:state=idle -->")
	idle := testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	second.ProgramStatus("state=blocked:app=codex")
	testworld.AwaitStateAfter(app, idle, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
	moveOn(t, app, first, terminal, "/new", "continue the other conversation")
	target := sessionShownIn(t, w, app, terminal)
	next := testworld.AwaitSession(app, target, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	first.Reply("Finished the other conversation. <!-- attn:state=idle -->")
	testworld.AwaitStateAfter(app, next, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	if s := listedState(t, w, duplicate); s.State != protocol.SessionStatePendingApproval {
		t.Fatalf("the sibling's new conversation changed the blocked duplicate to %s", s.State)
	}
}

func TestClosingEitherExplicitSharedResumeKeepsTheOtherSessionWorking(t *testing.T) {
	for _, ending := range []string{"close", "clean_exit", "failed_exit"} {
		for _, closing := range []string{"original", "duplicate"} {
			t.Run(ending+"/"+closing, func(t *testing.T) {
				w := newWorld(t, fakeagent.Codex)
				app := w.App()
				setSetting(t, app, "codex_shared_enabled", "true")
				original, first, _ := sharedCodexWaiting(t, w, app)
				native := first.ConversationID
				duplicate := w.Spawn(app, fakeagent.Codex, w.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.ResumeSessionID = protocol.Ptr(native) })
				second := w.Launched(duplicate)
				app.TypeLine(duplicate, "continue the checkout test")
				if got := second.Prompted(); got != "continue the checkout test" {
					t.Fatalf("duplicate prompt = %q", got)
				}
				second.Reply("Added the test. <!-- attn:state=idle -->")
				testworld.AwaitSession(app, duplicate, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
				closed, survivor, run := original, duplicate, first
				if closing == "duplicate" {
					closed, survivor, run = duplicate, original, second
				}
				switch ending {
				case "close":
					closeSession(t, w.Client(), closed, "finished with this terminal")
				case "clean_exit":
					run.Exit(0)
					testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.WebSocketEvent) bool { return e.Session != nil && string(e.Session.ID) == closed })
				case "failed_exit":
					run.Exit(1)
					testworld.AwaitSession(app, closed, func(s protocol.Session) bool {
						return s.State == protocol.SessionStateIdle && protocol.Deref(s.StateReason) == "process_exited"
					})
				}
				before := listedState(t, w, survivor)
				got := submitSessionAnnotationFeedback(app, survivor, sessionAnnotationFeedback)
				if !got.success || got.status != "delivered" {
					t.Fatalf("feedback to remaining explicit resume = %+v, want delivered", got)
				}
				server := w.CodexServer()
				if prompt := server.Prompted(native); prompt != sessionAnnotationFeedback {
					t.Fatalf("remaining conversation prompt = %q, want %q", prompt, sessionAnnotationFeedback)
				}
				working := testworld.AwaitStateAfter(app, before, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
				server.Reply(native, "The survivor still works. <!-- attn:state=waiting_input -->")
				testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateWaitingInput })
				messageWindowShowing(app, survivor, "The survivor still works.")
			})
		}
	}
}
