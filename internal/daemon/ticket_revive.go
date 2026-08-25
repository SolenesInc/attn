package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/store"
)

// `crashed` is terminal and invisible to ActiveTicketsForSession, so un-stamping
// is also what re-arms crash detection for a second death.
func (d *Daemon) reviveCrashedTicketsForSession(sessionID string) {
	if d.store == nil {
		return
	}
	tickets, err := d.store.CrashedTicketsForAssignee(sessionID)
	if err != nil {
		d.logf("ticket revive: list crashed tickets for %s: %v", sessionID, err)
		return
	}
	if len(tickets) == 0 {
		return
	}
	d.coalesceSnapshots(func() {
		for _, ticket := range tickets {
			if ticket == nil {
				continue
			}
			if _, err := d.store.SetTicketStatus(
				ticket.ID,
				store.TicketStatusWorking,
				store.TicketAuthorAttn,
				"session was reloaded and is running again",
				time.Now(),
			); err != nil {
				d.logf("ticket revive for %s: %v", sessionID, err)
				continue
			}
			d.logf("ticket %q revived: session %s is live again", ticket.ID, sessionID)
			d.notifyTicketObservers(ticket.ID)
			d.publishTicketFact(FactTicketStatusChanged, ticket.ID)
		}
	})
}
