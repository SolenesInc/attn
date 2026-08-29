package daemon

import (
	"os"
	"strings"
	"time"
)

const (
	defaultAutomationTicketRetentionTTL           = 30 * 24 * time.Hour
	defaultAutomationTicketRetentionSweepInterval = time.Hour
)

func automationTicketRetentionTTL() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_AUTOMATION_TICKET_RETENTION_TTL")); v != "" {
		if dur, err := time.ParseDuration(v); err == nil && dur > 0 {
			return dur
		}
	}
	return defaultAutomationTicketRetentionTTL
}

func automationTicketRetentionSweepInterval() time.Duration {
	if v := strings.TrimSpace(os.Getenv("ATTN_AUTOMATION_TICKET_RETENTION_SWEEP_INTERVAL")); v != "" {
		if dur, err := time.ParseDuration(v); err == nil && dur > 0 {
			return dur
		}
	}
	return defaultAutomationTicketRetentionSweepInterval
}

func (d *Daemon) runAutomationTicketRetentionSweep() {
	ticker := time.NewTicker(automationTicketRetentionSweepInterval())
	defer ticker.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.automationTicketRetentionSweepPass(time.Now())
		}
	}
}

func (d *Daemon) automationTicketRetentionSweepPass(now time.Time) {
	if d.store == nil {
		return
	}
	removed, err := d.store.SweepExpiredAutomationTickets(now, automationTicketRetentionTTL())
	if err != nil {
		d.logf("automation ticket retention sweep: %v", err)
		return
	}
	if removed > 0 {
		d.logf("automation ticket retention sweep: removed %d expired ticket(s)", removed)
	}
}
