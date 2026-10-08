package sessionstate

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestClauseOrder(t *testing.T) {
	policy := testPolicy()

	for _, tc := range []struct {
		why        string
		evidence   Evidence
		wantState  protocol.SessionState
		wantReason Reason
	}{
		{
			why: "an exited process outranks everything: no amount of live-looking " +
				"evidence should color a dead session as alive, and the evidence " +
				"cannot refresh again to correct itself",
			evidence: Evidence{
				Process:          seen(SourceProcess, ClaimExited, time.Second),
				Heartbeat:        seen(SourceHeartbeat, ClaimBusy, 100*time.Millisecond),
				LastBusyAt:       now.Add(-100 * time.Millisecond),
				TurnOpen:         true,
				TurnEverOpened:   true,
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonProcessExited,
		},
		{
			why: "a running agent outranks its own approval request: it cannot be " +
				"blocked on the user while visibly working, so the request was " +
				"answered and its closing edge was lost",
			evidence: Evidence{
				Heartbeat:        seen(SourceHeartbeat, ClaimBusy, 100*time.Millisecond),
				LastBusyAt:       now.Add(-100 * time.Millisecond),
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, 5*time.Second),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonHeartbeatBusy,
		},
		{
			why: "an unanswered approval outranks a parked wakeup and an open " +
				"bracket alike: both describe a turn that will continue, and this " +
				"one says it will not continue until a person acts",
			evidence: Evidence{
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, time.Second),
				PendingCron:      true,
				TurnOpen:         true,
				TurnEverOpened:   true,
				LastBusyAt:       now.Add(-2 * time.Second),
			},
			wantState:  protocol.SessionStatePendingApproval,
			wantReason: ReasonApprovalOpen,
		},
		{
			why: "a question the classifier read out of the transcript outranks a " +
				"parked wakeup: the wakeup will resume the session, but not with " +
				"the answer the turn stopped for",
			evidence: Evidence{
				LastClassifier: seen(SourceClassifier, ClaimNeedsInput, time.Second),
				PendingCron:    true,
				TurnEverOpened: true,
				LastBusyAt:     now.Add(-2 * time.Second),
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonClassifierVerdict,
		},
		{
			why: "an announced question outranks the classifier's guess about how " +
				"the turn ended: the harness said what it is waiting for, and the " +
				"classifier is inferring it from a transcript",
			evidence: Evidence{
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimNeedsInput, time.Second),
				LastClassifier:   seen(SourceClassifier, ClaimIdle, time.Second),
				TurnEverOpened:   true,
				LastBusyAt:       now.Add(-2 * time.Second),
			},
			wantState:  protocol.SessionStateWaitingInput,
			wantReason: ReasonQuestionOpen,
		},
		{
			why: "a parked verdict outranks the prompt-idle confirmation: the " +
				"judge read the yield, the flat timer read a clock",
			evidence: Evidence{
				BackgroundWork: true,
				LastClassifier: seen(SourceClassifier, ClaimParked, 55*time.Second),
				PromptIdleAt:   now.Add(-time.Second),
				LastBusyAt:     now.Add(-time.Minute),
				LastMovement:   now.Add(-time.Second),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBackgroundParked,
		},
		{
			why: "the harness saying the agent is parked at its prompt outranks an " +
				"outstanding background task: the task is a guess about whether " +
				"anyone is waited on, and this is the harness answering it directly",
			evidence: Evidence{
				BackgroundWork: true,
				PromptIdleAt:   now.Add(-time.Second),
				LastBusyAt:     now.Add(-30 * time.Second),
				LastMovement:   now.Add(-time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonPromptIdle,
		},
		{
			why: "an outstanding background task still outranks everything below " +
				"while the harness has said nothing: without a confirmation the " +
				"task is the best account of why the session went quiet",
			evidence: Evidence{
				BackgroundWork: true,
				LastBusyAt:     now.Add(-30 * time.Second),
				LastMovement:   now.Add(-time.Second),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBackgroundWork,
		},
		{
			why: "the harness's confirmation that the agent sits at its prompt " +
				"outranks an open bracket: a bracket closes on a hook that may " +
				"never arrive, and this is a second hook, on a different trigger, " +
				"saying the same turn is over",
			evidence: Evidence{
				PromptIdleAt:   now.Add(-time.Second),
				TurnOpen:       true,
				TurnEverOpened: true,
				Heartbeat:      seen(SourceHeartbeat, ClaimBusy, 2*time.Second),
				LastBusyAt:     now.Add(-2 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonPromptIdle,
		},
		{
			why: "an open bracket outranks a settled heartbeat: claude paints a " +
				"not-busy glyph between tool calls and while a foreground tool is " +
				"still running, and settling on one of those frames reports a " +
				"finished turn that is still going",
			evidence: Evidence{
				TurnOpen:       true,
				TurnEverOpened: true,
				Heartbeat:      seen(SourceHeartbeat, ClaimSettled, 500*time.Millisecond),
				LastBusyAt:     now.Add(-time.Second),
				LastMovement:   now.Add(-500 * time.Millisecond),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			why: "total silence outranks an open bracket: the bracket is the one " +
				"level with no expiry of its own, so an agent with hooks and no " +
				"heartbeat would otherwise pin itself green for good",
			evidence: Evidence{
				TurnOpen:       true,
				TurnEverOpened: true,
				LastMovement:   now.Add(-91 * time.Second),
			},
			wantState:  protocol.SessionStateUnknown,
			wantReason: ReasonStuck,
		},
		{
			why: "and it does not outrank a first turn that has not happened: an " +
				"agent launched and left alone is quiet because there is nothing " +
				"to report, not because it stopped reporting",
			evidence: Evidence{
				Heartbeat:    seen(SourceHeartbeat, ClaimSettled, 91*time.Second),
				LastMovement: now.Add(-91 * time.Second),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonAtPrompt,
		},
		{
			why: "a program status report outranks the title heartbeat and has no " +
				"expiry: the agent states what it is doing and repeats nothing " +
				"while it keeps doing it",
			evidence: Evidence{
				ProgramStatus:  seen(SourceProgramStatus, ClaimBusy, 10*time.Minute),
				Heartbeat:      seen(SourceHeartbeat, ClaimSettled, time.Second),
				TurnEverOpened: true,
				LastBusyAt:     now.Add(-10 * time.Minute),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonProgramWorking,
		},
		{
			why: "a blocked report with a permission kind is an approval the agent " +
				"announced itself, no hook needed",
			evidence: Evidence{
				ProgramStatus:  seen(SourceProgramStatus, ClaimApprovalPending, time.Second),
				TurnOpen:       true,
				TurnEverOpened: true,
			},
			wantState:  protocol.SessionStatePendingApproval,
			wantReason: ReasonProgramBlocked,
		},
		{
			why: "a harness edge newer than the program status record outranks it: " +
				"two signals about the same moment, and the later one is news",
			evidence: Evidence{
				ProgramStatus:    seen(SourceProgramStatus, ClaimBusy, 2*time.Second),
				LastHarnessEvent: seen(SourceHarnessEvent, ClaimApprovalPending, time.Second),
				TurnOpen:         true,
				TurnEverOpened:   true,
				LastBusyAt:       now.Add(-2 * time.Second),
			},
			wantState:  protocol.SessionStatePendingApproval,
			wantReason: ReasonApprovalOpen,
		},
		{
			why: "an open bracket measures its silence from the moment the agent " +
				"stopped working, not from when it started: a long turn reports " +
				"working once, and its stop hook still lands after its done report",
			evidence: Evidence{
				ProgramStatus:  seen(SourceProgramStatus, ClaimSettled, time.Second),
				TurnOpen:       true,
				TurnEverOpened: true,
				LastBusyAt:     now.Add(-10 * time.Minute),
				LastMovement:   now.Add(-time.Second),
			},
			wantState:  protocol.SessionStateWorking,
			wantReason: ReasonBracketOpen,
		},
		{
			why: "a settled report after a turn defers to the classifier, as a " +
				"settled heartbeat does",
			evidence: Evidence{
				ProgramStatus:  seen(SourceProgramStatus, ClaimSettled, time.Second),
				TurnEverOpened: true,
				LastBusyAt:     now.Add(-time.Minute),
			},
			wantState:  protocol.SessionStateIdle,
			wantReason: ReasonProgramSettled,
		},
	} {
		t.Run(tc.why, func(t *testing.T) {
			got := Resolve(tc.evidence, policy, now)
			if got.State != tc.wantState || got.Reason != tc.wantReason {
				t.Fatalf(
					"Resolve() = %s/%s, want %s/%s",
					got.State, got.Reason, tc.wantState, tc.wantReason,
				)
			}
		})
	}
}
