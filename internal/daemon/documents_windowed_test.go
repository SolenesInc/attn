package daemon

import (
	"fmt"
	"math/rand"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func putDocSeq(t *testing.T, d *Daemon, id, body string) int64 {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleDocPut(c, &protocol.DocPutMessage{
			Cmd: protocol.CmdDocPut, Namespace: testDocNS, Collection: testDocColl, ID: id, Body: body,
		})
	})
	if !resp.Ok {
		t.Fatalf("put %s: %v", id, protocol.Deref(resp.Error))
	}
	return int64(resp.DocPutResult.Seq)
}

func deleteDocSeq(t *testing.T, d *Daemon, id string) int64 {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleDocDelete(c, &protocol.DocDeleteMessage{
			Cmd: protocol.CmdDocDelete, Namespace: testDocNS, Collection: testDocColl, ID: id,
		})
	})
	if !resp.Ok {
		t.Fatalf("delete %s: %v", id, protocol.Deref(resp.Error))
	}
	return int64(resp.DocDeleteResult.Seq)
}

func queryIDs(t *testing.T, d *Daemon, q protocol.DocumentQuery) []string {
	t.Helper()
	resp := docCall(t, func(c net.Conn) {
		d.handleDocQuery(c, &protocol.DocQueryMessage{Cmd: protocol.CmdDocQuery, Query: q})
	})
	if !resp.Ok {
		t.Fatalf("query: %v", protocol.Deref(resp.Error))
	}
	out := make([]string, 0, len(resp.DocQueryResult.Documents))
	for _, doc := range resp.DocQueryResult.Documents {
		out = append(out, doc.ID)
	}
	return out
}

func (lq *liveQuery) settle(t *testing.T, seq int64) window {
	t.Helper()
	for {
		w := lq.next(t)
		if w.asOfSeq >= seq {
			return w
		}
	}
}

func TestSubscribingWithAnAfterCursorIsRefused(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)

	after := "a"
	q := testQuery()
	q.After = &after

	lq := subscribe(t, d, q)
	resp := lq.nextRaw(t)
	if resp.Ok {
		t.Fatal("subscribing with an after cursor was accepted")
	}
	if code := protocol.Deref(resp.ErrorCode); code != protocol.ErrorCodeInvalidQuery {
		t.Fatalf("error code = %q, want %q", code, protocol.ErrorCodeInvalidQuery)
	}
	msg := protocol.Deref(resp.Error)
	for _, want := range []string{"limit", "window"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal %q does not name the alternative (%q)", msg, want)
		}
	}
	lq.stop()
	if n := d.documentSubscriptionCount(); n != 0 {
		t.Fatalf("a refused subscribe left %d subscriptions behind", n)
	}
}

func TestOneShotQueryStillTakesAnAfterCursor(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)
	putDoc(t, d, "b", `{"status":"pending"}`)

	after := "a"
	q := testQuery()
	q.After = &after
	if got := queryIDs(t, d, q); !equalStrings(got, []string{"b"}) {
		t.Fatalf("page after a = %v, want [b]", got)
	}
}

func TestADeliveryCarriesOnlyTheBodiesThatChanged(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	for _, id := range []string{"a", "b", "c"} {
		putDoc(t, d, id, `{"status":"pending"}`)
	}

	lq := subscribe(t, d, testQuery())
	first := lq.next(t)
	if got := first.changed(); len(got) != 3 {
		t.Fatalf("the first delivery sent %d bodies, want all 3 — a fresh subscriber holds nothing", len(got))
	}

	seq := putDocSeq(t, d, "b", `{"status":"approved"}`)
	next := lq.settle(t, seq)
	if got := ids(next); !equalStrings(got, []string{"a", "b", "c"}) {
		t.Fatalf("order = %v, want every document still named", got)
	}
	if got := next.changed(); !equalStrings(got, []string{"b"}) {
		t.Fatalf("bodies sent = %v, want only the one that changed", got)
	}
	for _, doc := range next.documents {
		want := `{"status":"pending"}`
		if doc.ID == "b" {
			want = `{"status":"approved"}`
		}
		if doc.Body != want {
			t.Fatalf("%s applied as %s, want %s", doc.ID, doc.Body, want)
		}
	}
}

func TestResumingSendsExactlyWhatChangedWhileAway(t *testing.T) {
	limit := 2
	sorted := func() protocol.DocumentQuery {
		q := testQuery()
		q.Sort = &protocol.DocumentSort{Field: "status"}
		q.Limit = &limit
		return q
	}

	cases := []struct {
		name    string
		mutate  func(t *testing.T, d *Daemon) int64
		order   []string
		changed []string
	}{
		{
			name:    "edited inside the window",
			mutate:  func(t *testing.T, d *Daemon) int64 { return putDocSeq(t, d, "a", `{"status":"aa"}`) },
			order:   []string{"a", "b"},
			changed: []string{"a"},
		},
		{
			name:    "deleted",
			mutate:  func(t *testing.T, d *Daemon) int64 { return deleteDocSeq(t, d, "a") },
			order:   []string{"b", "c"},
			changed: []string{"c"},
		},
		{
			name:    "displaced past the limit",
			mutate:  func(t *testing.T, d *Daemon) int64 { return putDocSeq(t, d, "z", `{"status":"a0"}`) },
			order:   []string{"z", "a"},
			changed: []string{"z"},
		},
		{
			name:    "edited out of the filter",
			mutate:  func(t *testing.T, d *Daemon) int64 { return putDocSeq(t, d, "b", `{"status":"zz"}`) },
			order:   []string{"a", "c"},
			changed: []string{"c"},
		},
		{
			name:    "nothing changed at all",
			mutate:  func(t *testing.T, d *Daemon) int64 { return 0 },
			order:   []string{"a", "b"},
			changed: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newDaemonForTest(t)
			defineTestCollection(t, d)
			putDoc(t, d, "a", `{"status":"a1"}`)
			putDoc(t, d, "b", `{"status":"b1"}`)
			putDoc(t, d, "c", `{"status":"c1"}`)

			lq := subscribe(t, d, sorted())
			held := lq.next(t).documents
			if got := idsOf(held); !equalStrings(got, []string{"a", "b"}) {
				t.Fatalf("initial window = %v, want [a b]", got)
			}
			lq.stop()

			seq := tc.mutate(t, d)

			resumed := subscribeResuming(t, d, sorted(), held)
			first := resumed.next(t)
			if first.asOfSeq < seq {
				t.Fatalf("resumed at seq %d, below the write at %d", first.asOfSeq, seq)
			}
			if got := ids(first); !equalStrings(got, tc.order) {
				t.Fatalf("resumed window = %v, want %v", got, tc.order)
			}
			if got := first.changed(); !equalStrings(got, tc.changed) {
				t.Fatalf("bodies sent on resume = %v, want %v", got, tc.changed)
			}
			if got, want := ids(first), queryIDs(t, d, sorted()); !equalStrings(got, want) {
				t.Fatalf("resumed window = %v, but a fresh query says %v", got, want)
			}
		})
	}
}

func TestResumingWithARevisionTheStoreNeverIssuedStillConverges(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)

	stale := []protocol.StoredDocument{{ID: "a", Body: `{"status":"whatever this was"}`, Rev: 4242}}
	lq := subscribeResuming(t, d, testQuery(), stale)
	first := lq.next(t)
	if got := first.changed(); !equalStrings(got, []string{"a"}) {
		t.Fatalf("bodies sent = %v, want the document whose claimed revision was never issued", got)
	}
	if len(first.documents) != 1 || first.documents[0].Body != `{"status":"pending"}` {
		t.Fatalf("applied window = %+v, want the stored body", first.documents)
	}
}

func TestResumingForgetsWhatIsNoLongerInTheWindow(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)

	lq := subscribe(t, d, testQuery())
	held := lq.next(t).documents
	lq.stop()

	deleteDocSeq(t, d, "a")

	resumed := subscribeResuming(t, d, testQuery(), held)
	first := resumed.next(t)
	if len(first.order) != 0 {
		t.Fatalf("resumed window = %v, want empty", first.order)
	}
	if len(first.upsert) != 0 {
		t.Fatalf("a body travelled for a document that is not in the window: %v", first.changed())
	}
}

func TestASubscriptionStaysCurrentUnderContention(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)

	lq := subscribe(t, d, testQuery())
	lq.next(t)

	const flips = 40
	bodies := []string{`{"status":"pending"}`, `{"status":"approved"}`}

	var sentinelSeq int64
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for i := range flips {
			putDocSeq(t, d, "a", bodies[i%2])
		}
		sentinelSeq = putDocSeq(t, d, "sentinel", `{"status":"pending"}`)
	}()

	var final window
	for {
		w := lq.next(t)
		holds := false
		for _, doc := range w.documents {
			if doc.ID != "a" {
				continue
			}
			holds = true
			if doc.Body != bodies[0] && doc.Body != bodies[1] {
				t.Fatalf("window showed %s, which the collection was never in", doc.Body)
			}
		}
		if !holds {
			t.Fatalf("window = %v, and the flipped document is not in it", ids(w))
		}
		if len(w.order) == 2 {
			final = w
			break
		}
	}
	writer.Wait()

	if final.asOfSeq < sentinelSeq {
		t.Fatalf("the delivery carrying the last write reports seq %d, below the write's own %d",
			final.asOfSeq, sentinelSeq)
	}
	if got, want := ids(final), queryIDs(t, d, testQuery()); !equalStrings(got, want) {
		t.Fatalf("final window = %v, fresh query = %v", got, want)
	}
}

// net.Pipe gives no buffer, so the daemon blocks in Write until this test reads.
func TestABurstOfWritesCollapsesIntoAFewDeliveries(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)

	lq := subscribe(t, d, testQuery())
	lq.next(t)

	const writes = 40
	var last int64
	want := make([]string, 0, writes)
	for i := range writes {
		id := fmt.Sprintf("d%02d", i)
		last = putDocSeq(t, d, id, `{"status":"pending"}`)
		want = append(want, id)
	}

	deliveries := 0
	var final window
	for {
		final = lq.next(t)
		deliveries++
		if final.asOfSeq >= last {
			break
		}
		if deliveries > 4 {
			t.Fatalf("%d writes produced %d deliveries before catching up; the wake slot is not collapsing them",
				writes, deliveries)
		}
	}
	if deliveries > 2 {
		t.Fatalf("%d writes produced %d deliveries, want at most 2 — one in flight plus one current",
			writes, deliveries)
	}
	if got := ids(final); !equalStrings(got, want) {
		t.Fatalf("final window = %v, want every written document", got)
	}
}

func TestEverySubscriberGetsItsOwnWindow(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"pending"}`)

	first := subscribe(t, d, testQuery())
	if got := first.next(t).changed(); !equalStrings(got, []string{"a"}) {
		t.Fatalf("first subscriber's initial bodies = %v", got)
	}

	second := subscribe(t, d, testQuery())
	if got := second.next(t).changed(); !equalStrings(got, []string{"a"}) {
		t.Fatalf("second subscriber's initial bodies = %v — a fresh subscriber holds nothing", got)
	}

	seq := putDocSeq(t, d, "b", `{"status":"pending"}`)
	for name, lq := range map[string]*liveQuery{"first": first, "second": second} {
		w := lq.settle(t, seq)
		if got := ids(w); !equalStrings(got, []string{"a", "b"}) {
			t.Fatalf("%s subscriber's window = %v, want [a b]", name, got)
		}
		if got := w.changed(); !equalStrings(got, []string{"b"}) {
			t.Fatalf("%s subscriber received %v, want only the new document", name, got)
		}
	}
}

var windowFuzzBodies = []string{
	`{"n":1}`,
	`{"n":1}`,
	`{"n":2}`,
	`{"n":"1"}`,
	`{"n":"apple"}`,
	`{"n":null}`,
	`{}`,
	`{"n":[1,2]}`,
	`{"n":{"deep":1}}`,
}

func TestTheWindowInvariantHoldsOverAFuzzedCorpus(t *testing.T) {
	const (
		documents = 7
		limit     = 3
		rounds    = 120
	)

	d := newDaemonForTest(t)
	resp := docCall(t, func(c net.Conn) {
		d.handleDocDefine(c, &protocol.DocDefineMessage{
			Cmd: protocol.CmdDocDefine,
			Schema: protocol.DocumentCollectionSchema{
				Namespace: testDocNS, Collection: testDocColl,
				Fields: []protocol.DocumentFieldSpec{{Name: "n", Type: "number"}},
			},
		})
	})
	if !resp.Ok {
		t.Fatalf("define: %v", protocol.Deref(resp.Error))
	}

	ids := make([]string, documents)
	for i := range ids {
		ids[i] = fmt.Sprintf("d%d", i)
	}

	cap := limit
	q := testQuery()
	q.Sort = &protocol.DocumentSort{Field: "n", Desc: protocol.Ptr(true)}
	q.Limit = &cap

	lq := subscribe(t, d, q)
	lq.next(t)

	rng := rand.New(rand.NewSource(20260805))
	for round := range rounds {
		id := ids[rng.Intn(len(ids))]
		var seq int64
		if rng.Intn(4) == 0 {
			seq = deleteDocSeq(t, d, id)
		} else {
			seq = putDocSeq(t, d, id, windowFuzzBodies[rng.Intn(len(windowFuzzBodies))])
		}
		if seq == 0 {
			continue
		}
		w := lq.settle(t, seq)
		if got, want := w.order, queryIDs(t, d, q); !equalStrings(got, want) {
			t.Fatalf("round %d: window = %v, fresh query = %v", round, got, want)
		}
		if len(w.order) > limit {
			t.Fatalf("round %d: window holds %d documents, above the limit of %d", round, len(w.order), limit)
		}
	}
}

func TestADocumentEntersTheWindowBecauseAnotherLeft(t *testing.T) {
	d := newDaemonForTest(t)
	defineTestCollection(t, d)
	putDoc(t, d, "a", `{"status":"1"}`)
	putDoc(t, d, "b", `{"status":"2"}`)
	putDoc(t, d, "c", `{"status":"3"}`)

	limit := 2
	q := testQuery()
	q.Sort = &protocol.DocumentSort{Field: "status"}
	q.Limit = &limit

	lq := subscribe(t, d, q)
	if got := ids(lq.next(t)); !equalStrings(got, []string{"a", "b"}) {
		t.Fatalf("initial window = %v, want [a b]", got)
	}

	seq := deleteDocSeq(t, d, "a")
	w := lq.settle(t, seq)
	if got := ids(w); !equalStrings(got, []string{"b", "c"}) {
		t.Fatalf("window after the delete = %v, want [b c]", got)
	}
	if got := w.changed(); !equalStrings(got, []string{"c"}) {
		t.Fatalf("bodies sent = %v, want only the document that entered", got)
	}
}

func idsOf(docs []protocol.StoredDocument) []string {
	out := make([]string, 0, len(docs))
	for _, doc := range docs {
		out = append(out, doc.ID)
	}
	return out
}
