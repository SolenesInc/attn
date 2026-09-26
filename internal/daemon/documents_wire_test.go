package daemon_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	gateNS   = "app/approval-gate"
	requests = "requests"
)

func defineRequests(t *testing.T, cli *client.Client, namespace string, fields ...protocol.DocumentFieldSpec) {
	t.Helper()
	if len(fields) == 0 {
		fields = []protocol.DocumentFieldSpec{{Name: "status", Type: "string"}, {Name: "attempts", Type: "number"}}
	}
	defineCollection(t, cli, namespace, requests, fields...)
}

func defineCollection(t *testing.T, cli *client.Client, namespace, collection string, fields ...protocol.DocumentFieldSpec) {
	t.Helper()
	if _, err := cli.DocDefine(protocol.DocumentCollectionSchema{Namespace: namespace, Collection: collection, Fields: fields}); err != nil {
		t.Fatalf("define %s/%s: %v", namespace, collection, err)
	}
}

func put(t *testing.T, cli *client.Client, namespace, id, body string) *protocol.DocPutResult {
	t.Helper()
	written, err := cli.DocPut(namespace, requests, id, body, nil)
	if err != nil {
		t.Fatalf("put %s/%s: %v", namespace, id, err)
	}
	return written
}

func get(t *testing.T, cli *client.Client, namespace, id string) *protocol.DocGetResult {
	t.Helper()
	read, err := cli.DocGet(namespace, requests, id)
	if err != nil {
		t.Fatalf("get %s/%s: %v", namespace, id, err)
	}
	return read
}

func query(t *testing.T, cli *client.Client, q protocol.DocumentQuery) []protocol.StoredDocument {
	t.Helper()
	read, err := cli.DocQuery(q)
	if err != nil {
		t.Fatalf("query %+v: %v", q, err)
	}
	return read.Documents
}

func requestsWhere(namespace string, filters ...protocol.DocumentFilter) protocol.DocumentQuery {
	return protocol.DocumentQuery{Namespace: namespace, Collection: requests, Filters: filters}
}

func where(field, op string, value any) protocol.DocumentFilter {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return protocol.DocumentFilter{Field: field, Op: op, ValueJson: string(raw)}
}

func docIDs(docs []protocol.StoredDocument) string {
	ids := make([]string, 0, len(docs))
	for _, doc := range docs {
		ids = append(ids, doc.ID)
	}
	return strings.Join(ids, " ")
}

func subscribeOverTheWire(app *testworld.Peer, q protocol.DocumentQuery) string {
	id := uuid.NewString()
	app.Send(protocol.DocSubscribeMessage{Cmd: protocol.CmdDocSubscribe, Query: q, SubscriptionID: protocol.Ptr(id)})
	return id
}

func awaitDelivery(app *testworld.Peer, subscription string, match func(protocol.DocSubscriptionDeliveryMessage) bool) protocol.DocSubscriptionDeliveryMessage {
	app.T.Helper()
	return testworld.Await(app, protocol.EventDocSubscriptionDelivery, func(m protocol.DocSubscriptionDeliveryMessage) bool {
		return m.SubscriptionID == subscription && match(m)
	})
}

func TestEveryDocumentAnswerIsPositionedOnTheLogItsSubscribersFollow(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.App()
	defineRequests(t, cli, gateNS)
	subscription := subscribeOverTheWire(app, requestsWhere(gateNS))
	awaitDelivery(app, subscription, func(m protocol.DocSubscriptionDeliveryMessage) bool { return m.Delivery == 1 })

	written := put(t, cli, gateNS, "a", `{"status":"pending"}`)
	if delivered := awaitDelivery(app, subscription, reached(written.Seq)); written.Seq == 0 || !slices.Contains(delivered.Order, "a") {
		t.Fatalf("the first delivery at or after the write's seq %d holds %v", written.Seq, delivered.Order)
	}

	if read := get(t, cli, gateNS, "a"); !read.Found || read.AsOfSeq < written.Seq {
		t.Errorf("get stands at %d (found=%v) for a write at %d", read.AsOfSeq, read.Found, written.Seq)
	}
	if read, err := cli.DocQuery(requestsWhere(gateNS)); err != nil || len(read.Documents) != 1 || read.AsOfSeq < written.Seq {
		t.Errorf("query = %+v, %v; want the write, at or after seq %d", read, err, written.Seq)
	}
	if counted, err := cli.DocCount(requestsWhere(gateNS)); err != nil || counted.Count != 1 || counted.AsOfSeq < written.Seq {
		t.Errorf("count = %+v, %v; want 1, at or after seq %d", counted, err, written.Seq)
	}

	removed, err := cli.DocDelete(gateNS, requests, "a", nil)
	if err != nil || !removed.Existed {
		t.Fatalf("delete = %+v, %v", removed, err)
	}
	if emptied := awaitDelivery(app, subscription, reached(removed.Seq)); removed.Seq <= written.Seq || len(emptied.Order) != 0 {
		t.Fatalf("the first delivery at or after the delete's seq %d (the write was at %d) holds %v", removed.Seq, written.Seq, emptied.Order)
	}
}

func reached(seq int) func(protocol.DocSubscriptionDeliveryMessage) bool {
	return func(m protocol.DocSubscriptionDeliveryMessage) bool { return m.AsOfSeq >= seq }
}

func TestAWriteThatChangedNothingAnnouncesNothing(t *testing.T) {
	w := newWorld(t)
	cli, app := w.Client(), w.App()
	defineRequests(t, cli, gateNS)
	put(t, cli, gateNS, "a", `{"status":"pending"}`)

	missing, err := cli.DocDelete(gateNS, requests, "never-existed", nil)
	if err != nil || missing.Existed || missing.Seq != 0 {
		t.Fatalf("deleting a missing document = %+v, %v; want nothing removed and no position", missing, err)
	}
	stale := 8
	if _, err := cli.DocPut(gateNS, requests, "a", `{"status":"approved"}`, &stale); client.ErrorCode(err) != protocol.ErrorCodeConflict {
		t.Fatalf("a stale write returned %v, want a conflict", err)
	}
	if changes := producedBy(busStatus(t, app), "document.changed"); changes != 1 {
		t.Fatalf("the log holds %d document.changed fact(s), want only the one write that changed something", changes)
	}
}

func TestReadingAnUndeclaredCollectionIsReportedAsUndeclared(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	if _, err := cli.DocGet(gateNS, "nothing-here", "a"); client.ErrorCode(err) != protocol.ErrorCodeUndeclaredCollection {
		t.Errorf("get on an undeclared collection = %v", err)
	}
	if _, err := cli.DocCount(protocol.DocumentQuery{Namespace: gateNS, Collection: "nothing-here"}); client.ErrorCode(err) != protocol.ErrorCodeUndeclaredCollection {
		t.Errorf("count on an undeclared collection = %v", err)
	}
}

func TestAQueryIsCheckedAgainstTheCurrentDeclaration(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	defineRequests(t, cli, gateNS)
	for i, id := range []string{"a", "b", "c"} {
		put(t, cli, gateNS, id, fmt.Sprintf(`{"attempts":%d}`, i+1))
	}
	numeric := requestsWhere(gateNS, where("attempts", "eq", 2))
	if got := docIDs(query(t, cli, numeric)); got != "b" {
		t.Fatalf("attempts = 2 matched %q, want b", got)
	}

	defineRequests(t, cli, gateNS, protocol.DocumentFieldSpec{Name: "attempts", Type: "string"})
	if _, err := cli.DocQuery(numeric); err == nil || !strings.Contains(err.Error(), "needs a string value") {
		t.Fatalf("a numeric filter after attempts became a string = %v, want the type named", err)
	}
}

func TestABodyComesBackExactlyAsWritten(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	defineRequests(t, cli, gateNS)
	body := `{"status":"pending","nested":{"deep":[1,2,{"x":null}]},"undeclared":"kept"}`
	put(t, cli, gateNS, "a", body)

	if got := get(t, cli, gateNS, "a").Document.Body; got != body {
		t.Errorf("get returned %s, want %s", got, body)
	}
	if got := query(t, cli, requestsWhere(gateNS, where("status", "eq", "pending"))); len(got) != 1 || got[0].Body != body {
		t.Errorf("query returned %+v, want the body as written", got)
	}
}

func TestReplacingADocumentKeepsCreatedAtAndMovesUpdatedAt(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		defineRequests(t, cli, gateNS)
		put(t, cli, gateNS, "a", `{"status":"pending"}`)
		first := get(t, cli, gateNS, "a").Document

		w.advance(time.Second)
		put(t, cli, gateNS, "a", `{"status":"approved"}`)
		second := get(t, cli, gateNS, "a").Document
		if second.CreatedAt != first.CreatedAt {
			t.Errorf("created_at moved from %s to %s", first.CreatedAt, second.CreatedAt)
		}
		if gap := stamp(t, second.UpdatedAt).Sub(stamp(t, first.UpdatedAt)); gap != time.Second {
			t.Errorf("updated_at moved by %s over a second", gap)
		}
	})
}

func stamp(t *testing.T, raw string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatalf("stamp %q: %v", raw, err)
	}
	return at
}

func TestASweepByUpdatedAtFindsExactlyWhatChangedSinceItsBound(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		defineRequests(t, cli, gateNS)
		start := time.Now().UTC()
		written := []string{}
		for _, step := range []struct {
			id    string
			after time.Duration
		}{{"t0", 0}, {"t1234", 123400 * time.Microsecond}, {"t12345", 50 * time.Microsecond}, {"t5", 376550 * time.Microsecond}} {
			w.advance(step.after)
			put(t, cli, gateNS, step.id, `{"status":"pending"}`)
			written = append(written, step.id)
		}
		byUpdatedAt := func(filters ...protocol.DocumentFilter) protocol.DocumentQuery {
			q := requestsWhere(gateNS, filters...)
			q.Sort = &protocol.DocumentSort{Field: "updated_at"}
			return q
		}

		if got := docIDs(query(t, cli, byUpdatedAt(where("updated_at", "gte", start.Format(time.RFC3339))))); got != strings.Join(written, " ") {
			t.Errorf("updated_at >= the first write's second = %q, want every document in write order", got)
		}
		if got := docIDs(query(t, cli, byUpdatedAt(where("updated_at", "gt", start.Format(time.RFC3339Nano))))); got != strings.Join(written[1:], " ") {
			t.Errorf("updated_at > the first write = %q, want the rest", got)
		}

		firstPage := byUpdatedAt()
		firstPage.Limit = protocol.Ptr(2)
		seen := query(t, cli, firstPage)
		resumed := query(t, cli, byUpdatedAt(where("updated_at", "gt", seen[len(seen)-1].UpdatedAt)))
		if got := docIDs(seen) + " | " + docIDs(resumed); got != "t0 t1234 | t12345 t5" {
			t.Errorf("a sweep resumed from the stamp it was handed read %q", got)
		}
	})
}

func TestNamespacesAndCollectionsAreIsolated(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	defineRequests(t, cli, gateNS)
	defineRequests(t, cli, "app/other")
	defineCollection(t, cli, gateNS, "settings", protocol.DocumentFieldSpec{Name: "status", Type: "string"})
	put(t, cli, gateNS, "shared-id", `{"status":"pending"}`)
	put(t, cli, gateNS, "mine-only", `{"status":"pending"}`)
	put(t, cli, "app/other", "shared-id", `{"status":"approved"}`)
	if _, err := cli.DocPut(gateNS, "settings", "setting-1", `{"status":"approved"}`, nil); err != nil {
		t.Fatal(err)
	}

	if got := get(t, cli, gateNS, "shared-id").Document.Body; got != `{"status":"pending"}` {
		t.Errorf("the other namespace's write reached this one: %s", got)
	}
	if got := query(t, cli, requestsWhere(gateNS, where("status", "eq", "approved"))); len(got) != 0 {
		t.Errorf("a query matched another namespace's or collection's documents: %s", docIDs(got))
	}
	if counted, err := cli.DocCount(requestsWhere(gateNS)); err != nil || counted.Count != 2 {
		t.Errorf("count = %+v, %v; want only this collection's two", counted, err)
	}
	if got := docIDs(query(t, cli, protocol.DocumentQuery{Namespace: gateNS, Collection: "settings"})); got != "setting-1" {
		t.Errorf("settings holds %q, want only its own document", got)
	}

	if _, err := cli.DocDelete("app/other", requests, "shared-id", nil); err != nil {
		t.Fatal(err)
	}
	if !get(t, cli, gateNS, "shared-id").Found {
		t.Error("deleting in one namespace removed the other's document")
	}
}

func TestReadModifyWriteLoopsLoseNoUpdate(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	defineRequests(t, cli, gateNS)
	seeded := put(t, cli, gateNS, "counter", `{"attempts":0}`)

	if _, err := cli.DocPut(gateNS, requests, "missing", `{"attempts":0}`, protocol.Ptr(1)); !isConflictOn(err, false) {
		t.Fatalf("a conditional write to a missing document = %v, want a conflict that found nothing", err)
	}
	if get(t, cli, gateNS, "missing").Found {
		t.Fatal("the refused conditional write created the document")
	}

	const writers, increments = 4, 10
	var wg sync.WaitGroup
	failures := make(chan error, writers)
	for range writers {
		wg.Go(func() {
			own := w.Client()
			for range increments {
				if err := increment(own); err != nil {
					failures <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}

	final := get(t, cli, gateNS, "counter").Document
	var body struct{ Attempts int }
	if err := json.Unmarshal([]byte(final.Body), &body); err != nil {
		t.Fatal(err)
	}
	if body.Attempts != writers*increments || final.Rev != seeded.Rev+writers*increments {
		t.Fatalf("counter at %d, rev %d after %d accepted increments from rev %d", body.Attempts, final.Rev, writers*increments, seeded.Rev)
	}
}

func increment(cli *client.Client) error {
	for {
		read, err := cli.DocGet(gateNS, requests, "counter")
		if err != nil {
			return err
		}
		var body struct{ Attempts int }
		if err := json.Unmarshal([]byte(read.Document.Body), &body); err != nil {
			return err
		}
		written, err := cli.DocPut(gateNS, requests, "counter", fmt.Sprintf(`{"attempts":%d}`, body.Attempts+1), &read.Document.Rev)
		if isConflictOn(err, true) {
			continue
		}
		if err != nil {
			return err
		}
		if written.Rev != read.Document.Rev+1 {
			return fmt.Errorf("a write expecting rev %d was accepted at rev %d", read.Document.Rev, written.Rev)
		}
		return nil
	}
}

func isConflictOn(err error, found bool) bool {
	var refused *client.DaemonError
	return errors.As(err, &refused) && refused.Code == protocol.ErrorCodeConflict && refused.Conflict != nil && refused.Conflict.Found == found
}

func TestRedefiningAnUndefinedCollectionStartsItEmpty(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	defineRequests(t, cli, gateNS)
	put(t, cli, gateNS, "a", `{"status":"pending"}`)
	put(t, cli, gateNS, "b", `{"status":"pending"}`)

	if _, err := cli.DocUndefine(gateNS, requests); err != nil {
		t.Fatal(err)
	}
	defineRequests(t, cli, gateNS)
	if got := query(t, cli, requestsWhere(gateNS)); len(got) != 0 {
		t.Fatalf("the redefined collection holds %s", docIDs(got))
	}
}

func TestDocumentsSurviveADaemonRestart(t *testing.T) {
	w := newWorld(t)
	defineRequests(t, w.Client(), gateNS)
	put(t, w.Client(), gateNS, "a", `{"status":"pending"}`)
	before := get(t, w.Client(), gateNS, "a").Document

	w.restart()
	cli := w.Client()
	if got := docIDs(query(t, cli, requestsWhere(gateNS, where("status", "eq", "pending")))); got != "a" {
		t.Fatalf("a query on a declared field after the restart = %q", got)
	}
	if after := get(t, cli, gateNS, "a").Document; after.CreatedAt != before.CreatedAt || after.Body != before.Body {
		t.Fatalf("after the restart a = %+v, was %+v", after, before)
	}
}
