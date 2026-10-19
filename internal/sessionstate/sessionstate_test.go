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
		evidence   evidence
		wantState  protocol.SessionState
		wantReason Reason
		wantHold   bool
	}{

		{
			name: "a fresh heartbeat works without any bracket",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat: seen(SourceHeartbeat, ClaimBusy, 200*time.Millisecond)}},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonHeartbeatBusy,
		},
		{
			name: "an open bracket goes stale when the heartbeat goes silent",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 10*time.Second),
				LastBusyAt: now.Add(-10 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnOpen: true},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonBracketStale,
		},
		{
			name: "a stale bracket takes the classifier's verdict when there is one",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 5*time.Second),
				LastBusyAt: now.Add(-5 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnOpen: true,

					LastClassifier: seen(SourceClassifier, ClaimNeedsInput, 2*time.Second)},
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonClassifierVerdict,
		},
		{
			name: "a not-busy blip inside the window does not settle an open turn",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, 10*time.Millisecond),
				LastBusyAt: now.Add(-500 * time.Millisecond)},
				ConversationEvidence: ConversationEvidence{
					TurnOpen: true},
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			name: "a prompt-idle confirmation closes a bracket whose hook never came",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				LastBusyAt: now.Add(-90 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnOpen: true,

					PromptIdleAt: now.Add(-30 * time.Second)},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonPromptIdle,
		},
		{
			name: "a busy frame after the confirmation spends it",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 2*time.Second),
				LastBusyAt: now.Add(-2 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnOpen: true,

					PromptIdleAt: now.Add(-30 * time.Second)},
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			name: "an open approval outranks a prompt-idle confirmation",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				LastBusyAt: now.Add(-90 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnOpen: true,

					PromptIdleAt:     now.Add(-30 * time.Second),
					LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, 40*time.Second)},
			},
			wantState:  protocol.SessionStatePendingApproval,
			wantReason: ReasonApprovalOpen,
		},
		{
			name: "a prompt-idle confirmation defers to the classifier verdict",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				LastBusyAt: now.Add(-90 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnOpen: true,

					PromptIdleAt:   now.Add(-30 * time.Second),
					LastClassifier: seen(SourceClassifier, ClaimNeedsInput, 25*time.Second)},
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonClassifierVerdict,
		},
		{
			name: "busy resuming after a blip keeps the turn working",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 50*time.Millisecond),
				LastBusyAt: now.Add(-50 * time.Millisecond)},
				ConversationEvidence: ConversationEvidence{
					TurnOpen: true},
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			name: "a not-busy level past the window settles the turn",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, 10*time.Millisecond),
				LastBusyAt: now.Add(-10 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnOpen: true},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonBracketStale,
		},
		{
			name: "a bracket with no heartbeat at all stays open",
			evidence: evidence{ConversationEvidence: ConversationEvidence{
				ToolOpen: true}},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			name: "a mid-tool heartbeat gap inside StaleAfter keeps the bracket open",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 3500*time.Millisecond),
				LastBusyAt: now.Add(-3500 * time.Millisecond)},
				ConversationEvidence: ConversationEvidence{
					ToolOpen: true},
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},

		{
			name: "closed brackets and a quiet heartbeat settle with no verdict at all",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, 2*time.Second),
				LastBusyAt: now.Add(-30 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnEverOpened: true},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonHeartbeatSettled,
		},

		{
			name: "a bracket that just went stale holds instead of asserting idle",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 5*time.Second),
				LastBusyAt: now.Add(-5 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnOpen: true},
			},
			wantHold:   true,
			wantReason: ReasonSettleGrace,
		},
		{
			name: "a settle waits for a classification that is in flight",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt: now.Add(-30 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnEverOpened: true,

					ClassifyingSince: now.Add(-2 * time.Second)},
			},
			wantHold:   true,
			wantReason: ReasonAwaitingVerdict,
		},
		{
			name: "a classification past its timeout stops holding the settle",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt: now.Add(-90 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnEverOpened: true,

					ClassifyingSince: now.Add(-31 * time.Second)},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonHeartbeatSettled,
		},
		{
			name: "a verdict the agent has gone busy past is not this turn's answer",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt: now.Add(-6 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnEverOpened: true,

					LastClassifier:   seen(SourceClassifier, ClaimNeedsInput, 20*time.Second),
					ClassifyingSince: now.Add(-2 * time.Second)},
			},
			wantHold:   true,
			wantReason: ReasonAwaitingVerdict,
		},

		{
			name: "a session that has never opened a turn is at its prompt, not settled",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt: now.Add(-3 * time.Second)},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonAtPrompt,
		},
		{
			name: "a stale busy frame before the first turn is not a prompt",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, time.Hour),
				LastBusyAt: now.Add(-time.Hour)},
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonNoEvidence,
		},
		{
			name: "an approval the agent has gone busy past was answered",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt: now.Add(-10 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnEverOpened:   true,
					LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, 30*time.Second),

					LastClassifier: seen(SourceClassifier, ClaimIdle, 2*time.Second)},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonClassifierVerdict,
		},
		{
			name: "an approval still newer than the last busy frame is live",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt: now.Add(-20 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, 5*time.Second)},
			},
			wantState:  protocol.SessionStatePendingApproval,
			wantReason: ReasonApprovalOpen,
		},

		{
			name: "an exited process beats every live signal",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Process:   seen(SourceProcess, ClaimExited, time.Second),
				Heartbeat: seen(SourceHeartbeat, ClaimBusy, 10*time.Millisecond)},
				ConversationEvidence: ConversationEvidence{
					TurnOpen:         true,
					LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, time.Second)},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonProcessExited,
		},
		{
			name: "a fresh heartbeat beats a stale approval edge",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat: seen(SourceHeartbeat, ClaimBusy, 100*time.Millisecond)},
				ConversationEvidence: ConversationEvidence{
					LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, 30*time.Second)}},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonHeartbeatBusy,
		},
		{
			name: "an approval survives an expired heartbeat",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat: seen(SourceHeartbeat, ClaimBusy, 3*time.Second)},
				ConversationEvidence: ConversationEvidence{
					LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, 2*time.Second)}},
			wantState:  protocol.SessionStatePendingApproval,
			wantReason: ReasonApprovalOpen,
		},
		{
			name: "a reviewer in the loop does not hide an approval",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				ReviewerInLoop: true},
				ConversationEvidence: ConversationEvidence{
					LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, time.Second)}},
			wantState:  protocol.SessionStatePendingApproval,
			wantReason: ReasonApprovalOpen,
		},
		{
			name: "a pending cron does not rename what the turn settled to",
			evidence: evidence{ConversationEvidence: ConversationEvidence{
				PendingCron:    true,
				LastClassifier: seen(SourceClassifier, ClaimIdle, time.Second)},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonClassifierVerdict,
		},
		{
			name: "a pending cron names the settle without suppressing it",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt: now.Add(-30 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					PendingCron: true},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonCronPending,
		},
		{
			name: "a parked session that starts running again is working",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 100*time.Millisecond),
				LastBusyAt: now.Add(-100 * time.Millisecond)},
				ConversationEvidence: ConversationEvidence{
					PendingCron: true},
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonHeartbeatBusy,
		},
		{
			name: "a judged yield settles on its verdict: the running process is a leftover",
			evidence: evidence{ConversationEvidence: ConversationEvidence{
				BackgroundWork: true,
				LastClassifier: seen(SourceClassifier, ClaimIdle, time.Second)},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonClassifierVerdict,
		},
		{
			name: "a parked verdict holds a yielded turn working without decaying to unknown",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				LastMovement: now.Add(-2 * time.Minute)},
				ConversationEvidence: ConversationEvidence{
					BackgroundWork: true,
					TurnEverOpened: true,
					LastClassifier: seen(SourceClassifier, ClaimParked, 2*time.Minute)},
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBackgroundParked,
		},
		{
			name: "a parked verdict past the tripwire settles on the prompt-idle confirmation",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				LastBusyAt: now.Add(-32 * time.Minute),

				LastMovement: now.Add(-31 * time.Minute)},
				ConversationEvidence: ConversationEvidence{
					BackgroundWork: true,
					TurnEverOpened: true,
					LastClassifier: seen(SourceClassifier, ClaimParked, 31*time.Minute),

					PromptIdleAt: now.Add(-30 * time.Minute)},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonParkedExpired,
		},
		{
			name: "a parked verdict past the tripwire with no prompt confirmation is stuck",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				LastMovement: now.Add(-31 * time.Minute)},
				ConversationEvidence: ConversationEvidence{
					BackgroundWork: true,
					TurnEverOpened: true,
					LastClassifier: seen(SourceClassifier, ClaimParked, 31*time.Minute)},
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonStuck,
		},
		{
			name: "a busy frame spends a parked verdict",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				LastBusyAt:   now.Add(-10 * time.Second),
				LastMovement: now.Add(-10 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					BackgroundWork: true,
					TurnEverOpened: true,
					LastClassifier: seen(SourceClassifier, ClaimParked, 30*time.Second)},
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBackgroundWork,
		},
		{
			name: "a waiting verdict on a yield asks the user",
			evidence: evidence{ConversationEvidence: ConversationEvidence{
				BackgroundWork: true,
				LastClassifier: seen(SourceClassifier, ClaimNeedsInput, time.Second)},
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonClassifierVerdict,
		},
		{
			name: "outstanding work outranks a parked wakeup",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				LastMovement: now.Add(-time.Second)},
				ConversationEvidence: ConversationEvidence{
					BackgroundWork: true,
					PendingCron:    true},
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBackgroundWork,
		},
		{
			name: "a yield that resumed nothing and went quiet is stuck",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				LastMovement: now.Add(-91 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					BackgroundWork: true,
					TurnEverOpened: true},
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonStuck,
		},

		{
			name: "a judged turn is not a fresh prompt, whatever the brackets say",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat: seen(SourceHeartbeat, ClaimSettled, time.Second),

				LastBusyAt: now.Add(-5 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					LastClassifier: seen(SourceClassifier, ClaimNeedsInput, time.Second)},
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonClassifierVerdict,
		},
		{
			name: "a turn being judged right now is not a fresh prompt either",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat: seen(SourceHeartbeat, ClaimSettled, time.Second),

				LastBusyAt: now.Add(-5 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					ClassifyingSince: now.Add(-time.Second)},
			},
			wantHold:   true,
			wantReason: ReasonAwaitingVerdict,
		},

		{
			name: "an announced question waits on the user",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat: seen(SourceHeartbeat, ClaimSettled, time.Second),

				LastBusyAt: now.Add(-3 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnEverOpened: true,

					LastHarnessEvent: seen(SourceHarnessEvent, ClaimNeedsInput, 2*time.Second)},
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonQuestionOpen,
		},
		{
			name: "a question the agent has gone busy past was answered",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, time.Second),
				LastBusyAt: now.Add(-2 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnEverOpened:   true,
					LastHarnessEvent: seen(SourceHarnessEvent, ClaimNeedsInput, 10*time.Second),

					LastClassifier: seen(SourceClassifier, ClaimIdle, time.Second)},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonClassifierVerdict,
		},

		{
			name: "the classifier settles a finished turn to idle",
			evidence: evidence{ConversationEvidence: ConversationEvidence{
				LastClassifier: seen(SourceClassifier, ClaimIdle, time.Second)}},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonClassifierVerdict,
		},
		{
			name: "the classifier settles a question to waiting_input",
			evidence: evidence{ConversationEvidence: ConversationEvidence{
				LastClassifier: seen(SourceClassifier, ClaimNeedsInput, time.Second)}},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonClassifierVerdict,
		},

		{
			name: "evidence that stopped moving is stuck",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				LastMovement: now.Add(-91 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnEverOpened: true}},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonStuck,
		},
		{
			name: "a session that never took a turn is quiet, not stuck",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				LastMovement: now.Add(-10 * time.Minute)}},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonNoEvidence,
		},
		{
			name: "an open bracket stops outranking stuck once everything goes quiet",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				LastMovement: now.Add(-91 * time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnOpen:       true,
					TurnEverOpened: true},
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonStuck,
		},
		{
			name: "an open bracket still outranks stuck while evidence keeps arriving",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				LastBusyAt:   now.Add(-time.Second),
				LastMovement: now.Add(-time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnOpen:       true,
					TurnEverOpened: true},
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			name: "recent silence is not yet stuck",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				LastMovement: now.Add(-30 * time.Second)}},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonNoEvidence,
		},
		{
			name:       "an empty evidence table reports no evidence",
			evidence:   evidence{},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonNoEvidence,
		},

		{
			name: "an aborted turn settles immediately, with its bracket still open",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, 200*time.Millisecond),
				LastBusyAt: now.Add(-300 * time.Millisecond),

				LastMovement: now.Add(-200 * time.Millisecond)},
				ConversationEvidence: ConversationEvidence{
					TurnOpen:       true,
					TurnEverOpened: true,

					LastHarnessEvent: seen(SourceHarnessEvent, ClaimTurnAborted, 200*time.Millisecond)},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonTurnAborted,
		},
		{
			name: "an aborted turn does not wait for a verdict",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				LastMovement: now.Add(-time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnEverOpened:   true,
					LastHarnessEvent: seen(SourceHarnessEvent, ClaimTurnAborted, time.Second),
					ClassifyingSince: now.Add(-time.Second)},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonTurnAborted,
		},
		{
			name: "an aborted turn outranks compaction",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				LastMovement: now.Add(-time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnEverOpened:   true,
					Compacting:       true,
					LastHarnessEvent: seen(SourceHarnessEvent, ClaimTurnAborted, time.Second)},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonTurnAborted,
		},
		{
			name: "a busy frame after an abort retires it",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 100*time.Millisecond),
				LastBusyAt: now.Add(-100 * time.Millisecond),

				LastMovement: now.Add(-100 * time.Millisecond)},
				ConversationEvidence: ConversationEvidence{
					TurnOpen:       true,
					TurnEverOpened: true,

					LastHarnessEvent: seen(SourceHarnessEvent, ClaimTurnAborted, 3*time.Second)},
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			name: "an aborted turn is not read as an outstanding approval",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				LastMovement: now.Add(-time.Second)},
				ConversationEvidence: ConversationEvidence{
					TurnEverOpened:   true,
					LastHarnessEvent: seen(SourceHarnessEvent, ClaimTurnAborted, time.Second)},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonTurnAborted,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(tc.evidence.TerminalEvidence, tc.evidence.ConversationEvidence, testPolicy(), now)
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
		e := evidence{TerminalEvidence: TerminalEvidence{
			Heartbeat:  seen(SourceHeartbeat, ClaimBusy, tc.age),
			LastBusyAt: now.Add(-tc.age)},
			ConversationEvidence: ConversationEvidence{
				TurnEverOpened: true},
		}
		if got := Resolve(e.TerminalEvidence, e.ConversationEvidence, policy, now); got.State != tc.want {
			t.Fatalf("heartbeat aged %s resolved %s, want %s", tc.age, got.State, tc.want)
		}
	}
}

func TestARepaintGapWiderThanTheTTLDoesNotFlapTheSession(t *testing.T) {
	policy := testPolicy()
	const repaint = 1920 * time.Millisecond
	if repaint <= policy.HeartbeatTTL || repaint >= policy.HeartbeatSettleAfter {
		t.Fatalf("repaint %s must fall between the TTL %s and the settle window %s for this test to mean anything",
			repaint, policy.HeartbeatTTL, policy.HeartbeatSettleAfter)
	}

	for tick := 1; tick <= 30; tick++ {
		at := now.Add(time.Duration(tick) * time.Second)
		lastFrame := now.Add((at.Sub(now) / repaint) * repaint)
		e := evidence{TerminalEvidence: TerminalEvidence{
			Heartbeat:    &Observation{Source: SourceHeartbeat, Claim: ClaimBusy, ObservedAt: lastFrame},
			LastBusyAt:   lastFrame,
			LastMovement: lastFrame},
			ConversationEvidence: ConversationEvidence{
				TurnEverOpened: true},
		}
		if got := Resolve(e.TerminalEvidence, e.ConversationEvidence, policy, at); got.State != protocol.SessionStateWorking {
			t.Fatalf("tick %d: a busy frame %s old resolved %s/%s, want working: a repaint gap is not a settle",
				tick, at.Sub(lastFrame), got.State, got.Reason)
		}
	}
}

func TestAPromptIdleConfirmationRetiresAnOutstandingBackgroundTask(t *testing.T) {
	policy := testPolicy()

	yielded := now
	at := func(d time.Duration) evidence {
		e := evidence{TerminalEvidence: TerminalEvidence{
			Heartbeat:    &Observation{Source: SourceHeartbeat, Claim: ClaimSettled, ObservedAt: yielded},
			LastBusyAt:   yielded.Add(-time.Second),
			LastMovement: yielded},
			ConversationEvidence: ConversationEvidence{
				TurnEverOpened: true,
				BackgroundWork: true},
		}
		if d >= time.Minute {
			e.PromptIdleAt = yielded.Add(time.Minute)
			e.LastMovement = yielded.Add(time.Minute)
		}
		return e
	}

	if got := Resolve(at(30*time.Second).TerminalEvidence, at(30*time.Second).ConversationEvidence, policy, yielded.Add(30*time.Second)); got.State != protocol.SessionStateWorking {
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
		got := Resolve(at(d).TerminalEvidence, at(d).ConversationEvidence, policy, yielded.Add(d))
		if got.State == protocol.SessionStateUnknown {
			t.Fatalf("%s in: resolved unknown/%s after the harness said the agent is at its prompt", d, got.Reason)
		}
		if got.State != protocol.SessionStateIdle || got.Reason != ReasonPromptIdle {
			t.Fatalf("%s in: resolved %s/%s, want idle/%s", d, got.State, got.Reason, ReasonPromptIdle)
		}
	}
}

func TestAParkedVerdictOutlastsThePromptIdleConfirmation(t *testing.T) {
	policy := testPolicy()

	yielded := now
	at := func(d time.Duration) evidence {
		e := evidence{TerminalEvidence: TerminalEvidence{
			Heartbeat:    &Observation{Source: SourceHeartbeat, Claim: ClaimSettled, ObservedAt: yielded},
			LastBusyAt:   yielded.Add(-time.Second),
			LastMovement: yielded},
			ConversationEvidence: ConversationEvidence{
				TurnEverOpened: true,
				BackgroundWork: true,

				LastClassifier: &Observation{Source: SourceClassifier, Claim: ClaimParked, ObservedAt: yielded.Add(5 * time.Second)}},
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
		got := Resolve(at(d).TerminalEvidence, at(d).ConversationEvidence, policy, yielded.Add(d))
		if got.State != protocol.SessionStateWorking || got.Reason != ReasonBackgroundParked {
			t.Fatalf("%s in: resolved %s/%s, want working/%s", d, got.State, got.Reason, ReasonBackgroundParked)
		}
	}
}

func TestAParkedWakeupDoesNotExcuseTheAgentFromTheQueue(t *testing.T) {
	policy := testPolicy()
	e := evidence{TerminalEvidence: TerminalEvidence{
		LastBusyAt:   now.Add(-2 * time.Minute),
		LastMovement: now.Add(-time.Minute)},
		ConversationEvidence: ConversationEvidence{
			TurnEverOpened: true,
			PendingCron:    true},
	}

	got := Resolve(e.TerminalEvidence, e.ConversationEvidence, policy, now)
	if got.State != protocol.SessionStateIdle || got.Reason != ReasonCronPending {
		t.Fatalf("resolved %s/%s, want idle/cron_pending", got.State, got.Reason)
	}

	e.PromptIdleAt = now.Add(-time.Second)
	if got := Resolve(e.TerminalEvidence, e.ConversationEvidence, policy, now); got.State != protocol.SessionStateIdle || got.Reason != ReasonPromptIdle {
		t.Fatalf("resolved %s/%s after the confirmation, want idle/prompt_idle", got.State, got.Reason)
	}

	e.BackgroundWork = true
	if got := Resolve(e.TerminalEvidence, e.ConversationEvidence, policy, now); got.State != protocol.SessionStateIdle {
		t.Fatalf("resolved %s/%s with both facts set, want idle", got.State, got.Reason)
	}
}

func TestAParkedWakeupDoesNotRotIntoUnknown(t *testing.T) {
	policy := testPolicy()
	parked := now.Add(-time.Minute)
	e := evidence{TerminalEvidence: TerminalEvidence{
		LastBusyAt:   parked,
		LastMovement: parked},
		ConversationEvidence: ConversationEvidence{
			TurnEverOpened: true,
			PendingCron:    true},
	}

	for _, quiet := range []time.Duration{
		policy.StuckAfter - time.Second,
		policy.StuckAfter,
		policy.StuckAfter + time.Second,
		10 * policy.StuckAfter,
	} {
		at := parked.Add(quiet)
		got := Resolve(e.TerminalEvidence, e.ConversationEvidence, policy, at)
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
		e := evidence{TerminalEvidence: TerminalEvidence{
			Heartbeat:  seen(SourceHeartbeat, ClaimBusy, age),
			LastBusyAt: now.Add(-age)},
			ConversationEvidence: ConversationEvidence{
				TurnOpen: true},
		}
		got := Resolve(e.TerminalEvidence, e.ConversationEvidence, policy, now)
		if got.State != protocol.SessionStateWorking || got.Reason != ReasonBracketOpen {
			t.Fatalf("a %s gap past the %s TTL resolved %s/%s, want working/%s: the TTL must not close a bracket",
				age, policy.HeartbeatTTL, got.State, got.Reason, ReasonBracketOpen)
		}
	}
}

func TestARunningTurnKeepsOneAnswerWhileItKeepsPainting(t *testing.T) {
	policy := testPolicy()
	for _, tc := range []struct {
		name     string
		evidence func(age time.Duration) evidence
		believed time.Duration
	}{
		{
			name: "an open bracket believes a frame until the stale window",
			evidence: func(age time.Duration) evidence {
				return evidence{TerminalEvidence: TerminalEvidence{
					Heartbeat:    seen(SourceHeartbeat, ClaimBusy, age),
					LastBusyAt:   now.Add(-age),
					LastMovement: now.Add(-age)},
					ConversationEvidence: ConversationEvidence{
						TurnOpen:       true,
						TurnEverOpened: true},
				}
			},
			believed: policy.StaleAfter,
		},
		{
			name: "a bare heartbeat believes a frame until the settle window",
			evidence: func(age time.Duration) evidence {
				return evidence{TerminalEvidence: TerminalEvidence{
					Heartbeat:    seen(SourceHeartbeat, ClaimBusy, age),
					LastBusyAt:   now.Add(-age),
					LastMovement: now.Add(-age)},
					ConversationEvidence: ConversationEvidence{
						TurnEverOpened: true},
				}
			},
			believed: policy.HeartbeatSettleAfter,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := Resolve(tc.evidence(0).TerminalEvidence, tc.evidence(0).ConversationEvidence, policy, now)
			if want.State != protocol.SessionStateWorking {
				t.Fatalf("the freshest frame resolved %s, want working", want.State)
			}
			for age := time.Duration(0); age <= tc.believed; age += time.Millisecond {
				if got := Resolve(tc.evidence(age).TerminalEvidence, tc.evidence(age).ConversationEvidence, policy, now); got != want {
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
		e := evidence{TerminalEvidence: TerminalEvidence{
			Heartbeat: seen(SourceHeartbeat, ClaimBusy, tc.age), LastBusyAt: now.Add(-tc.age)},
			ConversationEvidence: ConversationEvidence{
				TurnOpen: true}}
		if got := Resolve(e.TerminalEvidence, e.ConversationEvidence, policy, now); got.Reason != tc.want {
			t.Fatalf("bracket with %s of silence resolved %s, want %s", tc.age, got.Reason, tc.want)
		}
	}
}

func TestResolveIsStableForTheSameInputs(t *testing.T) {
	e := evidence{TerminalEvidence: TerminalEvidence{
		Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 2*time.Second),
		LastBusyAt: now.Add(-2 * time.Second),

		LastMovement: now.Add(-time.Second)},
		ConversationEvidence: ConversationEvidence{
			TurnOpen: true,

			LastClassifier: seen(SourceClassifier, ClaimIdle, time.Second)},
	}
	first := Resolve(e.TerminalEvidence, e.ConversationEvidence, testPolicy(), now)
	for range 5 {
		if got := Resolve(e.TerminalEvidence, e.ConversationEvidence, testPolicy(), now); got != first {
			t.Fatalf("got %+v, want the same %+v", got, first)
		}
	}
}

func TestResolutionCarriesTheWinningDetail(t *testing.T) {
	e := evidence{TerminalEvidence: TerminalEvidence{
		Heartbeat: &Observation{
			Source:     SourceHeartbeat,
			Claim:      ClaimBusy,
			Detail:     "⠐ Run sleep command",
			ObservedAt: now,
		}}}
	if got := Resolve(e.TerminalEvidence, e.ConversationEvidence, testPolicy(), now); got.Detail != "⠐ Run sleep command" {
		t.Fatalf("detail %q, want the heartbeat's title", got.Detail)
	}
}

func TestDwellFor(t *testing.T) {
	policy := testPolicy()

	if got := DwellFor(protocol.SessionStatePendingApproval, TerminalEvidence{
		ReviewerInLoop: true}, ConversationEvidence{}, policy); got != policy.GuardianDwell {
		t.Fatalf("guardian dwell = %s, want %s", got, policy.GuardianDwell)
	}
	if got := DwellFor(protocol.SessionStatePendingApproval, TerminalEvidence{}, ConversationEvidence{}, policy); got != 0 {
		t.Fatalf("unattended approval dwell = %s, want 0", got)
	}
	for _, state := range []protocol.SessionState{
		protocol.SessionStateWaitingInput,
		protocol.SessionStateWorking,
		protocol.SessionStateIdle,
	} {
		if got := DwellFor(state, TerminalEvidence{
			ReviewerInLoop: true}, ConversationEvidence{}, policy); got != 0 {
			t.Fatalf("%s dwell = %s, want 0", state, got)
		}
	}
}

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
		evidence   evidence
		wantState  protocol.SessionState
		wantReason Reason
	}{
		{
			name: "at the prompt a shell is idle",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat: seen(SourceHeartbeat, ClaimSettled, 200*time.Millisecond)}},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonAtPrompt,
		},
		{
			name: "a foreground command is working",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, time.Second),
				LastBusyAt: now.Add(-time.Second)},
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonHeartbeatBusy,
		},
		{
			name: "the prompt returning settles the shell",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Heartbeat:  seen(SourceHeartbeat, ClaimSettled, 200*time.Millisecond),
				LastBusyAt: now.Add(-2 * time.Second)},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonAtPrompt,
		},
		{
			name: "an exited shell is idle whatever the heartbeat said",
			evidence: evidence{TerminalEvidence: TerminalEvidence{
				Process:    seen(SourceProcess, ClaimExited, time.Second),
				Heartbeat:  seen(SourceHeartbeat, ClaimBusy, 500*time.Millisecond),
				LastBusyAt: now.Add(-500 * time.Millisecond)},
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonProcessExited,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(tc.evidence.TerminalEvidence, tc.evidence.ConversationEvidence, policy, now)
			if got.Hold {
				t.Fatalf("Resolve() held, want %s/%s", tc.wantState, tc.wantReason)
			}
			if got.State != tc.wantState || got.Reason != tc.wantReason {
				t.Fatalf("Resolve() = %s/%s, want %s/%s", got.State, got.Reason, tc.wantState, tc.wantReason)
			}
		})
	}
}

func TestCompactionIsWorkNothingElseCanSee(t *testing.T) {
	policy := testPolicy()
	e := evidence{TerminalEvidence: TerminalEvidence{
		Heartbeat:    seen(SourceHeartbeat, ClaimSettled, 10*time.Second),
		LastBusyAt:   now.Add(-10 * time.Second),
		LastMovement: now.Add(-time.Second)},
		ConversationEvidence: ConversationEvidence{
			TurnEverOpened: true,
			Compacting:     true},
	}

	got := Resolve(e.TerminalEvidence, e.ConversationEvidence, policy, now)
	if got.State != protocol.SessionStateWorking || got.Reason != ReasonCompacting {
		t.Fatalf("resolved %s/%s while compacting, want working/compacting", got.State, got.Reason)
	}

	e.LastMovement = now.Add(-policy.StuckAfter - time.Second)
	if got := Resolve(e.TerminalEvidence, e.ConversationEvidence, policy, now); got.State != protocol.SessionStateUnknown {
		t.Fatalf("resolved %s/%s after total silence, want unknown: a lost PostCompact must not hold working forever", got.State, got.Reason)
	}
}

func TestATurnKilledByTheAPIAsksForTheUser(t *testing.T) {
	policy := testPolicy()
	e := evidence{TerminalEvidence: TerminalEvidence{
		LastBusyAt:   now.Add(-5 * time.Second),
		LastMovement: now.Add(-time.Second)},
		ConversationEvidence: ConversationEvidence{
			TurnEverOpened: true,
			LastHarnessEvent: &Observation{
				Source:     SourceHarnessEvent,
				Claim:      ClaimStopFailed,
				Detail:     "rate_limit: usage limit reached",
				ObservedAt: now.Add(-time.Second),
			}},
	}

	got := Resolve(e.TerminalEvidence, e.ConversationEvidence, policy, now)
	if got.State != protocol.SessionStateWaitingInput || got.Reason != ReasonStopFailed {
		t.Fatalf("resolved %s/%s, want waiting_input/stop_failed", got.State, got.Reason)
	}
	if got.Detail != "rate_limit: usage limit reached" {
		t.Fatalf("detail = %q, want the error carried through: which failure it was is the part worth reading", got.Detail)
	}

	e.Heartbeat = seen(SourceHeartbeat, ClaimBusy, 100*time.Millisecond)
	e.LastBusyAt = now.Add(-100 * time.Millisecond)
	if got := Resolve(e.TerminalEvidence, e.ConversationEvidence, policy, now); got.State != protocol.SessionStateWorking {
		t.Fatalf("resolved %s/%s once the agent ran again, want working", got.State, got.Reason)
	}
}

func TestHaltingATurnSettlesItWithoutWaitingOutTheStaleWindow(t *testing.T) {
	policy := testPolicy()
	abortedAt := now
	e := evidence{TerminalEvidence: TerminalEvidence{
		LastBusyAt: abortedAt.Add(-300 * time.Millisecond),
		Heartbeat: &Observation{
			Source:     SourceHeartbeat,
			Claim:      ClaimSettled,
			ObservedAt: abortedAt.Add(70 * time.Millisecond),
		},

		LastMovement: abortedAt.Add(70 * time.Millisecond)},
		ConversationEvidence: ConversationEvidence{
			TurnOpen:       true,
			TurnEverOpened: true,

			LastHarnessEvent: &Observation{
				Source:     SourceHarnessEvent,
				Claim:      ClaimTurnAborted,
				Detail:     "[Request interrupted by user]",
				ObservedAt: abortedAt,
			}},
	}

	for _, age := range []time.Duration{
		time.Second,
		policy.StaleAfter,
		policy.StaleAfter + policy.SettleGrace + time.Second,
		policy.StuckAfter + time.Second,
	} {
		got := Resolve(e.TerminalEvidence, e.ConversationEvidence, policy, abortedAt.Add(age))
		if got.State != protocol.SessionStateIdle || got.Reason != ReasonTurnAborted {
			t.Fatalf("%s after the halt: resolved %s/%s, want idle/turn_aborted", age, got.State, got.Reason)
		}
		if got.Detail != "[Request interrupted by user]" {
			t.Fatalf("%s after the halt: detail = %q, want what the transcript said", age, got.Detail)
		}
	}
}
