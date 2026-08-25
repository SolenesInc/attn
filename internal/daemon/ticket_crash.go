package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func isMidFlightCrashState(state string) bool {
	switch state {
	case protocol.StateLaunching, protocol.StateWorking, protocol.StatePendingApproval:
		return true
	default:
		return false
	}
}

// Callers have already established the ticket is non-terminal, and a racing terminal report still
// wins because the reconciliation claim that follows is first-writer-wins.
func (d *Daemon) crashTicket(ticketID, sessionID, state string) bool {
	if _, err := d.store.SetTicketStatus(
		ticketID,
		store.TicketStatusCrashed,
		store.TicketAuthorAttn,
		"agent process ended mid-flight without reporting",
		time.Now(),
	); err != nil {
		d.logf("ticket crash capture for %s: %v", sessionID, err)
		return false
	}
	d.logf("ticket %q crashed: session %s ended mid-flight (%s)", ticketID, sessionID, state)
	d.notifyTicketObservers(ticketID)
	d.publishTicketFact(FactTicketStatusChanged, ticketID)
	return true
}
