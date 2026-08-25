package crew

import (
	"fmt"
	"time"
)

type CacheState struct {
	// The TTL restarts on every request that writes OR reads the entry, which is why
	// this is time since the last request, not time since the cache was written.
	Age time.Duration
	TTL time.Duration
}

func (c CacheState) Remaining() time.Duration { return c.TTL - c.Age }

type ContextPressure struct {
	Tokens int64
	Budget int64
}

func (c ContextPressure) Full() bool {
	return c.Tokens > 0 && c.Budget > 0 && c.Tokens >= c.Budget
}

type Signals struct {
	AwayFor               time.Duration
	AwayLimit             time.Duration
	Cache                 CacheState
	Lead                  time.Duration
	Reachable             bool
	MidTurn               bool
	Context               ContextPressure
	HeartbeatEnabled      bool
	AutoSleepEnabled      bool
	ContextHandoffEnabled bool
}

type Action int

const (
	ActionNone Action = iota
	ActionHeartbeat
	ActionSleep
	ActionContextHandoff
)

func (a Action) String() string {
	switch a {
	case ActionHeartbeat:
		return "heartbeat"
	case ActionSleep:
		return "sleep"
	case ActionContextHandoff:
		return "context_handoff"
	default:
		return "none"
	}
}

func Decide(s Signals) Action {
	if !s.Reachable {
		return ActionNone
	}
	if s.ContextHandoffEnabled && s.Context.Full() {
		return ActionContextHandoff
	}
	if s.MidTurn {
		return ActionNone
	}
	if s.Cache.Remaining() > s.Lead {
		return ActionNone
	}
	if s.AwayFor >= s.AwayLimit {
		if !s.AutoSleepEnabled {
			return ActionNone
		}
		return ActionSleep
	}
	if !s.HeartbeatEnabled {
		return ActionNone
	}
	return ActionHeartbeat
}

// Default arithmetic, list prices not a measurement: ~3.5k priming tokens x $15/M
// input x 2 (1h-TTL cache write) = ~$0.15 a wake, ~$1.20 per member per absence.
type WakeLedger struct {
	Stamps []time.Time
	Limit  int
	Window time.Duration
}

func (l WakeLedger) Within(now time.Time) []time.Time {
	cutoff := now.Add(-l.Window)
	kept := make([]time.Time, 0, len(l.Stamps))
	for _, at := range l.Stamps {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	return kept
}

func (l WakeLedger) Allows(memberID string, now time.Time) ([]time.Time, error) {
	kept := l.Within(now)
	name := DisplayName(memberID)
	if l.Limit <= 0 {
		return kept, fmt.Errorf("autonomous wakes are turned off (crew.wake_limit=%d), so %s was not woken; wake it yourself from the sidebar, or raise the limit", l.Limit, name)
	}
	if len(kept) >= l.Limit {
		return kept, fmt.Errorf("%s has been woken %d times without the user in the last %s, and the limit is %d (crew.wake_limit=%d, crew.wake_limit_window_seconds=%d); nothing was woken. Wake it yourself from the sidebar, or raise the limit",
			name, len(kept), l.Window, l.Limit, l.Limit, int(l.Window/time.Second))
	}
	return append(kept, now), nil
}
