package daemon

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/sessionstate"
)

func evidenceOf(t *testing.T, d *Daemon, sessionID string) sessionstate.Evidence {
	t.Helper()
	got, ok := d.evidenceTable().snapshot(sessionID)
	if !ok {
		t.Fatalf("no evidence recorded for %s", sessionID)
	}
	return got
}

func TestACodexApprovalTitleBecomesAnApproval(t *testing.T) {
	d := newTraceDaemon(t)
	id := "sess-codex-approval"
	addCharacterizationSession(t, d, id, protocol.SessionAgentCodex, protocol.SessionStateWorking)
	at := time.Now()
	d.recordPTYEvidence(id, pty.Observation{
		Source: pty.SourceHeartbeat,
		Claim:  "approval",
		Detail: "scratchpad",
		At:     at,
	})

	got := evidenceOf(t, d, id)
	if got.LastHarnessEvent == nil || got.LastHarnessEvent.Claim != sessionstate.ClaimApprovalPending {
		t.Fatal("the codex approval title recorded no approval")
	}
	if got.Heartbeat == nil || got.Heartbeat.Claim != sessionstate.ClaimBusy {
	} else {
		t.Fatal("the approval title left the heartbeat busy, which hides the approval")
	}
	if !got.LastBusyAt.IsZero() {
		t.Fatal("an approval title advanced LastBusyAt; it is not a running turn")
	}

	res := sessionstate.Resolve(got, sessionstate.PolicyFor(string(protocol.SessionAgentCodex)), at)
	if res.State != protocol.SessionStatePendingApproval {
		t.Fatalf("resolved %q (%s), want pending_approval", res.State, res.Reason)
	}
}
