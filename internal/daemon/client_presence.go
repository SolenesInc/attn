package daemon

import (
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type PresenceTier int

const (
	PresenceAway PresenceTier = iota
	PresencePresent
	PresenceWatching
)

func (t PresenceTier) String() string {
	switch t {
	case PresenceWatching:
		return "watching"
	case PresencePresent:
		return "present"
	default:
		return "away"
	}
}

type clientPresence struct {
	Visible          bool
	DashboardVisible bool
	IdleSeconds      float64
	ReportedAt       time.Time
	FirstReportAt    time.Time
}

// Unreported is unknown, not zero, which would read as fresh input forever.
func (p clientPresence) idleFor() (time.Duration, bool) {
	if p.IdleSeconds < 0 {
		return 0, false
	}
	return time.Duration(p.IdleSeconds * float64(time.Second)), true
}

func (p clientPresence) watchingIdle(now time.Time) time.Duration {
	if idle, ok := p.idleFor(); ok {
		return idle
	}
	if p.FirstReportAt.IsZero() {
		return 0
	}
	return now.Sub(p.FirstReportAt)
}

const presenceHeartbeatGrace = 90 * time.Second

const presenceWatchingIdleLimit = 10 * time.Minute

func (p clientPresence) tier(now time.Time, idleLimit time.Duration) PresenceTier {
	if p.ReportedAt.IsZero() || now.Sub(p.ReportedAt) > presenceHeartbeatGrace {
		return PresenceAway
	}
	if !p.Visible {
		return PresenceAway
	}
	if p.DashboardVisible && p.watchingIdle(now) <= presenceWatchingIdleLimit {
		return PresenceWatching
	}
	if idle, ok := p.idleFor(); ok && idle <= idleLimit {
		return PresencePresent
	}
	return PresenceAway
}

func (c *wsClient) setPresence(msg *protocol.SetClientPresenceMessage, now time.Time) {
	idle := -1.0
	if msg.IdleSeconds != nil {
		idle = *msg.IdleSeconds
	}
	c.presenceMu.Lock()
	defer c.presenceMu.Unlock()
	firstAt := c.presence.FirstReportAt
	if firstAt.IsZero() {
		firstAt = now
	}
	c.presence = clientPresence{
		Visible:          msg.Visible,
		DashboardVisible: msg.DashboardVisible,
		IdleSeconds:      idle,
		ReportedAt:       now,
		FirstReportAt:    firstAt,
	}
}

func (c *wsClient) presenceReport() clientPresence {
	c.presenceMu.RLock()
	defer c.presenceMu.RUnlock()
	return c.presence
}

func (d *Daemon) handleSetClientPresence(client *wsClient, msg *protocol.SetClientPresenceMessage) {
	client.setPresence(msg, time.Now())
}

func (d *Daemon) PresenceTier() PresenceTier {
	if d.wsHub == nil {
		return PresenceAway
	}
	now := time.Now()
	idleLimit := d.presenceIdleLimit()
	highest := PresenceAway
	d.wsHub.ForEachClient(func(client *wsClient) {
		if tier := client.presenceReport().tier(now, idleLimit); tier > highest {
			highest = tier
		}
	})
	d.notePresence(highest, now)
	return highest
}

func (d *Daemon) notePresence(tier PresenceTier, now time.Time) {
	if tier == PresenceAway {
		return
	}
	d.presenceMu.Lock()
	defer d.presenceMu.Unlock()
	if now.After(d.presentSince) {
		d.presentSince = now
	}
}

// The stamp is seeded at daemon start: a zero would read as an absence stretching
// back to the epoch and put every crew member to bed the moment the daemon came up.
func (d *Daemon) UserAwayFor(now time.Time) time.Duration {
	if d.PresenceTier() != PresenceAway {
		return 0
	}
	d.presenceMu.RLock()
	since := d.presentSince
	d.presenceMu.RUnlock()
	if since.IsZero() || !now.After(since) {
		return 0
	}
	return now.Sub(since)
}
