package daemon_test

import (
	"encoding/json"
	"regexp"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

var hashedSubject = regexp.MustCompile(`^h:[0-9a-f]{12}$`)

func TestTheMirrorShipsAFactsUnclassifiedStringsAsLengthsAndItsSubjectHashed(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli, app := w.Client(), w.App()
		defineRequests(t, cli, gateNS)
		setAnalyticsEnabled(app, "true")
		first := put(t, cli, gateNS, "request-0001", `{}`)
		again := put(t, cli, gateNS, "request-0001", `{"status":"approved"}`)
		other := put(t, cli, gateNS, "request-0002", `{}`)
		synctest.Wait()

		records := mirroredDocumentChanges(t, app)
		if len(records) != 3 {
			t.Fatalf("the outbox holds %d document.changed records, want the 3 writes: %+v", len(records), records)
		}
		written := records[0]
		if written.Seq != int64(first.Seq) || records[1].Seq != int64(again.Seq) || records[2].Seq != int64(other.Seq) {
			t.Errorf("records carry seqs %d, %d, %d; want the bus seqs %d, %d, %d", written.Seq, records[1].Seq, records[2].Seq, first.Seq, again.Seq, other.Seq)
		}
		if props := string(written.Props); props != `{"collection":{"s":8},"id":{"s":12},"namespace":{"s":18}}` {
			t.Errorf("props = %s, want each caller-chosen string as its length", props)
		}
		payload := `{"namespace":"test/approval-gate","collection":"requests","id":"request-0001"}`
		if written.Bytes != len(payload) {
			t.Errorf("bytes = %d, want the payload's %d", written.Bytes, len(payload))
		}
		if !hashedSubject.MatchString(written.Subject) {
			t.Errorf("subject = %q, want h: and 12 hex characters", written.Subject)
		}
		if records[1].Subject != written.Subject || records[2].Subject == written.Subject {
			t.Errorf("subjects %q, %q, %q; want one document's writes to share a hash and another document's to differ", written.Subject, records[1].Subject, records[2].Subject)
		}
	})
}

func TestDisablingAnalyticsUnregistersTheMirrorAndReenablingStartsAtTheHead(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli, app := w.Client(), w.App()
		defineRequests(t, cli, gateNS)
		if listsConsumer(busStatus(t, app), "analytics-mirror") {
			t.Fatal("analytics-mirror is registered while analytics is off")
		}

		setAnalyticsEnabled(app, "true")
		put(t, cli, gateNS, "a", `{}`)
		synctest.Wait()
		setAnalyticsEnabled(app, "false")
		if listsConsumer(busStatus(t, app), "analytics-mirror") {
			t.Error("disabling analytics left analytics-mirror registered")
		}
		put(t, cli, gateNS, "bb", `{}`)

		setAnalyticsEnabled(app, "true")
		synctest.Wait()
		status := busStatus(t, app)
		if mirror := consumer(t, status, "analytics-mirror"); mirror.Cursor != status.Head || mirror.Lag != 0 {
			t.Errorf("re-enabled analytics-mirror reads %+v, want it at the head %d", mirror, status.Head)
		}
		put(t, cli, gateNS, "ccc", `{}`)
		synctest.Wait()

		if got, want := mirroredDocumentIDLengths(t, app), []string{`{"s":1}`, `{"s":3}`}; !slices.Equal(got, want) {
			t.Errorf("mirrored document ids %v, want %v: the write made while analytics was off is not captured", got, want)
		}
	})
}

func listsConsumer(status protocol.BusStatusResultMessage, name string) bool {
	return slices.ContainsFunc(status.Consumers, func(c protocol.BusConsumerStatus) bool { return c.Name == name })
}

func mirroredDocumentChanges(t *testing.T, app *testworld.Peer) []analytics.Record {
	t.Helper()
	listed := listAnalyticsRecords(app, protocol.ListAnalyticsRecordsMessage{Limit: 1000, Stream: protocol.Ptr("bus")})
	var records []analytics.Record
	for _, row := range slices.Backward(listed.Records) {
		var record analytics.Record
		if err := json.Unmarshal([]byte(row.Record), &record); err != nil {
			t.Fatalf("outbox row %d is not a record: %v", row.ID, err)
		}
		if record.Name == "document.changed" {
			records = append(records, record)
		}
	}
	return records
}

func TestListAnalyticsRecordsPagesMirroredFactsNewestFirst(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli, app := w.Client(), w.App()
		defineRequests(t, cli, gateNS)
		setAnalyticsEnabled(app, "true")
		for _, id := range []string{"a", "b", "c", "d", "e"} {
			put(t, cli, gateNS, id, `{}`)
		}
		synctest.Wait()

		all := listAnalyticsRecords(app, protocol.ListAnalyticsRecordsMessage{Limit: 1000, Stream: protocol.Ptr("bus")})
		if len(all.Records) < 5 || all.Waiting < len(all.Records) {
			t.Fatalf("the bus stream lists %d records with %d waiting, want at least the 5 writes, all waiting", len(all.Records), all.Waiting)
		}
		seqs := recordSeqs(t, all.Records)
		for i := 1; i < len(all.Records); i++ {
			if all.Records[i].ID >= all.Records[i-1].ID || seqs[i] >= seqs[i-1] {
				t.Fatalf("records %d and %d are ids %d, %d with seqs %d, %d; want newest first", i-1, i, all.Records[i-1].ID, all.Records[i].ID, seqs[i-1], seqs[i])
			}
		}

		var paged []protocol.AnalyticsOutboxRow
		page := listAnalyticsRecords(app, protocol.ListAnalyticsRecordsMessage{Limit: 2, Stream: protocol.Ptr("bus")})
		for len(page.Records) > 0 {
			paged = append(paged, page.Records...)
			page = listAnalyticsRecords(app, protocol.ListAnalyticsRecordsMessage{Limit: 2, Stream: protocol.Ptr("bus"), BeforeID: protocol.Ptr(page.Records[len(page.Records)-1].ID)})
		}
		if !slices.Equal(recordSeqs(t, paged), seqs) {
			t.Errorf("paging by 2 with before_id gave seqs %v, want the single listing's %v", recordSeqs(t, paged), seqs)
		}

		if other := listAnalyticsRecords(app, protocol.ListAnalyticsRecordsMessage{Limit: 1000, Stream: protocol.Ptr("app")}); len(other.Records) != 0 {
			t.Errorf("the app stream lists %d records, want none: the mirror writes only bus records", len(other.Records))
		}
	})
}

func TestTheAnalyticsOutboxForgetsMirroredFactsAfterSevenDays(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		defineRequests(t, cli, gateNS)
		setAnalyticsEnabled(w.App(), "true")
		put(t, cli, gateNS, "a", `{}`)
		synctest.Wait()
		w.stop()
		w.elapse(6 * 24 * time.Hour)
		w.start()
		put(t, w.Client(), gateNS, "bb", `{}`)
		synctest.Wait()
		if got, want := mirroredDocumentIDLengths(t, w.App()), []string{`{"s":1}`, `{"s":2}`}; !slices.Equal(got, want) {
			t.Fatalf("after 6 days the outbox holds document writes %v, want both %v", got, want)
		}
		w.stop()
		w.elapse(24 * time.Hour)
		w.start()

		w.advance(time.Hour + time.Minute)
		if got, want := mirroredDocumentIDLengths(t, w.App()), []string{`{"s":2}`}; !slices.Equal(got, want) {
			t.Errorf("after 7 days and an hour the outbox holds document writes %v, want only the one younger than 7 days %v", got, want)
		}
	})
}

func mirroredDocumentIDLengths(t *testing.T, app *testworld.Peer) []string {
	t.Helper()
	var lengths []string
	for _, record := range mirroredDocumentChanges(t, app) {
		var props struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(record.Props, &props); err != nil {
			t.Fatal(err)
		}
		lengths = append(lengths, string(props.ID))
	}
	return lengths
}

func recordSeqs(t *testing.T, rows []protocol.AnalyticsOutboxRow) []int64 {
	t.Helper()
	seqs := make([]int64, 0, len(rows))
	for _, row := range rows {
		var record analytics.Record
		if err := json.Unmarshal([]byte(row.Record), &record); err != nil {
			t.Fatalf("outbox row %d is not a record: %v", row.ID, err)
		}
		seqs = append(seqs, record.Seq)
	}
	return seqs
}
