package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestBusStatusNamesDisabledCoreConsumerWithoutPinning(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli, app := w.Client(), w.App()
		setBusConsumerEnabled(t, app, "garden-seed-bells", false)
		if _, err := cli.CreateTicket("planner", "Price the order", "", ""); err != nil {
			t.Fatal(err)
		}
		status := busStatus(t, app)
		bells := consumer(t, status, "garden-seed-bells")
		if bells.HoldsRetentionFloor || bells.PinAlarm || bells.Lag == 0 {
			t.Errorf("disabled garden-seed-bells = %+v; want unread facts without a retention pin", bells)
		}
		if off := healthOf(status, "consumer_disabled", "garden-seed-bells"); off == nil || !strings.Contains(off.Message, "does not hold retention open") {
			t.Errorf("disabled consumer health = %+v; want a warning that it releases retention", off)
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
