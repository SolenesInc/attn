package daemon_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type docLiveView map[string]protocol.StoredDocument

func (v docLiveView) apply(t *testing.T, order []string, upsert []protocol.StoredDocument) []protocol.StoredDocument {
	t.Helper()
	arrived := map[string]protocol.StoredDocument{}
	for _, doc := range upsert {
		arrived[doc.ID] = doc
	}
	window := make([]protocol.StoredDocument, 0, len(order))
	for _, id := range order {
		doc, ok := arrived[id]
		if !ok {
			doc, ok = v[id]
		}
		if !ok {
			t.Fatalf("a delivery ordered %q without its body, and the subscriber was never sent one", id)
		}
		window = append(window, doc)
	}
	clear(v)
	for _, doc := range window {
		v[doc.ID] = doc
	}
	return window
}

func docRevisions(docs []protocol.StoredDocument) []protocol.DocumentRevision {
	have := make([]protocol.DocumentRevision, 0, len(docs))
	for _, doc := range docs {
		have = append(have, protocol.DocumentRevision{ID: doc.ID, Rev: doc.Rev})
	}
	return have
}

func docSubscribeHolding(app *testworld.Peer, q protocol.DocumentQuery, have []protocol.DocumentRevision) string {
	id := uuid.NewString()
	app.Send(protocol.DocSubscribeMessage{Cmd: protocol.CmdDocSubscribe, Query: q, Have: have, SubscriptionID: protocol.Ptr(id)})
	return id
}

func docNextDelivery(app *testworld.Peer, subscription string) protocol.DocSubscriptionDeliveryMessage {
	app.T.Helper()
	return awaitDelivery(app, subscription, func(protocol.DocSubscriptionDeliveryMessage) bool { return true })
}

func docSubscriptionEnded(app *testworld.Peer, subscription string) protocol.DocSubscriptionEndedMessage {
	app.T.Helper()
	return testworld.Await(app, protocol.EventDocSubscriptionEnded, func(m protocol.DocSubscriptionEndedMessage) bool {
		return m.SubscriptionID == subscription
	})
}

func docSettle(app *testworld.Peer, subscription string, seq int) protocol.DocSubscriptionDeliveryMessage {
	app.T.Helper()
	for {
		if delivered := docNextDelivery(app, subscription); delivered.AsOfSeq >= seq {
			return delivered
		}
	}
}

type docSocketSubscription struct {
	conn net.Conn
	dec  *json.Decoder
}

func docSocketSubscribe(t *testing.T, w *world, msg protocol.DocSubscribeMessage) *docSocketSubscription {
	t.Helper()
	conn, err := w.DialUnix()
	if err != nil {
		t.Fatalf("dial the daemon's socket: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	msg.Cmd = protocol.CmdDocSubscribe
	if err := json.NewEncoder(conn).Encode(msg); err != nil {
		t.Fatalf("send doc_subscribe: %v", err)
	}
	return &docSocketSubscription{conn: conn, dec: json.NewDecoder(conn)}
}

func (s *docSocketSubscription) next(t *testing.T) protocol.Response {
	t.Helper()
	_ = s.conn.SetReadDeadline(time.Now().Add(fakeagent.HangGuard))
	var resp protocol.Response
	if err := s.dec.Decode(&resp); err != nil {
		t.Fatalf("read the subscription: %v", err)
	}
	return resp
}

func (s *docSocketSubscription) window(t *testing.T) *protocol.DocSubscribeResult {
	t.Helper()
	resp := s.next(t)
	if !resp.Ok || resp.DocSubscribeResult == nil {
		t.Fatalf("the subscription ended: %s (%s)", protocol.Deref(resp.Error), protocol.Deref(resp.ErrorCode))
	}
	return resp.DocSubscribeResult
}

func TestALiveQueryDeliversItsWindowThenOnlyWhatChangedInIt(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.App()
	defineRequests(t, cli, gateNS)
	defineRequests(t, cli, "app/other")
	put(t, cli, gateNS, "already-here", `{"status":"pending"}`)

	first := subscribeOverTheWire(app, requestsWhere(gateNS))
	if opened := docNextDelivery(app, first); opened.Delivery != 1 || docIDs(opened.Upsert) != "already-here" || strings.Join(opened.Order, " ") != "already-here" {
		t.Fatalf("the first delivery = #%d ordering %v with bodies %q, want #1 with the document already there", opened.Delivery, opened.Order, docIDs(opened.Upsert))
	}

	if missing, err := cli.DocDelete(gateNS, requests, "never-existed", nil); err != nil || missing.Existed {
		t.Fatalf("deleting a missing document = %+v, %v; want it reported absent", missing, err)
	}
	if _, err := cli.DocPut(gateNS, requests, "already-here", `{"status":"rejected"}`, protocol.Ptr(99)); client.ErrorCode(err) != protocol.ErrorCodeConflict {
		t.Fatalf("a write against a revision that never existed = %v, want a conflict", err)
	}
	put(t, cli, "app/other", "theirs", `{"status":"pending"}`)
	written := put(t, cli, gateNS, "b", `{"status":"approved"}`)

	woken := docNextDelivery(app, first)
	if woken.Delivery != 2 || strings.Join(woken.Order, " ") != "already-here b" || docIDs(woken.Upsert) != "b" || woken.AsOfSeq < written.Seq {
		t.Fatalf("after a no-op delete, a refused write, another namespace's write and one real write, the next delivery = #%d at seq %d ordering %v with bodies %q; want #2 at or after seq %d with only b's body",
			woken.Delivery, woken.AsOfSeq, woken.Order, docIDs(woken.Upsert), written.Seq)
	}
	if body := woken.Upsert[0].Body; body != `{"status":"approved"}` {
		t.Fatalf("b arrived as %s", body)
	}

	second := subscribeOverTheWire(app, requestsWhere(gateNS))
	if joined := docNextDelivery(app, second); joined.Delivery != 1 || docIDs(joined.Upsert) != "already-here b" {
		t.Fatalf("a second subscription on the same connection opened with #%d carrying %q, want every body: it holds nothing yet", joined.Delivery, docIDs(joined.Upsert))
	}

	removed, err := cli.DocDelete(gateNS, requests, "already-here", nil)
	if err != nil || !removed.Existed {
		t.Fatalf("delete = %+v, %v", removed, err)
	}
	for subscription, want := range map[string]int{first: 3, second: 2} {
		if emptied := docNextDelivery(app, subscription); emptied.Delivery != want || strings.Join(emptied.Order, " ") != "b" || len(emptied.Upsert) != 0 {
			t.Errorf("after the removal a subscription got #%d ordering %v with bodies %q; want #%d holding only b and no bodies", emptied.Delivery, emptied.Order, docIDs(emptied.Upsert), want)
		}
	}
}

func TestALimitedWindowLetsTheNextDocumentInWhenOneLeaves(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.App()
	defineRequests(t, cli, gateNS)
	for i, id := range []string{"a", "b", "c"} {
		put(t, cli, gateNS, id, fmt.Sprintf(`{"status":"%d"}`, i+1))
	}
	ranked := requestsWhere(gateNS)
	ranked.Sort = &protocol.DocumentSort{Field: "status"}
	ranked.Limit = protocol.Ptr(2)
	view := docLiveView{}

	subscription := subscribeOverTheWire(app, ranked)
	opened := docNextDelivery(app, subscription)
	view.apply(t, opened.Order, opened.Upsert)
	if got := strings.Join(opened.Order, " "); got != "a b" {
		t.Fatalf("the window opened on %q, want a b", got)
	}

	edited := put(t, cli, gateNS, "b", `{"status":"2 edited"}`)
	moved := awaitDelivery(app, subscription, reached(edited.Seq))
	bodies := view.apply(t, moved.Order, moved.Upsert)
	if got := strings.Join(moved.Order, " "); got != "a b" || docIDs(moved.Upsert) != "b" {
		t.Fatalf("after editing b the window is %q with bodies %q, want a b with only b's body", got, docIDs(moved.Upsert))
	}
	if bodies[0].Body != `{"status":"1"}` || bodies[1].Body != `{"status":"2 edited"}` {
		t.Fatalf("the applied window holds %s and %s", bodies[0].Body, bodies[1].Body)
	}

	removed, err := cli.DocDelete(gateNS, requests, "a", nil)
	if err != nil {
		t.Fatal(err)
	}
	entered := awaitDelivery(app, subscription, reached(removed.Seq))
	view.apply(t, entered.Order, entered.Upsert)
	if got := strings.Join(entered.Order, " "); got != "b c" || docIDs(entered.Upsert) != "c" {
		t.Fatalf("after a left the window is %q with bodies %q, want b c with only c's body", got, docIDs(entered.Upsert))
	}
}

func TestResumingALiveQuerySendsOnlyWhatChangedWhileAway(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.App()
	putIn := func(collection, id, body string) int {
		t.Helper()
		written, err := cli.DocPut(gateNS, collection, id, body, nil)
		if err != nil {
			t.Fatal(err)
		}
		return written.Seq
	}
	deleteIn := func(collection string, ids ...string) int {
		t.Helper()
		seq := 0
		for _, id := range ids {
			removed, err := cli.DocDelete(gateNS, collection, id, nil)
			if err != nil {
				t.Fatal(err)
			}
			seq = removed.Seq
		}
		return seq
	}

	for i, c := range []struct {
		name    string
		away    func(collection string, held []protocol.DocumentRevision) ([]protocol.DocumentRevision, int)
		order   string
		changed string
	}{
		{"edited inside the window", func(coll string, held []protocol.DocumentRevision) ([]protocol.DocumentRevision, int) {
			return held, putIn(coll, "a", `{"status":"aa"}`)
		}, "a b", "a"},
		{"deleted", func(coll string, held []protocol.DocumentRevision) ([]protocol.DocumentRevision, int) {
			return held, deleteIn(coll, "a")
		}, "b c", "c"},
		{"displaced past the limit", func(coll string, held []protocol.DocumentRevision) ([]protocol.DocumentRevision, int) {
			return held, putIn(coll, "z", `{"status":"a0"}`)
		}, "z a", "z"},
		{"sorted out of the window", func(coll string, held []protocol.DocumentRevision) ([]protocol.DocumentRevision, int) {
			return held, putIn(coll, "b", `{"status":"zz"}`)
		}, "a c", "c"},
		{"nothing changed", func(coll string, held []protocol.DocumentRevision) ([]protocol.DocumentRevision, int) {
			return held, 0
		}, "a b", ""},
		{"held at a revision the store never issued", func(coll string, held []protocol.DocumentRevision) ([]protocol.DocumentRevision, int) {
			return []protocol.DocumentRevision{{ID: "a", Rev: 4242}, held[1]}, 0
		}, "a b", "a"},
		{"everything held is gone", func(coll string, held []protocol.DocumentRevision) ([]protocol.DocumentRevision, int) {
			return held, deleteIn(coll, "a", "b", "c")
		}, "", ""},
	} {
		collection := fmt.Sprintf("resume-%d", i)
		defineCollection(t, cli, gateNS, collection, protocol.DocumentFieldSpec{Name: "status", Type: "string"})
		for _, id := range []string{"a", "b", "c"} {
			putIn(collection, id, fmt.Sprintf(`{"status":"%s1"}`, id))
		}
		window := protocol.DocumentQuery{Namespace: gateNS, Collection: collection, Sort: &protocol.DocumentSort{Field: "status"}, Limit: protocol.Ptr(2)}
		view := docLiveView{}

		first := subscribeOverTheWire(app, window)
		opened := docNextDelivery(app, first)
		held := view.apply(t, opened.Order, opened.Upsert)
		app.Send(protocol.DocUnsubscribeMessage{Cmd: protocol.CmdDocUnsubscribe, SubscriptionID: first})

		have, seq := c.away(collection, docRevisions(held))
		resumed := docNextDelivery(app, docSubscribeHolding(app, window, have))
		applied := view.apply(t, resumed.Order, resumed.Upsert)
		if got := strings.Join(resumed.Order, " "); got != c.order || docIDs(resumed.Upsert) != c.changed || resumed.AsOfSeq < seq {
			t.Fatalf("%s: resumed at seq %d on %q with bodies %q; want %q with bodies %q at or after seq %d",
				c.name, resumed.AsOfSeq, got, docIDs(resumed.Upsert), c.order, c.changed, seq)
		}
		fresh := query(t, cli, window)
		if docIDs(fresh) != c.order {
			t.Fatalf("%s: a fresh query answers %q, the resumed window %q", c.name, docIDs(fresh), c.order)
		}
		for j, doc := range applied {
			if doc.Body != fresh[j].Body {
				t.Errorf("%s: the resumed window holds %s as %s, the store as %s", c.name, doc.ID, doc.Body, fresh[j].Body)
			}
		}
	}
}

func TestSubscriptionRefusalsAndEndingsShareOneEnvelope(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.App()

	undeclared := subscribeOverTheWire(app, requestsWhere(gateNS))
	if refused := docSubscriptionEnded(app, undeclared); refused.Code != protocol.ErrorCodeUndeclaredCollection {
		t.Fatalf("subscribing to an undeclared collection ended with %q: %s", refused.Code, refused.Error)
	}

	defineRequests(t, cli, gateNS)
	live := subscribeOverTheWire(app, requestsWhere(gateNS))
	docNextDelivery(app, live)

	app.Send(protocol.DocSubscribeMessage{Cmd: protocol.CmdDocSubscribe, Query: requestsWhere(gateNS), SubscriptionID: protocol.Ptr(live)})
	if duplicate := docSubscriptionEnded(app, live); duplicate.Code != protocol.ErrorCodeInvalidQuery {
		t.Fatalf("a second subscription under a live id ended with %q: %s", duplicate.Code, duplicate.Error)
	}
	written := put(t, cli, gateNS, "a", `{"status":"pending"}`)
	if still := awaitDelivery(app, live, reached(written.Seq)); still.Delivery != 2 {
		t.Fatalf("after the duplicate was refused the live subscription delivered #%d, want #2", still.Delivery)
	}

	walking := requestsWhere(gateNS)
	walking.After = protocol.Ptr("a")
	cursor := subscribeOverTheWire(app, walking)
	if refused := docSubscriptionEnded(app, cursor); refused.Code != protocol.ErrorCodeInvalidQuery || !strings.Contains(refused.Error, "after cursor") {
		t.Fatalf("subscribing with an after cursor ended with %q: %s; want invalid_query naming the cursor", refused.Code, refused.Error)
	}

	held := []string{live}
	for len(held) < protocol.DocSubscriptionsPerClient {
		id := subscribeOverTheWire(app, requestsWhere(gateNS))
		docNextDelivery(app, id)
		held = append(held, id)
	}
	app.Send(protocol.DocSubscribeMessage{Cmd: protocol.CmdDocSubscribe, Query: requestsWhere(gateNS), SubscriptionID: protocol.Ptr("one-too-many")})
	overflow := docSubscriptionEnded(app, "one-too-many")
	if overflow.Code != protocol.ErrorCodeSubscriptionLimit || !strings.Contains(overflow.Error, fmt.Sprint(protocol.DocSubscriptionsPerClient)) || !strings.Contains(overflow.Error, "one-too-many") {
		t.Fatalf("the subscription past the limit ended with %q: %s; want subscription_limit naming the limit and the ask", overflow.Code, overflow.Error)
	}

	freed := held[1]
	app.Send(protocol.DocUnsubscribeMessage{Cmd: protocol.CmdDocUnsubscribe, SubscriptionID: freed})
	app.Send(protocol.DocUnsubscribeMessage{Cmd: protocol.CmdDocUnsubscribe, SubscriptionID: freed})
	app.Send(protocol.DocSubscribeMessage{Cmd: protocol.CmdDocSubscribe, Query: requestsWhere(gateNS), SubscriptionID: protocol.Ptr(freed)})
	if reopened := docNextDelivery(app, freed); reopened.Delivery != 1 {
		t.Fatalf("reusing an unsubscribed id opened with delivery #%d, want a fresh #1 under the limit", reopened.Delivery)
	}

	socket := docSocketSubscribe(t, w, protocol.DocSubscribeMessage{Query: requestsWhere(gateNS), SubscriptionID: protocol.Ptr("tile-1")})
	if refused := socket.next(t); refused.Ok || protocol.Deref(refused.ErrorCode) != protocol.ErrorCodeInvalidQuery {
		t.Fatalf("the unix socket answered a subscription_id with %+v, want invalid_query", refused)
	}

	if _, err := cli.DocUndefine(gateNS, requests); err != nil {
		t.Fatal(err)
	}
	if ended := docSubscriptionEnded(app, live); ended.Code != protocol.ErrorCodeCollectionUndefined {
		t.Fatalf("undefining the collection ended the live subscription with %q: %s", ended.Code, ended.Error)
	}
}

func TestAfterCursorsPageQueriesButNotLiveQueries(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	defineRequests(t, cli, gateNS)
	for _, id := range []string{"a", "b", "c"} {
		put(t, cli, gateNS, id, `{"status":"pending"}`)
	}

	page := requestsWhere(gateNS)
	page.Sort = &protocol.DocumentSort{Field: "status"}
	page.Limit = protocol.Ptr(1)
	var walked []string
	for range 3 {
		docs := query(t, cli, page)
		if len(docs) != 1 {
			t.Fatalf("the page after %q holds %q, want one document", protocol.Deref(page.After), docIDs(docs))
		}
		walked = append(walked, docs[0].ID)
		page.After = protocol.Ptr(docs[0].ID)
	}
	if got := strings.Join(walked, " "); got != "a b c" {
		t.Fatalf("paging by the after cursor walked %q, want a b c", got)
	}

	walking := requestsWhere(gateNS)
	walking.After = protocol.Ptr("a")
	refused := docSocketSubscribe(t, w, protocol.DocSubscribeMessage{Query: walking}).next(t)
	if refused.Ok || protocol.Deref(refused.ErrorCode) != protocol.ErrorCodeInvalidQuery ||
		!strings.Contains(protocol.Deref(refused.Error), "limit") || !strings.Contains(protocol.Deref(refused.Error), "window") {
		t.Fatalf("a live query with an after cursor answered %+v; want invalid_query pointing at a limit and its window", refused)
	}
}

func TestABurstOfWritesReachesASlowReaderInAtMostTwoDeliveries(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		defineRequests(t, cli, gateNS)
		subscription := docSocketSubscribe(t, w, protocol.DocSubscribeMessage{Query: requestsWhere(gateNS)})
		subscription.window(t)

		const writes = 40
		var want []string
		last := 0
		for i := range writes {
			id := fmt.Sprintf("d%02d", i)
			last = put(t, cli, gateNS, id, `{"status":"pending"}`).Seq
			want = append(want, id)
		}

		deliveries := 0
		for {
			window := subscription.window(t)
			deliveries++
			if window.AsOfSeq < last {
				continue
			}
			if deliveries > 2 || !slices.Equal(window.Order, want) {
				t.Fatalf("%d writes reached the reader in %d deliveries ending on %v; want at most 2, the last holding every document", writes, deliveries, window.Order)
			}
			return
		}
	})
}

func TestAWriterNeverWaitsOnASubscriberThatStoppedReading(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		defineRequests(t, cli, gateNS)
		stalled := docSocketSubscribe(t, w, protocol.DocSubscribeMessage{Query: requestsWhere(gateNS)})
		stalled.window(t)

		const writes = 16
		bulky := fmt.Sprintf(`{"status":"pending","note":%q}`, strings.Repeat("x", 48<<10))
		for i := range writes {
			put(t, cli, gateNS, fmt.Sprintf("d%02d", i), bulky)
		}
		if counted, err := cli.DocCount(requestsWhere(gateNS)); err != nil || counted.Count != writes {
			t.Fatalf("count = %+v, %v; want all %d writes landed", counted, err, writes)
		}
	})
}

func TestALiveQueryNeverShowsATornBodyUnderContention(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.App()
	defineRequests(t, cli, gateNS)
	bodies := []string{`{"status":"pending"}`, `{"status":"approved"}`}
	put(t, cli, gateNS, "a", bodies[0])
	subscription := subscribeOverTheWire(app, requestsWhere(gateNS))
	view := docLiveView{}
	opened := docNextDelivery(app, subscription)
	view.apply(t, opened.Order, opened.Upsert)

	sentinel := make(chan int, 1)
	go func() {
		writer := w.Client()
		for i := range 40 {
			if _, err := writer.DocPut(gateNS, requests, "a", bodies[i%2], nil); err != nil {
				t.Errorf("flip %d: %v", i, err)
			}
		}
		written, err := writer.DocPut(gateNS, requests, "sentinel", `{"status":"pending"}`, nil)
		if err != nil {
			t.Errorf("sentinel: %v", err)
			sentinel <- 0
			return
		}
		sentinel <- written.Seq
	}()

	for {
		delivered := docNextDelivery(app, subscription)
		window := view.apply(t, delivered.Order, delivered.Upsert)
		for _, doc := range window {
			if doc.ID == "a" && !slices.Contains(bodies, doc.Body) {
				t.Fatalf("delivery #%d showed a as %s, which it never was", delivered.Delivery, doc.Body)
			}
		}
		if !slices.Contains(delivered.Order, "a") {
			t.Fatalf("delivery #%d lost the flipped document: %v", delivered.Delivery, delivered.Order)
		}
		if len(delivered.Order) < 2 {
			continue
		}
		if seq := <-sentinel; delivered.AsOfSeq < seq {
			t.Fatalf("the delivery carrying the sentinel stands at seq %d, below its write at %d", delivered.AsOfSeq, seq)
		}
		fresh := query(t, cli, requestsWhere(gateNS))
		for i, doc := range window {
			if doc.ID != fresh[i].ID || doc.Body != fresh[i].Body {
				t.Fatalf("the settled window holds %s=%s where a fresh query holds %s=%s", doc.ID, doc.Body, fresh[i].ID, fresh[i].Body)
			}
		}
		return
	}
}

func TestALimitedLiveWindowAlwaysMatchesAFreshQuery(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.App()
	defineCollection(t, cli, gateNS, requests, protocol.DocumentFieldSpec{Name: "n", Type: "number"})
	bodies := []string{`{"n":1}`, `{"n":1}`, `{"n":2}`, `{"n":"1"}`, `{"n":"apple"}`, `{"n":null}`, `{}`, `{"n":[1,2]}`, `{"n":{"deep":1}}`}
	const limit = 3
	window := requestsWhere(gateNS)
	window.Sort = &protocol.DocumentSort{Field: "n", Desc: protocol.Ptr(true)}
	window.Limit = protocol.Ptr(limit)
	subscription := subscribeOverTheWire(app, window)
	docNextDelivery(app, subscription)

	corpus := rand.New(rand.NewPCG(20260805, 0))
	for round := range 80 {
		id := fmt.Sprintf("d%d", corpus.IntN(7))
		seq := 0
		if corpus.IntN(4) == 0 {
			removed, err := cli.DocDelete(gateNS, requests, id, nil)
			if err != nil {
				t.Fatal(err)
			}
			seq = removed.Seq
		} else {
			seq = put(t, cli, gateNS, id, bodies[corpus.IntN(len(bodies))]).Seq
		}
		if seq == 0 {
			continue
		}
		settled := docSettle(app, subscription, seq)
		if fresh := query(t, cli, window); strings.Join(settled.Order, " ") != docIDs(fresh) || len(settled.Order) > limit {
			t.Fatalf("round %d: the live window is %v, a fresh query %q, the limit %d", round, settled.Order, docIDs(fresh), limit)
		}
	}
}

func TestDocumentsOverTheCLIClientDeliverResumeAndEnd(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	defineRequests(t, cli, gateNS)
	put(t, cli, gateNS, "a", `{"status":"pending"}`)
	put(t, cli, gateNS, "b", `{"status":"pending"}`)
	q := requestsWhere(gateNS)
	if read, err := cli.DocQuery(q); err != nil || len(read.Documents) != 2 || read.AsOfSeq <= 0 {
		t.Fatalf("query = %+v, %v; want both documents at a position on the log", read, err)
	}

	var windows []client.DocWindow
	liveSeq := 0
	err := cli.DocSubscribe(q, nil, func(window client.DocWindow) bool {
		windows = append(windows, window)
		if len(windows) == 1 {
			liveSeq = put(t, cli, gateNS, "c", `{"status":"pending"}`).Seq
		}
		return len(windows) < 2
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if got := strings.Join(windows[0].Changed, " "); got != "a b" {
		t.Fatalf("the first window sent bodies for %q, want every document", got)
	}
	live := windows[1]
	if len(live.Documents) != 3 || strings.Join(live.Changed, " ") != "c" || live.AsOfSeq < int64(liveSeq) {
		t.Fatalf("the live window holds %q at seq %d, sent %q; want three documents at or after seq %d with only c sent",
			docIDs(live.Documents), live.AsOfSeq, live.Changed, liveSeq)
	}

	if _, err := cli.DocDelete(gateNS, requests, "a", nil); err != nil {
		t.Fatal(err)
	}
	put(t, cli, gateNS, "b", `{"status":"approved"}`)
	var resumed client.DocWindow
	if err := cli.DocSubscribe(q, live.Documents, func(window client.DocWindow) bool {
		resumed = window
		return false
	}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if docIDs(resumed.Documents) != "b c" || strings.Join(resumed.Changed, " ") != "b" ||
		resumed.Documents[0].Body != `{"status":"approved"}` || resumed.Documents[1].Body != `{"status":"pending"}` {
		t.Fatalf("resuming holds %+v having been sent %q; want b (edited) and c with only b sent", resumed.Documents, resumed.Changed)
	}

	opened := make(chan struct{})
	ended := make(chan error, 1)
	go func() {
		first := true
		ended <- w.Client().DocSubscribe(q, nil, func(client.DocWindow) bool {
			if first {
				first = false
				close(opened)
			}
			return true
		})
	}()
	select {
	case <-opened:
	case err := <-ended:
		t.Fatalf("the subscription ended before its first window: %v", err)
	}
	if _, err := cli.DocUndefine(gateNS, requests); err != nil {
		t.Fatal(err)
	}
	if code, isEnd := client.DocSubscriptionCode(<-ended); !isEnd || code != protocol.ErrorCodeCollectionUndefined {
		t.Fatalf("undefining the collection ended the subscription with code %q (a subscription ending: %v), want %q", code, isEnd, protocol.ErrorCodeCollectionUndefined)
	}
}
