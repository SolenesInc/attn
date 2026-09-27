package crew

import (
	"strings"
	"testing"
	"time"
)

func TestDecide(t *testing.T) {
	const (
		ttl   = time.Hour
		lead  = 5 * time.Minute
		limit = 2 * time.Hour
	)
	warm := CacheState{Age: 10 * time.Minute, TTL: ttl}
	expiring := CacheState{Age: 58 * time.Minute, TTL: ttl}
	lapsed := CacheState{Age: 90 * time.Minute, TTL: ttl}

	base := Signals{
		AwayLimit: limit, Lead: lead, Reachable: true,
		HeartbeatEnabled: true, AutoSleepEnabled: true,
	}
	with := func(mutate func(*Signals)) Signals {
		s := base
		mutate(&s)
		return s
	}

	cases := []struct {
		name    string
		signals Signals
		want    Action
	}{
		{
			name:    "a quiet attended session with a fresh cache is left alone",
			signals: with(func(s *Signals) { s.Cache = warm }),
			want:    ActionNone,
		},
		{
			name:    "a quiet UNattended session with a fresh cache is still left alone",
			signals: with(func(s *Signals) { s.Cache = warm; s.AwayFor = 6 * time.Hour }),
			want:    ActionNone,
		},
		{
			name:    "the user is here and the cache is about to lapse, so warm it",
			signals: with(func(s *Signals) { s.Cache = expiring }),
			want:    ActionHeartbeat,
		},
		{
			name:    "a cache the estimate says has already lapsed still warms",
			signals: with(func(s *Signals) { s.Cache = lapsed }),
			want:    ActionHeartbeat,
		},
		{
			name:    "the user is gone and the cache is about to lapse, so end the day",
			signals: with(func(s *Signals) { s.Cache = expiring; s.AwayFor = 3 * time.Hour }),
			want:    ActionSleep,
		},
		{
			name:    "an absence shorter than the limit is not an absence",
			signals: with(func(s *Signals) { s.Cache = expiring; s.AwayFor = limit - time.Second }),
			want:    ActionHeartbeat,
		},
		{
			name:    "an open user turn can still be heartbeated safely",
			signals: with(func(s *Signals) { s.Cache = expiring }),
			want:    ActionHeartbeat,
		},
		{
			name:    "an unreachable session is never nudged, however pressed its cache",
			signals: with(func(s *Signals) { s.Cache = lapsed; s.Reachable = false }),
			want:    ActionNone,
		},
		{
			name:    "an unreachable session is not put to sleep either",
			signals: with(func(s *Signals) { s.Cache = lapsed; s.AwayFor = 3 * time.Hour; s.Reachable = false }),
			want:    ActionNone,
		},
		{
			name:    "heartbeat off means no heartbeat",
			signals: with(func(s *Signals) { s.Cache = expiring; s.HeartbeatEnabled = false }),
			want:    ActionNone,
		},
		{
			name:    "heartbeat off does not become sleep",
			signals: with(func(s *Signals) { s.Cache = lapsed; s.HeartbeatEnabled = false }),
			want:    ActionNone,
		},
		{
			name:    "auto-sleep off does not become a heartbeat while the user is gone",
			signals: with(func(s *Signals) { s.Cache = expiring; s.AwayFor = 3 * time.Hour; s.AutoSleepEnabled = false }),
			want:    ActionNone,
		},
		{
			name:    "a lapsing cache waits for the turn to end",
			signals: with(func(s *Signals) { s.Cache = expiring; s.MidTurn = true }),
			want:    ActionNone,
		},
		{
			name:    "an absence waits for the turn to end",
			signals: with(func(s *Signals) { s.Cache = expiring; s.AwayFor = 3 * time.Hour; s.MidTurn = true }),
			want:    ActionNone,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Decide(tc.signals); got != tc.want {
				t.Fatalf("Decide() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestWakeLedger(t *testing.T) {
	now := time.Date(2026, 8, 14, 22, 0, 0, 0, time.UTC)
	window := 12 * time.Hour
	cases := []struct {
		name    string
		limit   int
		stamps  []time.Time
		kept    int
		refusal []string
	}{
		{
			name:   "wakes outside the window no longer count",
			limit:  2,
			stamps: []time.Time{now.Add(-30 * time.Hour), now.Add(-11 * time.Hour)},
			kept:   2,
		},
		{
			name:   "a wake exactly one window ago has aged out",
			limit:  1,
			stamps: []time.Time{now.Add(-window)},
			kept:   1,
		},
		{
			name:    "the limit reached inside the window refuses and names the settings",
			limit:   2,
			stamps:  []time.Time{now.Add(-2 * time.Hour), now.Add(-time.Hour)},
			kept:    2,
			refusal: []string{"Trellis", "crew.wake_limit=2", "crew.wake_limit_window_seconds=43200", "nothing was woken"},
		},
		{
			name:    "a zero limit turns autonomous wakes off",
			limit:   0,
			kept:    0,
			refusal: []string{"turned off", "crew.wake_limit=0"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kept, err := WakeLedger{Limit: tc.limit, Window: window, Stamps: tc.stamps}.Allows("trellis", now)
			if len(kept) != tc.kept {
				t.Fatalf("kept %d stamps (%v), want %d", len(kept), kept, tc.kept)
			}
			for _, at := range kept {
				if !at.After(now.Add(-window)) {
					t.Errorf("a stamp from %s survived a %s window", at, window)
				}
			}
			if tc.refusal == nil {
				if err != nil {
					t.Fatalf("Allows() refused: %v", err)
				}
				if !kept[len(kept)-1].Equal(now) {
					t.Fatalf("the newest kept stamp is %s, want this wake at %s", kept[len(kept)-1], now)
				}
				return
			}
			if err == nil {
				t.Fatal("Allows() let the wake through")
			}
			for _, want := range tc.refusal {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %q does not name %q", err, want)
				}
			}
		})
	}
}
