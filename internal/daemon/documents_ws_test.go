package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
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

// A tripwire on a deadlock, not a wait for something slow: every assertion here
// is woken by a real write.
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

func deliveryOrder(t *testing.T, event map[string]any) []string {
	t.Helper()
	raw, _ := event["order"].([]any)
	out := make([]string, 0, len(raw))
	for _, id := range raw {
		out = append(out, fmt.Sprint(id))
	}
	return out
}

func upsertIDs(t *testing.T, event map[string]any) []string {
	t.Helper()
	raw, _ := event["upsert"].([]any)
	out := make([]string, 0, len(raw))
	for _, doc := range raw {
		entry, _ := doc.(map[string]any)
		out = append(out, fmt.Sprint(entry["id"]))
	}
	return out
}

func wsSubscribe(d *Daemon, s *wsSubscriber, id string, have []protocol.DocumentRevision) {
	d.handleDocSubscribeWS(s.client, &protocol.DocSubscribeMessage{
		Cmd:            protocol.CmdDocSubscribe,
		Query:          testQuery(),
		Have:           have,
		SubscriptionID: protocol.Ptr(id),
	})
}

func TestWebSocketSubscriptionDeliversAndStaysLive(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "already-here", `{"status":"pending"}`)

	sub := newWSSubscriber()
	wsSubscribe(d, sub, "tile-1", nil)

	first := sub.nextDelivery(t, "tile-1")
	if got := deliveryOrder(t, first); len(got) != 1 || got[0] != "already-here" {
		t.Fatalf("first delivery ordered %v", got)
	}
	if first["delivery"] != float64(1) {
		t.Fatalf("first delivery counted %v, want 1", first["delivery"])
	}

	putDoc(t, d, "b", `{"status":"pending"}`)
	second := sub.nextDelivery(t, "tile-1")
	if got := deliveryOrder(t, second); len(got) != 2 {
		t.Fatalf("second delivery ordered %v, want both documents", got)
	}
	if got := upsertIDs(t, second); len(got) != 1 || got[0] != "b" {
		t.Fatalf("second delivery carried bodies %v, want only b", got)
	}
}

func TestWebSocketSubscriptionsAreIndependentOnOneConnection(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	sub := newWSSubscriber()
	wsSubscribe(d, sub, "tile-1", nil)
	sub.nextDelivery(t, "tile-1")
	wsSubscribe(d, sub, "tile-2", nil)
	sub.nextDelivery(t, "tile-2")

	if got := d.documentSubscriptionCount(); got != 2 {
		t.Fatalf("daemon holds %d subscriptions, want 2", got)
	}

	putDoc(t, d, "a", `{"status":"pending"}`)
	seen := map[string]bool{}
	for range 2 {
		event := sub.nextEvent(t)
		seen[fmt.Sprint(event["subscription_id"])] = true
	}
	if !seen["tile-1"] || !seen["tile-2"] {
		t.Fatalf("the write woke %v, want both subscriptions", seen)
	}
}

func TestUnsubscribeEndsTheSubscription(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	sub := newWSSubscriber()
	wsSubscribe(d, sub, "tile-1", nil)
	sub.nextDelivery(t, "tile-1")

	d.handleDocUnsubscribeWS(sub.client, &protocol.DocUnsubscribeMessage{
		Cmd: protocol.CmdDocUnsubscribe, SubscriptionID: "tile-1",
	})
	waitForSubscriptionCount(t, d, 0)

	d.handleDocUnsubscribeWS(sub.client, &protocol.DocUnsubscribeMessage{
		Cmd: protocol.CmdDocUnsubscribe, SubscriptionID: "tile-1",
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

func TestTooManySubscriptionsIsARefusalThatNamesTheLimit(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	sub := newWSSubscriber()
	for i := range protocol.DocSubscriptionsPerClient {
		wsSubscribe(d, sub, fmt.Sprintf("tile-%d", i), nil)
		sub.nextDelivery(t, fmt.Sprintf("tile-%d", i))
	}

	wsSubscribe(d, sub, "one-too-many", nil)
	event := sub.nextEvent(t)
	if event["event"] != protocol.EventDocSubscriptionEnded {
		t.Fatalf("expected the subscription to be refused, got %v", event)
	}
	if event["code"] != protocol.ErrorCodeSubscriptionLimit {
		t.Fatalf("refusal code = %v, want %q", event["code"], protocol.ErrorCodeSubscriptionLimit)
	}
	message := fmt.Sprint(event["error"])
	if !strings.Contains(message, fmt.Sprint(protocol.DocSubscriptionsPerClient)) {
		t.Fatalf("refusal does not name the limit: %s", message)
	}
	if !strings.Contains(message, "one-too-many") {
		t.Fatalf("refusal does not name the ask: %s", message)
	}
	if got := sub.client.docSubscriptions.count(); got != protocol.DocSubscriptionsPerClient {
		t.Fatalf("the refused subscription changed the count to %d", got)
	}
}

func TestASecondSubscriptionUnderOneIDIsRefused(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	sub := newWSSubscriber()
	wsSubscribe(d, sub, "tile-1", nil)
	sub.nextDelivery(t, "tile-1")

	wsSubscribe(d, sub, "tile-1", nil)
	event := sub.nextEvent(t)
	if event["event"] != protocol.EventDocSubscriptionEnded ||
		event["code"] != protocol.ErrorCodeInvalidQuery {
		t.Fatalf("expected an invalid_query refusal, got %v", event)
	}
	if got := d.documentSubscriptionCount(); got != 1 {
		t.Fatalf("the refusal disturbed the live subscription: count = %d", got)
	}
}

func TestSubscribeRefusalsAndEndingsShareOneEnvelope(t *testing.T) {
	d := newDaemonForTest(t)

	sub := newWSSubscriber()
	wsSubscribe(d, sub, "tile-1", nil)
	refusal := sub.nextEvent(t)
	if refusal["event"] != protocol.EventDocSubscriptionEnded ||
		refusal["code"] != protocol.ErrorCodeUndeclaredCollection {
		t.Fatalf("expected an undeclared_collection refusal, got %v", refusal)
	}

	defineTestCollection(t, d)
	wsSubscribe(d, sub, "tile-2", nil)
	sub.nextDelivery(t, "tile-2")

	undefineTestCollection(t, d)
	ended := sub.nextEvent(t)
	if ended["event"] != protocol.EventDocSubscriptionEnded ||
		ended["code"] != protocol.ErrorCodeCollectionUndefined {
		t.Fatalf("expected a collection_undefined ending, got %v", ended)
	}
	waitForSubscriptionCount(t, d, 0)
}

func TestSubscribingWithACursorIsRefusedOnTheWebSocket(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	sub := newWSSubscriber()
	q := testQuery()
	q.After = protocol.Ptr("a")
	d.handleDocSubscribeWS(sub.client, &protocol.DocSubscribeMessage{
		Cmd: protocol.CmdDocSubscribe, Query: q, SubscriptionID: protocol.Ptr("tile-1"),
	})
	event := sub.nextEvent(t)
	if event["event"] != protocol.EventDocSubscriptionEnded ||
		event["code"] != protocol.ErrorCodeInvalidQuery {
		t.Fatalf("expected an invalid_query refusal, got %v", event)
	}
	if !strings.Contains(fmt.Sprint(event["error"]), "after cursor") {
		t.Fatalf("refusal does not say what was wrong: %v", event["error"])
	}
}

func TestWebSocketResumeSendsOnlyWhatChanged(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "kept", `{"status":"pending"}`)
	putDoc(t, d, "moved", `{"status":"pending"}`)

	sub := newWSSubscriber()
	wsSubscribe(d, sub, "tile-1", nil)
	first := sub.nextDelivery(t, "tile-1")
	held := map[string]int{}
	for _, doc := range first["upsert"].([]any) {
		entry := doc.(map[string]any)
		held[fmt.Sprint(entry["id"])] = int(entry["rev"].(float64))
	}
	d.handleDocUnsubscribeWS(sub.client, &protocol.DocUnsubscribeMessage{
		Cmd: protocol.CmdDocUnsubscribe, SubscriptionID: "tile-1",
	})
	waitForSubscriptionCount(t, d, 0)

	putDoc(t, d, "moved", `{"status":"approved"}`)

	have := []protocol.DocumentRevision{
		{ID: "kept", Rev: held["kept"]},
		{ID: "moved", Rev: held["moved"]},
	}
	wsSubscribe(d, sub, "tile-2", have)
	resumed := sub.nextDelivery(t, "tile-2")
	if got := len(deliveryOrder(t, resumed)); got != 2 {
		t.Fatalf("resumed window holds %d documents, want 2", got)
	}
	if got := upsertIDs(t, resumed); len(got) != 1 || got[0] != "moved" {
		t.Fatalf("resume carried bodies %v, want only the one that changed", got)
	}
}

func TestIPCSubscribeRefusesASubscriptionID(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	resp := docCall(t, func(c net.Conn) {
		d.handleDocSubscribe(c, &protocol.DocSubscribeMessage{
			Cmd: protocol.CmdDocSubscribe, Query: testQuery(), SubscriptionID: protocol.Ptr("tile-1"),
		})
	})
	if resp.Ok {
		t.Fatal("the unix socket accepted a subscription_id")
	}
	if got := protocol.Deref(resp.ErrorCode); got != protocol.ErrorCodeInvalidQuery {
		t.Fatalf("refusal code = %q, want %q", got, protocol.ErrorCodeInvalidQuery)
	}
}

func undefineTestCollection(t *testing.T, d *Daemon) {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleDocUndefine(c, &protocol.DocUndefineMessage{
			Cmd: protocol.CmdDocUndefine, Namespace: testDocNS, Collection: testDocColl,
		})
	})
	if !resp.Ok {
		t.Fatalf("undefine: %v", protocol.Deref(resp.Error))
	}
}

// The loop ends on its own goroutine, so the registry count moving is the only
// signal that it did.
func waitForSubscriptionCount(t *testing.T, d *Daemon, want int) {
	t.Helper()
	waitFor(t, fmt.Sprintf("the daemon to hold %d live subscriptions", want), func() bool {
		return d.documentSubscriptionCount() == want
	})
}
