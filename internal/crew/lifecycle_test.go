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
		HeartbeatEnabled: true, AutoSleepEnabled: true, ContextHandoffEnabled: true,
	}
	full := ContextPressure{Tokens: 160000, Budget: 160000}
	roomy := ContextPressure{Tokens: 40000, Budget: 160000}
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
			name:    "a full context ends the day with the user watching and the cache warm",
			signals: with(func(s *Signals) { s.Cache = warm; s.Context = full }),
			want:    ActionContextHandoff,
		},
		{
			name:    "a full context outranks the heartbeat its cache would have earned",
			signals: with(func(s *Signals) { s.Cache = expiring; s.Context = full }),
			want:    ActionContextHandoff,
		},
		{
			name:    "a context with room left decides nothing on its own",
			signals: with(func(s *Signals) { s.Cache = warm; s.Context = roomy }),
			want:    ActionNone,
		},
		{
			name:    "an unreachable session is not asked to close a full context",
			signals: with(func(s *Signals) { s.Cache = warm; s.Context = full; s.Reachable = false }),
			want:    ActionNone,
		},
		{
			name:    "a full context is asked mid-turn",
			signals: with(func(s *Signals) { s.Cache = warm; s.Context = full; s.MidTurn = true }),
			want:    ActionContextHandoff,
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
		{
			name:    "the context half off leaves a full context alone",
			signals: with(func(s *Signals) { s.Cache = warm; s.Context = full; s.ContextHandoffEnabled = false }),
			want:    ActionNone,
		},
		{
			name:    "the context half off does not become a heartbeat",
			signals: with(func(s *Signals) { s.Cache = expiring; s.Context = full; s.ContextHandoffEnabled = false }),
			want:    ActionHeartbeat,
		},
		{
			name:    "a budget attn could not resolve never reads as full",
			signals: with(func(s *Signals) { s.Cache = warm; s.Context = ContextPressure{Tokens: 900000} }),
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

func TestCacheStateRemaining(t *testing.T) {
	fresh := CacheState{Age: time.Minute, TTL: time.Hour}
	if got := fresh.Remaining(); got != 59*time.Minute {
		t.Fatalf("Remaining() = %s, want 59m", got)
	}
	stale := CacheState{Age: 2 * time.Hour, TTL: time.Hour}
	if got := stale.Remaining(); got != -time.Hour {
		t.Fatalf("Remaining() = %s, want -1h", got)
	}
}

func TestWakeLedger_CountsOnlyWakesInsideTheWindow(t *testing.T) {
	now := time.Date(2026, 8, 14, 22, 0, 0, 0, time.UTC)
	ledger := WakeLedger{
		Limit:  2,
		Window: 12 * time.Hour,
		Stamps: []time.Time{
			now.Add(-30 * time.Hour),
			now.Add(-11 * time.Hour),
		},
	}
	kept, err := ledger.Allows("trellis", now)
	if err != nil {
		t.Fatalf("Allows() refused a wake with one stamp inside the window: %v", err)
	}
	if len(kept) != 2 {
		t.Fatalf("Allows() kept %d stamps, want the in-window one plus this wake", len(kept))
	}
	if !kept[len(kept)-1].Equal(now) {
		t.Fatalf("the newest kept stamp is %s, want this wake at %s", kept[len(kept)-1], now)
	}
	for _, at := range kept {
		if at.Before(now.Add(-ledger.Window)) {
			t.Fatalf("a stamp from %s survived a %s window", at, ledger.Window)
		}
	}
}

func TestWakeLedger_RefusalNamesTheLimitAndTheAsk(t *testing.T) {
	now := time.Date(2026, 8, 14, 22, 0, 0, 0, time.UTC)
	ledger := WakeLedger{
		Limit:  2,
		Window: 12 * time.Hour,
		Stamps: []time.Time{now.Add(-2 * time.Hour), now.Add(-time.Hour)},
	}
	_, err := ledger.Allows("trellis", now)
	if err == nil {
		t.Fatal("Allows() let a third wake through a limit of 2")
	}
	for _, want := range []string{"Trellis", "crew.wake_limit=2", "crew.wake_limit_window_seconds=43200", "nothing was woken"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q", err, want)
		}
	}
}

func TestWakeLedger_ZeroTurnsAutonomousWakesOff(t *testing.T) {
	now := time.Date(2026, 8, 14, 22, 0, 0, 0, time.UTC)
	_, err := WakeLedger{Limit: 0, Window: 12 * time.Hour}.Allows("trellis", now)
	if err == nil {
		t.Fatal("Allows() woke a member with the limit set to 0")
	}
	if !strings.Contains(err.Error(), "turned off") || !strings.Contains(err.Error(), "crew.wake_limit=0") {
		t.Fatalf("the refusal %q does not say autonomous wakes are off", err)
	}
}
