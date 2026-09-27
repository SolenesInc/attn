package daemon_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAppHoldingTheLogPastTheAlarmAgeIsAnnouncedOncePerEpisodeWithTheWayOut(t *testing.T) {
	t.Setenv("ATTN_APP_RUNTIME_HOST", filepath.Join(t.TempDir(), "not-installed"))
	t.Setenv("ATTN_BUS_PIN_ALARM_AGE", "1h")
	inBubble(t, func(t *testing.T, w *world) {
		cli, app := w.Client(), w.App()
		applySubscribedApp(t, cli, "greeter")
		fileTicket(t, cli, "Price the order")
		pinned := func() []protocol.Notification { return appNotificationsOf(app, "bus_retention_pinned") }

		w.advance(59 * time.Minute)
		if notes := pinned(); len(notes) != 0 {
			t.Fatalf("an app behind for less than the alarm age was announced: %+v", notes)
		}
		w.advance(45 * time.Minute)
		notes := pinned()
		if len(notes) != 1 || notes[0].Title != "Event log held open by app:greeter" || notes[0].Severity != protocol.NotificationSeverityWarning {
			t.Fatalf("pin notifications after 104 minutes = %+v, want one warning naming app:greeter", notes)
		}
		requireMentions(t, "the pin notification", notes[0].Body, "greeter app", "attn app status greeter", "attn app runtime restart", "attn bus disable app:greeter")
		w.advance(3 * time.Hour)
		if notes := pinned(); len(notes) != 1 {
			t.Fatalf("the same pin was announced %d times, want once", len(notes))
		}

		setBusConsumerEnabled(t, app, "app:greeter", false)
		w.advance(20 * time.Minute)
		setBusConsumerEnabled(t, app, "app:greeter", true)
		w.advance(14 * time.Minute)
		if notes := pinned(); len(notes) != 1 {
			t.Fatalf("the pin was announced again at its first sighting after it cleared: %d notifications", len(notes))
		}
		w.advance(20 * time.Minute)
		if notes := pinned(); len(notes) != 2 {
			t.Errorf("after the pin cleared and came back, %d notifications, want a second one", len(notes))
		}
	})
}

func setBusConsumerEnabled(t *testing.T, app *testworld.Peer, consumer string, enabled bool) {
	t.Helper()
	requestID := consumer + time.Now().String()
	result := testworld.Request(app, protocol.BusSetConsumerEnabledMessage{
		Cmd: protocol.CmdBusSetConsumerEnabled, RequestID: requestID, Consumer: consumer, Enabled: enabled,
	}, protocol.EventBusSetConsumerEnabledResult, func(m protocol.BusSetConsumerEnabledResultMessage) bool { return m.RequestID == requestID })
	if !result.Success {
		t.Fatalf("set %s enabled=%t: %s", consumer, enabled, protocol.Deref(result.Error))
	}
}
