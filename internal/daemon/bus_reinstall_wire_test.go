package daemon_test

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRemovingARetryingAppEndsItsDeliveryAtOnceAndReinstallingStartsAtTheHead(t *testing.T) {
	t.Setenv("ATTN_APP_RUNTIME_HOST", filepath.Join(t.TempDir(), "not-installed"))
	inBubble(t, func(t *testing.T, w *world) {
		cli, app := w.Client(), w.App()
		applySubscribedApp(t, cli, "greeter")
		fileTicket(t, cli, "Price the order")
		w.advance(10 * time.Minute)
		if stuck := consumer(t, busStatus(t, app), "app:greeter"); stuck.Stalled == "" {
			t.Fatalf("greeter without a runtime = %+v, want it retrying", stuck)
		}

		asked := time.Now()
		if _, err := cli.AppRemove("greeter"); err != nil {
			t.Fatal(err)
		}
		if waited := time.Since(asked); waited > 0 {
			t.Errorf("removing greeter waited %s for its retry backoff", waited)
		}
		for _, c := range busStatus(t, app).Consumers {
			if c.Name == "app:greeter" {
				t.Fatalf("after removal the bus still lists %+v", c)
			}
		}

		fileTicket(t, cli, "Ship the order")
		head := busStatus(t, app).Head
		applySubscribedApp(t, cli, "greeter")
		if again := consumer(t, busStatus(t, app), "app:greeter"); again.Cursor < head {
			t.Errorf("the reinstalled greeter starts at cursor %d, want the head %d, not the old one's place", again.Cursor, head)
		}
	})
}
