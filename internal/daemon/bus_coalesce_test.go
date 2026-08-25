package daemon

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestCoalesceSnapshotsCollapsesRepeatedPushes(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	trace := wireRecorder(d)

	d.coalesceSnapshots(func() {
		for _, id := range []string{"pr-1", "pr-2", "pr-3"} {
			d.publishFact(FactPRUpdated, id, nil)
		}
	})

	if got := trace.EventNames(); len(got) != 1 || got[0] != string(protocol.EventPRsUpdated) {
		t.Fatalf("three PR facts should collapse to one prs_updated, got %v", got)
	}
}

func TestCoalesceSnapshotsKeepsDistinctSnapshotsSeparate(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	trace := wireRecorder(d)

	d.coalesceSnapshots(func() {
		d.publishFact(FactPRHeatChanged, "pr-1", nil)
		d.publishFact(FactPRHeatChanged, "pr-2", nil)
		d.publishFact(FactRepoMuteChanged, "owner/repo", nil)
	})

	want := []string{string(protocol.EventPRsUpdated), string(protocol.EventReposUpdated)}
	got := trace.EventNames()
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want %v, got %v", want, got)
		}
	}
}

func TestCoalesceSnapshotsCollapsesTheGarden(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	var pushes int
	d.gardenBroadcastHook = func([]protocol.Seed, int) { pushes++ }

	d.coalesceSnapshots(func() {
		for _, id := range []string{"s-1", "s-2", "s-3", "s-4", "s-5"} {
			d.publishFact(FactGardenNoted, id, nil)
		}
	})

	if pushes != 1 {
		t.Fatalf("five garden facts in one bulk block pushed %d gardens, want 1", pushes)
	}
}

func TestUncoalescedFactsPushOncePerFact(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	trace := wireRecorder(d)

	d.publishFact(FactPRUpdated, "pr-1", nil)
	d.publishFact(FactPRUpdated, "pr-2", nil)

	if got := trace.Count(); got != 2 {
		t.Fatalf("want 2 pushes, got %d (%v)", got, trace.EventNames())
	}
}

func TestNestedCoalesceFlushesOnlyAtTheOuterBoundary(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	trace := wireRecorder(d)

	d.coalesceSnapshots(func() {
		d.publishFact(FactPRUpdated, "pr-1", nil)
		d.coalesceSnapshots(func() {
			d.publishFact(FactPRUpdated, "pr-2", nil)
		})
		if got := trace.Count(); got != 0 {
			t.Fatalf("inner block flushed early: %v", trace.EventNames())
		}
	})

	if got := trace.Count(); got != 1 {
		t.Fatalf("want one push after the outer block, got %d (%v)", got, trace.EventNames())
	}
}
