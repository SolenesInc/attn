package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/store"
)

func pinAlarmDaemon() *Daemon {
	return &Daemon{store: store.New()}
}

func samplePin(consumer string, cursor int64) bus.Pin {
	return bus.Pin{
		Consumer:       consumer,
		Cursor:         cursor,
		Events:         412,
		Bytes:          31_000,
		OldestUnreadAt: time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC),
		Age:            3 * time.Hour,
		Threshold:      time.Hour,
	}
}

func TestMovedCursorRestartsTheConfirmation(t *testing.T) {
	d := pinAlarmDaemon()

	d.recordBusPins([]bus.Pin{samplePin("notifier", 100)})
	if n := d.recordBusPins([]bus.Pin{samplePin("notifier", 140)}); n != 0 {
		t.Fatalf("a moved cursor was treated as a confirmation of the old position")
	}
	if n := d.recordBusPins([]bus.Pin{samplePin("notifier", 140)}); n != 1 {
		t.Fatalf("the new position was never confirmed")
	}
}

func TestNonAppPinNotificationOffersTheBusWayOut(t *testing.T) {
	body := busPinNotificationBody(samplePin("notifier", 12))

	if strings.Contains(body, "app runtime") {
		t.Errorf("a non-app consumer was told to restart the app runtime:\n%s", body)
	}
	for _, want := range []string{"attn bus status", "attn bus disable notifier"} {
		if !strings.Contains(body, want) {
			t.Errorf("body is missing %q:\n%s", want, body)
		}
	}
}
