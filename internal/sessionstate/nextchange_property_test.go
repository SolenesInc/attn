package sessionstate_test

import (
	"fmt"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionstate"
)

var nextChangeBase = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

var policies = []sessionstate.Policy{
	sessionstate.PolicyFor(string(protocol.SessionAgentClaude)),
	sessionstate.PolicyFor(string(protocol.SessionAgentCodex)),
	sessionstate.PolicyFor(string(protocol.SessionAgentShell)),
	sessionstate.PolicyFor(""),
}

func policyLifetimes(p sessionstate.Policy) []time.Duration {
	return []time.Duration{
		0,
		p.HeartbeatTTL,
		p.HeartbeatSettleAfter,
		p.StaleAfter,
		p.StaleAfter + p.SettleGrace,
		p.StuckAfter,
		p.ClassifierTimeout,
		p.GuardianDwell,
		p.ParkedAfter,
	}
}

func instantNear(t *rapid.T, label string, p sessionstate.Policy) time.Time {
	if rapid.IntRange(0, 5).Draw(t, label+"_zero") == 0 {
		return time.Time{}
	}
	lifetime := rapid.SampledFrom(policyLifetimes(p)).Draw(t, label+"_lifetime")
	jitter := rapid.OneOf(
		rapid.Int64Range(-1, 1),
		rapid.Int64Range(int64(-2*time.Second), int64(2*time.Second)),
	).Draw(t, label+"_jitter")
	return nextChangeBase.Add(-lifetime + time.Duration(jitter))
}

func sometimes(t *rapid.T, label string, oneIn int) bool {
	return rapid.IntRange(1, oneIn).Draw(t, label) == 1
}

func observation(t *rapid.T, label string, p sessionstate.Policy, oneIn int, source sessionstate.Source, claims []sessionstate.Claim) *sessionstate.Observation {
	if !sometimes(t, label+"_present", oneIn) {
		return nil
	}
	at := instantNear(t, label, p)
	return &sessionstate.Observation{
		Source:     source,
		Claim:      rapid.SampledFrom(claims).Draw(t, label+"_claim"),
		Detail:     rapid.SampledFrom([]string{"", "esc to interrupt", "Allow?"}).Draw(t, label+"_detail"),
		ObservedAt: at,
	}
}

var everyClaim = []sessionstate.Claim{
	sessionstate.ClaimBusy,
	sessionstate.ClaimSettled,
	sessionstate.ClaimApprovalPending,
	sessionstate.ClaimNeedsInput,
	sessionstate.ClaimIdle,
	sessionstate.ClaimParked,
	sessionstate.ClaimExited,
	sessionstate.ClaimStopFailed,
	sessionstate.ClaimTurnAborted,
}

func evidence(t *rapid.T, p sessionstate.Policy) sessionstate.Evidence {
	return sessionstate.Evidence{
		Heartbeat: observation(t, "heartbeat", p, 2, sessionstate.SourceHeartbeat,
			[]sessionstate.Claim{sessionstate.ClaimBusy, sessionstate.ClaimSettled}),
		LastHarnessEvent: observation(t, "harness", p, 4, sessionstate.SourceHarnessEvent, everyClaim),
		LastClassifier: observation(t, "classifier", p, 2, sessionstate.SourceClassifier,
			[]sessionstate.Claim{sessionstate.ClaimNeedsInput, sessionstate.ClaimIdle, sessionstate.ClaimParked}),
		Process: observation(t, "process", p, 10, sessionstate.SourceProcess,
			[]sessionstate.Claim{sessionstate.ClaimExited}),
		TurnOpen:          sometimes(t, "turn_open", 2),
		TurnEverOpened:    sometimes(t, "turn_ever_opened", 3),
		ToolOpen:          sometimes(t, "tool_open", 3),
		BackgroundWork:    sometimes(t, "background_work", 2),
		PendingCron:       sometimes(t, "pending_cron", 3),
		Compacting:        sometimes(t, "compacting", 5),
		ReviewerInLoop:    sometimes(t, "reviewer_in_loop", 3),
		InitialPromptOwed: sometimes(t, "initial_prompt_owed", 3),
		PlacedInputOwed:   sometimes(t, "placed_input_owed", 3),
		LastBusyAt:        instantNear(t, "last_busy", p),
		PromptIdleAt:      rarely(t, "prompt_idle", p),
		ClassifyingSince:  instantNear(t, "classifying_since", p),
		LastMovement:      instantNear(t, "last_movement", p),
	}
}

func rarely(t *rapid.T, label string, p sessionstate.Policy) time.Time {
	if !sometimes(t, label+"_present", 3) {
		return time.Time{}
	}
	return instantNear(t, label, p)
}

func laterInstant(t *rapid.T, label string, p sessionstate.Policy, e sessionstate.Evidence, until time.Time) time.Time {
	stamps := []time.Time{nextChangeBase, e.LastBusyAt, e.PromptIdleAt, e.ClassifyingSince, e.LastMovement}
	for _, o := range []*sessionstate.Observation{e.Heartbeat, e.LastHarnessEvent, e.LastClassifier, e.Process} {
		if o != nil {
			stamps = append(stamps, o.ObservedAt)
		}
	}
	at := rapid.SampledFrom(stamps).Draw(t, label+"_from").
		Add(rapid.SampledFrom(policyLifetimes(p)).Draw(t, label+"_lifetime")).
		Add(time.Duration(rapid.Int64Range(-2, 2).Draw(t, label+"_ns")))
	if rapid.Bool().Draw(t, label+"_uniform") {
		at = nextChangeBase.Add(time.Duration(rapid.Int64Range(0, int64(until.Sub(nextChangeBase))).Draw(t, label+"_offset")))
	}
	if at.Before(nextChangeBase) {
		return nextChangeBase
	}
	if !at.Before(until) {
		return until.Add(-time.Nanosecond)
	}
	return at
}

func TestNextChangeIsTheFirstInstantTheResolutionMoves(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		p := rapid.SampledFrom(policies).Draw(t, "policy")
		e := evidence(t, p)
		now := nextChangeBase
		current := sessionstate.Resolve(e, p, now)

		next, ok := sessionstate.NextChange(e, p, now)
		until := now.Add(48 * time.Hour)
		if ok {
			if !next.After(now) {
				t.Fatalf("the next change %s is not after now %s", next, now)
			}
			if moved := sessionstate.Resolve(e, p, next); moved == current {
				t.Fatalf("the resolution at the reported change %s is still %+v", next, moved)
			}
			if before := sessionstate.Resolve(e, p, next.Add(-time.Nanosecond)); before != current {
				t.Fatalf("the resolution moved to %+v before the reported change %s; now it is %+v", before, next, current)
			}
			until = next
		}
		for i := range 32 {
			at := laterInstant(t, fmt.Sprintf("probe%d", i), p, e, until)
			if got := sessionstate.Resolve(e, p, at); got != current {
				t.Fatalf("the resolution moved from %+v to %+v at %s, before the reported change (%v, %s)", current, got, at, ok, next)
			}
		}
	})
}
