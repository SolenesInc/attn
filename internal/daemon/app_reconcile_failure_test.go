package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/store"
)

func TestAGapWithNoHandlerDisablesTheAppWithoutMovingItsCursor(t *testing.T) {
	d := newAppDaemon(t)
	installApp(t, d, "greeter", subscribingWithoutReconcile("ticket.*"))
	seedAppConsumer(t, d, "greeter", true, 1)

	gap := &bus.Gap{Cursor: 1, Earliest: 4, Head: 6, Missed: 2}
	err := d.appPreDrain("greeter")(context.Background(),
		bus.Consumer{Name: apps.ConsumerName("greeter")}, gap)
	if err == nil || !strings.Contains(err.Error(), "does not declare reconcile") {
		t.Fatalf("pre-drain error = %v", err)
	}
	if appEnabled(t, d, "greeter") {
		t.Fatal("an app that cannot reconcile a gap was left enabled")
	}
	consumer, _, err := d.store.GetBusConsumer(apps.ConsumerName("greeter"))
	if err != nil || consumer.Cursor != 1 {
		t.Fatalf("cursor = %d, %v; a gap it cannot rebuild must not move it", consumer.Cursor, err)
	}
	notes := appNotifications(t, d, notificationKindAppAutoDisabled)
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "reconcile") {
		t.Fatalf("notifications = %+v", notes)
	}
	var missing []store.AppInvocation
	for _, inv := range invocationsOf(t, d, "greeter") {
		if inv.Handler == "missing_reconcile" {
			missing = append(missing, inv)
		}
	}
	if len(missing) != 1 || missing[0].Kind != store.AppInvocationKindReconcile {
		t.Fatalf("missing_reconcile invocations = %+v, want exactly one", missing)
	}

	status := appStatus(t, d, "greeter").AppStatusResult.Reconcile
	if status.State != appReconcileStateUnsupported || status.Reason == nil {
		t.Fatalf("status = %+v", status)
	}
}
