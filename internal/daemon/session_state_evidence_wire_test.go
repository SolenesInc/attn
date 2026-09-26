package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAReplyWithoutAUsableMarkerSettlesIdle(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	run := w.Launched(session)
	settled := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	for turn, reply := range []string{
		"All done, the branch is pushed.",
		"Approve this? <!-- attn:state=pending_approval -->",
		"Parking until CI finishes. <!-- attn:state=parked -->",
	} {
		app.TypeLine(session, "next")
		run.Prompted()
		working := testworld.AwaitStateAfter(app, settled, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
		run.Reply(reply)
		settled = testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State != protocol.SessionStateWorking })
		if settled.State != protocol.SessionStateIdle {
			t.Fatalf("after %q the session is %s (%s), want idle", reply, settled.State, protocol.Deref(settled.StateReason))
		}

		explained, err := cli.StateExplain(session)
		if err != nil {
			t.Fatalf("state explain: %v", err)
		}
		refusals := 0
		for _, obs := range explained.Observations {
			if obs.Source == "classifier" {
				if obs.Claim != string(protocol.SessionStateUnknown) || protocol.Deref(obs.Detail) != "classifier_error" {
					t.Fatalf("explain shows the classifier claiming %s (%s) for an unusable reply", obs.Claim, protocol.Deref(obs.Detail))
				}
				refusals++
			}
		}
		if refusals != turn+1 {
			t.Fatalf("after %d unusable replies explain shows %d classifier errors", turn+1, refusals)
		}
	}
}

func TestAnIdleSessionStaysIdleHoweverLongItIsQuiet(t *testing.T) {
	for _, tc := range []struct {
		name   string
		settle func(cli *client.Client) error
	}{
		{"idle at its prompt", func(cli *client.Client) error {
			return cli.RecordNotification("s1", "idle_prompt", "Claude is waiting for your input")
		}},
		{"idle with only a cron pending", func(cli *client.Client) error {
			return cli.SendStop("s1", "", client.StopFacts{PendingSessionCrons: 1})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inBubble(t, func(t *testing.T, w *world) {
				app, cli := w.App(), w.Client()
				sessionStateEvidenceAtWork(t, w, app, cli)
				if err := tc.settle(cli); err != nil {
					t.Fatalf("settle: %v", err)
				}
				idle := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
				before := stateChangesOf(app, "s1")

				w.advance(3*90*time.Second + time.Second)

				if after := stateChangesOf(app, "s1"); after != before {
					t.Fatalf("a quiet idle session changed state %d times: %s", after-before, describeUpdates(sessionUpdatesOf(app, "s1")))
				}
				if got := queriedSession(t, cli, "s1"); got.State != protocol.SessionStateIdle || got.StateSince != idle.StateSince {
					t.Fatalf("after three stuck windows the session is %s since %s, want idle since %s", got.State, got.StateSince, idle.StateSince)
				}
			})
		})
	}
}

func TestAReasonChangeAloneReachesTheApp(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		working := sessionStateEvidenceAtWork(t, w, app, cli)

		for _, step := range []struct {
			report func() error
			reason string
		}{
			{func() error { return cli.RecordCompaction("s1", true, "auto") }, "compacting"},
			{func() error { return cli.RecordCompaction("s1", false, "auto") }, "bracket_open"},
		} {
			before := len(sessionUpdatesOf(app, "s1"))
			if err := step.report(); err != nil {
				t.Fatalf("report %s: %v", step.reason, err)
			}
			w.advance(time.Second)
			updates := sessionUpdatesOf(app, "s1")[before:]
			if len(updates) != 1 {
				t.Fatalf("moving the reason to %s sent %d updates: %s", step.reason, len(updates), describeUpdates(updates))
			}
			if got := updates[0]; got.State != protocol.SessionStateWorking || got.StateSince != working.StateSince ||
				protocol.Deref(got.StateReason) != step.reason {
				t.Fatalf("the app was sent %s since %s (%s), want working since %s (%s)",
					got.State, got.StateSince, protocol.Deref(got.StateReason), working.StateSince, step.reason)
			}
		}

		before := len(sessionUpdatesOf(app, "s1"))
		for range 2 {
			if err := cli.UpdateState("s1", protocol.StateWorking); err != nil {
				t.Fatalf("report working again: %v", err)
			}
			w.advance(time.Second)
		}
		if updates := sessionUpdatesOf(app, "s1")[before:]; len(updates) != 0 {
			t.Fatalf("evidence that changed neither state nor reason sent %s", describeUpdates(updates))
		}
	})
}

func TestAStopWithBackgroundWorkHoldsTheSessionUntilItClears(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		sessionStateEvidenceAtWork(t, w, app, cli)

		if err := cli.SendStop("s1", "", client.StopFacts{BackgroundTasks: []protocol.StopBackgroundTask{
			{Type: "background_session", Status: "running", Name: protocol.Ptr("gh run watch 1234")},
		}}); err != nil {
			t.Fatalf("stop with background work: %v", err)
		}
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
			return s.State == protocol.SessionStateWorking && protocol.Deref(s.StateReason) == "background_work"
		})
		w.advance(time.Minute)
		if got := queriedSession(t, cli, "s1"); got.State != protocol.SessionStateWorking {
			t.Fatalf("a minute into its background work the session is %s (%s), want working", got.State, protocol.Deref(got.StateReason))
		}

		if err := cli.SendStop("s1", "", client.StopFacts{PendingSessionCrons: 1}); err != nil {
			t.Fatalf("stop with only a cron pending: %v", err)
		}
		w.advance(5 * time.Second)
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
			return s.State == protocol.SessionStateIdle && protocol.Deref(s.StateReason) == "cron_pending"
		})
	})
}

func TestEachNotificationTypeMovesTheSessionOnlyAsItsKindSays(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		sessionStateEvidenceAtWork(t, w, app, cli)
		before := len(sessionUpdatesOf(app, "s1"))

		if err := cli.RecordNotification("s1", "some_future_type", "something new"); err != nil {
			t.Fatalf("notify some_future_type: %v", err)
		}
		w.advance(time.Second)
		if updates := sessionUpdatesOf(app, "s1")[before:]; len(updates) != 0 {
			t.Fatalf("an unknown notification type moved the session: %s", describeUpdates(updates))
		}

		if err := cli.RecordNotification("s1", "idle_prompt", "Claude is waiting for your input"); err != nil {
			t.Fatalf("notify idle_prompt: %v", err)
		}
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
			return s.State == protocol.SessionStateIdle && protocol.Deref(s.StateReason) == "prompt_idle"
		})
		for _, s := range sessionUpdatesOf(app, "s1") {
			if s.State == protocol.SessionStatePendingApproval {
				t.Fatalf("an idle prompt was shown as an approval: %s", describeUpdates(sessionUpdatesOf(app, "s1")))
			}
		}
	})
}

func TestClaudeLeavingAutoModeSurfacesApprovalsWithoutTheDwell(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		guardedClaudeAtWork(t, app, cli, w)
		if err := cli.UpdateStateFromHook("s1", protocol.StateWorking, "default"); err != nil {
			t.Fatalf("report working in default mode: %v", err)
		}
		w.advance(time.Second)

		asked := time.Now()
		if err := cli.RecordNotification("s1", "permission_prompt", "Allow edit?"); err != nil {
			t.Fatalf("notify: %v", err)
		}
		surfaced := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })
		if since := stateSince(t, surfaced); !since.Equal(asked) {
			t.Fatalf("the approval surfaced at %s, want at once, %s", since, asked)
		}
	})
}

func sessionStateEvidenceAtWork(t *testing.T, w *world, app *testworld.Peer, cli *client.Client) protocol.Session {
	t.Helper()
	if err := cli.Register("s1", "s1", w.Path("s1")); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := cli.UpdateState("s1", protocol.StateWorking); err != nil {
		t.Fatalf("report working: %v", err)
	}
	working := testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
		return s.State == protocol.SessionStateWorking && protocol.Deref(s.StateReason) == "bracket_open"
	})
	w.advance(0)
	return working
}
