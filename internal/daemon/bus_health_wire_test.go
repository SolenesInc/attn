package daemon_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestBusStatusNamesTheAppHoldingTheLogPastItsAlarmAgeAndEachConsumerSwitchedOff(t *testing.T) {
	t.Setenv("ATTN_APP_RUNTIME_HOST", filepath.Join(t.TempDir(), "not-installed"))
	t.Setenv("ATTN_BUS_PIN_ALARM_AGE", "1h")
	inBubble(t, func(t *testing.T, w *world) {
		cli, app := w.Client(), w.App()
		applySubscribedApp(t, cli, "greeter")
		applyDeclaration(t, cli, "archive", subscribedApp("archive"), "only")
		setBusConsumerEnabled(t, app, "garden-seed-bells", false)
		w.advance(10 * time.Minute)
		filed := time.Now().UTC()
		fileTicket(t, cli, "Price the order")

		w.advance(59 * time.Minute)
		early := busStatus(t, app)
		greeter := consumer(t, early, "app:greeter")
		if !greeter.HoldsRetentionFloor || greeter.Lag == 0 || greeter.PinAlarm || greeter.OldestUnreadAt != filed.Format(time.RFC3339Nano) {
			t.Errorf("greeter, unable to read the ticket for 59m, = %+v; want it holding the floor unalarmed, its oldest unread dated %s", greeter, filed.Format(time.RFC3339Nano))
		}
		if stalled := healthOf(early, "consumer_stalled", "app:greeter"); stalled == nil || stalled.Level != "error" || !strings.Contains(stalled.Message, "retrying") {
			t.Errorf("health for the retrying greeter = %+v, want a stall error", stalled)
		}
		if pinned := healthOf(early, "retention_pinned", "app:greeter"); pinned != nil {
			t.Errorf("greeter was reported pinning the log before its alarm age: %+v", pinned)
		}

		setAppEnabled(t, cli, "archive", false)
		w.advance(2 * time.Minute)
		late := busStatus(t, app)
		if greeter := consumer(t, late, "app:greeter"); !greeter.PinAlarm || greeter.PinnedBytes == 0 {
			t.Errorf("greeter past the alarm age = %+v, want it alarmed with the bytes it pins", greeter)
		}
		if pinned := healthOf(late, "retention_pinned", "app:greeter"); pinned == nil || !strings.Contains(pinned.Message, "past the 1h tripwire") {
			t.Errorf("the pin finding = %+v, want it naming the 1h tripwire", pinned)
		}
		if bells := consumer(t, late, "garden-seed-bells"); bells.HoldsRetentionFloor || bells.PinAlarm || bells.Lag == 0 {
			t.Errorf("the switched-off garden-seed-bells = %+v, want it behind but neither holding nor alarming the log", bells)
		}
		if off := healthOf(late, "consumer_disabled", "garden-seed-bells"); off == nil || !strings.Contains(off.Message, "does not hold retention open") {
			t.Errorf("health for the switched-off garden-seed-bells = %+v, want a warning that it does not hold the log", off)
		}

		if archive := consumer(t, late, "app:archive"); archive.PinAlarm || archive.HoldsRetentionFloor {
			t.Errorf("the switched-off archive app = %+v, want it neither holding nor alarming the log it had read", archive)
		}
		if disabled := healthOf(late, "consumer_disabled", "app:archive"); disabled == nil || !strings.Contains(disabled.Message, "keeps the unread backlog") || !strings.Contains(disabled.Message, "uninstall") {
			t.Errorf("health for the switched-off archive app = %+v, want it to say the app keeps its backlog until uninstalled", disabled)
		}
	})
}

func healthOf(status protocol.BusStatusResultMessage, kind, subject string) *protocol.BusHealthEntry {
	for _, h := range status.Health {
		if h.Kind == kind && h.Subject == subject {
			return &h
		}
	}
	return nil
}
