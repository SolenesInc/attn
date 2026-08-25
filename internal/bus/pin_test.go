package bus

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func pinStore(t *testing.T, name string, enabled bool, unread time.Duration) *memStore {
	t.Helper()
	s := newMemStore()
	for i := 0; i < 2; i++ {
		if _, err := s.Append(Event{Name: "session.state.changed", Subject: "s1"}, statusNow.Add(-48*time.Hour)); err != nil {
			t.Fatalf("append read: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Append(Event{Name: "ticket.updated", Subject: fmt.Sprintf("t%d", i)}, statusNow.Add(-unread)); err != nil {
			t.Fatalf("append unread: %v", err)
		}
	}
	if err := s.SaveConsumer(Consumer{Name: name, Cursor: 2, Enabled: enabled}, statusNow.Add(-unread)); err != nil {
		t.Fatalf("save consumer: %v", err)
	}
	return s
}

func pinBus(t *testing.T, s *memStore, age time.Duration) *Bus {
	t.Helper()
	return New(Options{Store: s, Now: func() time.Time { return statusNow }, PinAlarmAge: age})
}

func consumerStatus(t *testing.T, s Status, name string) ConsumerStatus {
	t.Helper()
	for _, c := range s.Consumers {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no consumer %q in status", name)
	return ConsumerStatus{}
}

func TestPinUnderTheTripwireIsSilent(t *testing.T) {
	s := pinStore(t, "notifier", true, 20*time.Minute)
	b := pinBus(t, s, time.Hour)

	status, err := b.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	c := consumerStatus(t, status, "notifier")
	if !c.HoldsRetentionFloor {
		t.Fatal("the only enabled consumer is the floor; the fixture is wrong")
	}
	if c.PinAlarm {
		t.Errorf("a 20-minute pin alarmed under a 1h tripwire")
	}
	if _, ok := findHealth(status, HealthRetentionPinned, "notifier"); ok {
		t.Errorf("a 20-minute pin produced a health finding")
	}
	pins, err := b.PinAlarms()
	if err != nil {
		t.Fatalf("pin alarms: %v", err)
	}
	if len(pins) != 0 {
		t.Errorf("PinAlarms reported %+v under the tripwire", pins)
	}
}

func TestPinPastTheTripwireNamesTheHoldAndTheLimit(t *testing.T) {
	s := pinStore(t, "notifier", true, 3*time.Hour)
	b := pinBus(t, s, time.Hour)

	status, err := b.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	c := consumerStatus(t, status, "notifier")
	if !c.PinAlarm {
		t.Fatal("a 3-hour pin did not cross a 1h tripwire")
	}
	if c.PinnedBytes <= 0 {
		t.Errorf("pinned bytes = %d, want the weight of the three unread events", c.PinnedBytes)
	}
	h, ok := findHealth(status, HealthRetentionPinned, "notifier")
	if !ok {
		t.Fatal("no retention_pinned health entry for an alarming pin")
	}
	if h.Level != HealthWarn {
		t.Errorf("level = %q, want warn", h.Level)
	}
	for _, want := range []string{"notifier", "3h", "1h", "3 events", "seq 2"} {
		if !strings.Contains(h.Message, want) {
			t.Errorf("message %q is missing %q", h.Message, want)
		}
	}
}

func TestPinAlarmsMatchesTheSnapshot(t *testing.T) {
	s := pinStore(t, "app:ticketwatch", true, 6*time.Hour)
	b := pinBus(t, s, time.Hour)

	status, err := b.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	pins, err := b.PinAlarms()
	if err != nil {
		t.Fatalf("pin alarms: %v", err)
	}
	if len(pins) != 1 {
		t.Fatalf("pins = %+v, want exactly the one the snapshot marked", pins)
	}
	c := consumerStatus(t, status, "app:ticketwatch")
	if pins[0].Consumer != c.Name || pins[0].Cursor != c.Cursor ||
		pins[0].Events != c.Lag || pins[0].Bytes != c.PinnedBytes {
		t.Fatalf("pin %+v does not match the snapshot's consumer %+v", pins[0], c)
	}
	if pins[0].Threshold != time.Hour {
		t.Errorf("threshold = %s, want the tripwire it crossed", pins[0].Threshold)
	}
	if h, ok := findHealth(status, HealthRetentionPinned, "app:ticketwatch"); !ok || h.Message != PinMessage(pins[0]) {
		t.Errorf("health message and PinMessage disagree:\n %q\n %q", h.Message, PinMessage(pins[0]))
	}
}

func TestDisabledConsumerNeverAlarms(t *testing.T) {
	s := pinStore(t, "notifier", false, 30*24*time.Hour)
	b := pinBus(t, s, time.Hour)

	status, err := b.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if c := consumerStatus(t, status, "notifier"); c.PinAlarm {
		t.Error("a disabled consumer alarmed; disabling is the way out, not a way in")
	}
	pins, err := b.PinAlarms()
	if err != nil {
		t.Fatalf("pin alarms: %v", err)
	}
	if len(pins) != 0 {
		t.Errorf("PinAlarms reported a disabled consumer: %+v", pins)
	}
}

func TestOnlyTheFloorHolderAlarms(t *testing.T) {
	s := pinStore(t, "behind", true, 6*time.Hour)
	if err := s.SaveConsumer(Consumer{Name: "further-behind", Cursor: 0, Enabled: true}, statusNow.Add(-6*time.Hour)); err != nil {
		t.Fatalf("save second consumer: %v", err)
	}
	b := pinBus(t, s, time.Hour)

	pins, err := b.PinAlarms()
	if err != nil {
		t.Fatalf("pin alarms: %v", err)
	}
	if len(pins) != 1 || pins[0].Consumer != "further-behind" {
		t.Fatalf("pins = %+v, want only the consumer at the floor", pins)
	}
}

func TestCaughtUpConsumerNeverAlarms(t *testing.T) {
	s := newMemStore()
	if _, err := s.Append(Event{Name: "ticket.updated", Subject: "t"}, statusNow.Add(-90*24*time.Hour)); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.SaveConsumer(Consumer{Name: "notifier", Cursor: 1, Enabled: true}, statusNow.Add(-90*24*time.Hour)); err != nil {
		t.Fatalf("save consumer: %v", err)
	}
	b := pinBus(t, s, time.Hour)

	pins, err := b.PinAlarms()
	if err != nil {
		t.Fatalf("pin alarms: %v", err)
	}
	if len(pins) != 0 {
		t.Fatalf("a caught-up consumer alarmed: %+v", pins)
	}
}

func TestNegativePinAlarmAgeTurnsTheFindingOff(t *testing.T) {
	s := pinStore(t, "notifier", true, 30*24*time.Hour)
	b := pinBus(t, s, -1)

	status, err := b.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if c := consumerStatus(t, status, "notifier"); c.PinAlarm {
		t.Error("the snapshot marked a consumer with the alarm turned off")
	}
	if _, ok := findHealth(status, HealthRetentionPinned, "notifier"); ok {
		t.Error("a health finding survived the alarm being turned off")
	}
	pins, err := b.PinAlarms()
	if err != nil {
		t.Fatalf("pin alarms: %v", err)
	}
	if len(pins) != 0 {
		t.Errorf("PinAlarms reported with the alarm off: %+v", pins)
	}
}

func TestPendingBytesWeighsOnlyTheBacklog(t *testing.T) {
	s := pinStore(t, "notifier", true, 3*time.Hour)
	whole, err := s.PendingBytes(0)
	if err != nil {
		t.Fatalf("pending bytes: %v", err)
	}
	backlog, err := s.PendingBytes(2)
	if err != nil {
		t.Fatalf("pending bytes: %v", err)
	}
	if backlog <= 0 || backlog >= whole {
		t.Fatalf("backlog above seq 2 = %d, whole log = %d; want a strict subset", backlog, whole)
	}
}

func TestPinAlarmAgeFromEnv(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want time.Duration
		says string
	}{
		{raw: "", want: DefaultPinAlarmAge},
		{raw: "90s", want: 90 * time.Second, says: "retention-pin alarm set to 1m30s"},
		{raw: "2h", want: 2 * time.Hour, says: "retention-pin alarm set to 2h"},
		// Off, and negative rather than zero: zero reads as "unset" inside New and
		// would quietly restore the default the user just turned off.
		{raw: "0", want: -1, says: "the retention-pin alarm is off"},
		{raw: "-5m", want: -1, says: "the retention-pin alarm is off"},
		{raw: "soon", want: DefaultPinAlarmAge, says: "is not a duration"},
	} {
		t.Run(fmt.Sprintf("%q", tc.raw), func(t *testing.T) {
			if tc.raw == "" {
				t.Setenv(PinAlarmAgeEnv, "")
			} else {
				t.Setenv(PinAlarmAgeEnv, tc.raw)
			}
			var said []string
			got := PinAlarmAgeFromEnv(func(format string, args ...interface{}) {
				said = append(said, fmt.Sprintf(format, args...))
			})
			if got != tc.want {
				t.Errorf("%q = %s, want %s", tc.raw, got, tc.want)
			}
			whole := strings.Join(said, "\n")
			if tc.says == "" {
				if whole != "" {
					t.Errorf("an unset knob said %q; a default nobody asked to change is not news", whole)
				}
				return
			}
			if !strings.Contains(whole, tc.says) {
				t.Errorf("said %q, want it to carry %q", whole, tc.says)
			}
		})
	}
}

func TestNewCarriesAResolvedPinAlarmAge(t *testing.T) {
	t.Setenv(PinAlarmAgeEnv, "0")
	b := New(Options{PinAlarmAge: PinAlarmAgeFromEnv(nil)})
	if b.pinAlarmAge >= 0 {
		t.Errorf("a bus built with the off switch has pinAlarmAge %s, want it negative", b.pinAlarmAge)
	}
}

func TestTheMessageNamesTheTripwireExactly(t *testing.T) {
	for _, tc := range []struct {
		threshold time.Duration
		want      string
	}{
		{threshold: time.Hour, want: "past the 1h tripwire"},
		{threshold: 90 * time.Second, want: "past the 1m30s tripwire"},
		{threshold: 45 * time.Second, want: "past the 45s tripwire"},
		{threshold: 90 * time.Minute, want: "past the 1h30m tripwire"},
	} {
		got := PinMessage(Pin{Consumer: "app:x", Age: 11 * time.Minute, Threshold: tc.threshold})
		if !strings.Contains(got, tc.want) {
			t.Errorf("a %s tripwire reads %q, want it to carry %q", tc.threshold, got, tc.want)
		}
	}
}

func TestRetentionFromEnv(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want time.Duration
		says string
	}{
		{raw: "", want: DefaultRetention},
		{raw: "1s", want: time.Second, says: "retention window set to 1s"},
		{raw: "5m", want: 5 * time.Minute, says: "retention window set to 5m"},
		{raw: "0", want: DefaultRetention, says: "is not a positive window"},
		{raw: "-5m", want: DefaultRetention, says: "is not a positive window"},
		{raw: "soon", want: DefaultRetention, says: "is not a duration"},
	} {
		t.Run(fmt.Sprintf("%q", tc.raw), func(t *testing.T) {
			t.Setenv(RetentionEnv, tc.raw)
			var said []string
			got := RetentionFromEnv(func(format string, args ...interface{}) {
				said = append(said, fmt.Sprintf(format, args...))
			})
			if got != tc.want {
				t.Errorf("%q = %s, want %s", tc.raw, got, tc.want)
			}
			whole := strings.Join(said, "\n")
			if tc.says == "" {
				if whole != "" {
					t.Errorf("an unset knob said %q; a default nobody asked to change is not news", whole)
				}
				return
			}
			if !strings.Contains(whole, tc.says) {
				t.Errorf("said %q, want it to carry %q", whole, tc.says)
			}
		})
	}
}

func TestAMovedRetentionWindowIsWhatLetsATrimRemoveAnything(t *testing.T) {
	seed := func(t *testing.T, b *Bus, s Store) {
		t.Helper()
		for _, name := range []string{"a.happened", "b.happened"} {
			if _, err := b.Publish(name, "subject-"+name, nil); err != nil {
				t.Fatalf("Publish(%s): %v", name, err)
			}
		}
		_, head, err := s.Bounds()
		if err != nil {
			t.Fatalf("Bounds: %v", err)
		}
		if err := s.SaveConsumer(Consumer{Name: "reader", Cursor: head, Enabled: true}, time.Now()); err != nil {
			t.Fatalf("SaveConsumer: %v", err)
		}
	}

	t.Run("at the shipped default nothing is old enough", func(t *testing.T) {
		s := newMemStore()
		b := New(Options{Store: s, Retention: DefaultRetention})
		seed(t, b, s)
		if n, err := b.Trim(); err != nil || n != 0 {
			t.Fatalf("trimmed %d event(s) from a fresh log at the 30-day window, want 0 (err=%v)", n, err)
		}
	})

	t.Run("moved to a window a run can cross", func(t *testing.T) {
		s := newMemStore()
		b := New(Options{Store: s, Retention: time.Nanosecond})
		seed(t, b, s)
		if n, err := b.Trim(); err != nil || n != 2 {
			t.Fatalf("trimmed %d event(s) with the window moved, want 2 (err=%v)", n, err)
		}
	})
}
