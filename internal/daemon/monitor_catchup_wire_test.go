package daemon_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAConversationReportRetriesAfterStorageRecovers(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app, cli := w.App(), w.Client()
		agent := w.bubbleClaude(t, app, "shop")
		transcript := fakeagent.WriteClaudeTranscript(t, w.Path("moved"), agent.transcript.ConversationID)
		transcript.Prompt("continue from the recovered transcript")
		restore := w.RefuseDatabaseCommits()
		defer restore()
		if err := cli.ObserveAgentConversation(protocol.TerminalID(agent.self), transcript.ConversationID, transcript.Path); err == nil {
			t.Fatal("unsaved conversation report returned success")
		}
		if err := cli.RecordPullRequestCreated(protocol.SessionID(agent.id), shopPull(1)); err == nil {
			t.Fatal("unsaved pull request returned success")
		}
		restore()
		w.advance(jobs.DefaultBackoffBase)
		if err := cli.ObserveAgentConversation(protocol.TerminalID(agent.self), transcript.ConversationID, transcript.Path); err != nil {
			t.Fatalf("recovered conversation report: %v", err)
		}
		read, err := cli.SessionTranscript(agent.id, "")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(read.Events, func(event protocol.SessionTranscriptEvent) bool {
			return protocol.Deref(event.Text) == "continue from the recovered transcript"
		}) {
			t.Fatalf("recovered transcript = %+v", read.Events)
		}
		if got := pullNumbers(queriedSession(t, cli, agent.id)); !slices.Equal(got, []int{1}) {
			t.Fatalf("recovered pull requests = %v, want [1]", got)
		}
	})
}

func TestASnoozeWakeRetriesAfterStorageRecovers(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := autoSettleOwingSession(t, w)
		deadline := time.Now().Add(time.Minute)
		snoozeUntil(app, "s1", deadline)
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return snoozedUntil(s).Equal(deadline) })
		restore := w.RefuseDatabaseCommits()
		defer restore()
		w.advance(time.Until(deadline))
		w.advance(jobs.DefaultBackoffBase)
		restore()
		w.advance(2*jobs.DefaultBackoffBase - time.Nanosecond)
		if got := queriedSession(t, cli, "s1"); protocol.Deref(got.TurnOwed) {
			t.Fatal("wake retried before its backoff elapsed")
		}
		w.advance(time.Nanosecond)
		got := queriedSession(t, cli, "s1")
		if !protocol.Deref(got.TurnOwed) || !openedAt(got).Equal(deadline) || !snoozedUntil(got).IsZero() {
			t.Fatalf("recovered wake: owed=%v opened=%v snoozed=%v, want owed at %v", protocol.Deref(got.TurnOwed), openedAt(got), snoozedUntil(got), deadline)
		}
	})
}

func TestAnAutoSettleRetriesAfterStorageRecovers(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := autoSettleOwingSession(t, w)
		setSetting(t, app, "auto_settle_arm_seconds", "5")
		setSetting(t, app, "auto_settle_countdown_seconds", "3")
		steered := autoSettleSteer(t, w, app, cli, "fix the flaky test")
		deadline := steered.Add(8 * time.Second)
		w.advance(5 * time.Second)
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool { return autoSettleFiresAt(t, s).Equal(deadline) })
		restore := w.RefuseDatabaseCommits()
		defer restore()
		w.advance(time.Until(deadline))
		restore()
		w.advance(jobs.DefaultBackoffBase)
		got := queriedSession(t, cli, "s1")
		if got.State != protocol.SessionStateWorking || protocol.Deref(got.TurnOwed) || got.AutoSettleFiresAt != nil {
			t.Fatalf("recovered auto-settle: state=%s owed=%v countdown=%v", got.State, protocol.Deref(got.TurnOwed), protocol.Deref(got.AutoSettleFiresAt))
		}
	})
}

func TestPullRequestReportsRetryTheirLatestViewAfterStorageRecovers(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app, cli := autoSettleOwingSession(t, w)
		restore := w.RefuseDatabaseCommits()
		defer restore()
		for _, number := range []int{1, 2} {
			if err := cli.RecordPullRequestCreated("s1", shopPull(number)); err == nil {
				t.Fatal("unsaved report returned success")
			}
		}
		restore()
		w.advance(jobs.DefaultBackoffBase)
		numbers := pullNumbers(queriedSession(t, cli, "s1"))
		slices.Sort(numbers)
		if !slices.Equal(numbers, []int{1, 2}) {
			t.Fatalf("recovered pull requests=%v, want [1 2]", numbers)
		}
		testworld.AwaitSession(app, "s1", func(s protocol.Session) bool {
			numbers := pullNumbers(s)
			slices.Sort(numbers)
			return slices.Equal(numbers, []int{1, 2})
		})
	})
}

func TestUnsavedSnoozeCommandsReportPendingAndRetry(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		_, cli := autoSettleOwingSession(t, w)
		deadline := time.Now().Add(time.Hour)
		for _, command := range []struct {
			name       string
			run        func() error
			wantSnooze bool
		}{
			{"snooze", func() error { return cli.SnoozeSession("", "s1", deadline) }, true},
			{"unsnooze", func() error { return cli.UnsnoozeSession("", "s1") }, false},
		} {
			restore := w.RefuseDatabaseCommits()
			err := command.run()
			restore()
			if err == nil || !strings.Contains(err.Error(), command.name+" is pending") || !strings.Contains(err.Error(), "keep retrying") {
				t.Fatalf("%s error=%v, want pending and retrying", command.name, err)
			}
			w.advance(jobs.DefaultBackoffBase)
			got := queriedSession(t, cli, "s1")
			if command.wantSnooze {
				if !snoozedUntil(got).Equal(deadline) || protocol.Deref(got.TurnOwed) {
					t.Fatalf("pending snooze was not saved: %+v", got)
				}
			} else if !snoozedUntil(got).IsZero() || !protocol.Deref(got.TurnOwed) {
				t.Fatalf("pending unsnooze was not saved: %+v", got)
			}
		}
	})
}

func TestUnsnoozeReversesAnUnsavedSnooze(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		_, cli := autoSettleOwingSession(t, w)
		restore := w.RefuseDatabaseCommits()
		defer restore()
		deadline := time.Now().Add(time.Hour)
		if err := cli.SnoozeSession("", "s1", deadline); err == nil || !strings.Contains(err.Error(), "snooze is pending") {
			t.Fatalf("unsaved snooze error=%v, want pending", err)
		}
		w.advance(time.Nanosecond)
		for range 2 {
			if err := cli.UnsnoozeSession("", "s1"); err == nil || !strings.Contains(err.Error(), "unsnooze is pending") {
				t.Fatalf("unsaved reversal error=%v, want pending", err)
			}
		}
		restore()
		w.advance(jobs.DefaultBackoffBase)
		got := queriedSession(t, cli, "s1")
		if !snoozedUntil(got).IsZero() || !protocol.Deref(got.TurnOwed) {
			t.Fatalf("recovered reversal: snoozed=%v owed=%v, want awake and owed", snoozedUntil(got), protocol.Deref(got.TurnOwed))
		}
	})
}
