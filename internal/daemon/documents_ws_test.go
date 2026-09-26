package daemon

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

type wsSubscriber struct {
	client *wsClient
}

func newWSSubscriber() *wsSubscriber {
	return &wsSubscriber{client: &wsClient{send: make(chan outboundMessage, 256)}}
}

func (s *wsSubscriber) nextEvent(t *testing.T) map[string]any {
	t.Helper()
	select {
	case msg := <-s.client.send:
		var out map[string]any
		if err := json.Unmarshal(msg.payload, &out); err != nil {
			t.Fatalf("decode outbound message: %v", err)
		}
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon sent nothing to this client")
		return nil
	}
}

func (s *wsSubscriber) nextDelivery(t *testing.T, id string) map[string]any {
	t.Helper()
	event := s.nextEvent(t)
	if event["event"] != protocol.EventDocSubscriptionDelivery {
		t.Fatalf("expected a delivery, got %v", event)
	}
	if event["subscription_id"] != id {
		t.Fatalf("delivery carried subscription_id %v, want %q", event["subscription_id"], id)
	}
	return event
}

func wsSubscribe(d *Daemon, s *wsSubscriber, id string, have []protocol.DocumentRevision) {
	d.handleDocSubscribeWS(s.client, &protocol.DocSubscribeMessage{
		Cmd:            protocol.CmdDocSubscribe,
		Query:          testQuery(),
		Have:           have,
		SubscriptionID: protocol.Ptr(id),
	})
}

func TestDisconnectDropsEveryLiveQueryTheClientHeld(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	sub := newWSSubscriber()
	wsSubscribe(d, sub, "tile-1", nil)
	sub.nextDelivery(t, "tile-1")
	wsSubscribe(d, sub, "tile-2", nil)
	sub.nextDelivery(t, "tile-2")

	d.dropDocSubscriptions(sub.client)
	waitForSubscriptionCount(t, d, 0)
	if got := sub.client.docSubscriptions.count(); got != 0 {
		t.Fatalf("the client still holds %d subscriptions", got)
	}
}

func waitForSubscriptionCount(t *testing.T, d *Daemon, want int) {
	t.Helper()
	waitFor(t, fmt.Sprintf("the daemon to hold %d live subscriptions", want), func() bool {
		return d.documentSubscriptionCount() == want
	})
}
