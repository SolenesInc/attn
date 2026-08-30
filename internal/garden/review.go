package garden

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/docstore"
)

const (
	ReviewRunStatusRunning  = "running"
	ReviewRunStatusComplete = "complete"
	ReviewRunStatusCanceled = "canceled"

	ReviewItemStatusQueued      = "queued"
	ReviewItemStatusRunning     = "running"
	ReviewItemStatusReady       = "ready"
	ReviewItemStatusFailed      = "failed"
	ReviewItemStatusInvalidated = "invalidated"

	ReviewResolutionUnresolved         = "unresolved"
	ReviewResolutionResolved           = "resolved"
	ReviewResolutionNoLongerApplicable = "no_longer_applicable"
)

type ReviewRecipe struct {
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	Effort string `json:"effort,omitempty"`
}

type ReviewRun struct {
	ID           string       `json:"id"`
	CandidateIDs []string     `json:"candidate_ids"`
	Recipe       ReviewRecipe `json:"recipe"`
	Status       string       `json:"status"`
	CapturedAt   string       `json:"captured_at"`
	CompletedAt  string       `json:"completed_at,omitempty"`
}

type ReviewEvidence struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

type ReviewItem struct {
	ID                 string           `json:"id"`
	RunID              string           `json:"run_id"`
	SeedID             string           `json:"seed_id"`
	SeedRev            int64            `json:"seed_rev"`
	EvidenceVersion    string           `json:"evidence_version"`
	Title              string           `json:"title"`
	Body               string           `json:"body"`
	Evidence           []ReviewEvidence `json:"evidence"`
	Actions            []string         `json:"actions"`
	Status             string           `json:"status"`
	Resolution         string           `json:"resolution"`
	ResolvedAction     string           `json:"resolved_action,omitempty"`
	ReviewAgainAt      string           `json:"review_again_at,omitempty"`
	Recommendation     string           `json:"recommendation,omitempty"`
	Explanation        string           `json:"explanation,omitempty"`
	CitedEvidence      []string         `json:"cited_evidence,omitempty"`
	Error              string           `json:"error,omitempty"`
	JobID              string           `json:"job_id,omitempty"`
	StartedAt          string           `json:"started_at,omitempty"`
	CompletedAt        string           `json:"completed_at,omitempty"`
	AdvisorState       string           `json:"-"`
	AdvisorAttempt     int              `json:"-"`
	AdvisorMaxAttempts int              `json:"-"`
	AdvisorRetryAt     string           `json:"-"`
	AdvisorUpdatedAt   string           `json:"-"`
	AdvisorError       string           `json:"-"`
}

func ReviewRunsSchema() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace: Namespace, Collection: CollectionReviewRuns,
		Fields: []docstore.FieldSpec{{Name: "status", Type: docstore.FieldString}},
	}
}

func ReviewItemsSchema() docstore.CollectionSchema {
	return docstore.CollectionSchema{
		Namespace: Namespace, Collection: CollectionReviewItems,
		Fields: []docstore.FieldSpec{
			{Name: "run_id", Type: docstore.FieldString},
			{Name: "seed_id", Type: docstore.FieldString},
			{Name: "status", Type: docstore.FieldString},
			{Name: "resolution", Type: docstore.FieldString},
		},
	}
}

func (run ReviewRun) Encode() ([]byte, error) { return json.Marshal(run) }

func DecodeReviewRun(body []byte) (ReviewRun, error) {
	var run ReviewRun
	if err := json.Unmarshal(body, &run); err != nil {
		return ReviewRun{}, fmt.Errorf("this Garden review run is not readable: %w", err)
	}
	return run, nil
}

func (item ReviewItem) Encode() ([]byte, error) { return json.Marshal(item) }

func DecodeReviewItem(body []byte) (ReviewItem, error) {
	var item ReviewItem
	if err := json.Unmarshal(body, &item); err != nil {
		return ReviewItem{}, fmt.Errorf("this Garden review item is not readable: %w", err)
	}
	return item, nil
}

func ReviewItemID(runID, seedID string) string {
	return strings.TrimSpace(runID) + "." + strings.TrimSpace(seedID)
}

type ReviewDirectoryState string

const (
	ReviewDirectoryPresent     ReviewDirectoryState = "present"
	ReviewDirectoryMissing     ReviewDirectoryState = "missing"
	ReviewDirectoryRemote      ReviewDirectoryState = "remote"
	ReviewDirectoryUnavailable ReviewDirectoryState = "unavailable"
	ReviewDirectoryUnknown     ReviewDirectoryState = "unknown"
)

type ReviewCandidateReason string

const (
	ReviewReasonDirectoryMissing ReviewCandidateReason = "directory_missing"
	ReviewReasonLifecycleStale   ReviewCandidateReason = "lifecycle_stale"
	ReviewReasonSubtreeStale     ReviewCandidateReason = "subtree_stale"
)

type ReviewObservation struct {
	Seed              Seed
	LifecycleAt       time.Time
	LifecycleExact    bool
	DocumentUpdatedAt time.Time
	NewestNoteAt      time.Time
	TenderHolds       bool
	DirectoryState    ReviewDirectoryState
	ResumeAvailable   bool
	HandoverAvailable bool
	ChiefAvailable    bool
	ReviewAgainAt     time.Time
}

type ReviewCandidate struct {
	SeedID            string
	Reason            ReviewCandidateReason
	DirectoryState    ReviewDirectoryState
	LifecycleAt       time.Time
	LifecycleExact    bool
	SubtreeActivityAt time.Time
	ResumeAvailable   bool
	HandoverAvailable bool
	ChiefAvailable    bool
	Plot              bool
	SubtreeIDs        []string
}

func ReviewCandidates(observations []ReviewObservation, window time.Duration, now time.Time) []ReviewCandidate {
	bySeed := make(map[string]ReviewObservation, len(observations))
	seeds := make([]Seed, 0, len(observations))
	children := make(map[string][]string)
	for _, observation := range observations {
		seed := observation.Seed
		bySeed[seed.ID] = observation
		seeds = append(seeds, seed)
		if parent, ok := parentOf(seed); ok {
			children[parent] = append(children[parent], seed.ID)
		}
	}

	index := byID(seeds)
	result := make([]ReviewCandidate, 0)
	for _, observation := range observations {
		seed := observation.Seed
		if seed.Status != StatusGrowing || seed.Gate || underTemplate(index, seed) {
			continue
		}
		if observation.ReviewAgainAt.After(now) {
			continue
		}

		subtree := reviewSubtree(seed.ID, children)
		isPlot := len(subtree) > 1
		if reviewSubtreeHeld(subtree, bySeed) {
			continue
		}

		lifecycleAt, exact := reviewLifecycleAt(observation)
		candidate := ReviewCandidate{
			SeedID:            seed.ID,
			DirectoryState:    normalizedReviewDirectoryState(observation.DirectoryState),
			LifecycleAt:       lifecycleAt,
			LifecycleExact:    exact,
			ResumeAvailable:   observation.ResumeAvailable,
			HandoverAvailable: observation.HandoverAvailable,
			ChiefAvailable:    observation.ChiefAvailable,
			Plot:              isPlot,
			SubtreeIDs:        subtree,
		}
		if isPlot {
			candidate.SubtreeActivityAt = reviewSubtreeActivity(subtree, bySeed)
			if reviewOldEnough(candidate.SubtreeActivityAt, window, now) {
				candidate.Reason = ReviewReasonSubtreeStale
				result = append(result, candidate)
			}
			continue
		}

		if candidate.DirectoryState == ReviewDirectoryMissing {
			candidate.Reason = ReviewReasonDirectoryMissing
			result = append(result, candidate)
			continue
		}
		if reviewOldEnough(lifecycleAt, window, now) {
			candidate.Reason = ReviewReasonLifecycleStale
			result = append(result, candidate)
		}
	}
	return result
}

func reviewSubtree(root string, children map[string][]string) []string {
	result := []string{root}
	seen := map[string]bool{root: true}
	for i := 0; i < len(result); i++ {
		for _, child := range children[result[i]] {
			if seen[child] {
				continue
			}
			seen[child] = true
			result = append(result, child)
		}
	}
	slices.Sort(result[1:])
	return result
}

func reviewSubtreeHeld(ids []string, observations map[string]ReviewObservation) bool {
	for _, id := range ids {
		if observations[id].TenderHolds {
			return true
		}
	}
	return false
}

func reviewLifecycleAt(observation ReviewObservation) (time.Time, bool) {
	if !observation.LifecycleAt.IsZero() {
		return observation.LifecycleAt, observation.LifecycleExact
	}
	return observation.DocumentUpdatedAt, false
}

func reviewSubtreeActivity(ids []string, observations map[string]ReviewObservation) time.Time {
	var newest time.Time
	for _, id := range ids {
		observation := observations[id]
		lifecycleAt, _ := reviewLifecycleAt(observation)
		for _, at := range []time.Time{lifecycleAt, observation.DocumentUpdatedAt, observation.NewestNoteAt} {
			if at.After(newest) {
				newest = at
			}
		}
	}
	return newest
}

func reviewOldEnough(at time.Time, window time.Duration, now time.Time) bool {
	return !at.IsZero() && !at.After(now) && now.Sub(at) >= window
}

func normalizedReviewDirectoryState(state ReviewDirectoryState) ReviewDirectoryState {
	switch state {
	case ReviewDirectoryPresent, ReviewDirectoryMissing, ReviewDirectoryRemote, ReviewDirectoryUnavailable:
		return state
	default:
		return ReviewDirectoryUnknown
	}
}
