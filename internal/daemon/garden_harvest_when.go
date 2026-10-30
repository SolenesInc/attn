package daemon

import (
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	seedEvents "github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const harvestWhenActor = crew.DaemonID

const harvestWhenClearedNote = "harvest-on-merge cleared"

func harvestWhenRequested(msg *protocol.SeedTransitionMessage) bool {
	return msg.WhenMerged != nil || protocol.Deref(msg.ClearHarvestWhen)
}

func (d *Daemon) applyHarvestWhenRequest(
	msg *protocol.SeedTransitionMessage, verb garden.Verb, ask garden.Ask, sessionID protocol.SessionID,
) (garden.Seed, docstore.Document, seedTransitionDetails, error) {
	seedID := strings.TrimSpace(msg.SeedID)
	if verb != garden.VerbHarvest {
		return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, fmt.Errorf(
			"harvest is the move that waits on a pull request: attn seed harvest %s --when-merged", seedID)
	}
	if strings.TrimSpace(protocol.Deref(msg.Comment)) != "" {
		return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, fmt.Errorf(
			"a lifecycle comment belongs to Park; use `attn seed note %s -m \"…\"` for %s", seedID, verb)
	}
	if protocol.Deref(msg.ClearHarvestWhen) {
		if strings.TrimSpace(ask.Reason) != "" {
			return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, fmt.Errorf(
				"--clear takes no reason; put it on the log with `attn seed note %s -m \"…\"`", seedID)
		}
		if msg.WhenMerged != nil && strings.TrimSpace(protocol.Deref(msg.WhenMerged.PullRequestURL)) != "" {
			return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, fmt.Errorf(
				"--clear drops the condition %s carries and takes no pull request url", seedID)
		}
		seed, doc, err := d.clearHarvestWhenRequested(seedID, ask, sessionID)
		details := seedTransitionDetails{Moved: err == nil}
		if err == nil {
			details.RingRecipients, err = d.seedParties(seed.ID, sessionID)
		}
		return seed, doc, details, err
	}
	return d.armHarvestWhenMerged(seedID, protocol.Deref(msg.WhenMerged.PullRequestURL), ask, sessionID)
}

func (d *Daemon) armHarvestWhenMerged(
	seedID, url string, ask garden.Ask, sessionID protocol.SessionID,
) (garden.Seed, docstore.Document, seedTransitionDetails, error) {
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, err
	}
	if seed.Status == garden.StatusHarvested {
		return d.applySeedTransitionDetailed(seed.ID, garden.VerbHarvest, ask, "")
	}
	if garden.Closed(seed.Status) {
		return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, fmt.Errorf(
			"%s is %s and waits on nothing; replant it first: attn seed replant %s", seed.ID, seed.Status, seed.ID)
	}
	if err := garden.ValidateReason(ask.Reason, seed.ID); err != nil {
		return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, err
	}
	rec, err := d.harvestWhenPullRequest(seed.ID, url, sessionID)
	if err != nil {
		return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, err
	}
	if rec.State == sessionPullRequestMerged {
		reason := strings.TrimSpace(ask.Reason)
		if reason == "" {
			reason = harvestWhenMergedReason(rec)
		}
		return d.applySeedTransitionDetailed(seed.ID, garden.VerbHarvest, garden.Ask{
			Actor: ask.Actor, Reason: reason, Force: ask.Force, CauseSession: sessionID,
		}, "")
	}
	if rec.State == sessionPullRequestClosed {
		return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, fmt.Errorf(
			"%s closed without merging, so it never will; %s has nothing to wait for", harvestWhenLabel(rec), seed.ID)
	}
	condition := garden.HarvestCondition{
		Reason:       strings.TrimSpace(ask.Reason),
		PullRequest:  rec.PRID,
		URL:          rec.URL,
		SetAt:        formatGardenTime(d.gardenTime()),
		SetBySession: protocol.TrimID(ask.Actor.Session),
		SetByMember:  strings.TrimSpace(ask.Actor.Member),
	}
	if err := garden.ValidateHarvestCondition(condition); err != nil {
		return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, err
	}

	schema, err := d.seedsCollection()
	if err != nil {
		return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, err
	}
	seedID = seed.ID
	const attempts = 3
	for range attempts {
		seed, doc, err := d.readSeed(seedID)
		if err != nil {
			return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, err
		}
		if seed.Status == garden.StatusHarvested {
			return d.applySeedTransitionDetailed(seed.ID, garden.VerbHarvest, ask, "")
		}
		if garden.Closed(seed.Status) {
			return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, fmt.Errorf(
				"%s is %s and waits on nothing; replant it first: attn seed replant %s", seed.ID, seed.Status, seed.ID)
		}
		next := seed
		next.HarvestWhen = &condition
		occurrences := []seedEvents.Occurrence{}
		var outcome garden.Outcome
		if seed.Status == garden.StatusGrowing {
			outcome, err = garden.Transition(next, garden.VerbPark, garden.Ask{Actor: ask.Actor, Force: ask.Force}, gardenStanding{daemon: d, profileID: seed.ProfileID, live: d.sessionActive})
			if err != nil {
				return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, err
			}
			next = outcome.Seed
			next.StateChangedAt = formatGardenTime(d.gardenTime())
			parked, eventErr := gardenSeedLifecycleOccurrence(garden.VerbPark, seed.ID, sessionID)
			if eventErr != nil {
				return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, eventErr
			}
			occurrences = append(occurrences, parked)
		}
		configured, eventErr := seedEvents.Occur(
			gardenSeedEventModel, gardenSeedEventVocabulary.HarvestWhenConfigured, seed.ID,
			seedEvents.HarvestWhenPayload{PullRequestID: condition.PullRequest, CausedBySessionID: sessionID},
		)
		if eventErr != nil {
			return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, eventErr
		}
		occurrences = append(occurrences, configured)

		notes := make([]garden.Note, 0, 3)
		if outcome.Displaced != nil {
			notes = append(notes, d.harvestWhenNote(
				seed.ID, d.seedMoveAuditBody(seed, garden.VerbPark, ask.Actor, *outcome.Displaced, outcome.By), ask.Actor))
		}
		notes = append(notes, d.harvestWhenNote(seed.ID, harvestWhenArmedNote(rec), ask.Actor))
		if attachment, ok := d.harvestWhenAttachment(seed.ID, rec, ask.Actor); ok {
			notes = append(notes, attachment)
		}

		d.lockGardenRoles()
		written, writtenNotes, err := d.writeSeedMoveWithNotes(*schema, next, doc.Rev, occurrences, notes, false)
		if err == nil {
			err = d.discardAllIneligibleGardenSeedBellsLocked()
		}
		d.unlockGardenRoles()
		if err != nil {
			if docstore.IsConflict(err) {
				continue
			}
			return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, err
		}
		settled, doc, err := d.settleFreshlyArmed(next, written, sessionID)
		details := seedTransitionDetails{Moved: true}
		if outcome.Displaced != nil && len(writtenNotes) > 0 {
			details.Audit = &writtenNotes[0]
		}
		if err == nil {
			details.RingRecipients, err = d.seedParties(settled.ID, sessionID)
		}
		return settled, doc, details, err
	}
	return garden.Seed{}, docstore.Document{}, seedTransitionDetails{}, fmt.Errorf(
		"%s was rewritten under all %d attempts to arm it; read it again with `attn seed show %s` and decide from what it says now",
		seedID, attempts, seedID)
}

func (d *Daemon) settleFreshlyArmed(
	seed garden.Seed, written docstore.Document, sessionID protocol.SessionID,
) (garden.Seed, docstore.Document, error) {
	rec, ok := d.store.SessionPullRequestByID(seed.HarvestWhen.PullRequest)
	if !ok {
		return seed, written, nil
	}
	switch rec.State {
	case sessionPullRequestMerged:
		return d.fulfilHarvestWhen(seed, rec, seed.HarvestWhen, string(sessionID))
	case sessionPullRequestClosed:
		cleared, doc, err := d.clearHarvestWhen(seed.ID, seed.HarvestWhen,
			harvestWhenClosedNote(rec), garden.Tender{Member: harvestWhenActor}, string(sessionID))
		if err != nil {
			return garden.Seed{}, docstore.Document{}, err
		}
		return cleared, doc, nil
	}
	return seed, written, nil
}

func sameHarvestCondition(current, observed *garden.HarvestCondition) bool {
	return current != nil && observed != nil &&
		current.PullRequest == observed.PullRequest && current.SetAt == observed.SetAt
}

func (d *Daemon) observedHarvestCondition(seedID string, observed *garden.HarvestCondition) (garden.Seed, docstore.Document, error) {
	seed, doc, err := d.readSeed(seedID)
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	if seed.HarvestWhen == nil {
		return garden.Seed{}, docstore.Document{}, fmt.Errorf("%s has no harvest condition", seed.ID)
	}
	if observed != nil && !sameHarvestCondition(seed.HarvestWhen, observed) {
		return garden.Seed{}, docstore.Document{}, fmt.Errorf(
			"%s now waits on %s set at %s, not the condition that was read", seed.ID, seed.HarvestWhen.PullRequest, seed.HarvestWhen.SetAt)
	}
	return seed, doc, nil
}

func (d *Daemon) fulfilHarvestWhen(
	seed garden.Seed, rec store.SessionPullRequestRecord, observed *garden.HarvestCondition, excludedSessions ...string,
) (garden.Seed, docstore.Document, error) {
	ask := garden.Ask{
		Actor: garden.Tender{Member: harvestWhenActor}, Force: true,
		CauseSession: protocol.SessionID(firstString(excludedSessions)),
	}
	var harvested garden.Seed
	var doc docstore.Document
	const attempts = 3
	for attempt := range attempts {
		current, read, err := d.readSeed(seed.ID)
		if err == nil && observed != nil {
			current, read, err = d.observedHarvestCondition(seed.ID, observed)
		}
		if err != nil {
			return garden.Seed{}, docstore.Document{}, err
		}
		ask.Reason = harvestWhenMergedReason(rec)
		if current.HarvestWhen != nil && current.HarvestWhen.Reason != "" {
			ask.Reason = current.HarvestWhen.Reason
		}
		harvested, doc, _, err = d.applySeedTransitionDetailedAsAtRevision(
			current.ID, garden.VerbHarvest, ask, "", d.sessionActive, read.Rev)
		if errors.Is(err, errSeedRevisionMoved) && attempt+1 < attempts {
			continue
		}
		if err != nil {
			return garden.Seed{}, docstore.Document{}, err
		}
		break
	}

	return harvested, doc, nil
}

func (d *Daemon) clearHarvestWhen(
	seedID string, observed *garden.HarvestCondition, noteBody string, actor garden.Tender, causedBy ...string,
) (garden.Seed, docstore.Document, error) {
	schema, err := d.seedsCollection()
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	const attempts = 3
	for range attempts {
		seed, doc, err := d.observedHarvestCondition(seedID, observed)
		if err != nil {
			return garden.Seed{}, docstore.Document{}, err
		}
		next := seed
		next.HarvestWhen = nil
		cause := protocol.SessionID(firstString(causedBy))
		if cause == "" {
			cause = protocol.TrimID(actor.Session)
		}
		cleared, eventErr := seedEvents.Occur(
			gardenSeedEventModel, gardenSeedEventVocabulary.HarvestWhenCleared, seed.ID,
			seedEvents.HarvestWhenPayload{
				PullRequestID: seed.HarvestWhen.PullRequest, CausedBySessionID: cause,
			},
		)
		if eventErr != nil {
			return garden.Seed{}, docstore.Document{}, eventErr
		}
		d.lockGardenRoles()
		written, _, err := d.writeSeedMoveWithNotes(*schema, next, doc.Rev, []seedEvents.Occurrence{cleared},
			[]garden.Note{d.harvestWhenNote(seed.ID, noteBody, actor)}, false)
		if err == nil {
			err = d.discardAllIneligibleGardenSeedBellsLocked()
		}
		d.unlockGardenRoles()
		if err != nil {
			if docstore.IsConflict(err) {
				continue
			}
			return garden.Seed{}, docstore.Document{}, err
		}
		return next, written, nil
	}
	return garden.Seed{}, docstore.Document{}, fmt.Errorf(
		"%s was rewritten under all %d attempts to clear its harvest condition; read it again with `attn seed show %s`",
		seedID, attempts, seedID)
}

func (d *Daemon) clearHarvestWhenRequested(
	seedID string, ask garden.Ask, sessionID protocol.SessionID,
) (garden.Seed, docstore.Document, error) {
	seed, doc, err := d.clearHarvestWhen(seedID, nil, harvestWhenClearedNote, ask.Actor, string(sessionID))
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}

	return seed, doc, nil
}

func (d *Daemon) harvestWhenNote(seedID, body string, actor garden.Tender) garden.Note {
	return garden.Note{
		Seed: seedID, Kind: garden.NoteKindNote, Body: body,
		AuthorSession: protocol.TrimID(actor.Session), AuthorMember: strings.TrimSpace(actor.Member),
	}
}

func (d *Daemon) harvestWhenAttachment(
	seedID string, rec store.SessionPullRequestRecord, actor garden.Tender,
) (garden.Note, bool) {
	artifact := garden.ArtifactReference{Kind: garden.ArtifactURL, URL: rec.URL}
	validated, err := garden.ValidateArtifact(artifact)
	if err != nil {
		d.logf("garden: the pull request of %s is not a reference: %v", seedID, err)
		return garden.Note{}, false
	}
	notes, err := d.readNotesDomain(seedID)
	if err != nil {
		d.logf("garden: reading the references of %s: %v", seedID, err)
		return garden.Note{}, false
	}
	for _, held := range garden.CurrentArtifacts(notes) {
		if held.Identity() == validated.Identity() {
			return garden.Note{}, false
		}
	}
	note := d.harvestWhenNote(seedID, garden.DefaultNoteBody(garden.NoteKindAttach, validated), actor)
	note.Kind = garden.NoteKindAttach
	note.Artifact = &validated
	return note, true
}

func (d *Daemon) harvestWhenPullRequest(seedID string, url string, sessionID protocol.SessionID) (store.SessionPullRequestRecord, error) {
	sessionID = protocol.TrimID(sessionID)
	if sessionID == "" {
		return store.SessionPullRequestRecord{}, fmt.Errorf(
			"harvest --when-merged needs a session to track the pull request; run `attn seed harvest %s --when-merged <pr-url>` from an attn session", seedID)
	}
	if url = strings.TrimSpace(url); url != "" {
		named, err := d.sessionPullRequestIdentity(sessionID, url)
		if err != nil {
			return store.SessionPullRequestRecord{}, err
		}
		if held, ok := sessionPullRequestByID(d.store.ListSessionPullRequests(sessionID), named.PRID); ok {
			return held, nil
		}
		if err := d.recordSessionPullRequest(named); err != nil {
			return store.SessionPullRequestRecord{}, err
		}
		if held, ok := sessionPullRequestByID(d.store.ListSessionPullRequests(sessionID), named.PRID); ok {
			return held, nil
		}
		return named, nil
	}

	var open []store.SessionPullRequestRecord
	for _, rec := range d.store.ListSessionPullRequests(sessionID) {
		if rec.State != sessionPullRequestMerged && rec.State != sessionPullRequestClosed {
			open = append(open, rec)
		}
	}
	switch len(open) {
	case 1:
		return open[0], nil
	case 0:
		return store.SessionPullRequestRecord{}, fmt.Errorf(
			"%s has no open pull request; pass the url: attn seed harvest %s --when-merged <pr-url>", sessionID, seedID)
	default:
		urls := make([]string, 0, len(open))
		for _, rec := range open {
			urls = append(urls, rec.URL)
		}
		return store.SessionPullRequestRecord{}, fmt.Errorf(
			"%s has %d open pull requests (%s); say which one to wait on: attn seed harvest %s --when-merged <pr-url>",
			sessionID, len(open), strings.Join(urls, ", "), seedID)
	}
}

func sessionPullRequestByID(records []store.SessionPullRequestRecord, prID string) (store.SessionPullRequestRecord, bool) {
	for _, rec := range records {
		if rec.PRID == prID {
			return rec, true
		}
	}
	return store.SessionPullRequestRecord{}, false
}

func harvestWhenArmedNote(rec store.SessionPullRequestRecord) string {
	return fmt.Sprintf("harvests when %s merges", harvestWhenLabel(rec))
}

func harvestWhenClosedNote(rec store.SessionPullRequestRecord) string {
	return fmt.Sprintf("PR #%d closed without merging; %s", rec.Number, harvestWhenClearedNote)
}

func harvestWhenMergedReason(rec store.SessionPullRequestRecord) string {
	reason := fmt.Sprintf("PR #%d merged", rec.Number)
	if title := strings.TrimSpace(rec.Title); title != "" {
		reason = fmt.Sprintf("%s: %s", reason, title)
	}
	return garden.TrimReason(reason)
}

func harvestWhenLabel(rec store.SessionPullRequestRecord) string {
	repository := rec.Repository
	if _, path, ok := strings.Cut(repository, "/"); ok {
		repository = path
	}
	return fmt.Sprintf("%s#%d", repository, rec.Number)
}
