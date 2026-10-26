package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/testworld"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
)

func TestAHookReportedStateReachesTheAppOnTheEdge(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client()
		if err := w.InjectSession("s1", "s1", w.Path("s1"), protocol.SessionAgentClaude); err != nil {
			t.Fatalf("register: %v", err)
		}
		for _, step := range []struct {
			report func() error
			want   protocol.SessionState
		}{
			{func() error {
				return cli.UpdateState(protocol.TerminalID(w.Terminal("s1")), protocol.StateWorking)
			}, protocol.SessionStateWorking},
			{func() error {
				return cli.RecordNotification(protocol.TerminalID(w.Terminal("s1")), "permission_prompt", "Allow edit?")
			}, protocol.SessionStatePendingApproval},
			{func() error {
				return cli.UpdateState(protocol.TerminalID(w.Terminal("s1")), protocol.StateWorking)
			}, protocol.SessionStateWorking},
			{func() error {
				return cli.UpdateState(protocol.TerminalID(w.Terminal("s1")), protocol.StateWaitingInput)
			}, protocol.SessionStateWaitingInput},
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
		{"an open bracket", func(w *world) error {
			return w.Client().UpdateState(protocol.TerminalID(w.Terminal("s1")), protocol.StateWorking)
		}, "bracket_open"},
		{"a compaction", func(w *world) error {
			return w.Client().RecordCompaction(protocol.TerminalID(w.Terminal("s1")), true, "auto")
		}, "compacting"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubble(t, func(t *testing.T, w *world) {
				app := w.App()
				if err := w.InjectSession("s1", "s1", w.Path("s1"), protocol.SessionAgentClaude); err != nil {
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
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		codex := guardedCodexAtWork(t, w, app, cli)

		asked := time.Now()
		if err := cli.UpdateState(protocol.TerminalID(w.Terminal(codex)), protocol.StatePendingApproval); err != nil {
			t.Fatalf("permission request: %v", err)
		}
		w.advance(time.Minute - time.Nanosecond)
		if s := queriedSession(t, cli, codex); s.State != protocol.SessionStateWorking {
			t.Fatalf("inside the dwell the approval already shows as %s", s.State)
		}
		w.advance(time.Nanosecond)

		surfaced := queriedSession(t, cli, codex)
		if surfaced.State != protocol.SessionStatePendingApproval {
			t.Fatalf("after the dwell the session is %s, want pending_approval", surfaced.State)
		}
		if since, want := stateSince(t, surfaced), asked.Add(time.Minute); !since.Equal(want) {
			t.Errorf("the approval surfaced at %s, want exactly one dwell after it was asked, %s", since, want)
		}
	})
}

func TestAnApprovalTheReviewerSettlesInsideTheDwellNeverReachesTheApp(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		codex := guardedCodexAtWork(t, w, app, cli)
		terminal := protocol.TerminalID(w.Terminal(codex))

		if err := cli.UpdateState(terminal, protocol.StatePendingApproval); err != nil {
			t.Fatalf("permission request: %v", err)
		}
		w.advance(10 * time.Second)
		if err := cli.UpdateState(terminal, protocol.StateWorking); err != nil {
			t.Fatalf("report working: %v", err)
		}
		w.advance(2 * time.Minute)
		if err := cli.UpdateState(terminal, protocol.StateWaitingInput); err != nil {
			t.Fatalf("report waiting_input: %v", err)
		}

		var seen []protocol.SessionState
		testworld.AwaitSession(app, codex, func(s protocol.Session) bool {
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

func TestWhatCodexShowsTheUserSurfacesAtOnceDespiteItsReviewer(t *testing.T) {
	for _, claim := range []string{pty.ProgramBlocked, pty.ProgramBlockedQuestion, pty.ProgramBlockedAuth, pty.ProgramBlockedPermission} {
		t.Run(claim, func(t *testing.T) {
			inBubbleWithAgents(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				codex := guardedCodexAtWork(t, w, app, cli)

				asked := time.Now()
				w.terminal(codex).ProgramStatus(claim)
				w.advance(0)
				surfaced := queriedSession(t, cli, codex)
				if surfaced.State != protocol.SessionStatePendingApproval || !stateSince(t, surfaced).Equal(asked) {
					t.Fatalf("a Codex %s reads %s since %s, want pending_approval at once, %s", claim, surfaced.State, surfaced.StateSince, asked)
				}
			})
		})
	}
}

func TestAnApprovalTheReviewerHandsToTheUserSurfacesWhenCodexShowsIt(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		codex := guardedCodexAtWork(t, w, app, cli)

		if err := cli.UpdateState(protocol.TerminalID(w.Terminal(codex)), protocol.StatePendingApproval); err != nil {
			t.Fatalf("permission request: %v", err)
		}
		w.advance(10 * time.Second)
		if s := queriedSession(t, cli, codex); s.State != protocol.SessionStateWorking {
			t.Fatalf("while the reviewer decides the approval already shows as %s", s.State)
		}

		shown := time.Now()
		w.terminal(codex).ProgramStatus(pty.ProgramBlockedPermission)
		w.advance(0)
		surfaced := queriedSession(t, cli, codex)
		if surfaced.State != protocol.SessionStatePendingApproval || !stateSince(t, surfaced).Equal(shown) {
			t.Fatalf("once Codex showed the approval the session reads %s since %s, want pending_approval at once, %s", surfaced.State, surfaced.StateSince, shown)
		}
	})
}

func TestHooksThatAgreeWithTheSessionStateBroadcastNothing(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		cli := w.Client()
		claudeAtWork(t, app, cli, w)
		w.advance(time.Second)
		before := stateChangesOf(app, "s1")

		for range 3 {
			if err := cli.UpdateStateFromHook(protocol.TerminalID(w.Terminal("s1")), protocol.StateWorking, "auto"); err != nil {
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
		if err := w.InjectSession("s1", "s1", w.Path("s1"), protocol.SessionAgentClaude); err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := cli.RecordNotification(protocol.TerminalID(w.Terminal("s1")), "permission_prompt", "Allow edit?"); err != nil {
			t.Fatalf("notify: %v", err)
		}
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
		w.advance(time.Second)
		before := stateChangesOf(app, "s1")

		if err := cli.UpdateState(protocol.TerminalID(w.Terminal("s1")), protocol.StateIdle); err != nil {
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

func stateChangesOf(p *testworld.Peer, id string) int {
	changes := 0
	for _, e := range p.Received() {
		if e.Event == protocol.EventSessionStateChanged && e.Session != nil && string(e.Session.ID) == id {
			changes++
		}
	}
	return changes
}

func claudeAtWork(t *testing.T, app *testworld.Peer, cli *client.Client, w *world) {
	t.Helper()
	if err := w.InjectSession("s1", "s1", w.Path("s1"), protocol.SessionAgentClaude); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := cli.UpdateStateFromHook(protocol.TerminalID(w.Terminal("s1")), protocol.StateWorking, "auto"); err != nil {
		t.Fatalf("report working: %v", err)
	}
	testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
}

func guardedCodexAtWork(t *testing.T, w *world, app *testworld.Peer, cli *client.Client) string {
	t.Helper()
	codex := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	if err := cli.UpdateState(protocol.TerminalID(w.Terminal(codex)), protocol.StateWorking); err != nil {
		t.Fatalf("report working: %v", err)
	}
	w.advance(0)
	if s := queriedSession(t, cli, codex); s.State != protocol.SessionStateWorking {
		t.Fatalf("the Codex at work is %s, want working", s.State)
	}
	return codex
}

func stateSince(t testing.TB, s protocol.Session) time.Time {
	t.Helper()
	since, err := time.Parse(time.RFC3339Nano, s.StateSince)
	if err != nil {
		t.Fatalf("state_since %q: %v", s.StateSince, err)
	}
	return since
}
