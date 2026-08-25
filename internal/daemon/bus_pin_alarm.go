package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
)

const (
	busPinAlarmKind    = "bus_pin_alarm"
	busPinAlarmTimeout = 30 * time.Second

	notificationKindBusPinned = "bus_retention_pinned"

	// Below a minute the queue works far faster than a pin can change.
	busPinAlarmMinInterval = time.Minute
)

// A quarter of the tripwire: less often reports an outage long after the
// tripwire named it, more often buys nothing on a condition measured in hours.
func busPinAlarmInterval(age time.Duration) time.Duration {
	if interval := age / 4; interval > busPinAlarmMinInterval {
		return interval
	}
	return busPinAlarmMinInterval
}

// The resolver never returns zero — off is negative — so zero means "not
// resolved".
func (d *Daemon) busPinAlarmAge() time.Duration {
	d.busPinMu.Lock()
	defer d.busPinMu.Unlock()
	if d.busPinAge == 0 {
		d.busPinAge = bus.PinAlarmAgeFromEnv(d.logf)
	}
	return d.busPinAge
}

type busPinEpisode struct {
	cursor   int64
	notified bool
}

func (d *Daemon) busPinAlarmHandler(_ context.Context, _ *jobs.Job) (any, error) {
	if d.eventBus == nil {
		return map[string]any{"pinned": 0}, nil
	}
	pins, err := d.eventBus.PinAlarms()
	if err != nil {
		return nil, fmt.Errorf("checking the event log's retention floor: %w", err)
	}
	notified := d.recordBusPins(pins)
	return map[string]any{"pinned": len(pins), "notified": notified}, nil
}

// Announces only on the SECOND consecutive check at the same cursor: after a suspend the
// oldest unread event is as old as the sleep, which one look cannot tell from an outage.
func (d *Daemon) recordBusPins(pins []bus.Pin) int {
	d.busPinMu.Lock()
	defer d.busPinMu.Unlock()

	if d.busPinEpisodes == nil {
		d.busPinEpisodes = map[string]*busPinEpisode{}
	}
	seen := make(map[string]bool, len(pins))
	for _, p := range pins {
		seen[p.Consumer] = true
	}
	for name := range d.busPinEpisodes {
		if !seen[name] {
			delete(d.busPinEpisodes, name)
		}
	}

	notified := 0
	for _, p := range pins {
		episode := d.busPinEpisodes[p.Consumer]
		if episode == nil || episode.cursor != p.Cursor {
			d.busPinEpisodes[p.Consumer] = &busPinEpisode{cursor: p.Cursor}
			continue
		}
		if episode.notified {
			continue
		}
		episode.notified = true
		d.notifyBusPin(p)
		notified++
	}
	return notified
}

func (d *Daemon) notifyBusPin(p bus.Pin) {
	d.logf("bus: %s", bus.PinMessage(p))
	if d.store == nil {
		return
	}
	record, err := d.store.AddNotification(store.NotificationRecord{
		Kind:       notificationKindBusPinned,
		Severity:   store.NotificationWarning,
		Title:      fmt.Sprintf("Event log held open by %s", p.Consumer),
		Body:       busPinNotificationBody(p),
		Detail:     fmt.Sprintf("oldest unread event: seq %d, %s", p.Cursor+1, p.OldestUnreadAt.UTC().Format(time.RFC3339)),
		SourceKind: "bus_consumer",
		SourceID:   p.Consumer,
	}, time.Now())
	if err != nil {
		d.logf("notifications: add bus-pin notification for %s: %v", p.Consumer, err)
		return
	}
	d.publishFact(FactNotificationCreated, record.ID, nil)
}

func busPinNotificationBody(p bus.Pin) string {
	way := fmt.Sprintf("`attn bus status` shows why it stopped; `attn bus disable %s` releases the log if you do not need it to catch up.", p.Consumer)
	if name, ok := strings.CutPrefix(p.Consumer, apps.ConsumerPrefix); ok {
		way = fmt.Sprintf("This is the %s app. `attn app status %s` and `attn app runtime status` show why it stopped — a parked runtime is the usual cause, and `attn app runtime restart` starts it again. `attn bus disable %s` releases the log instead.",
			name, name, p.Consumer)
	}
	return bus.PinMessage(p) + ". " + way
}
