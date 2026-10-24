package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAFactRedeliveredAfterACrashIsOneOutboxRow(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	handled := s.PauseAt(pausepoint.BusEventHandled)
	s.Start()
	app := s.App()
	setAnalyticsEnabled(app, "true")
	handled.Await()

	before := listAnalyticsRecords(app, protocol.ListAnalyticsRecordsMessage{Limit: 1000})
	if len(before.Records) != 1 {
		t.Fatalf("with the mirror held after its first fact the outbox lists %d records, want that one", len(before.Records))
	}
	recorded := recordSeqs(t, before.Records)[0]
	if mirror := consumer(t, busStatus(t, app), "analytics-mirror"); int64(mirror.Cursor) >= recorded {
		t.Fatalf("the held mirror's cursor is %d, want it short of the recorded seq %d so a restart redelivers it", mirror.Cursor, recorded)
	}
	s.Kill()
	handled.Release()

	s.Start()
	handled.Await()
	handled.Release()
	after := listAnalyticsRecords(s.App(), protocol.ListAnalyticsRecordsMessage{Limit: 1000})
	seqs := recordSeqs(t, after.Records)
	if n := countSeq(seqs, recorded); n != 1 {
		t.Errorf("after the crash and redelivery seq %d is %d outbox rows, want 1: %v", recorded, n, seqs)
	} else if row := after.Records[slices.Index(seqs, recorded)]; row.ID != before.Records[0].ID {
		t.Errorf("the redelivered seq %d moved from outbox row %d to %d, want the first row kept", recorded, before.Records[0].ID, row.ID)
	}
	if after.Waiting != len(after.Records) {
		t.Errorf("waiting = %d for %d rows, want each row counted once", after.Waiting, len(after.Records))
	}
}

func countSeq(seqs []int64, seq int64) int {
	return len(slices.DeleteFunc(slices.Clone(seqs), func(s int64) bool { return s != seq }))
}
