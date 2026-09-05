package daemon

import (
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

const harvestWhenActor = crew.DaemonID

const (
	harvestWhenRingArmed   = "armed"
	harvestWhenRingCleared = "disarmed"
)

const harvestWhenClearedNote = "harvest-on-merge cleared"

func harvestWhenRequested(msg *protocol.SeedTransitionMessage) bool {
	return msg.WhenMerged != nil || protocol.Deref(msg.ClearHarvestWhen)
}

func (d *Daemon) applyHarvestWhenRequest(
	msg *protocol.SeedTransitionMessage, verb garden.Verb, ask garden.Ask, sessionID string,
) (garden.Seed, docstore.Document, error) {
	seedID := strings.TrimSpace(msg.SeedID)
	if verb != garden.VerbHarvest {
		return garden.Seed{}, docstore.Document{}, fmt.Errorf(
			"harvest is the move that waits on a pull request: attn seed harvest %s --when-merged", seedID)
	}
	if strings.TrimSpace(protocol.Deref(msg.Comment)) != "" {
		return garden.Seed{}, docstore.Document{}, fmt.Errorf(
			"a lifecycle comment belongs to Park; use `attn seed note %s -m \"…\"` for %s", seedID, verb)
	}
	if protocol.Deref(msg.ClearHarvestWhen) {
		if msg.WhenMerged != nil && strings.TrimSpace(protocol.Deref(msg.WhenMerged.PullRequestURL)) != "" {
			return garden.Seed{}, docstore.Document{}, fmt.Errorf(
				"--clear drops the condition %s carries and takes no pull request url", seedID)
		}
		return d.clearHarvestWhenRequested(seedID, ask, sessionID)
	}
	if strings.TrimSpace(ask.Reason) != "" {
		return garden.Seed{}, docstore.Document{}, fmt.Errorf(
			"the merge writes the reason; drop -m and %s harvests itself saying which pull request landed", seedID)
	}
	return d.armHarvestWhenMerged(seedID, protocol.Deref(msg.WhenMerged.PullRequestURL), ask, sessionID)
}

func (d *Daemon) armHarvestWhenMerged(
	seedID, url string, ask garden.Ask, sessionID string,
) (garden.Seed, docstore.Document, error) {
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	if garden.Closed(seed.Status) {
		return garden.Seed{}, docstore.Document{}, fmt.Errorf(
			"%s is %s and waits on nothing; replant it first: attn seed replant %s", seed.ID, seed.Status, seed.ID)
	}
	rec, err := d.harvestWhenPullRequest(seed.ID, url, sessionID)
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	if rec.State == sessionPullRequestMerged {
		return d.fulfilHarvestWhen(seed, rec, nil, sessionID)
	}
	if rec.State == sessionPullRequestClosed {
		return garden.Seed{}, docstore.Document{}, fmt.Errorf(
			"%s closed without merging, so it never will; %s has nothing to wait for", harvestWhenLabel(rec), seed.ID)
	}
	condition := garden.HarvestCondition{
		PullRequest:  rec.PRID,
		URL:          rec.URL,
		SetAt:        formatGardenTime(d.gardenTime()),
		SetBySession: strings.TrimSpace(ask.Actor.Session),
		SetByMember:  strings.TrimSpace(ask.Actor.Member),
	}
	if err := garden.ValidateHarvestCondition(condition); err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}

	schema, err := d.seedsCollection()
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	seedID = seed.ID
	const attempts = 3
	for range attempts {
		seed, doc, err := d.readSeed(seedID)
		if err != nil {
			return garden.Seed{}, docstore.Document{}, err
		}
		if garden.Closed(seed.Status) {
			return garden.Seed{}, docstore.Document{}, fmt.Errorf(
				"%s is %s and waits on nothing; replant it first: attn seed replant %s", seed.ID, seed.Status, seed.ID)
		}
		next := seed
		next.HarvestWhen = &condition
		fact, ring := FactGardenHarvestWhenChanged, harvestWhenRingArmed
		var displaced *garden.Tender
		if seed.Status == garden.StatusGrowing {
			if held := seed.Tender(); ask.Force && held.Holds(d.sessionExists) && !held.Is(ask.Actor) {
				displaced = &held
			}
			next, err = garden.Transition(next, garden.VerbPark, garden.Ask{Actor: ask.Actor, Force: ask.Force}, d.sessionExists)
			if err != nil {
				return garden.Seed{}, docstore.Document{}, err
			}
			next.StateChangedAt = formatGardenTime(d.gardenTime())
			fact, ring = FactGardenParked, gardenRingEvents[garden.VerbPark]
		}

		notes := make([]garden.Note, 0, 3)
		if displaced != nil {
			notes = append(notes, d.harvestWhenNote(
				seed.ID, forcedSeedMoveBody(seed.ID, garden.VerbPark, ask.Actor, *displaced), ask.Actor))
		}
		notes = append(notes, d.harvestWhenNote(seed.ID, harvestWhenArmedNote(rec), ask.Actor))
		if attachment, ok := d.harvestWhenAttachment(seed.ID, rec, ask.Actor); ok {
			notes = append(notes, attachment)
		}

		written, wireNotes, err := d.writeSeedMoveWithNotes(*schema, next, doc.Rev, fact, notes)
		if err != nil {
			if docstore.IsConflict(err) {
				continue
			}
			return garden.Seed{}, docstore.Document{}, err
		}
		for _, note := range wireNotes {
			d.mirrorSeedNoteOntoTicket(sessionID, seed.ID, note.Body)
		}
		if fact == FactGardenParked {
			d.mirrorSeedMoveOntoTicket(sessionID, seed.ID, garden.VerbPark, "")
		}
		d.ringSeedActivity(seed.ID, ring, sessionID)
		return d.settleFreshlyArmed(next, written, sessionID)
	}
	return garden.Seed{}, docstore.Document{}, fmt.Errorf(
		"%s was rewritten under all %d attempts to arm it; read it again with `attn seed show %s` and decide from what it says now",
		seedID, attempts, seedID)
}

// The refresh may have settled the pull request between the check above and the
// arm commit; a merged row then leaves the open set and no tick looks at it again.
func (d *Daemon) settleFreshlyArmed(
	seed garden.Seed, written docstore.Document, sessionID string,
) (garden.Seed, docstore.Document, error) {
	rec, ok := d.store.SessionPullRequestByID(seed.HarvestWhen.PullRequest)
	if !ok {
		return seed, written, nil
	}
	switch rec.State {
	case sessionPullRequestMerged:
		return d.fulfilHarvestWhen(seed, rec, seed.HarvestWhen, sessionID)
	case sessionPullRequestClosed:
		cleared, doc, err := d.clearHarvestWhen(seed.ID, seed.HarvestWhen,
			harvestWhenClosedNote(rec), garden.Tender{Member: harvestWhenActor})
		if err != nil {
			return garden.Seed{}, docstore.Document{}, err
		}
		d.ringSeedActivity(seed.ID, harvestWhenRingCleared, sessionID)
		return cleared, doc, nil
	}
	return seed, written, nil
}

// The same pull request set at the same time: a re-arm on the same pull request
// is a new promise, and an ordinary edit in between is not.
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

// observed pins the harvest to that condition; nil harvests whatever is armed now.
func (d *Daemon) fulfilHarvestWhen(
	seed garden.Seed, rec store.SessionPullRequestRecord, observed *garden.HarvestCondition, excludedSessions ...string,
) (garden.Seed, docstore.Document, error) {
	reason := harvestWhenMergedReason(rec)
	ask := garden.Ask{Actor: garden.Tender{Member: harvestWhenActor}, Reason: reason, Force: true}
	var harvested garden.Seed
	var doc docstore.Document
	var notes seedTransitionNotes
	const attempts = 3
	for attempt := range attempts {
		current, read, err := d.readSeed(seed.ID)
		if err == nil && observed != nil {
			current, read, err = d.observedHarvestCondition(seed.ID, observed)
		}
		if err != nil {
			return garden.Seed{}, docstore.Document{}, err
		}
		harvested, doc, notes, err = d.applySeedTransitionDetailedAsAtRevision(
			current.ID, garden.VerbHarvest, ask, "", d.sessionExists, read.Rev)
		if errors.Is(err, errSeedRevisionMoved) && attempt+1 < attempts {
			continue
		}
		if err != nil {
			return garden.Seed{}, docstore.Document{}, err
		}
		break
	}
	for _, note := range notes.all() {
		d.mirrorSeedNoteOntoTicket("", seed.ID, note.Body)
	}
	d.mirrorSeedMoveOntoTicket("", seed.ID, garden.VerbHarvest, reason)
	d.ringSeedActivity(seed.ID, gardenRingEvents[garden.VerbHarvest], excludedSessions...)
	return harvested, doc, nil
}

// observed pins the clear to that condition; nil clears whatever is armed now.
func (d *Daemon) clearHarvestWhen(
	seedID string, observed *garden.HarvestCondition, noteBody string, actor garden.Tender,
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
		written, _, err := d.writeSeedMoveWithNotes(*schema, next, doc.Rev, FactGardenHarvestWhenChanged,
			[]garden.Note{d.harvestWhenNote(seed.ID, noteBody, actor)})
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
	seedID string, ask garden.Ask, sessionID string,
) (garden.Seed, docstore.Document, error) {
	seed, doc, err := d.clearHarvestWhen(seedID, nil, harvestWhenClearedNote, ask.Actor)
	if err != nil {
		return garden.Seed{}, docstore.Document{}, err
	}
	d.mirrorSeedNoteOntoTicket(sessionID, seed.ID, harvestWhenClearedNote)
	d.ringSeedActivity(seed.ID, harvestWhenRingCleared, sessionID)
	return seed, doc, nil
}

func (d *Daemon) harvestWhenNote(seedID, body string, actor garden.Tender) garden.Note {
	return garden.Note{
		Seed: seedID, Kind: garden.NoteKindNote, Body: body,
		AuthorSession: strings.TrimSpace(actor.Session), AuthorMember: strings.TrimSpace(actor.Member),
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

func (d *Daemon) harvestWhenPullRequest(seedID, url, sessionID string) (store.SessionPullRequestRecord, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return store.SessionPullRequestRecord{}, fmt.Errorf(
			"harvest --when-merged needs a session to track the pull request")
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
			"%s has %d open pull requests, so say which one to wait on: attn seed harvest %s --when-merged <pr-url>\n%s",
			sessionID, len(open), seedID, strings.Join(urls, "\n"))
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
