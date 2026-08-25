package daemon

import (
	"os"
	"strings"
	"time"
)

// Ticket TTL sweep: the periodic caller of store.SweepExpiredTickets, which
// also releases any continuity binding the deleted tickets document (see hasPriorAutomationContinuityRun).
const (
	defaultTicketRetentionTTL           = 30 * 24 * time.Hour
	defaultTicketRetentionSweepInterval = time.Hour
)

func ticketRetentionTTL() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_TICKET_RETENTION_TTL")); v != "" {
		if dur, err := time.ParseDuration(v); err == nil && dur > 0 {
			return dur
		}
	}
	return defaultTicketRetentionTTL
}

func ticketRetentionSweepInterval() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_TICKET_RETENTION_SWEEP_INTERVAL")); v != "" {
		if dur, err := time.ParseDuration(v); err == nil && dur > 0 {
			return dur
		}
	}
	return defaultTicketRetentionSweepInterval
}

// runTicketRetentionSweep runs no initial pass at boot: the TTL is measured in weeks and must not compete with startup churn.
func (d *Daemon) runTicketRetentionSweep() {
	ticker := time.NewTicker(ticketRetentionSweepInterval())
	defer ticker.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.ticketRetentionSweepPass(time.Now())
		}
	}
}

func (d *Daemon) ticketRetentionSweepPass(now time.Time) {
	if d.store == nil {
		return
	}
	removed, err := d.store.SweepExpiredTickets(now, ticketRetentionTTL())
	if err != nil {
		d.logf("ticket retention sweep: %v", err)
		return
	}
	if removed > 0 {
		d.logf("ticket retention sweep: removed %d expired ticket(s)", removed)
	}
}
