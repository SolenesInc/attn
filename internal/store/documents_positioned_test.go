package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

func changeFact(id string, deleted bool) BusEvent {
	payload := fmt.Sprintf(`{"namespace":"app/approval-gate","collection":"requests","id":%q,"deleted":%t}`, id, deleted)
	return BusEvent{
		Name:    "document.changed",
		Subject: "app/approval-gate/requests/" + id,
		Payload: payload,
	}
}

func factsOnLog(t *testing.T, s *Store) []BusEvent {
	t.Helper()
	events, err := s.BusEventsSince(0, 1000)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	return events
}

func TestACommittedWriteReportsWhereItsFactLanded(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{})
	schema := requestsDecl(t, s)

	written, err := s.CommitDocumentWrite(
		DocumentWrite{Schema: schema, ID: "a", Body: []byte(`{"status":"pending"}`)},
		changeFact("a", false), base)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if written.Rev != docstore.FirstRev {
		t.Fatalf("rev = %d, want %d", written.Rev, docstore.FirstRev)
	}
	if written.Seq == 0 || !written.Changed {
		t.Fatalf("write reported no position: %+v", written)
	}

	events := factsOnLog(t, s)
	if len(events) != 1 {
		t.Fatalf("log holds %d event(s), want 1", len(events))
	}
	if events[0].Seq != written.Seq {
		t.Fatalf("fact is at seq %d, write reported %d", events[0].Seq, written.Seq)
	}
	if events[0].Subject != "app/approval-gate/requests/a" {
		t.Fatalf("subject = %q", events[0].Subject)
	}
}

// The failure is injected through the real path — a trigger refusing every insert
// into bus_events — so what is proven is that the two statements share a transaction.
func TestAWriteDoesNotSurviveTheFactItCouldNotAppend(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`})
	schema := requestsDecl(t, s)

	if _, err := s.db.Exec(`CREATE TRIGGER refuse_facts BEFORE INSERT ON bus_events
	                        BEGIN SELECT RAISE(ABORT, 'the log had a bad night'); END`); err != nil {
		t.Fatalf("installing the failing append: %v", err)
	}

	_, err := s.CommitDocumentWrite(
		DocumentWrite{Schema: schema, ID: "a", Body: []byte(`{"status":"approved"}`)},
		changeFact("a", false), base.Add(time.Second))
	if err == nil {
		t.Fatal("a write whose fact could not be appended reported success")
	}
	if !strings.Contains(err.Error(), "bad night") {
		t.Fatalf("error does not name the append failure: %v", err)
	}

	doc, found, err := s.GetDocument(schema, "a")
	if err != nil || !found {
		t.Fatalf("re-reading a: found=%v err=%v", found, err)
	}
	if got := string(doc.Body); got != `{"status":"pending"}` {
		t.Fatalf("the document survived a write whose fact did not: body = %s", got)
	}
	if doc.Rev != docstore.FirstRev {
		t.Fatalf("rev = %d, want the write to have left it at %d", doc.Rev, docstore.FirstRev)
	}
}

func TestDocumentWriteBatchDoesNotSurviveItsSecondFactFailure(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{})
	schema := requestsDecl(t, s)

	if _, err := s.db.Exec(`CREATE TRIGGER refuse_second_fact BEFORE INSERT ON bus_events
	                        WHEN NEW.subject = 'app/approval-gate/requests/b'
	                        BEGIN SELECT RAISE(ABORT, 'the second fact failed'); END`); err != nil {
		t.Fatalf("installing the failing append: %v", err)
	}
	absent := docstore.ExpectAbsent
	_, err := s.CommitDocumentWrites([]DocumentCommit{
		{
			Write: DocumentWrite{Schema: schema, ID: "a", Body: []byte(`{"status":"pending"}`), Expected: &absent},
			Fact:  changeFact("a", false),
		},
		{
			Write: DocumentWrite{Schema: schema, ID: "b", Body: []byte(`{"status":"pending"}`), Expected: &absent},
			Fact:  changeFact("b", false),
		},
	}, base)
	if err == nil || !strings.Contains(err.Error(), "second fact failed") {
		t.Fatalf("batch error = %v, want the second fact failure", err)
	}
	for _, id := range []string{"a", "b"} {
		if _, found, readErr := s.GetDocument(schema, id); readErr != nil || found {
			t.Fatalf("document %s survived the batch: found=%v err=%v", id, found, readErr)
		}
	}
	if events := factsOnLog(t, s); len(events) != 0 {
		t.Fatalf("the failed batch left %d fact(s) on the log", len(events))
	}
}

func TestDocumentWriteBatchReportsEachFactsPosition(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{})
	schema := requestsDecl(t, s)
	absent := docstore.ExpectAbsent

	written, err := s.CommitDocumentWrites([]DocumentCommit{
		{
			Write: DocumentWrite{Schema: schema, ID: "a", Body: []byte(`{"status":"pending"}`), Expected: &absent},
			Fact:  changeFact("a", false),
		},
		{
			Write: DocumentWrite{Schema: schema, ID: "b", Body: []byte(`{"status":"pending"}`), Expected: &absent},
			Fact:  changeFact("b", false),
		},
	}, base)
	if err != nil {
		t.Fatalf("commit batch: %v", err)
	}
	if len(written) != 2 || written[0].Rev != docstore.FirstRev || written[1].Rev != docstore.FirstRev ||
		written[0].Seq == 0 || written[1].Seq != written[0].Seq+1 {
		t.Fatalf("batch results = %+v", written)
	}
	events := factsOnLog(t, s)
	if len(events) != 2 || events[0].Seq != written[0].Seq || events[1].Seq != written[1].Seq {
		t.Fatalf("facts = %+v for results %+v", events, written)
	}
}

func TestADeleteDoesNotSurviveTheFactItCouldNotAppend(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`})
	schema := requestsDecl(t, s)

	if _, err := s.db.Exec(`CREATE TRIGGER refuse_facts BEFORE INSERT ON bus_events
	                        BEGIN SELECT RAISE(ABORT, 'the log had a bad night'); END`); err != nil {
		t.Fatalf("installing the failing append: %v", err)
	}

	if _, err := s.CommitDocumentWrite(
		DocumentWrite{Schema: schema, ID: "a", Delete: true},
		changeFact("a", true), base.Add(time.Second)); err == nil {
		t.Fatal("a delete whose fact could not be appended reported success")
	}
	if _, found, err := s.GetDocument(schema, "a"); err != nil || !found {
		t.Fatalf("the document went with a removal that was never announced: found=%v err=%v", found, err)
	}
}

func TestAWriteThatChangedNothingAnnouncesNothing(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{"a": `{"status":"pending"}`})
	schema := requestsDecl(t, s)

	written, err := s.CommitDocumentWrite(
		DocumentWrite{Schema: schema, ID: "never-existed", Delete: true},
		changeFact("never-existed", true), base.Add(time.Second))
	if err != nil {
		t.Fatalf("deleting a missing document: %v", err)
	}
	if written.Changed || written.Seq != 0 {
		t.Fatalf("a delete that removed nothing reported %+v", written)
	}

	stale := docstore.FirstRev + 7
	if _, err := s.CommitDocumentWrite(
		DocumentWrite{Schema: schema, ID: "a", Body: []byte(`{"status":"approved"}`), Expected: &stale},
		changeFact("a", false), base.Add(2*time.Second)); !docstore.IsConflict(err) {
		t.Fatalf("a refused write returned %v, want a conflict", err)
	}

	if events := factsOnLog(t, s); len(events) != 0 {
		t.Fatalf("the log holds %d event(s) for writes that changed nothing", len(events))
	}
}

func TestAnAnswerCarriesThePositionItWasTrueAt(t *testing.T) {
	s, base := storeWithRequests(t, map[string]string{})
	schema := requestsDecl(t, s)

	read, found, err := s.ReadQuery(docstore.Query{Namespace: schema.Namespace, Collection: schema.Collection})
	if err != nil || !found {
		t.Fatalf("query: found=%v err=%v", found, err)
	}
	if read.AsOfSeq != 0 {
		t.Fatalf("as_of_seq over an empty log = %d, want 0", read.AsOfSeq)
	}

	written, err := s.CommitDocumentWrite(
		DocumentWrite{Schema: schema, ID: "a", Body: []byte(`{"status":"pending"}`)},
		changeFact("a", false), base)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}

	read, _, err = s.ReadQuery(docstore.Query{Namespace: schema.Namespace, Collection: schema.Collection})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if read.AsOfSeq < written.Seq {
		t.Fatalf("a query that returned the write stands at %d, before the write at %d", read.AsOfSeq, written.Seq)
	}
	if len(read.Documents) != 1 {
		t.Fatalf("query returned %d document(s)", len(read.Documents))
	}

	got, declared, err := s.ReadDocument(schema.Namespace, schema.Collection, "a")
	if err != nil || !declared {
		t.Fatalf("get: declared=%v err=%v", declared, err)
	}
	if !got.Found || got.AsOfSeq < written.Seq {
		t.Fatalf("get stands at %d for a write at %d (found=%v)", got.AsOfSeq, written.Seq, got.Found)
	}

	count, declared, err := s.CountQuery(docstore.Query{Namespace: schema.Namespace, Collection: schema.Collection})
	if err != nil || !declared {
		t.Fatalf("count: declared=%v err=%v", declared, err)
	}
	if count.Count != 1 || count.AsOfSeq < written.Seq {
		t.Fatalf("count = %d as of %d, for a write at %d", count.Count, count.AsOfSeq, written.Seq)
	}
}

func TestReadingAnUndeclaredCollectionSaysSo(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{})

	if _, declared, err := s.ReadDocument("app/approval-gate", "nothing-here", "a"); err != nil || declared {
		t.Fatalf("get on an undeclared collection: declared=%v err=%v", declared, err)
	}
	if _, declared, err := s.CountQuery(docstore.Query{
		Namespace: "app/approval-gate", Collection: "nothing-here",
	}); err != nil || declared {
		t.Fatalf("count on an undeclared collection: declared=%v err=%v", declared, err)
	}
}

func TestACountAgreesWithTheQueryItCounts(t *testing.T) {
	s := New()
	base := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	if _, err := s.DefineDocumentCollection(requestsDeclaration(), base); err != nil {
		t.Fatalf("define: %v", err)
	}
	schema := requestsDecl(t, s)

	stamps := []time.Duration{0, 123400 * time.Microsecond, 123450 * time.Microsecond, 500 * time.Millisecond}
	bodies := []string{
		`{"status":"pending","attempts":5,"urgent":true}`,
		`{"status":"pending","attempts":"5","urgent":"true"}`,
		`{"status":"approved","attempts":10,"urgent":false}`,
		`{"status":"pending","attempts":[1,2],"urgent":true}`,
	}
	for i, body := range bodies {
		if _, err := s.PutDocument(schema, fmt.Sprintf("d%d", i), []byte(body), base.Add(stamps[i]), nil); err != nil {
			t.Fatalf("put d%d: %v", i, err)
		}
	}

	queries := []docstore.Query{
		{Namespace: schema.Namespace, Collection: schema.Collection},
		{Namespace: schema.Namespace, Collection: schema.Collection,
			Filters: []docstore.Filter{{Field: "status", Op: docstore.OpEq, Value: "pending"}}},
		{Namespace: schema.Namespace, Collection: schema.Collection,
			Filters: []docstore.Filter{{Field: "attempts", Op: docstore.OpGte, Value: float64(5)}}},
		{Namespace: schema.Namespace, Collection: schema.Collection,
			Filters: []docstore.Filter{{Field: "urgent", Op: docstore.OpEq, Value: true}}},
		{Namespace: schema.Namespace, Collection: schema.Collection,
			Filters: []docstore.Filter{{
				Field: docstore.FieldCreatedAt, Op: docstore.OpGte,
				Value: base.Add(123400 * time.Microsecond).Format(time.RFC3339Nano),
			}}},
	}
	for i, q := range queries {
		q.Limit = docstore.MaxLimit
		read, found, err := s.ReadQuery(q)
		if err != nil || !found {
			t.Fatalf("query %d: found=%v err=%v", i, found, err)
		}
		count, found, err := s.CountQuery(q)
		if err != nil || !found {
			t.Fatalf("count %d: found=%v err=%v", i, found, err)
		}
		if count.Count != len(read.Documents) {
			t.Fatalf("query %d matched %d document(s) but counted %d", i, len(read.Documents), count.Count)
		}
	}
}

func TestACountAfterACursorCountsWhatIsLeft(t *testing.T) {
	s, _ := storeWithRequests(t, map[string]string{
		"a": `{"attempts":1}`,
		"b": `{"attempts":2}`,
		"c": `{"attempts":3}`,
	})
	q := docstore.Query{
		Namespace: "app/approval-gate", Collection: "requests",
		Sort: &docstore.Sort{Field: "attempts"}, After: "a", Limit: docstore.MaxLimit,
	}
	count, found, err := s.CountQuery(q)
	if err != nil || !found {
		t.Fatalf("count: found=%v err=%v", found, err)
	}
	if count.Count != 2 {
		t.Fatalf("count after a = %d, want 2", count.Count)
	}
}
