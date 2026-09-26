package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/testworld"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAHookReportedStateReachesTheAppOnTheEdge(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client()
		if err := cli.Register("s1", "s1", w.Path("s1")); err != nil {
			t.Fatalf("register: %v", err)
		}
		for _, step := range []struct {
			report func() error
			want   protocol.SessionState
		}{
			{func() error { return cli.UpdateState("s1", protocol.StateWorking) }, protocol.SessionStateWorking},
			{func() error { return cli.RecordNotification("s1", "permission_prompt", "Allow edit?") }, protocol.SessionStatePendingApproval},
			{func() error { return cli.UpdateState("s1", protocol.StateWorking) }, protocol.SessionStateWorking},
			{func() error { return cli.UpdateState("s1", protocol.StateWaitingInput) }, protocol.SessionStateWaitingInput},
		} {
			reported := time.Now()
			if err := step.report(); err != nil {
				t.Fatalf("report %s: %v", step.want, err)
			}
			testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == step.want })
			if lag := time.Since(reported); lag != 0 {
				t.Errorf("%s reached the app %s after the hook reported it", step.want, lag)
			}
		}
	})
}

func TestAnOpenTurnThatStopsMovingGoesStuckOnTime(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report func(w *world) error
		reason string
	}{
		{"an open bracket", func(w *world) error { return w.Client().UpdateState("s1", protocol.StateWorking) }, "bracket_open"},
		{"a compaction", func(w *world) error { return w.Client().RecordCompaction("s1", true, "auto") }, "compacting"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubble(t, func(t *testing.T, w *world) {
				app := w.App()
				if err := w.Client().Register("s1", "s1", w.Path("s1")); err != nil {
					t.Fatalf("register: %v", err)
				}
				reported := time.Now()
				if err := tc.report(w); err != nil {
					t.Fatalf("report: %v", err)
				}
				testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
					return s.State == protocol.SessionStateWorking && protocol.Deref(s.StateReason) == tc.reason
				})

				w.advance(90*time.Second - time.Since(reported))
				show, err := w.Client().SessionShow("s1")
				if err != nil {
					t.Fatalf("session show: %v", err)
				}
				if show.Entry.State != protocol.SessionStateWorking {
					t.Fatalf("after exactly 90s without movement the session is %s, want still working", show.Entry.State)
				}

				stuck := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStateUnknown })
				since := stateSince(t, stuck)
				if deadline := reported.Add(90 * time.Second); !since.After(deadline) || since.After(deadline.Add(time.Millisecond)) {
					t.Errorf("went stuck at %s, want just after %s", since, deadline)
				}
				if reason := protocol.Deref(stuck.StateReason); reason != "stuck" {
					t.Errorf("stuck session's reason is %q, want stuck", reason)
				}
			})
		})
	}
}

func TestAGuardedApprovalSurfacesExactlyAfterItsDwell(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client()
		guardedClaudeAtWork(t, app, cli, w)

		asked := time.Now()
		if err := cli.RecordNotification("s1", "permission_prompt", "Allow edit?"); err != nil {
			t.Fatalf("notify: %v", err)
		}
		w.advance(time.Minute)

		surfaced := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
		if since, want := stateSince(t, surfaced), asked.Add(time.Minute); !since.Equal(want) {
			t.Errorf("the approval surfaced at %s, want exactly one dwell after it was asked, %s", since, want)
		}
	})
}

func TestAnApprovalTheReviewerSettlesInsideTheDwellNeverReachesTheApp(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client()
		guardedClaudeAtWork(t, app, cli, w)

		if err := cli.RecordNotification("s1", "permission_prompt", "Allow edit?"); err != nil {
			t.Fatalf("notify: %v", err)
		}
		w.advance(10 * time.Second)
		if err := cli.UpdateStateFromHook("s1", protocol.StateWorking, "auto"); err != nil {
			t.Fatalf("report working: %v", err)
		}
		w.advance(2 * time.Minute)
		if err := cli.UpdateState("s1", protocol.StateWaitingInput); err != nil {
			t.Fatalf("report waiting_input: %v", err)
		}

		var seen []protocol.SessionState
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
			seen = append(seen, s.State)
			return s.State == protocol.SessionStateWaitingInput
		})
		for _, state := range seen {
			if state == protocol.SessionStatePendingApproval {
				t.Fatalf("the app saw the approval the reviewer answered: %v", seen)
			}
		}
	})
}

func TestHooksThatAgreeWithTheSessionStateBroadcastNothing(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client()
		guardedClaudeAtWork(t, app, cli, w)
		w.advance(time.Second)
		before := stateChangesOf(app, "s1")

		for range 3 {
			if err := cli.UpdateStateFromHook("s1", protocol.StateWorking, "auto"); err != nil {
				t.Fatalf("report working again: %v", err)
			}
			w.advance(time.Second)
		}

		if after := stateChangesOf(app, "s1"); after != before {
			t.Fatalf("hooks that agree with a working session broadcast %d state changes", after-before)
		}
	})
}

func TestAHookThatStrongerEvidenceOutranksDoesNotMoveTheSession(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client()
		if err := cli.Register("s1", "s1", w.Path("s1")); err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := cli.RecordNotification("s1", "permission_prompt", "Allow edit?"); err != nil {
			t.Fatalf("notify: %v", err)
		}
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
		w.advance(time.Second)
		before := stateChangesOf(app, "s1")

		if err := cli.UpdateState("s1", protocol.StateIdle); err != nil {
			t.Fatalf("report idle: %v", err)
		}
		w.advance(time.Second)

		if after := stateChangesOf(app, "s1"); after != before {
			t.Fatalf("a hook reporting idle over an open approval broadcast %d state changes", after-before)
		}
		show, err := cli.SessionShow("s1")
		if err != nil {
			t.Fatalf("session show: %v", err)
		}
		if show.Entry.State != protocol.SessionStatePendingApproval {
			t.Fatalf("the hook moved the session to %s over its open approval", show.Entry.State)
		}
	})
}

func TestAnAgentThatDiesMidTurnReportsItsExitBeforeItsIdle(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"), func(m *protocol.SpawnSessionMessage) {
		m.InitialPrompt = protocol.Ptr("rename the checkout module")
	})
	run := w.Launched(session)
	run.Prompted()
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	run.Exit(1)

	testworld.Await(app, protocol.EventSessionStateChanged, func(e protocol.WebSocketEvent) bool {
		return e.Session != nil && e.Session.ID == session && protocol.Deref(e.Session.StateReason) == "process_exited"
	})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.WebSocketEvent) bool { return protocol.Deref(e.ID) == session })
	if order := exitOrderOf(app, session); order != "exited, idle" {
		t.Fatalf("the app heard of the death as %q, want the exit before the idle it causes", order)
	}
}

func exitOrderOf(p *testworld.Peer, id string) string {
	var order []string
	for _, e := range p.Received() {
		switch {
		case e.Event == protocol.EventSessionExited && protocol.Deref(e.ID) == id:
			order = append(order, "exited")
		case e.Event == protocol.EventSessionStateChanged && e.Session != nil && e.Session.ID == id &&
			protocol.Deref(e.Session.StateReason) == "process_exited":
			order = append(order, "idle")
		}
	}
	return strings.Join(order, ", ")
}

func stateChangesOf(p *testworld.Peer, id string) int {
	changes := 0
	for _, e := range p.Received() {
		if e.Event == protocol.EventSessionStateChanged && e.Session != nil && e.Session.ID == id {
			changes++
		}
	}
	return changes
}

func guardedClaudeAtWork(t *testing.T, app *testworld.Peer, cli *client.Client, w *world) {
	t.Helper()
	if err := cli.RegisterWithAgent("s1", "s1", w.Path("s1"), string(protocol.SessionAgentClaude)); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := cli.UpdateStateFromHook("s1", protocol.StateWorking, "auto"); err != nil {
		t.Fatalf("report working: %v", err)
	}
	testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
}

func stateSince(t *testing.T, s protocol.Session) time.Time {
	t.Helper()
	since, err := time.Parse(time.RFC3339Nano, s.StateSince)
	if err != nil {
		t.Fatalf("state_since %q: %v", s.StateSince, err)
	}
	return since
}
