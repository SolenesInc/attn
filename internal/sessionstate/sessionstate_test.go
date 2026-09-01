package sessionstate

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

var now = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

func testPolicy() Policy {
	return Policy{
		HeartbeatTTL:         time.Second,
		HeartbeatSettleAfter: 3 * time.Second,

		StaleAfter:        4 * time.Second,
		StuckAfter:        90 * time.Second,
		GuardianDwell:     60 * time.Second,
		SettleGrace:       4 * time.Second,
		ClassifierTimeout: 30 * time.Second,
		ParkedAfter:       30 * time.Minute,
	}
}

func seen(source Source, claim Claim, age time.Duration) *Observation {
	return &Observation{Source: source, Claim: claim, ObservedAt: now.Add(-age)}
}

func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		name       string
		evidence   Evidence
		wantState  protocol.SessionState
		wantReason Reason
		wantHold   bool
	}{

		{
			name: "a fresh heartbeat works without any bracket",
			evidence: Evidence{
				Heartbeat: seen(SourceHeartbeat, ClaimBusy, 200*time.Millisecond),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonHeartbeatBusy,
		},
		{
			name: "an open bracket goes stale when the heartbeat goes silent",
			evidence: Evidence{
				TurnOpen:   true,
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 10*time.Second),
				LastBusyAt: now.Add(-10 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonBracketStale,
		},
		{
			name: "a stale bracket takes the classifier's verdict when there is one",
			evidence: Evidence{
				TurnOpen:       true,
				Heartbeat:      seen(SourceHeartbeat, ClaimBusy, 5*time.Second),
				LastBusyAt:     now.Add(-5 * time.Second),
				LastClassifier: seen(SourceClassifier, ClaimNeedsInput, 2*time.Second),
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonClassifierVerdict,
		},
		{
			name: "a not-busy blip inside the window does not settle an open turn",
			evidence: Evidence{
				TurnOpen:   true,
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, 10*time.Millisecond),
				LastBusyAt: now.Add(-500 * time.Millisecond),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			name: "a prompt-idle confirmation closes a bracket whose hook never came",
			evidence: Evidence{
				TurnOpen:     true,
				LastBusyAt:   now.Add(-90 * time.Second),
				PromptIdleAt: now.Add(-30 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonPromptIdle,
		},
		{
			name: "a busy frame after the confirmation spends it",
			evidence: Evidence{
				TurnOpen:     true,
				Heartbeat:    seen(SourceHeartbeat, ClaimBusy, 2*time.Second),
				LastBusyAt:   now.Add(-2 * time.Second),
				PromptIdleAt: now.Add(-30 * time.Second),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			name: "an open approval outranks a prompt-idle confirmation",
			evidence: Evidence{
				TurnOpen:         true,
				LastBusyAt:       now.Add(-90 * time.Second),
				PromptIdleAt:     now.Add(-30 * time.Second),
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, 40*time.Second),
			},
			wantState:  protocol.SessionStatePendingApproval,
			wantReason: ReasonApprovalOpen,
		},
		{
			name: "a prompt-idle confirmation defers to the classifier verdict",
			evidence: Evidence{
				TurnOpen:       true,
				LastBusyAt:     now.Add(-90 * time.Second),
				PromptIdleAt:   now.Add(-30 * time.Second),
				LastClassifier: seen(SourceClassifier, ClaimNeedsInput, 25*time.Second),
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonClassifierVerdict,
		},
		{
			name: "busy resuming after a blip keeps the turn working",
			evidence: Evidence{
				TurnOpen:   true,
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 50*time.Millisecond),
				LastBusyAt: now.Add(-50 * time.Millisecond),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			name: "a not-busy level past the window settles the turn",
			evidence: Evidence{
				TurnOpen:   true,
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, 10*time.Millisecond),
				LastBusyAt: now.Add(-10 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonBracketStale,
		},
		{
			name: "a bracket with no heartbeat at all stays open",
			evidence: Evidence{
				ToolOpen: true,
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			name: "a mid-tool heartbeat gap inside StaleAfter keeps the bracket open",
			evidence: Evidence{
				ToolOpen:   true,
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 3500*time.Millisecond),
				LastBusyAt: now.Add(-3500 * time.Millisecond),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},

		{
			name: "closed brackets and a quiet heartbeat settle with no verdict at all",
			evidence: Evidence{
				TurnEverOpened: true,
				Heartbeat:      seen(SourceHeartbeat, ClaimSettled, 2*time.Second),
				LastBusyAt:     now.Add(-30 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonHeartbeatSettled,
		},

		{
			// Measured claude approval: the prompt renders at t=14.6s, its Notification
			// hook lands at t=20.6s, and the bracket goes stale at 18.6s.
			name: "a bracket that just went stale holds instead of asserting idle",
			evidence: Evidence{
				TurnOpen:   true,
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 5*time.Second),
				LastBusyAt: now.Add(-5 * time.Second),
			},
			wantHold:   true,
			wantReason: ReasonSettleGrace,
		},
		{
			name: "a settle waits for a classification that is in flight",
			evidence: Evidence{
				TurnEverOpened:   true,
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt:       now.Add(-30 * time.Second),
				ClassifyingSince: now.Add(-2 * time.Second),
			},
			wantHold:   true,
			wantReason: ReasonAwaitingVerdict,
		},
		{
			name: "a classification past its timeout stops holding the settle",
			evidence: Evidence{
				TurnEverOpened:   true,
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt:       now.Add(-90 * time.Second),
				ClassifyingSince: now.Add(-31 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonHeartbeatSettled,
		},
		{
			name: "a verdict the agent has gone busy past is not this turn's answer",
			evidence: Evidence{
				TurnEverOpened:   true,
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt:       now.Add(-6 * time.Second),
				LastClassifier:   seen(SourceClassifier, ClaimNeedsInput, 20*time.Second),
				ClassifyingSince: now.Add(-2 * time.Second),
			},
			wantHold:   true,
			wantReason: ReasonAwaitingVerdict,
		},

		{
			// codex flickers a busy frame while booting, so a busy frame alone must not
			// count as a turn having started.
			name: "a session that has never opened a turn is at its prompt, not settled",
			evidence: Evidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt: now.Add(-3 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonAtPrompt,
		},
		{
			name: "a stale busy frame before the first turn is not a prompt",
			evidence: Evidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, time.Hour),
				LastBusyAt: now.Add(-time.Hour),
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonNoEvidence,
		},
		{
			name: "an approval the agent has gone busy past was answered",
			evidence: Evidence{
				TurnEverOpened:   true,
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, 30*time.Second),
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt:       now.Add(-10 * time.Second),
				LastClassifier:   seen(SourceClassifier, ClaimIdle, 2*time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonClassifierVerdict,
		},
		{
			name: "an approval still newer than the last busy frame is live",
			evidence: Evidence{
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, 5*time.Second),
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt:       now.Add(-20 * time.Second),
			},
			wantState:  protocol.SessionStatePendingApproval,
			wantReason: ReasonApprovalOpen,
		},

		{
			name: "an exited process beats every live signal",
			evidence: Evidence{
				Process:          seen(SourceProcess, ClaimExited, time.Second),
				Heartbeat:        seen(SourceHeartbeat, ClaimBusy, 10*time.Millisecond),
				TurnOpen:         true,
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonProcessExited,
		},
		{
			name: "a fresh heartbeat beats a stale approval edge",
			evidence: Evidence{
				Heartbeat:        seen(SourceHeartbeat, ClaimBusy, 100*time.Millisecond),
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, 30*time.Second),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonHeartbeatBusy,
		},
		{
			name: "an approval survives an expired heartbeat",
			evidence: Evidence{
				Heartbeat:        seen(SourceHeartbeat, ClaimBusy, 3*time.Second),
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, 2*time.Second),
			},
			wantState:  protocol.SessionStatePendingApproval,
			wantReason: ReasonApprovalOpen,
		},
		{
			name: "a reviewer in the loop does not hide an approval",
			evidence: Evidence{
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, time.Second),
				ReviewerInLoop:   true,
			},
			wantState:  protocol.SessionStatePendingApproval,
			wantReason: ReasonApprovalOpen,
		},
		{
			name: "a pending cron does not rename what the turn settled to",
			evidence: Evidence{
				PendingCron:    true,
				LastClassifier: seen(SourceClassifier, ClaimIdle, time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonClassifierVerdict,
		},
		{
			name: "a pending cron names the settle without suppressing it",
			evidence: Evidence{
				PendingCron: true,
				Heartbeat:   seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt:  now.Add(-30 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonCronPending,
		},
		{
			name: "a parked session that starts running again is working",
			evidence: Evidence{
				PendingCron: true,
				Heartbeat:   seen(SourceHeartbeat, ClaimBusy, 100*time.Millisecond),
				LastBusyAt:  now.Add(-100 * time.Millisecond),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonHeartbeatBusy,
		},
		{
			name: "a judged yield settles on its verdict: the running process is a leftover",
			evidence: Evidence{
				BackgroundWork: true,
				LastClassifier: seen(SourceClassifier, ClaimIdle, time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonClassifierVerdict,
		},
		{
			name: "a parked verdict holds a yielded turn working without decaying to unknown",
			evidence: Evidence{
				BackgroundWork: true,
				TurnEverOpened: true,
				LastClassifier: seen(SourceClassifier, ClaimParked, 2*time.Minute),
				LastMovement:   now.Add(-2 * time.Minute),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBackgroundParked,
		},
		{
			name: "a parked verdict past the tripwire settles on the prompt-idle confirmation",
			evidence: Evidence{
				BackgroundWork: true,
				TurnEverOpened: true,
				LastClassifier: seen(SourceClassifier, ClaimParked, 31*time.Minute),
				LastBusyAt:     now.Add(-32 * time.Minute),
				PromptIdleAt:   now.Add(-30 * time.Minute),
				LastMovement:   now.Add(-31 * time.Minute),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonParkedExpired,
		},
		{
			name: "a parked verdict past the tripwire with no prompt confirmation is stuck",
			evidence: Evidence{
				BackgroundWork: true,
				TurnEverOpened: true,
				LastClassifier: seen(SourceClassifier, ClaimParked, 31*time.Minute),
				LastMovement:   now.Add(-31 * time.Minute),
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonStuck,
		},
		{
			name: "a busy frame spends a parked verdict",
			evidence: Evidence{
				BackgroundWork: true,
				TurnEverOpened: true,
				LastClassifier: seen(SourceClassifier, ClaimParked, 30*time.Second),
				LastBusyAt:     now.Add(-10 * time.Second),
				LastMovement:   now.Add(-10 * time.Second),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBackgroundWork,
		},
		{
			name: "a waiting verdict on a yield asks the user",
			evidence: Evidence{
				BackgroundWork: true,
				LastClassifier: seen(SourceClassifier, ClaimNeedsInput, time.Second),
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonClassifierVerdict,
		},
		{
			name: "outstanding work outranks a parked wakeup",
			evidence: Evidence{
				BackgroundWork: true,
				PendingCron:    true,
				LastMovement:   now.Add(-time.Second),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBackgroundWork,
		},
		{
			name: "a yield that resumed nothing and went quiet is stuck",
			evidence: Evidence{
				BackgroundWork: true,
				TurnEverOpened: true,
				LastMovement:   now.Add(-91 * time.Second),
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonStuck,
		},

		{
			name: "a judged turn is not a fresh prompt, whatever the brackets say",
			evidence: Evidence{
				Heartbeat:      seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastClassifier: seen(SourceClassifier, ClaimNeedsInput, time.Second),
				LastBusyAt:     now.Add(-5 * time.Second),
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonClassifierVerdict,
		},
		{
			name: "a turn being judged right now is not a fresh prompt either",
			evidence: Evidence{
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, time.Second),
				ClassifyingSince: now.Add(-time.Second),
				LastBusyAt:       now.Add(-5 * time.Second),
			},
			wantHold:   true,
			wantReason: ReasonAwaitingVerdict,
		},

		{
			name: "an announced question waits on the user",
			evidence: Evidence{
				TurnEverOpened:   true,
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimNeedsInput, 2*time.Second),
				LastBusyAt:       now.Add(-3 * time.Second),
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonQuestionOpen,
		},
		{
			name: "a question the agent has gone busy past was answered",
			evidence: Evidence{
				TurnEverOpened:   true,
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimNeedsInput, 10*time.Second),
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt:       now.Add(-2 * time.Second),
				LastClassifier:   seen(SourceClassifier, ClaimIdle, time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonClassifierVerdict,
		},

		{
			name: "the classifier settles a finished turn to idle",
			evidence: Evidence{
				LastClassifier: seen(SourceClassifier, ClaimIdle, time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonClassifierVerdict,
		},
		{
			name: "the classifier settles a question to waiting_input",
			evidence: Evidence{
				LastClassifier: seen(SourceClassifier, ClaimNeedsInput, time.Second),
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonClassifierVerdict,
		},

		{
			name: "evidence that stopped moving is stuck",
			evidence: Evidence{
				TurnEverOpened: true,
				LastMovement:   now.Add(-91 * time.Second),
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonStuck,
		},
		{
			// Witnessed live: a session launched and left alone turned `unknown` ninety
			// seconds after launch.
			name: "a session that never took a turn is quiet, not stuck",
			evidence: Evidence{
				LastMovement: now.Add(-10 * time.Minute),
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonNoEvidence,
		},
		{
			name: "an open bracket stops outranking stuck once everything goes quiet",
			evidence: Evidence{
				TurnOpen:       true,
				TurnEverOpened: true,
				LastMovement:   now.Add(-91 * time.Second),
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonStuck,
		},
		{
			name: "an open bracket still outranks stuck while evidence keeps arriving",
			evidence: Evidence{
				TurnOpen:       true,
				TurnEverOpened: true,
				LastBusyAt:     now.Add(-time.Second),
				LastMovement:   now.Add(-time.Second),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			name: "recent silence is not yet stuck",
			evidence: Evidence{
				LastMovement: now.Add(-30 * time.Second),
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonNoEvidence,
		},
		{
			name:       "an empty evidence table reports no evidence",
			evidence:   Evidence{},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonNoEvidence,
		},

		{
			name: "an aborted turn settles immediately, with its bracket still open",
			evidence: Evidence{
				TurnOpen:         true,
				TurnEverOpened:   true,
				Heartbeat:        seen(SourceHeartbeat, ClaimSettled, 200*time.Millisecond),
				LastBusyAt:       now.Add(-300 * time.Millisecond),
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimTurnAborted, 200*time.Millisecond),
				LastMovement:     now.Add(-200 * time.Millisecond),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonTurnAborted,
		},
		{
			name: "an aborted turn does not wait for a verdict",
			evidence: Evidence{
				TurnEverOpened:   true,
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimTurnAborted, time.Second),
				ClassifyingSince: now.Add(-time.Second),
				LastMovement:     now.Add(-time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonTurnAborted,
		},
		{
			name: "an aborted turn outranks compaction",
			evidence: Evidence{
				TurnEverOpened:   true,
				Compacting:       true,
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimTurnAborted, time.Second),
				LastMovement:     now.Add(-time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonTurnAborted,
		},
		{
			name: "a busy frame after an abort retires it",
			evidence: Evidence{
				TurnOpen:         true,
				TurnEverOpened:   true,
				Heartbeat:        seen(SourceHeartbeat, ClaimBusy, 100*time.Millisecond),
				LastBusyAt:       now.Add(-100 * time.Millisecond),
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimTurnAborted, 3*time.Second),
				LastMovement:     now.Add(-100 * time.Millisecond),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			name: "an aborted turn is not read as an outstanding approval",
			evidence: Evidence{
				TurnEverOpened:   true,
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimTurnAborted, time.Second),
				LastMovement:     now.Add(-time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonTurnAborted,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(tc.evidence, testPolicy(), now)
			if got.Hold != tc.wantHold {
				t.Fatalf("hold %v, want %v (got %s/%s)", got.Hold, tc.wantHold, got.State, got.Reason)
			}
			if got.State != tc.wantState || got.Reason != tc.wantReason {
				t.Fatalf("got %s/%s, want %s/%s", got.State, got.Reason, tc.wantState, tc.wantReason)
			}
		})
	}
}

func TestHeartbeatFreshnessBoundary(t *testing.T) {
	policy := testPolicy()
	for _, tc := range []struct {
		age  time.Duration
		want protocol.SessionState
	}{
		{age: policy.HeartbeatTTL - time.Millisecond, want: protocol.SessionStateWorking},
		{age: policy.HeartbeatTTL, want: protocol.SessionStateWorking},
		{age: policy.HeartbeatTTL + time.Millisecond, want: protocol.SessionStateWorking},
		{age: policy.HeartbeatSettleAfter, want: protocol.SessionStateWorking},
		{age: policy.HeartbeatSettleAfter + time.Millisecond, want: protocol.SessionStateIdle},
	} {
		e := Evidence{
			Heartbeat:      seen(SourceHeartbeat, ClaimBusy, tc.age),
			LastBusyAt:     now.Add(-tc.age),
			TurnEverOpened: true,
		}
		if got := Resolve(e, policy, now); got.State != tc.want {
			t.Fatalf("heartbeat aged %s resolved %s, want %s", tc.age, got.State, tc.want)
		}
	}
}

func TestARepaintGapWiderThanTheTTLDoesNotFlapTheSession(t *testing.T) {
	policy := testPolicy()
	// Measured on claude 2.1.220 during a compaction, and periodic to the
	// millisecond: it sits between the two windows, where every flap lives.
	const repaint = 1920 * time.Millisecond
	if repaint <= policy.HeartbeatTTL || repaint >= policy.HeartbeatSettleAfter {
		t.Fatalf("repaint %s must fall between the TTL %s and the settle window %s for this test to mean anything",
			repaint, policy.HeartbeatTTL, policy.HeartbeatSettleAfter)
	}

	for tick := 1; tick <= 30; tick++ {
		at := now.Add(time.Duration(tick) * time.Second)
		lastFrame := now.Add((at.Sub(now) / repaint) * repaint)
		e := Evidence{
			TurnEverOpened: true,
			Heartbeat:      &Observation{Source: SourceHeartbeat, Claim: ClaimBusy, ObservedAt: lastFrame},
			LastBusyAt:     lastFrame,
			LastMovement:   lastFrame,
		}
		if got := Resolve(e, policy, at); got.State != protocol.SessionStateWorking {
			t.Fatalf("tick %d: a busy frame %s old resolved %s/%s, want working: a repaint gap is not a settle",
				tick, at.Sub(lastFrame), got.State, got.Reason)
		}
	}
}

// Replays the shape behind every `unknown` observed on 2026-07-27.
func TestAPromptIdleConfirmationRetiresAnOutstandingBackgroundTask(t *testing.T) {
	policy := testPolicy()

	yielded := now
	at := func(d time.Duration) Evidence {
		e := Evidence{
			TurnEverOpened: true,
			BackgroundWork: true,
			Heartbeat:      &Observation{Source: SourceHeartbeat, Claim: ClaimSettled, ObservedAt: yielded},
			LastBusyAt:     yielded.Add(-time.Second),
			LastMovement:   yielded,
		}
		if d >= time.Minute {
			e.PromptIdleAt = yielded.Add(time.Minute)
			e.LastMovement = yielded.Add(time.Minute)
		}
		return e
	}

	if got := Resolve(at(30*time.Second), policy, yielded.Add(30*time.Second)); got.State != protocol.SessionStateWorking {
		t.Fatalf("30s in: resolved %s/%s, want working: an outstanding background task is the only fact so far",
			got.State, got.Reason)
	}

	for _, d := range []time.Duration{
		time.Minute,
		time.Minute + 30*time.Second,
		time.Minute + policy.StuckAfter,
		time.Minute + policy.StuckAfter + time.Second,
		10 * time.Minute,
	} {
		got := Resolve(at(d), policy, yielded.Add(d))
		if got.State == protocol.SessionStateUnknown {
			t.Fatalf("%s in: resolved unknown/%s after the harness said the agent is at its prompt", d, got.Reason)
		}
		if got.State != protocol.SessionStateIdle || got.Reason != ReasonPromptIdle {
			t.Fatalf("%s in: resolved %s/%s, want idle/%s", d, got.State, got.Reason, ReasonPromptIdle)
		}
	}
}

// Replays the 2026-08-01 incident: a yield on an outstanding build, claude's
// flat-timer prompt-idle notification at 60s, and a wrongly settled session.
func TestAParkedVerdictOutlastsThePromptIdleConfirmation(t *testing.T) {
	policy := testPolicy()

	yielded := now
	at := func(d time.Duration) Evidence {
		e := Evidence{
			TurnEverOpened: true,
			BackgroundWork: true,
			Heartbeat:      &Observation{Source: SourceHeartbeat, Claim: ClaimSettled, ObservedAt: yielded},
			LastBusyAt:     yielded.Add(-time.Second),
			LastMovement:   yielded,
			LastClassifier: &Observation{Source: SourceClassifier, Claim: ClaimParked, ObservedAt: yielded.Add(5 * time.Second)},
		}
		if d >= time.Minute {
			e.PromptIdleAt = yielded.Add(time.Minute)
			e.LastMovement = yielded.Add(time.Minute)
		}
		return e
	}

	for _, d := range []time.Duration{
		30 * time.Second,
		time.Minute,
		time.Minute + policy.StuckAfter + time.Second,
		10 * time.Minute,
	} {
		got := Resolve(at(d), policy, yielded.Add(d))
		if got.State != protocol.SessionStateWorking || got.Reason != ReasonBackgroundParked {
			t.Fatalf("%s in: resolved %s/%s, want working/%s", d, got.State, got.Reason, ReasonBackgroundParked)
		}
	}
}

func TestAParkedWakeupDoesNotExcuseTheAgentFromTheQueue(t *testing.T) {
	policy := testPolicy()
	e := Evidence{
		TurnEverOpened: true,
		PendingCron:    true,
		LastBusyAt:     now.Add(-2 * time.Minute),
		LastMovement:   now.Add(-time.Minute),
	}

	got := Resolve(e, policy, now)
	if got.State != protocol.SessionStateIdle || got.Reason != ReasonCronPending {
		t.Fatalf("resolved %s/%s, want idle/cron_pending", got.State, got.Reason)
	}

	e.PromptIdleAt = now.Add(-time.Second)
	if got := Resolve(e, policy, now); got.State != protocol.SessionStateIdle || got.Reason != ReasonPromptIdle {
		t.Fatalf("resolved %s/%s after the confirmation, want idle/prompt_idle", got.State, got.Reason)
	}

	e.BackgroundWork = true
	if got := Resolve(e, policy, now); got.State != protocol.SessionStateIdle {
		t.Fatalf("resolved %s/%s with both facts set, want idle", got.State, got.Reason)
	}
}

func TestAParkedWakeupDoesNotRotIntoUnknown(t *testing.T) {
	policy := testPolicy()
	parked := now.Add(-time.Minute)
	e := Evidence{
		TurnEverOpened: true,
		PendingCron:    true,
		LastBusyAt:     parked,
		LastMovement:   parked,
	}

	for _, quiet := range []time.Duration{
		policy.StuckAfter - time.Second,
		policy.StuckAfter,
		policy.StuckAfter + time.Second,
		10 * policy.StuckAfter,
	} {
		at := parked.Add(quiet)
		got := Resolve(e, policy, at)
		if got.State != protocol.SessionStateIdle || got.Reason != ReasonCronPending {
			t.Fatalf("resolved %s/%s after %s of silence, want idle/cron_pending", got.State, got.Reason, quiet)
		}
	}
}

func TestHeartbeatTTLExpiryCannotSettleAnOpenBracket(t *testing.T) {
	policy := testPolicy()
	for _, age := range []time.Duration{
		policy.HeartbeatTTL + time.Millisecond,
		2 * policy.HeartbeatTTL,
		policy.StaleAfter - time.Millisecond,
		policy.StaleAfter,
	} {
		e := Evidence{
			TurnOpen:   true,
			Heartbeat:  seen(SourceHeartbeat, ClaimBusy, age),
			LastBusyAt: now.Add(-age),
		}
		got := Resolve(e, policy, now)
		if got.State != protocol.SessionStateWorking || got.Reason != ReasonBracketOpen {
			t.Fatalf("a %s gap past the %s TTL resolved %s/%s, want working/%s: the TTL must not close a bracket",
				age, policy.HeartbeatTTL, got.State, got.Reason, ReasonBracketOpen)
		}
	}
}

// Measured over 8.4 production days: session.state.changed was 73.7% of the bus
// log, and 81.6% of consecutive facts for one session landed within the 1s tick.
func TestARunningTurnKeepsOneAnswerWhileItKeepsPainting(t *testing.T) {
	policy := testPolicy()
	for _, tc := range []struct {
		name     string
		evidence func(age time.Duration) Evidence
		believed time.Duration
	}{
		{
			name: "an open bracket believes a frame until the stale window",
			evidence: func(age time.Duration) Evidence {
				return Evidence{
					TurnOpen:       true,
					TurnEverOpened: true,
					Heartbeat:      seen(SourceHeartbeat, ClaimBusy, age),
					LastBusyAt:     now.Add(-age),
					LastMovement:   now.Add(-age),
				}
			},
			believed: policy.StaleAfter,
		},
		{
			name: "a bare heartbeat believes a frame until the settle window",
			evidence: func(age time.Duration) Evidence {
				return Evidence{
					TurnEverOpened: true,
					Heartbeat:      seen(SourceHeartbeat, ClaimBusy, age),
					LastBusyAt:     now.Add(-age),
					LastMovement:   now.Add(-age),
				}
			},
			believed: policy.HeartbeatSettleAfter,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := Resolve(tc.evidence(0), policy, now)
			if want.State != protocol.SessionStateWorking {
				t.Fatalf("the freshest frame resolved %s, want working", want.State)
			}
			for age := time.Duration(0); age <= tc.believed; age += time.Millisecond {
				if got := Resolve(tc.evidence(age), policy, now); got != want {
					t.Fatalf("a %s-old busy frame resolved %+v, want the %+v it resolved at 0: the answer moved while the session did not",
						age, got, want)
				}
			}
		})
	}
}

func TestBracketStalenessBoundary(t *testing.T) {
	policy := testPolicy()
	for _, tc := range []struct {
		age  time.Duration
		want Reason
	}{
		{age: policy.StaleAfter, want: ReasonBracketOpen},
		{age: policy.StaleAfter + time.Millisecond, want: ReasonSettleGrace},
		{age: policy.StaleAfter + policy.SettleGrace, want: ReasonSettleGrace},
		{age: policy.StaleAfter + policy.SettleGrace + time.Millisecond, want: ReasonBracketStale},
	} {
		e := Evidence{TurnOpen: true, Heartbeat: seen(SourceHeartbeat, ClaimBusy, tc.age), LastBusyAt: now.Add(-tc.age)}
		if got := Resolve(e, policy, now); got.Reason != tc.want {
			t.Fatalf("bracket with %s of silence resolved %s, want %s", tc.age, got.Reason, tc.want)
		}
	}
}

func TestResolveIsStableForTheSameInputs(t *testing.T) {
	e := Evidence{
		TurnOpen:       true,
		Heartbeat:      seen(SourceHeartbeat, ClaimBusy, 2*time.Second),
		LastBusyAt:     now.Add(-2 * time.Second),
		LastClassifier: seen(SourceClassifier, ClaimIdle, time.Second),
		LastMovement:   now.Add(-time.Second),
	}
	first := Resolve(e, testPolicy(), now)
	for range 5 {
		if got := Resolve(e, testPolicy(), now); got != first {
			t.Fatalf("got %+v, want the same %+v", got, first)
		}
	}
}

func TestResolutionCarriesTheWinningDetail(t *testing.T) {
	e := Evidence{Heartbeat: &Observation{
		Source:     SourceHeartbeat,
		Claim:      ClaimBusy,
		Detail:     "⠐ Run sleep command",
		ObservedAt: now,
	}}
	if got := Resolve(e, testPolicy(), now); got.Detail != "⠐ Run sleep command" {
		t.Fatalf("detail %q, want the heartbeat's title", got.Detail)
	}
}

func TestDwellFor(t *testing.T) {
	policy := testPolicy()

	if got := DwellFor(protocol.SessionStatePendingApproval, Evidence{ReviewerInLoop: true}, policy); got != policy.GuardianDwell {
		t.Fatalf("guardian dwell = %s, want %s", got, policy.GuardianDwell)
	}
	if got := DwellFor(protocol.SessionStatePendingApproval, Evidence{}, policy); got != 0 {
		t.Fatalf("unattended approval dwell = %s, want 0", got)
	}
	for _, state := range []protocol.SessionState{
		protocol.SessionStateWaitingInput,
		protocol.SessionStateWorking,
		protocol.SessionStateIdle,
	} {
		if got := DwellFor(state, Evidence{ReviewerInLoop: true}, policy); got != 0 {
			t.Fatalf("%s dwell = %s, want 0", state, got)
		}
	}
}

// The per-agent TTLs are measured; codex repaints ~10x faster than claude.
func TestPolicyForUsesTheMeasuredPerAgentTTL(t *testing.T) {
	claude := PolicyFor(string(protocol.SessionAgentClaude))
	codex := PolicyFor(string(protocol.SessionAgentCodex))

	if claude.HeartbeatTTL <= codex.HeartbeatTTL {
		t.Fatalf("claude TTL %s must exceed codex's %s: claude repaints ~1Hz, codex ~10Hz",
			claude.HeartbeatTTL, codex.HeartbeatTTL)
	}
	if got := PolicyFor("copilot"); got.HeartbeatTTL > claude.HeartbeatTTL {
		t.Fatalf("unknown agent TTL %s, want no more than claude's %s", got.HeartbeatTTL, claude.HeartbeatTTL)
	}
	for _, policy := range []Policy{claude, codex, PolicyFor("copilot")} {
		if policy.StaleAfter <= 0 || policy.StuckAfter <= 0 || policy.GuardianDwell <= 0 {
			t.Fatalf("policy has an unset window: %+v", policy)
		}
		if policy.StaleAfter <= policy.HeartbeatTTL {
			t.Fatalf("StaleAfter %s must exceed HeartbeatTTL %s", policy.StaleAfter, policy.HeartbeatTTL)
		}
		if policy.HeartbeatSettleAfter <= policy.HeartbeatTTL {
			t.Fatalf("HeartbeatSettleAfter %s must exceed HeartbeatTTL %s",
				policy.HeartbeatSettleAfter, policy.HeartbeatTTL)
		}
	}
}

func TestShellLifecycleResolvesOnTheForegroundHeartbeatAlone(t *testing.T) {
	policy := PolicyFor(string(protocol.SessionAgentShell))

	if policy.HeartbeatTTL <= time.Second {
		t.Fatalf("shell HeartbeatTTL %s must exceed the 1s poll interval", policy.HeartbeatTTL)
	}

	for _, tc := range []struct {
		name       string
		evidence   Evidence
		wantState  protocol.SessionState
		wantReason Reason
	}{
		{
			name: "at the prompt a shell is idle",
			evidence: Evidence{
				Heartbeat: seen(SourceHeartbeat, ClaimSettled, 200*time.Millisecond),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonAtPrompt,
		},
		{
			name: "a foreground command is working",
			evidence: Evidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, time.Second),
				LastBusyAt: now.Add(-time.Second),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonHeartbeatBusy,
		},
		{
			name: "the prompt returning settles the shell",
			evidence: Evidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, 200*time.Millisecond),
				LastBusyAt: now.Add(-2 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonAtPrompt,
		},
		{
			name: "an exited shell is idle whatever the heartbeat said",
			evidence: Evidence{
				Process:    seen(SourceProcess, ClaimExited, time.Second),
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 500*time.Millisecond),
				LastBusyAt: now.Add(-500 * time.Millisecond),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonProcessExited,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(tc.evidence, policy, now)
			if got.Hold {
				t.Fatalf("Resolve() held, want %s/%s", tc.wantState, tc.wantReason)
			}
			if got.State != tc.wantState || got.Reason != tc.wantReason {
				t.Fatalf("Resolve() = %s/%s, want %s/%s", got.State, got.Reason, tc.wantState, tc.wantReason)
			}
		})
	}
}

// codex has no compaction hook, so the HeartbeatSettleAfter fallback stays.
func TestCompactionIsWorkNothingElseCanSee(t *testing.T) {
	policy := testPolicy()
	e := Evidence{
		TurnEverOpened: true,
		Compacting:     true,
		Heartbeat:      seen(SourceHeartbeat, ClaimSettled, 10*time.Second),
		LastBusyAt:     now.Add(-10 * time.Second),
		LastMovement:   now.Add(-time.Second),
	}

	got := Resolve(e, policy, now)
	if got.State != protocol.SessionStateWorking || got.Reason != ReasonCompacting {
		t.Fatalf("resolved %s/%s while compacting, want working/compacting", got.State, got.Reason)
	}

	e.LastMovement = now.Add(-policy.StuckAfter - time.Second)
	if got := Resolve(e, policy, now); got.State != protocol.SessionStateUnknown {
		t.Fatalf("resolved %s/%s after total silence, want unknown: a lost PostCompact must not hold working forever", got.State, got.Reason)
	}
}

func TestATurnKilledByTheAPIAsksForTheUser(t *testing.T) {
	policy := testPolicy()
	e := Evidence{
		TurnEverOpened: true,
		LastHarnessEvent: &Observation{
			Source:     SourceHarnessEvent,
			Claim:      ClaimStopFailed,
			Detail:     "rate_limit: usage limit reached",
			ObservedAt: now.Add(-time.Second),
		},
		LastBusyAt:   now.Add(-5 * time.Second),
		LastMovement: now.Add(-time.Second),
	}

	got := Resolve(e, policy, now)
	if got.State != protocol.SessionStateWaitingInput || got.Reason != ReasonStopFailed {
		t.Fatalf("resolved %s/%s, want waiting_input/stop_failed", got.State, got.Reason)
	}
	if got.Detail != "rate_limit: usage limit reached" {
		t.Fatalf("detail = %q, want the error carried through: which failure it was is the part worth reading", got.Detail)
	}

	e.Heartbeat = seen(SourceHeartbeat, ClaimBusy, 100*time.Millisecond)
	e.LastBusyAt = now.Add(-100 * time.Millisecond)
	if got := Resolve(e, policy, now); got.State != protocol.SessionStateWorking {
		t.Fatalf("resolved %s/%s once the agent ran again, want working", got.State, got.Reason)
	}
}

// An interrupt fires no hook on any agent, so without the abort edge only
// StaleAfter retires the bracket.
func TestHaltingATurnSettlesItWithoutWaitingOutTheStaleWindow(t *testing.T) {
	policy := testPolicy()
	// Measured on claude 2.1.220: the last busy frame lands just before ESC, the
	// idle glyph 0.07s after it, and nothing at all after that.
	abortedAt := now
	e := Evidence{
		TurnOpen:       true,
		TurnEverOpened: true,
		LastBusyAt:     abortedAt.Add(-300 * time.Millisecond),
		Heartbeat: &Observation{
			Source:     SourceHeartbeat,
			Claim:      ClaimSettled,
			ObservedAt: abortedAt.Add(70 * time.Millisecond),
		},
		LastHarnessEvent: &Observation{
			Source:     SourceHarnessEvent,
			Claim:      ClaimTurnAborted,
			Detail:     "[Request interrupted by user]",
			ObservedAt: abortedAt,
		},
		LastMovement: abortedAt.Add(70 * time.Millisecond),
	}

	for _, age := range []time.Duration{
		time.Second,
		policy.StaleAfter,
		policy.StaleAfter + policy.SettleGrace + time.Second,
		policy.StuckAfter + time.Second,
	} {
		got := Resolve(e, policy, abortedAt.Add(age))
		if got.State != protocol.SessionStateIdle || got.Reason != ReasonTurnAborted {
			t.Fatalf("%s after the halt: resolved %s/%s, want idle/turn_aborted", age, got.State, got.Reason)
		}
		if got.Detail != "[Request interrupted by user]" {
			t.Fatalf("%s after the halt: detail = %q, want what the transcript said", age, got.Detail)
		}
	}
}
