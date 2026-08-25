package bus

import (
	"fmt"
	"sort"
	"time"
)

const (
	RecentWindow   = time.Hour
	BaselineWindow = 24 * time.Hour

	// An absolute ceiling, not a multiple of the producer's own history. Measured over 8
	// days of production: healthy classes peak at 480/h in a 6h window, flapping at 5763/h.
	SurgeWindow      = 6 * time.Hour
	SurgeRatePerHour = 1000.0

	// Delivery polls every DefaultPollInterval (5s) and a failing handler retries
	// at most DefaultRetryCap (2m) apart; 5 minutes is 2.5x past the retry cap.
	StallAge = 5 * time.Minute

	// Sits past every stall attn resolves by itself (16m0s is the longest, an app's
	// auto-disable clock). Cost measured over 9.5 days: 111KB in a mean hour.
	DefaultPinAlarmAge = time.Hour
)

type ProducerRow struct {
	Name     string
	Events   int64
	Bytes    int64
	Subjects int64
	Recent   []int64
}

type Producer struct {
	Name             string
	Events           int64
	Bytes            int64
	Subjects         int64
	Share            float64
	RecentPerHour    float64
	BaselinePerHour  float64
	SustainedPerHour float64
	Surging          bool
	SurgeWindow      time.Duration
	SurgePerHour     float64
}

func (p *Producer) surge() {
	windows := []struct {
		d    time.Duration
		rate float64
	}{
		{SurgeWindow, p.SustainedPerHour},
		{BaselineWindow, p.BaselinePerHour},
	}
	for _, w := range windows {
		if w.rate >= SurgeRatePerHour && w.rate > p.SurgePerHour {
			p.Surging = true
			p.SurgeWindow = w.d
			p.SurgePerHour = w.rate
		}
	}
}

type ConsumerStatus struct {
	Name          string
	Cursor        int64
	Lag           int64
	Filter        string
	Enabled       bool
	PinsRetention bool
	UpdatedAt     time.Time
	// Meaningful only when Status.Delivering is set.
	Live                bool
	Stalled             string
	OldestUnreadAt      time.Time
	HoldsRetentionFloor bool
	PinAlarm            bool
	PinnedBytes         int64
}

type Pin struct {
	Consumer       string
	Cursor         int64
	Events         int64
	Bytes          int64
	OldestUnreadAt time.Time
	Age            time.Duration
	Threshold      time.Duration
}

func pinAlarmed(c ConsumerStatus, now time.Time, threshold time.Duration) bool {
	if threshold <= 0 || !c.Enabled || !c.HoldsRetentionFloor || c.Lag <= 0 {
		return false
	}
	if c.OldestUnreadAt.IsZero() {
		return false
	}
	return now.Sub(c.OldestUnreadAt) >= threshold
}

const (
	HealthWarn  = "warn"
	HealthError = "error"
)

const (
	HealthConsumerDisabled = "consumer_disabled"
	HealthConsumerStalled  = "consumer_stalled"
	HealthConsumerNotLive  = "consumer_not_live"
	HealthConsumerLagging  = "consumer_lagging"
	HealthRetentionPinned  = "retention_pinned"
	HealthProducerSurging  = "producer_surging"
)

type Health struct {
	Level   string
	Kind    string
	Subject string
	Message string
}

type Status struct {
	Head            int64
	Earliest        int64
	Rows            int64
	Bytes           int64
	OldestAt        time.Time
	NewestAt        time.Time
	Delivering      bool
	RetentionWindow time.Duration
	PinAlarmAge     time.Duration

	Producers []Producer
	Consumers []ConsumerStatus
	Health    []Health
}

func (b *Bus) Status() (Status, error) {
	if b.store == nil {
		return Status{}, nil
	}
	now := b.now()

	earliest, head, err := b.store.Bounds()
	if err != nil {
		return Status{}, err
	}
	consumers, err := b.store.ListConsumers()
	if err != nil {
		return Status{}, err
	}
	// Cutoffs are positional; the reads below must match this order.
	cutoffs := []time.Time{
		now.Add(-RecentWindow),
		now.Add(-SurgeWindow),
		now.Add(-BaselineWindow),
	}
	producers, err := b.store.Producers(cutoffs)
	if err != nil {
		return Status{}, err
	}

	out := Status{
		Head:            head,
		Earliest:        earliest,
		Delivering:      b.delivering(),
		RetentionWindow: b.retention,
		PinAlarmAge:     b.pinAlarmAge,
	}
	for _, p := range producers {
		out.Rows += p.Events
		out.Bytes += p.Bytes
	}
	if out.Rows > 0 {
		if t, ok, err := b.store.EventTimeAt(earliest); err == nil && ok {
			out.OldestAt = t
		}
		if t, ok, err := b.store.EventTimeAt(head); err == nil && ok {
			out.NewestAt = t
		}
	}

	for _, p := range producers {
		entry := Producer{
			Name:             p.Name,
			Events:           p.Events,
			Bytes:            p.Bytes,
			Subjects:         p.Subjects,
			RecentPerHour:    perHour(p.Recent[0], RecentWindow),
			SustainedPerHour: perHour(p.Recent[1], SurgeWindow),
			BaselinePerHour:  perHour(p.Recent[2], BaselineWindow),
		}
		if out.Rows > 0 {
			entry.Share = float64(p.Events) / float64(out.Rows)
		}
		entry.surge()
		out.Producers = append(out.Producers, entry)
	}

	live := b.liveDurables()
	floor := retentionFloorName(consumers)
	for _, c := range consumers {
		entry := ConsumerStatus{
			Name:                c.Name,
			Cursor:              c.Cursor,
			Lag:                 max64(head-c.Cursor, 0),
			Filter:              c.Filter,
			Enabled:             c.Enabled,
			PinsRetention:       c.PinsRetention,
			UpdatedAt:           c.UpdatedAt,
			HoldsRetentionFloor: c.Name == floor,
		}
		if entry.Lag > 0 {
			if t, ok, err := b.store.EventTimeAt(c.Cursor + 1); err == nil && ok {
				entry.OldestUnreadAt = t
			}
		}
		if d, ok := live[c.Name]; ok {
			entry.Live = true
			entry.Stalled = d.stallReason()
		}
		if pinAlarmed(entry, now, b.pinAlarmAge) {
			entry.PinAlarm = true
			if n, err := b.store.PendingBytes(c.Cursor); err == nil {
				entry.PinnedBytes = n
			}
		}
		out.Consumers = append(out.Consumers, entry)
	}

	out.Health = health(out, now)
	return out, nil
}

func health(s Status, now time.Time) []Health {
	var out []Health
	for _, c := range s.Consumers {
		switch {
		case c.Stalled != "":
			out = append(out, Health{
				Level: HealthError, Kind: HealthConsumerStalled, Subject: c.Name,
				Message: fmt.Sprintf("consumer %s is stalled at seq %d and is retrying: %s",
					c.Name, c.Cursor+1, c.Stalled),
			})
		case c.Enabled && c.Lag > 0 && stale(c.UpdatedAt, now):
			msg := fmt.Sprintf("consumer %s is %s behind and not advancing; its cursor has not moved for %s",
				c.Name, events(c.Lag), roundDuration(now.Sub(c.UpdatedAt)))
			if !c.OldestUnreadAt.IsZero() {
				msg += fmt.Sprintf(", and its oldest unread event has waited %s",
					roundDuration(now.Sub(c.OldestUnreadAt)))
			}
			out = append(out, Health{
				Level: HealthError, Kind: HealthConsumerLagging, Subject: c.Name, Message: msg,
			})
		case s.Delivering && c.Enabled && !c.Live:
			out = append(out, Health{
				Level: HealthWarn, Kind: HealthConsumerNotLive, Subject: c.Name,
				Message: fmt.Sprintf("consumer %s is registered and enabled but has no delivery loop in this daemon, so nothing is reading its %s backlog",
					c.Name, events(c.Lag)),
			})
		case !c.Enabled && c.PinsRetention:
			out = append(out, Health{
				Level: HealthWarn, Kind: HealthConsumerDisabled, Subject: c.Name,
				Message: fmt.Sprintf("consumer %s is disabled: delivery is paused at seq %d and its installed app keeps the unread backlog; enable it to resume from this cursor or uninstall it to release retention",
					c.Name, c.Cursor),
			})
		case !c.Enabled:
			out = append(out, Health{
				Level: HealthWarn, Kind: HealthConsumerDisabled, Subject: c.Name,
				Message: fmt.Sprintf("consumer %s is disabled: it is not being delivered to, and it does not hold retention open — once trimming passes its cursor at seq %d, enabling it resumes at head with a logged gap",
					c.Name, c.Cursor),
			})
		}
	}
	for _, c := range s.Consumers {
		if !c.PinAlarm {
			continue
		}
		out = append(out, Health{
			Level: HealthWarn, Kind: HealthRetentionPinned, Subject: c.Name,
			Message: pinMessage(c.pin(now, s.PinAlarmAge)),
		})
	}
	for _, p := range s.Producers {
		if !p.Surging {
			continue
		}
		out = append(out, Health{
			Level: HealthWarn, Kind: HealthProducerSurging, Subject: p.Name,
			Message: surgeMessage(p),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Level == HealthError && out[j].Level != HealthError
	})
	return out
}

func (c ConsumerStatus) pin(now time.Time, threshold time.Duration) Pin {
	return Pin{
		Consumer:       c.Name,
		Cursor:         c.Cursor,
		Events:         c.Lag,
		Bytes:          c.PinnedBytes,
		OldestUnreadAt: c.OldestUnreadAt,
		Age:            now.Sub(c.OldestUnreadAt),
		Threshold:      threshold,
	}
}

func pinMessage(p Pin) string {
	return fmt.Sprintf("consumer %s has pinned the retention floor at seq %d for %s, past the %s tripwire: nothing below it can be trimmed, and the log is holding %s (%s) it has not read",
		p.Consumer, p.Cursor, roundDuration(p.Age), limitDuration(p.Threshold),
		events(p.Events), humanBytes(p.Bytes))
}

// Indexed reads over the consumer table and the log's ends, against Status's
// walk of every row (209ms at 945k, measured).
func (b *Bus) PinAlarms() ([]Pin, error) {
	if b.store == nil || b.pinAlarmAge <= 0 {
		return nil, nil
	}
	now := b.now()
	_, head, err := b.store.Bounds()
	if err != nil {
		return nil, fmt.Errorf("reading log bounds: %w", err)
	}
	consumers, err := b.store.ListConsumers()
	if err != nil {
		return nil, fmt.Errorf("listing consumers: %w", err)
	}
	floor := retentionFloorName(consumers)
	var out []Pin
	for _, c := range consumers {
		entry := ConsumerStatus{
			Name:                c.Name,
			Cursor:              c.Cursor,
			Lag:                 max64(head-c.Cursor, 0),
			Enabled:             c.Enabled,
			PinsRetention:       c.PinsRetention,
			HoldsRetentionFloor: c.Name == floor,
		}
		if entry.Lag > 0 {
			if t, ok, err := b.store.EventTimeAt(c.Cursor + 1); err == nil && ok {
				entry.OldestUnreadAt = t
			}
		}
		if !pinAlarmed(entry, now, b.pinAlarmAge) {
			continue
		}
		if n, err := b.store.PendingBytes(c.Cursor); err == nil {
			entry.PinnedBytes = n
		}
		out = append(out, entry.pin(now, b.pinAlarmAge))
	}
	return out, nil
}

func PinMessage(p Pin) string { return pinMessage(p) }

func surgeMessage(p Producer) string {
	return fmt.Sprintf("producer %s is publishing %.0f events/hour sustained over the last %s, past the %.0f/hour tripwire; it holds %s (%.0f%% of the log) across %d subject(s)",
		p.Name, p.SurgePerHour, roundDuration(p.SurgeWindow), SurgeRatePerHour,
		events(p.Events), p.Share*100, p.Subjects)
}

func retentionFloorName(consumers []Consumer) string {
	name := ""
	floor := int64(-1)
	for _, c := range consumers {
		if !c.Enabled && !c.PinsRetention {
			continue
		}
		if floor < 0 || c.Cursor < floor {
			floor = c.Cursor
			name = c.Name
		}
	}
	return name
}

func (b *Bus) liveDurables() map[string]*durable {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]*durable, len(b.durables))
	for _, d := range b.durables {
		out[d.name] = d
	}
	return out
}

func (b *Bus) delivering() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.started && len(b.durables) > 0
}

func perHour(count int64, window time.Duration) float64 {
	if window <= 0 {
		return 0
	}
	return float64(count) / window.Hours()
}

func stale(updatedAt, now time.Time) bool {
	return !updatedAt.IsZero() && now.Sub(updatedAt) >= StallAge
}

func events(n int64) string {
	if n == 1 {
		return "1 event"
	}
	return fmt.Sprintf("%s events", humanCount(n))
}

func humanCount(n int64) string {
	s := fmt.Sprintf("%d", n)
	if n < 0 || len(s) <= 3 {
		return s
	}
	head := len(s) % 3
	if head == 0 {
		head = 3
	}
	out := s[:head]
	for i := head; i < len(s); i += 3 {
		out += "," + s[i:i+3]
	}
	return out
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func roundDuration(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%.0fd", d.Hours()/24)
	case d >= time.Hour:
		if m := int(d.Minutes()) % 60; m != 0 {
			return fmt.Sprintf("%dh%dm", int(d.Hours()), m)
		}
		return fmt.Sprintf("%.0fh", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%.0fm", d.Minutes())
	default:
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
}

// limitDuration renders a configured limit EXACTLY, where roundDuration rounds an
// observation: a limit shown as "2m" when set to 1m30s cannot be checked against.
func limitDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d/time.Hour), int(d%time.Hour/time.Minute), int(d%time.Minute/time.Second)
	switch {
	case h > 0 && m == 0 && s == 0:
		return fmt.Sprintf("%dh", h)
	case h > 0 && s == 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	case m > 0 && s == 0:
		return fmt.Sprintf("%dm", m)
	case m > 0:
		return fmt.Sprintf("%dm%ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
