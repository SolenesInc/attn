package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/store"
)

const (
	gardenReviewClassifyKind        = "garden_review_classify"
	gardenReviewClassifyTimeout     = 5 * time.Minute
	gardenReviewClassifyConcurrency = 2
	gardenReviewMaxAttempts         = 3
	gardenReviewNoteEvidenceLimit   = 3
	gardenReviewChildEvidenceLimit  = 6
)

type gardenReviewJobPayload struct {
	RunID           string `json:"run_id"`
	ItemID          string `json:"item_id"`
	EvidenceVersion string `json:"evidence_version"`
}

type gardenReviewNote struct {
	ID        string
	SeedID    string
	Kind      string
	Body      string
	CreatedAt time.Time
}

type gardenReviewCapture struct {
	read         gardenRead
	observations map[string]garden.ReviewObservation
	candidates   []garden.ReviewCandidate
	items        map[string]garden.ReviewItem
	notes        map[string][]gardenReviewNote
}

func (d *Daemon) captureGardenReview() (gardenReviewCapture, error) {
	read, err := d.readWholeGarden()
	if err != nil {
		return gardenReviewCapture{}, err
	}
	notes, newestNotes, err := d.readGardenReviewNotes()
	if err != nil {
		return gardenReviewCapture{}, err
	}

	observations := make([]garden.ReviewObservation, 0, len(read.seeds))
	byID := make(map[string]garden.ReviewObservation, len(read.seeds))
	for _, seed := range read.seeds {
		doc := read.docs[seed.ID]
		lifecycleAt, exact := reviewLifecycleTime(seed, doc)
		continuation := d.continuationForSeed(seed)
		directoryState := garden.ReviewDirectoryUnknown
		resumeAvailable := false
		if continuation != nil {
			directoryState = garden.ReviewDirectoryState(continuation.DirectoryState)
			resumeAvailable = continuation.ResumeAvailable
		}
		observation := garden.ReviewObservation{
			Seed:              seed,
			LifecycleAt:       lifecycleAt,
			LifecycleExact:    exact,
			DocumentUpdatedAt: doc.UpdatedAt,
			NewestNoteAt:      newestNotes[seed.ID],
			TenderHolds:       seed.Tender().Holds(d.sessionExists),
			DirectoryState:    directoryState,
			ResumeAvailable:   resumeAvailable,
		}
		observations = append(observations, observation)
		byID[seed.ID] = observation
	}

	candidates := garden.ReviewCandidates(
		observations,
		garden.DefaultStaleWindow,
		d.gardenTime(),
	)
	capture := gardenReviewCapture{
		read: read, observations: byID, candidates: candidates,
		items: make(map[string]garden.ReviewItem, len(candidates)), notes: notes,
	}
	for _, candidate := range candidates {
		item, itemErr := capture.reviewItem(candidate)
		if itemErr != nil {
			return gardenReviewCapture{}, itemErr
		}
		capture.items[candidate.SeedID] = item
	}
	return capture, nil
}

func reviewLifecycleTime(seed garden.Seed, doc docstore.Document) (time.Time, bool) {
	raw := strings.TrimSpace(seed.StateChangedAt)
	if raw == "" {
		return doc.UpdatedAt, false
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return doc.UpdatedAt, false
	}
	return at, true
}

func (d *Daemon) readWholeGarden() (gardenRead, error) {
	read := gardenRead{
		docs: make(map[string]docstore.Document), ready: make(map[string]bool),
	}
	after := ""
	for {
		page, _, err := d.runDocQuery(docstore.Query{
			Namespace: garden.Namespace, Collection: garden.CollectionSeeds,
			Sort:  &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true},
			Limit: docstore.MaxLimit, After: after,
		})
		if err != nil {
			return gardenRead{}, err
		}
		for _, doc := range page.Documents {
			seed, decodeErr := garden.Decode(doc.Body)
			if decodeErr != nil {
				d.logf("garden review: seed %s has an unreadable body: %v", doc.ID, decodeErr)
				continue
			}
			read.seeds = append(read.seeds, seed)
			read.docs[seed.ID] = doc
		}
		if len(page.Documents) < docstore.MaxLimit {
			break
		}
		after = page.Documents[len(page.Documents)-1].ID
	}
	for _, seed := range garden.Ready(read.seeds, d.sessionExists) {
		read.ready[seed.ID] = true
	}
	return read, nil
}

func (d *Daemon) readGardenReviewNotes() (map[string][]gardenReviewNote, map[string]time.Time, error) {
	bySeed := make(map[string][]gardenReviewNote)
	newest := make(map[string]time.Time)
	after := ""
	for {
		page, _, err := d.runDocQuery(docstore.Query{
			Namespace: garden.Namespace, Collection: garden.CollectionNotes,
			Sort:  &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true},
			Limit: docstore.MaxLimit, After: after,
		})
		if err != nil {
			return nil, nil, err
		}
		for _, doc := range page.Documents {
			note, decodeErr := garden.DecodeNote(doc.Body)
			if decodeErr != nil {
				d.logf("garden review: note %s has an unreadable body: %v", doc.ID, decodeErr)
				continue
			}
			entry := gardenReviewNote{
				ID: note.ID, SeedID: note.Seed, Kind: note.Kind,
				Body: note.Body, CreatedAt: doc.CreatedAt,
			}
			bySeed[note.Seed] = append(bySeed[note.Seed], entry)
			if doc.CreatedAt.After(newest[note.Seed]) {
				newest[note.Seed] = doc.CreatedAt
			}
		}
		if len(page.Documents) < docstore.MaxLimit {
			break
		}
		after = page.Documents[len(page.Documents)-1].ID
	}
	return bySeed, newest, nil
}

func (capture gardenReviewCapture) reviewItem(candidate garden.ReviewCandidate) (garden.ReviewItem, error) {
	seed, ok := capture.observations[candidate.SeedID]
	if !ok {
		return garden.ReviewItem{}, fmt.Errorf("review candidate %s has no source seed", candidate.SeedID)
	}
	evidence := capture.reviewEvidence(candidate)
	item := garden.ReviewItem{
		SeedID:     candidate.SeedID,
		SeedRev:    capture.read.docs[candidate.SeedID].Rev,
		Title:      seed.Seed.Title,
		Body:       seed.Seed.Body,
		Evidence:   evidence,
		Actions:    gardenReviewActions(candidate),
		Status:     garden.ReviewItemStatusQueued,
		Resolution: garden.ReviewResolutionUnresolved,
	}
	version, err := gardenReviewEvidenceVersion(item, candidate)
	if err != nil {
		return garden.ReviewItem{}, err
	}
	item.EvidenceVersion = version
	return item, nil
}

func gardenReviewActions(candidate garden.ReviewCandidate) []string {
	actions := make([]string, 0, 5)
	if candidate.ResumeAvailable {
		actions = append(actions, "resume")
	}
	return append(actions, "handover", "park", "harvest", "wither")
}

func (capture gardenReviewCapture) reviewEvidence(candidate garden.ReviewCandidate) []garden.ReviewEvidence {
	observation := capture.observations[candidate.SeedID]
	evidence := []garden.ReviewEvidence{
		{Label: "Review signal", Text: reviewCandidateReason(candidate)},
		{Label: "Lifecycle", Text: reviewLifecycleEvidence(candidate)},
		{Label: "Working directory", Text: reviewDirectoryEvidence(observation)},
	}
	if candidate.Plot {
		evidence = append(evidence, garden.ReviewEvidence{
			Label: "Plot activity",
			Text: fmt.Sprintf("No active claim exists in the subtree. Its newest recorded activity was %s.",
				candidate.SubtreeActivityAt.UTC().Format(time.RFC3339)),
		})
		children := candidate.SubtreeIDs[1:]
		limit := min(len(children), gardenReviewChildEvidenceLimit)
		for _, id := range children[:limit] {
			child := capture.observations[id].Seed
			evidence = append(evidence, garden.ReviewEvidence{
				Label: "Related seed " + id,
				Text:  fmt.Sprintf("%s (%s)\n%s", child.Title, child.Status, child.Body),
			})
		}
		if len(children) > limit {
			evidence = append(evidence, garden.ReviewEvidence{
				Label: "Related seeds omitted",
				Text:  fmt.Sprintf("%d more seeds are in this plot subtree.", len(children)-limit),
			})
		}
	}

	notes := capture.reviewNotesFor(candidate)
	limit := min(len(notes), gardenReviewNoteEvidenceLimit)
	for _, note := range notes[:limit] {
		evidence = append(evidence, garden.ReviewEvidence{
			Label: "Seed log " + note.CreatedAt.UTC().Format(time.RFC3339),
			Text:  note.Body,
		})
	}
	if len(notes) > limit {
		evidence = append(evidence, garden.ReviewEvidence{
			Label: "Seed log omitted",
			Text:  fmt.Sprintf("%d older relevant log entries were omitted.", len(notes)-limit),
		})
	}
	return evidence
}

func (capture gardenReviewCapture) reviewNotesFor(candidate garden.ReviewCandidate) []gardenReviewNote {
	var notes []gardenReviewNote
	for _, id := range candidate.SubtreeIDs {
		notes = append(notes, capture.notes[id]...)
	}
	slices.SortFunc(notes, func(a, b gardenReviewNote) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return notes
}

func reviewCandidateReason(candidate garden.ReviewCandidate) string {
	switch candidate.Reason {
	case garden.ReviewReasonDirectoryMissing:
		return "The saved local working directory is confirmed missing and no active agent holds the seed."
	case garden.ReviewReasonSubtreeStale:
		return "No active agent or recorded activity exists anywhere in this plot subtree within seven days."
	default:
		return "No active agent holds the seed and its lifecycle has not changed within seven days."
	}
}

func reviewLifecycleEvidence(candidate garden.ReviewCandidate) string {
	if candidate.LifecycleAt.IsZero() {
		return "No lifecycle timestamp is available."
	}
	if candidate.LifecycleExact {
		return "The lifecycle state last changed at " + candidate.LifecycleAt.UTC().Format(time.RFC3339) + "."
	}
	return "The seed predates exact lifecycle clocks. Its document update at " +
		candidate.LifecycleAt.UTC().Format(time.RFC3339) + " is used as a conservative lower bound."
}

func reviewDirectoryEvidence(observation garden.ReviewObservation) string {
	switch observation.DirectoryState {
	case garden.ReviewDirectoryPresent:
		return "The saved local working directory is present."
	case garden.ReviewDirectoryMissing:
		return "The saved local working directory is confirmed missing."
	case garden.ReviewDirectoryRemote:
		return "The saved execution belongs to a remote host; its working directory was not inspected locally."
	case garden.ReviewDirectoryUnavailable:
		return "The saved local working directory could not be verified."
	default:
		return "No verifiable working-directory context is available."
	}
}

func gardenReviewEvidenceVersion(item garden.ReviewItem, candidate garden.ReviewCandidate) (string, error) {
	payload, err := json.Marshal(struct {
		SeedRev   int64                   `json:"seed_rev"`
		Title     string                  `json:"title"`
		Body      string                  `json:"body"`
		Evidence  []garden.ReviewEvidence `json:"evidence"`
		Actions   []string                `json:"actions"`
		Candidate garden.ReviewCandidate  `json:"candidate"`
	}{item.SeedRev, item.Title, item.Body, item.Evidence, item.Actions, candidate})
	if err != nil {
		return "", fmt.Errorf("encode Garden review evidence: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func (d *Daemon) startGardenReview() (garden.ReviewRun, []garden.ReviewItem, error) {
	d.gardenReviewMu.Lock()
	defer d.gardenReviewMu.Unlock()

	if run, found, err := d.unfinishedGardenReview(); err != nil {
		return garden.ReviewRun{}, nil, err
	} else if found {
		items, itemsErr := d.readGardenReviewItems(run.ID)
		if itemsErr != nil {
			return garden.ReviewRun{}, nil, itemsErr
		}
		if err := d.enqueueGardenReviewItems(run, items); err != nil {
			return garden.ReviewRun{}, nil, err
		}
		return run, items, nil
	}

	capture, err := d.captureGardenReview()
	if err != nil {
		return garden.ReviewRun{}, nil, err
	}
	config, err := d.gardenAdvisorConfig()
	if err != nil {
		return garden.ReviewRun{}, nil, err
	}
	now := d.gardenTime()
	run := garden.ReviewRun{
		ID:         "r-" + uuid.NewString(),
		Recipe:     garden.ReviewRecipe{Agent: config.Agent, Model: config.Model, Effort: config.Effort},
		Status:     garden.ReviewRunStatusRunning,
		CapturedAt: formatGardenTime(now),
	}
	items := make([]garden.ReviewItem, 0, len(capture.candidates))
	for _, candidate := range capture.candidates {
		item := capture.items[candidate.SeedID]
		item.RunID = run.ID
		item.ID = garden.ReviewItemID(run.ID, item.SeedID)
		run.CandidateIDs = append(run.CandidateIDs, item.SeedID)
		items = append(items, item)
	}
	if len(items) == 0 {
		run.Status = garden.ReviewRunStatusComplete
		run.CompletedAt = formatGardenTime(now)
	}
	if err := d.createGardenReview(run, items); err != nil {
		return garden.ReviewRun{}, nil, err
	}
	if err := d.enqueueGardenReviewItems(run, items); err != nil {
		return run, items, err
	}
	return run, items, nil
}

func (d *Daemon) createGardenReview(run garden.ReviewRun, items []garden.ReviewItem) error {
	runSchema, err := d.reviewRunsCollection()
	if err != nil {
		return err
	}
	itemSchema, err := d.reviewItemsCollection()
	if err != nil {
		return err
	}
	runBody, err := run.Encode()
	if err != nil {
		return err
	}
	absent := int64(docstore.ExpectAbsent)
	commits := []store.DocumentCommit{{
		Write: store.DocumentWrite{Schema: *runSchema, ID: run.ID, Body: runBody, Expected: &absent},
		Fact:  documentChangedFact(garden.Namespace, garden.CollectionReviewRuns, run.ID, false),
	}}
	for _, item := range items {
		body, encodeErr := item.Encode()
		if encodeErr != nil {
			return encodeErr
		}
		commits = append(commits, store.DocumentCommit{
			Write: store.DocumentWrite{Schema: *itemSchema, ID: item.ID, Body: body, Expected: &absent},
			Fact:  documentChangedFact(garden.Namespace, garden.CollectionReviewItems, item.ID, false),
		})
	}
	written, err := d.store.CommitDocumentWrites(commits, d.gardenTime())
	if err != nil {
		return err
	}
	for i, commit := range commits {
		d.announceCommittedWrite(commit.Fact, written[i].Seq)
	}
	d.publishFact(FactGardenReviewChanged, run.ID, nil)
	return nil
}

func (d *Daemon) enqueueGardenReviewItems(run garden.ReviewRun, items []garden.ReviewItem) error {
	if run.Status != garden.ReviewRunStatusRunning {
		return nil
	}
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return errors.New("background job queue is unavailable")
	}
	for _, item := range items {
		if item.Resolution != garden.ReviewResolutionUnresolved ||
			(item.Status != garden.ReviewItemStatusQueued && item.Status != garden.ReviewItemStatusRunning) {
			continue
		}
		_, err := runner.Enqueue(gardenReviewClassifyKind, jobs.EnqueueOptions{
			UniqueKey: item.ID,
			Payload: gardenReviewJobPayload{
				RunID: run.ID, ItemID: item.ID, EvidenceVersion: item.EvidenceVersion,
			},
			MaxAttempts: gardenReviewMaxAttempts,
		})
		if err != nil {
			return fmt.Errorf("queue Garden review item %s: %w", item.SeedID, err)
		}
	}
	return nil
}

func (d *Daemon) resumeGardenReviews() {
	runs, err := d.runningGardenReviews()
	if err != nil {
		d.logf("garden review: find unfinished runs after restart: %v", err)
		return
	}
	for _, run := range runs {
		items, readErr := d.readGardenReviewItems(run.ID)
		if readErr != nil {
			d.logf("garden review: read unfinished run %s after restart: %v", run.ID, readErr)
			continue
		}
		if enqueueErr := d.enqueueGardenReviewItems(run, items); enqueueErr != nil {
			d.logf("garden review: resume run %s: %v", run.ID, enqueueErr)
		}
	}
}

func (d *Daemon) gardenReviewClassifyHandler(ctx context.Context, job *jobs.Job) (any, error) {
	var payload gardenReviewJobPayload
	if err := job.DecodePayload(&payload); err != nil {
		return nil, err
	}
	run, _, found, err := d.readGardenReviewRun(payload.RunID)
	if err != nil {
		return nil, err
	}
	if !found || run.Status != garden.ReviewRunStatusRunning {
		return nil, nil
	}
	item, _, found, err := d.readGardenReviewItem(payload.ItemID)
	if err != nil {
		return nil, err
	}
	if !found || item.Resolution != garden.ReviewResolutionUnresolved ||
		item.EvidenceVersion != payload.EvidenceVersion {
		return nil, nil
	}
	before, err := d.captureGardenReview()
	if err != nil {
		return nil, err
	}
	if current, candidate := before.items[item.SeedID]; !candidate || current.EvidenceVersion != item.EvidenceVersion {
		next := invalidatedGardenReviewItem(item, candidate, d.gardenTime())
		if !job.CommitGuard.Enter() {
			return nil, context.Canceled
		}
		defer job.CommitGuard.Leave()
		return nil, d.finishGardenReviewItem(payload, next)
	}

	advice, err := d.adviseGardenSeedWithConfig(ctx, gardenAdvisorInput{
		SeedID: item.SeedID, Title: item.Title, Body: item.Body,
		Evidence: gardenAdvisorEvidenceFromReview(item.Evidence), AvailableActions: item.Actions,
	}, gardenAdvisorConfig{Agent: run.Recipe.Agent, Model: run.Recipe.Model, Effort: run.Recipe.Effort})
	if err != nil {
		return nil, err
	}

	capture, err := d.captureGardenReview()
	if err != nil {
		return nil, err
	}
	current, stillCandidate := capture.items[item.SeedID]
	next := item
	next.CompletedAt = formatGardenTime(d.gardenTime())
	switch {
	case !stillCandidate:
		next = invalidatedGardenReviewItem(item, false, d.gardenTime())
	case current.EvidenceVersion != item.EvidenceVersion:
		next = invalidatedGardenReviewItem(item, true, d.gardenTime())
	default:
		next.Status = garden.ReviewItemStatusReady
		next.Recommendation = advice.Recommendation
		next.Explanation = advice.Explanation
		next.CitedEvidence = advice.Evidence
		next.Error = ""
	}

	if !job.CommitGuard.Enter() {
		return nil, context.Canceled
	}
	defer job.CommitGuard.Leave()
	if err := d.finishGardenReviewItem(payload, next); err != nil {
		return nil, err
	}
	return struct {
		ItemID string `json:"item_id"`
		Status string `json:"status"`
	}{next.ID, next.Status}, nil
}

func invalidatedGardenReviewItem(item garden.ReviewItem, stillCandidate bool, now time.Time) garden.ReviewItem {
	item.CompletedAt = formatGardenTime(now)
	item.Recommendation = ""
	item.Explanation = ""
	item.CitedEvidence = nil
	if !stillCandidate {
		item.Status = garden.ReviewItemStatusReady
		item.Resolution = garden.ReviewResolutionNoLongerApplicable
		item.Error = "The seed no longer needs review."
		return item
	}
	item.Status = garden.ReviewItemStatusInvalidated
	item.Error = "The seed or its evidence changed during classification."
	return item
}

func gardenAdvisorEvidenceFromReview(evidence []garden.ReviewEvidence) []gardenAdvisorEvidence {
	out := make([]gardenAdvisorEvidence, 0, len(evidence))
	for _, entry := range evidence {
		out = append(out, gardenAdvisorEvidence{Label: entry.Label, Text: entry.Text})
	}
	return out
}

func (d *Daemon) finishGardenReviewItem(payload gardenReviewJobPayload, next garden.ReviewItem) error {
	d.gardenReviewMu.Lock()
	defer d.gardenReviewMu.Unlock()

	run, runDoc, found, err := d.readGardenReviewRun(payload.RunID)
	if err != nil || !found {
		return err
	}
	if run.Status != garden.ReviewRunStatusRunning {
		return nil
	}
	current, itemDoc, found, err := d.readGardenReviewItem(payload.ItemID)
	if err != nil || !found {
		return err
	}
	if current.EvidenceVersion != payload.EvidenceVersion ||
		current.Resolution != garden.ReviewResolutionUnresolved {
		return nil
	}
	next.ID, next.RunID = current.ID, current.RunID
	items, err := d.readGardenReviewItems(run.ID)
	if err != nil {
		return err
	}
	for i := range items {
		if items[i].ID == next.ID {
			items[i] = next
		}
	}
	complete := gardenReviewItemsResolved(items)
	if complete {
		run.Status = garden.ReviewRunStatusComplete
		run.CompletedAt = formatGardenTime(d.gardenTime())
	}
	return d.updateGardenReviewRecords(run, runDoc, next, itemDoc, complete)
}

func gardenReviewItemsResolved(items []garden.ReviewItem) bool {
	for _, item := range items {
		if item.Resolution == garden.ReviewResolutionUnresolved {
			return false
		}
	}
	return true
}

func (d *Daemon) updateGardenReviewRecords(
	run garden.ReviewRun,
	runDoc docstore.Document,
	item garden.ReviewItem,
	itemDoc docstore.Document,
	writeRun bool,
) error {
	itemSchema, err := d.reviewItemsCollection()
	if err != nil {
		return err
	}
	itemBody, err := item.Encode()
	if err != nil {
		return err
	}
	itemExpected := itemDoc.Rev
	commits := []store.DocumentCommit{{
		Write: store.DocumentWrite{Schema: *itemSchema, ID: item.ID, Body: itemBody, Expected: &itemExpected},
		Fact:  documentChangedFact(garden.Namespace, garden.CollectionReviewItems, item.ID, false),
	}}
	if writeRun {
		runSchema, schemaErr := d.reviewRunsCollection()
		if schemaErr != nil {
			return schemaErr
		}
		runBody, encodeErr := run.Encode()
		if encodeErr != nil {
			return encodeErr
		}
		runExpected := runDoc.Rev
		commits = append(commits, store.DocumentCommit{
			Write: store.DocumentWrite{Schema: *runSchema, ID: run.ID, Body: runBody, Expected: &runExpected},
			Fact:  documentChangedFact(garden.Namespace, garden.CollectionReviewRuns, run.ID, false),
		})
	}
	written, err := d.store.CommitDocumentWrites(commits, d.gardenTime())
	if err != nil {
		return err
	}
	for i, commit := range commits {
		d.announceCommittedWrite(commit.Fact, written[i].Seq)
	}
	d.publishFact(FactGardenReviewChanged, run.ID, nil)
	return nil
}

func (d *Daemon) failGardenReviewJob(job *jobs.Job) {
	if job == nil || job.Kind != gardenReviewClassifyKind {
		return
	}
	d.gardenReviewMu.Lock()
	defer d.gardenReviewMu.Unlock()

	var payload gardenReviewJobPayload
	if err := job.DecodePayload(&payload); err != nil {
		d.logf("garden review: decode failed job %s: %v", job.ID, err)
		return
	}
	item, itemDoc, found, err := d.readGardenReviewItem(payload.ItemID)
	if err != nil || !found || item.EvidenceVersion != payload.EvidenceVersion {
		return
	}
	run, runDoc, found, err := d.readGardenReviewRun(payload.RunID)
	if err != nil || !found || run.Status != garden.ReviewRunStatusRunning {
		return
	}
	item.Status = garden.ReviewItemStatusFailed
	item.Error = job.LastError
	item.CompletedAt = formatGardenTime(d.gardenTime())
	items, err := d.readGardenReviewItems(run.ID)
	if err != nil {
		d.logf("garden review: read items after terminal failure: %v", err)
		return
	}
	for i := range items {
		if items[i].ID == item.ID {
			items[i] = item
		}
	}
	complete := gardenReviewItemsResolved(items)
	if complete {
		run.Status = garden.ReviewRunStatusComplete
		run.CompletedAt = formatGardenTime(d.gardenTime())
	}
	if err := d.updateGardenReviewRecords(run, runDoc, item, itemDoc, complete); err != nil {
		d.logf("garden review: record terminal failure for %s: %v", item.SeedID, err)
	}
}

func (d *Daemon) unfinishedGardenReview() (garden.ReviewRun, bool, error) {
	runs, err := d.runningGardenReviews()
	if err != nil {
		return garden.ReviewRun{}, false, err
	}
	if len(runs) == 0 {
		return garden.ReviewRun{}, false, nil
	}
	return runs[0], true, nil
}

func (d *Daemon) runningGardenReviews() ([]garden.ReviewRun, error) {
	runs := make([]garden.ReviewRun, 0)
	after := ""
	for {
		read, _, err := d.runDocQuery(docstore.Query{
			Namespace: garden.Namespace, Collection: garden.CollectionReviewRuns,
			Filters: []docstore.Filter{{Field: "status", Op: docstore.OpEq, Value: garden.ReviewRunStatusRunning}},
			Sort:    &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true},
			Limit:   docstore.MaxLimit, After: after,
		})
		if err != nil {
			return nil, err
		}
		for _, doc := range read.Documents {
			run, decodeErr := garden.DecodeReviewRun(doc.Body)
			if decodeErr != nil {
				return nil, decodeErr
			}
			runs = append(runs, run)
		}
		if len(read.Documents) < docstore.MaxLimit {
			return runs, nil
		}
		after = read.Documents[len(read.Documents)-1].ID
	}
}

func (d *Daemon) readGardenReviewRun(id string) (garden.ReviewRun, docstore.Document, bool, error) {
	schema, err := d.reviewRunsCollection()
	if err != nil {
		return garden.ReviewRun{}, docstore.Document{}, false, err
	}
	doc, found, err := d.store.GetDocument(*schema, strings.TrimSpace(id))
	if err != nil || !found {
		return garden.ReviewRun{}, docstore.Document{}, found, err
	}
	run, err := garden.DecodeReviewRun(doc.Body)
	return run, *doc, true, err
}

func (d *Daemon) readGardenReviewItem(id string) (garden.ReviewItem, docstore.Document, bool, error) {
	schema, err := d.reviewItemsCollection()
	if err != nil {
		return garden.ReviewItem{}, docstore.Document{}, false, err
	}
	doc, found, err := d.store.GetDocument(*schema, strings.TrimSpace(id))
	if err != nil || !found {
		return garden.ReviewItem{}, docstore.Document{}, found, err
	}
	item, err := garden.DecodeReviewItem(doc.Body)
	return item, *doc, true, err
}

func (d *Daemon) readGardenReviewItems(runID string) ([]garden.ReviewItem, error) {
	items := make([]garden.ReviewItem, 0)
	after := ""
	for {
		read, _, err := d.runDocQuery(docstore.Query{
			Namespace: garden.Namespace, Collection: garden.CollectionReviewItems,
			Filters: []docstore.Filter{{Field: "run_id", Op: docstore.OpEq, Value: strings.TrimSpace(runID)}},
			Sort:    &docstore.Sort{Field: docstore.FieldCreatedAt},
			Limit:   docstore.MaxLimit, After: after,
		})
		if err != nil {
			return nil, err
		}
		for _, doc := range read.Documents {
			item, decodeErr := garden.DecodeReviewItem(doc.Body)
			if decodeErr != nil {
				return nil, decodeErr
			}
			items = append(items, item)
		}
		if len(read.Documents) < docstore.MaxLimit {
			break
		}
		after = read.Documents[len(read.Documents)-1].ID
	}
	return items, nil
}

func (d *Daemon) showGardenReview(runID string) (garden.ReviewRun, []garden.ReviewItem, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		read, _, err := d.runDocQuery(docstore.Query{
			Namespace: garden.Namespace, Collection: garden.CollectionReviewRuns,
			Sort: &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: true}, Limit: 1,
		})
		if err != nil {
			return garden.ReviewRun{}, nil, err
		}
		if len(read.Documents) == 0 {
			return garden.ReviewRun{}, nil, errors.New("no Garden review has been started")
		}
		runID = read.Documents[0].ID
	}
	run, _, found, err := d.readGardenReviewRun(runID)
	if err != nil {
		return garden.ReviewRun{}, nil, err
	}
	if !found {
		return garden.ReviewRun{}, nil, fmt.Errorf("no Garden review %s exists", runID)
	}
	items, err := d.readGardenReviewItems(run.ID)
	if err != nil {
		return garden.ReviewRun{}, nil, err
	}
	return run, d.overlayGardenReviewJobStates(items), nil
}

func (d *Daemon) gardenReviewOverview() (*garden.ReviewRun, []garden.ReviewItem, int, error) {
	run, found, err := d.unfinishedGardenReview()
	if err != nil {
		return nil, nil, 0, err
	}
	if found {
		items, readErr := d.readGardenReviewItems(run.ID)
		if readErr != nil {
			return nil, nil, 0, readErr
		}
		items = d.overlayGardenReviewJobStates(items)
		return &run, items, unresolvedGardenReviewItemCount(items), nil
	}
	capture, err := d.captureGardenReview()
	if err != nil {
		return nil, nil, 0, err
	}
	return nil, nil, len(capture.candidates), nil
}

func unresolvedGardenReviewItemCount(items []garden.ReviewItem) int {
	count := 0
	for _, item := range items {
		if item.Resolution == garden.ReviewResolutionUnresolved {
			count++
		}
	}
	return count
}

func (d *Daemon) overlayGardenReviewJobStates(items []garden.ReviewItem) []garden.ReviewItem {
	runner := d.jobQueueRef()
	if runner == nil || runner.Disabled() {
		return items
	}
	for i := range items {
		if items[i].Status != garden.ReviewItemStatusQueued {
			continue
		}
		job, err := runner.GetByKey(gardenReviewClassifyKind, items[i].ID)
		if err == nil && job != nil && job.State == jobs.StateRunning {
			items[i].Status = garden.ReviewItemStatusRunning
			items[i].StartedAt = formatGardenTime(job.UpdatedAt)
		}
	}
	return items
}

func (d *Daemon) cancelGardenReview(runID string) (garden.ReviewRun, []garden.ReviewItem, error) {
	d.gardenReviewMu.Lock()
	defer d.gardenReviewMu.Unlock()
	run, doc, found, err := d.readGardenReviewRun(strings.TrimSpace(runID))
	if err != nil {
		return garden.ReviewRun{}, nil, err
	}
	if !found {
		return garden.ReviewRun{}, nil, fmt.Errorf("no Garden review %s exists", strings.TrimSpace(runID))
	}
	items, err := d.readGardenReviewItems(run.ID)
	if err != nil {
		return garden.ReviewRun{}, nil, err
	}
	if run.Status == garden.ReviewRunStatusCanceled {
		return run, items, nil
	}
	if run.Status == garden.ReviewRunStatusComplete {
		return garden.ReviewRun{}, nil, fmt.Errorf("review %s is already complete", run.ID)
	}
	run.Status = garden.ReviewRunStatusCanceled
	run.CompletedAt = formatGardenTime(d.gardenTime())
	if err := d.writeGardenReviewRun(run, doc); err != nil {
		return garden.ReviewRun{}, nil, err
	}
	if runner := d.jobQueueRef(); runner != nil {
		for _, item := range items {
			runner.RemoveByKey(gardenReviewClassifyKind, item.ID)
		}
	}
	return run, items, nil
}

func (d *Daemon) retryGardenReviewItem(runID, seedID string) (garden.ReviewRun, garden.ReviewItem, error) {
	d.gardenReviewMu.Lock()
	defer d.gardenReviewMu.Unlock()
	run, runDoc, found, err := d.readGardenReviewRun(strings.TrimSpace(runID))
	if err != nil {
		return garden.ReviewRun{}, garden.ReviewItem{}, err
	}
	if !found {
		return garden.ReviewRun{}, garden.ReviewItem{}, fmt.Errorf("no Garden review %s exists", strings.TrimSpace(runID))
	}
	if run.Status == garden.ReviewRunStatusCanceled {
		return garden.ReviewRun{}, garden.ReviewItem{}, fmt.Errorf("review %s was canceled", run.ID)
	}
	itemID := garden.ReviewItemID(run.ID, strings.TrimSpace(seedID))
	item, itemDoc, found, err := d.readGardenReviewItem(itemID)
	if err != nil {
		return garden.ReviewRun{}, garden.ReviewItem{}, err
	}
	if !found {
		return garden.ReviewRun{}, garden.ReviewItem{}, fmt.Errorf("seed %s is not part of Garden review %s", strings.TrimSpace(seedID), run.ID)
	}
	if item.Status != garden.ReviewItemStatusFailed && item.Status != garden.ReviewItemStatusInvalidated {
		return garden.ReviewRun{}, garden.ReviewItem{}, fmt.Errorf(
			"seed %s is %s in Garden review %s; only failed or changed items can be retried",
			item.SeedID, item.Status, run.ID)
	}

	capture, err := d.captureGardenReview()
	if err != nil {
		return garden.ReviewRun{}, garden.ReviewItem{}, err
	}
	current, candidate := capture.items[item.SeedID]
	if !candidate {
		item.Status = garden.ReviewItemStatusReady
		item.Resolution = garden.ReviewResolutionNoLongerApplicable
		item.Error = "The seed no longer needs review."
		item.CompletedAt = formatGardenTime(d.gardenTime())
		items, readErr := d.readGardenReviewItems(run.ID)
		if readErr != nil {
			return garden.ReviewRun{}, garden.ReviewItem{}, readErr
		}
		for i := range items {
			if items[i].ID == item.ID {
				items[i] = item
			}
		}
		complete := gardenReviewItemsResolved(items)
		if complete {
			run.Status = garden.ReviewRunStatusComplete
			run.CompletedAt = formatGardenTime(d.gardenTime())
		}
		if err := d.updateGardenReviewRecords(run, runDoc, item, itemDoc, complete); err != nil {
			return garden.ReviewRun{}, garden.ReviewItem{}, err
		}
		return run, item, nil
	}
	current.ID, current.RunID = item.ID, run.ID
	run.Status = garden.ReviewRunStatusRunning
	run.CompletedAt = ""
	if err := d.updateGardenReviewRecords(run, runDoc, current, itemDoc, true); err != nil {
		return garden.ReviewRun{}, garden.ReviewItem{}, err
	}
	if err := d.enqueueGardenReviewItems(run, []garden.ReviewItem{current}); err != nil {
		return run, current, err
	}
	return run, current, nil
}

func (d *Daemon) writeGardenReviewRun(run garden.ReviewRun, doc docstore.Document) error {
	schema, err := d.reviewRunsCollection()
	if err != nil {
		return err
	}
	body, err := run.Encode()
	if err != nil {
		return err
	}
	expected := doc.Rev
	fact := documentChangedFact(garden.Namespace, garden.CollectionReviewRuns, run.ID, false)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: *schema, ID: run.ID, Body: body, Expected: &expected,
	}, fact, d.gardenTime())
	if err != nil {
		return err
	}
	d.announceCommittedWrite(fact, written.Seq)
	d.publishFact(FactGardenReviewChanged, run.ID, nil)
	return nil
}

func (d *Daemon) reviewRunsCollection() (*docstore.CollectionSchema, error) {
	return d.collectionFor(garden.Namespace, garden.CollectionReviewRuns)
}

func (d *Daemon) reviewItemsCollection() (*docstore.CollectionSchema, error) {
	return d.collectionFor(garden.Namespace, garden.CollectionReviewItems)
}
