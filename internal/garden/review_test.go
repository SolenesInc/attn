package garden

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestReviewCandidateTruthTable(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	old := now.Add(-DefaultStaleWindow)
	recent := now.Add(-time.Hour)

	tests := []struct {
		name        string
		observation ReviewObservation
		want        bool
		why         ReviewCandidateReason
	}{
		{
			name:        "ended agent and missing directory is immediate",
			observation: reviewObservation("s-a", recent, ReviewDirectoryMissing),
			want:        true, why: ReviewReasonDirectoryMissing,
		},
		{
			name:        "present directory waits for the lifecycle clock",
			observation: reviewObservation("s-a", recent, ReviewDirectoryPresent),
		},
		{
			name:        "present directory enters at exactly seven days",
			observation: reviewObservation("s-a", old, ReviewDirectoryPresent),
			want:        true, why: ReviewReasonLifecycleStale,
		},
		{
			name:        "unknown directory uses the lifecycle clock",
			observation: reviewObservation("s-a", old, ReviewDirectoryUnknown),
			want:        true, why: ReviewReasonLifecycleStale,
		},
		{
			name:        "unverifiable directory uses the lifecycle clock",
			observation: reviewObservation("s-a", old, ReviewDirectoryUnavailable),
			want:        true, why: ReviewReasonLifecycleStale,
		},
		{
			name:        "remote directory uses the lifecycle clock",
			observation: reviewObservation("s-a", old, ReviewDirectoryRemote),
			want:        true, why: ReviewReasonLifecycleStale,
		},
		{
			name: "tracked session protects its claim",
			observation: func() ReviewObservation {
				observation := reviewObservation("s-a", old, ReviewDirectoryMissing)
				observation.TenderHolds = true
				return observation
			}(),
		},
		{
			name: "planted backlog is outside this pass",
			observation: func() ReviewObservation {
				observation := reviewObservation("s-a", old, ReviewDirectoryMissing)
				observation.Seed.Status = StatusPlanted
				return observation
			}(),
		},
		{
			name: "parked work is deliberate",
			observation: func() ReviewObservation {
				observation := reviewObservation("s-a", old, ReviewDirectoryMissing)
				observation.Seed.Status = StatusDormant
				return observation
			}(),
		},
		{
			name: "closed work is not reviewed",
			observation: func() ReviewObservation {
				observation := reviewObservation("s-a", old, ReviewDirectoryMissing)
				observation.Seed.Status = StatusHarvested
				return observation
			}(),
		},
		{
			name: "gates stay with their existing flow",
			observation: func() ReviewObservation {
				observation := reviewObservation("s-a", old, ReviewDirectoryMissing)
				observation.Seed.Gate = true
				return observation
			}(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ReviewCandidates([]ReviewObservation{test.observation}, DefaultStaleWindow, now)
			if len(got) != 0 != test.want {
				t.Fatalf("ReviewCandidates returned %d item(s), want candidate=%v", len(got), test.want)
			}
			if test.want && got[0].Reason != test.why {
				t.Fatalf("reason = %q, want %q", got[0].Reason, test.why)
			}
		})
	}
}

func TestReviewCandidatesUseConservativeLegacyLifecycleClock(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	observation := reviewObservation("s-a", time.Time{}, ReviewDirectoryPresent)
	observation.DocumentUpdatedAt = now.Add(-DefaultStaleWindow)

	got := ReviewCandidates([]ReviewObservation{observation}, DefaultStaleWindow, now)
	if len(got) != 1 {
		t.Fatalf("ReviewCandidates returned %d items, want one", len(got))
	}
	if got[0].LifecycleExact || !got[0].LifecycleAt.Equal(observation.DocumentUpdatedAt) {
		t.Fatalf("legacy clock = %v exact=%v, want document time and inexact", got[0].LifecycleAt, got[0].LifecycleExact)
	}
}

func TestReviewPlotUsesActivityAndClaimsAcrossItsWholeSubtree(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	old := now.Add(-DefaultStaleWindow)
	recent := now.Add(-time.Hour)
	plot := reviewObservation("s-plot", old, ReviewDirectoryMissing)
	child := reviewObservation("s-child", old, ReviewDirectoryMissing)
	child.Seed.Edges = []Edge{{Kind: EdgePartOf, To: plot.Seed.ID}}
	grandchild := reviewObservation("s-grand", old, ReviewDirectoryMissing)
	grandchild.Seed.Edges = []Edge{{Kind: EdgePartOf, To: child.Seed.ID}}

	t.Run("recent nested activity protects only the plots", func(t *testing.T) {
		active := grandchild
		active.NewestNoteAt = recent
		got := ReviewCandidates([]ReviewObservation{plot, child, active}, DefaultStaleWindow, now)
		assertCandidateIDs(t, got, []string{"s-grand"})
	})

	t.Run("a nested active claim protects every ancestor and itself", func(t *testing.T) {
		held := grandchild
		held.TenderHolds = true
		got := ReviewCandidates([]ReviewObservation{plot, child, held}, DefaultStaleWindow, now)
		assertCandidateIDs(t, got, nil)
	})

	t.Run("an inactive old subtree offers plots and leaves independently", func(t *testing.T) {
		got := ReviewCandidates([]ReviewObservation{plot, child, grandchild}, DefaultStaleWindow, now)
		assertCandidateIDs(t, got, []string{"s-plot", "s-child", "s-grand"})
		if !got[0].Plot || got[0].Reason != ReviewReasonSubtreeStale || len(got[0].SubtreeIDs) != 3 {
			t.Fatalf("plot candidate = %+v", got[0])
		}
	})
}

func TestReviewCandidatesExcludePacketSubtrees(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	packet := reviewObservation("s-packet", now.Add(-DefaultStaleWindow), ReviewDirectoryMissing)
	packet.Seed.Template = true
	child := reviewObservation("s-child", now.Add(-DefaultStaleWindow), ReviewDirectoryMissing)
	child.Seed.Edges = []Edge{{Kind: EdgePartOf, To: packet.Seed.ID}}

	assertCandidateIDs(t, ReviewCandidates([]ReviewObservation{packet, child}, DefaultStaleWindow, now), nil)
}

func TestReviewCandidateSubtreeProtectionProperties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 40).Draw(t, "seed count")
		held := rapid.IntRange(0, n-1).Draw(t, "held seed")
		recent := rapid.IntRange(0, n-1).Draw(t, "recent seed")
		now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
		old := now.Add(-DefaultStaleWindow)
		observations := make([]ReviewObservation, n)
		for i := range n {
			id := fmt.Sprintf("s-%06x", i)
			observations[i] = reviewObservation(id, old, ReviewDirectoryPresent)
			if i > 0 {
				parent := rapid.IntRange(0, i-1).Draw(t, fmt.Sprintf("parent %d", i))
				observations[i].Seed.Edges = []Edge{{Kind: EdgePartOf, To: observations[parent].Seed.ID}}
			}
		}
		observations[held].TenderHolds = true
		observations[recent].NewestNoteAt = now.Add(-time.Hour)

		for _, candidate := range ReviewCandidates(observations, DefaultStaleWindow, now) {
			if slices.Contains(candidate.SubtreeIDs, observations[held].Seed.ID) {
				t.Fatalf("candidate %s contains held seed %s", candidate.SeedID, observations[held].Seed.ID)
			}
			if candidate.Plot && slices.Contains(candidate.SubtreeIDs, observations[recent].Seed.ID) {
				t.Fatalf("plot candidate %s contains recent activity at %s", candidate.SeedID, observations[recent].Seed.ID)
			}
		}
	})
}

func reviewObservation(id string, lifecycleAt time.Time, directory ReviewDirectoryState) ReviewObservation {
	return ReviewObservation{
		Seed:        Seed{ID: id, Status: StatusGrowing},
		LifecycleAt: lifecycleAt, LifecycleExact: !lifecycleAt.IsZero(),
		DocumentUpdatedAt: lifecycleAt, DirectoryState: directory,
	}
}

func assertCandidateIDs(t *testing.T, got []ReviewCandidate, want []string) {
	t.Helper()
	ids := make([]string, 0, len(got))
	for _, candidate := range got {
		ids = append(ids, candidate.SeedID)
	}
	if len(ids) != len(want) {
		t.Fatalf("candidate ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("candidate ids = %v, want %v", ids, want)
		}
	}
}
