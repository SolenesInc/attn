package bus

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

var statusNow = time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)

func statusBus(t *testing.T) (*Bus, *memStore) {
	t.Helper()
	s := newMemStore()
	b := New(Options{Store: s, Now: func() time.Time { return statusNow }})
	return b, s
}

func publishAt(t *testing.T, s *memStore, name string, subjects, n int, ago time.Duration) {
	t.Helper()
	for i := 0; i < n; i++ {
		subject := fmt.Sprintf("%s_%d", name, i%subjects)
		if _, err := s.Append(Event{
			Name: name, Subject: subject, Source: "test",
		}, statusNow.Add(-ago)); err != nil {
			t.Fatalf("append %s: %v", name, err)
		}
	}
}

func producer(t *testing.T, s Status, name string) Producer {
	t.Helper()
	for _, p := range s.Producers {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no producer %q in status", name)
	return Producer{}
}

func findHealth(s Status, kind, subject string) (Health, bool) {
	for _, h := range s.Health {
		if h.Kind == kind && h.Subject == subject {
			return h, true
		}
	}
	return Health{}, false
}

func TestStatusReportsProducerRatesAndShare(t *testing.T) {
	b, s := statusBus(t)
	publishAt(t, s, "session.state.changed", 3, 60, 30*time.Minute)
	publishAt(t, s, "session.state.changed", 3, 120, 5*time.Hour)
	publishAt(t, s, "pr.updated", 20, 20, 2*time.Hour)

	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Rows != 200 {
		t.Fatalf("rows = %d, want 200", status.Rows)
	}

	loud := producer(t, status, "session.state.changed")
	if loud.RecentPerHour != 60 {
		t.Errorf("recent rate = %v, want 60", loud.RecentPerHour)
	}
	if loud.BaselinePerHour != 7.5 {
		t.Errorf("baseline rate = %v, want 7.5", loud.BaselinePerHour)
	}
	if want := 180.0 / 200.0; loud.Share != want {
		t.Errorf("share = %v, want %v", loud.Share, want)
	}
	if loud.Subjects != 3 {
		t.Errorf("subjects = %d, want 3", loud.Subjects)
	}
}

func TestStatusDoesNotCallABurstySmallProducerLoud(t *testing.T) {
	b, s := statusBus(t)
	publishAt(t, s, "pr.updated", 40, 900, 10*time.Minute)

	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	p := producer(t, status, "pr.updated")
	if p.Surging {
		t.Errorf("a burst inside one window is not a sustained rate; surge = %v/h over %s",
			p.SurgePerHour, p.SurgeWindow)
	}
	if _, ok := findHealth(status, HealthProducerSurging, "pr.updated"); ok {
		t.Error("a burst must not raise a health warning")
	}
}

func TestStatusCatchesASurgeOnTheSustainedWindow(t *testing.T) {
	b, s := statusBus(t)
	for i := 0; i < 6001; i++ {
		publishAt(t, s, "session.state.changed", 2, 1, time.Duration(i%360)*time.Minute)
	}

	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	p := producer(t, status, "session.state.changed")
	if !p.Surging {
		t.Fatalf("sustained %v/h did not trip the %v/h ceiling", p.SustainedPerHour, SurgeRatePerHour)
	}
	h, ok := findHealth(status, HealthProducerSurging, "session.state.changed")
	if !ok {
		t.Fatal("a surging producer must raise a health warning")
	}
	for _, want := range []string{"session.state.changed", "1000/hour", "events/hour"} {
		if !strings.Contains(h.Message, want) {
			t.Errorf("message %q is missing %q", h.Message, want)
		}
	}
}

func TestStatusCatchesAStandingLoudProducerOnTheBaselineWindow(t *testing.T) {
	b, s := statusBus(t)
	publishAt(t, s, "session.state.changed", 4, 200, 3*time.Hour)
	for i := 0; i < 24000; i++ {
		publishAt(t, s, "session.state.changed", 4, 1, 7*time.Hour+time.Duration(i%600)*time.Minute)
	}

	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	p := producer(t, status, "session.state.changed")
	if p.SustainedPerHour >= SurgeRatePerHour {
		t.Fatalf("this case needs a quiet 6h window; sustained = %v/h", p.SustainedPerHour)
	}
	if !p.Surging {
		t.Fatalf("a producer at %v/h over 24h must still be called loud", p.BaselinePerHour)
	}
	if p.SurgeWindow != BaselineWindow {
		t.Errorf("surge window = %s, want the 24h window that tripped", p.SurgeWindow)
	}
}

func TestReportLoudProducersIsSilentOnAHealthyLog(t *testing.T) {
	s := newMemStore()
	var lines []string
	var mu sync.Mutex
	b := New(Options{
		Store: s,
		Now:   func() time.Time { return statusNow },
		Log: func(format string, args ...interface{}) {
			mu.Lock()
			lines = append(lines, fmt.Sprintf(format, args...))
			mu.Unlock()
		},
	})
	publishAt(t, s, "pr.updated", 30, 400, 3*time.Hour)

	b.ReportLoudProducers()
	if len(lines) != 0 {
		t.Fatalf("want silence on a healthy log, got %q", lines)
	}
}

func TestReportLoudProducersNamesTheFactRateAndWindow(t *testing.T) {
	s := newMemStore()
	var lines []string
	var mu sync.Mutex
	b := New(Options{
		Store: s,
		Now:   func() time.Time { return statusNow },
		Log: func(format string, args ...interface{}) {
			mu.Lock()
			lines = append(lines, fmt.Sprintf(format, args...))
			mu.Unlock()
		},
	})
	for i := 0; i < 7000; i++ {
		publishAt(t, s, "session.state.changed", 2, 1, time.Duration(i%360)*time.Minute)
	}

	b.ReportLoudProducers()
	if len(lines) != 1 {
		t.Fatalf("want exactly one line for one loud class, got %q", lines)
	}
	for _, want := range []string{"session.state.changed", "1000/hour", "% of the log"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("line %q is missing %q", lines[0], want)
		}
	}
}

func TestStartReportsLoudProducersBeforeTheFirstTick(t *testing.T) {
	s := newMemStore()
	said := make(chan string, 8)
	b := New(Options{
		Store:        s,
		Now:          func() time.Time { return statusNow },
		TrimInterval: time.Hour,
		Log: func(format string, args ...interface{}) {
			select {
			case said <- fmt.Sprintf(format, args...):
			default:
			}
		},
	})
	for i := 0; i < 7000; i++ {
		publishAt(t, s, "session.state.changed", 2, 1, time.Duration(i%360)*time.Minute)
	}

	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer b.Stop()

	select {
	case line := <-said:
		if !strings.Contains(line, "session.state.changed") || !strings.Contains(line, "1000/hour") {
			t.Fatalf("first line does not report the loud producer: %q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Start never reported the loud producer; it waited for the retention tick")
	}
}

func TestStatusSaysWhenAnEnabledConsumerStopsAdvancing(t *testing.T) {
	b, s := statusBus(t)
	publishAt(t, s, "session.state.changed", 4, 100, 3*time.Hour)
	if err := s.SaveConsumer(Consumer{Name: "stuck", Cursor: 1, Enabled: true}, statusNow.Add(-StallAge)); err != nil {
		t.Fatalf("SaveConsumer: %v", err)
	}

	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	h, ok := findHealth(status, HealthConsumerLagging, "stuck")
	if !ok {
		t.Fatal("a consumer whose cursor stopped moving while behind must be reported")
	}
	if h.Level != HealthError {
		t.Errorf("level = %q, want %q", h.Level, HealthError)
	}
	for _, want := range []string{"stuck", "99 events", "not advancing"} {
		if !strings.Contains(h.Message, want) {
			t.Errorf("message %q is missing %q", h.Message, want)
		}
	}
}

func TestStatusDoesNotCallAMovingConsumerStalled(t *testing.T) {
	b, s := statusBus(t)
	publishAt(t, s, "session.state.changed", 4, 100, 3*time.Hour)
	if err := s.SaveConsumer(Consumer{Name: "catching-up", Cursor: 1, Enabled: true}, statusNow.Add(-time.Second)); err != nil {
		t.Fatalf("SaveConsumer: %v", err)
	}

	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if h, ok := findHealth(status, HealthConsumerLagging, "catching-up"); ok {
		t.Errorf("a consumer that just advanced is not stalled: %q", h.Message)
	}
}

func TestStatusOnlyClaimsDeliveryKnowledgeWhenItHasIt(t *testing.T) {
	b, s := statusBus(t)
	publishAt(t, s, "pr.updated", 2, 5, time.Hour)
	if err := s.SaveConsumer(Consumer{Name: "elsewhere", Cursor: 5, Enabled: true}, statusNow); err != nil {
		t.Fatalf("SaveConsumer: %v", err)
	}

	status, err := b.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Delivering {
		t.Error("a bus with no registered consumers is not delivering")
	}
	if _, ok := findHealth(status, HealthConsumerNotLive, "elsewhere"); ok {
		t.Error("a reader outside the daemon cannot know a consumer is not running")
	}

	running, _ := statusBus(t)
	running.store = s
	if err := running.Register("mine", All, func(context.Context, Event) error { return nil }); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := running.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(running.Stop)

	status, err = running.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Delivering {
		t.Fatal("a started bus with a registered consumer is delivering")
	}
	if _, ok := findHealth(status, HealthConsumerNotLive, "elsewhere"); !ok {
		t.Error("a daemon that owns delivery must report a registration nothing is reading")
	}
}
