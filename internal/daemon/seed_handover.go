package daemon

import (
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	seedEvents "github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type seedHandoverPlan struct {
	seed         garden.Seed
	doc          docstore.Document
	alreadyBound bool
}

func (d *Daemon) handoverAlreadyBound(operationID string, sessionID protocol.SessionID, seedID string) bool {
	dispatch, ok := d.gardenDispatch(sessionID)
	if !ok || strings.TrimSpace(dispatch.OperationID) != strings.TrimSpace(operationID) ||
		activeDispatchCrown(dispatch) != strings.TrimSpace(seedID) {
		return false
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return false
	}
	tender, _ := seed.Claim.Tender()
	party, _ := d.broadcastBindings().PartyOf(sessionID)
	return tender == party && !party.IsZero() && seed.LastExecutionID == sessionID
}

func (d *Daemon) prepareSeedHandover(
	msg *resolvedDelegationLaunch, operationID string, sessionID protocol.SessionID,
) (*seedHandoverPlan, error) {
	request := msg.Handover
	if request == nil {
		return nil, nil
	}
	if strings.TrimSpace(operationID) == "" {
		return nil, errors.New("seed Handover requires a durable delegation operation")
	}
	seedID := strings.TrimSpace(request.SeedID)
	seed, doc, err := d.readSeed(seedID)
	if err != nil {
		return nil, err
	}
	plan := &seedHandoverPlan{seed: seed, doc: doc}
	if d.handoverAlreadyBound(operationID, sessionID, seedID) {
		plan.alreadyBound = true
		return plan, nil
	}
	if request.Review != nil {
		item, reviewErr := d.validateGardenReviewAction(request.Review, seedID, "handover")
		if reviewErr != nil {
			return nil, reviewErr
		}
		if item.SeedRev != doc.Rev {
			return nil, fmt.Errorf("%s changed since you reviewed it; refresh the garden", seedID)
		}
	}
	if garden.Closed(seed.Status) {
		return nil, fmt.Errorf("%s is %s; replant it before handing it over", seed.ID, seed.Status)
	}
	if int(doc.Rev) != request.ExpectedRev {
		return nil, fmt.Errorf("%s changed since you opened it; refresh it before handing it over", seed.ID)
	}
	return plan, nil
}

func (d *Daemon) gardenDispatchDocument(sessionID protocol.SessionID) (garden.Execution, docstore.Document, bool, error) {
	schema, err := d.dispatchesCollection()
	if err != nil {
		return garden.Execution{}, docstore.Document{}, false, err
	}
	doc, found, err := d.store.GetDocument(*schema, string(protocol.TrimID(sessionID)))
	if err != nil || !found {
		return garden.Execution{}, docstore.Document{}, found, err
	}
	dispatch, err := garden.DecodeExecution(doc.Body)
	return dispatch, *doc, true, err
}

func (d *Daemon) bindSeedHandoverProtected(
	_ foregroundCleanupProtection,
	msg *resolvedDelegationLaunch,
	operationID string, sessionID protocol.SessionID, directory string, agent string,
	observed garden.Execution,
	fromChief bool,
) (*protocol.SeedNote, error) {
	request := msg.Handover
	if request == nil {
		return nil, errors.New("missing Handover request")
	}
	if d.handoverAlreadyBound(operationID, sessionID, request.SeedID) {
		if err := d.resolveGardenReviewAction(request.Review, request.SeedID, "handover"); err != nil {
			d.logf("Garden review: settle %s after recovered Handover: %v", request.SeedID, err)
		}
		return nil, nil
	}
	if _, err := d.validateGardenReviewAction(request.Review, request.SeedID, "handover"); err != nil {
		return nil, err
	}
	d.lockGardenRoles()
	defer d.unlockGardenRoles()
	b, err := d.bindings()
	if err != nil {
		return nil, err
	}
	delegate, ok := b.PartyOf(sessionID)
	if !ok {
		return nil, fmt.Errorf("delegation session %s is not reserved", sessionID)
	}
	seed, doc, err := d.readSeed(request.SeedID)
	if err != nil {
		return nil, err
	}
	if int(doc.Rev) != request.ExpectedRev {
		return nil, fmt.Errorf("%s changed while the new worker was starting; refresh it before handing it over", seed.ID)
	}
	if garden.Closed(seed.Status) {
		return nil, fmt.Errorf("%s became %s while the new worker was starting", seed.ID, seed.Status)
	}

	next, err := garden.Assign(seed, delegate)
	if err != nil {
		return nil, err
	}
	if next.Status != seed.Status {
		next.StateChangedAt = formatGardenTime(d.gardenTime())
	}
	next.LastExecutionID = sessionID

	seedSchema, err := d.seedsCollection()
	if err != nil {
		return nil, err
	}
	seedBody, err := next.Encode()
	if err != nil {
		return nil, err
	}
	seedExpected := doc.Rev
	seedCommit := store.DocumentCommit{
		Write: store.DocumentWrite{Schema: *seedSchema, ID: seed.ID, Body: seedBody, Expected: &seedExpected},
		Fact:  documentChangedFact(garden.Namespace, garden.CollectionSeeds, seed.ID, false),
	}
	noteCommit, note, err := d.handoffNoteCommit(seed.ID, msg)
	if err != nil {
		return nil, err
	}
	cause := msg.Dispatcher.Ref()
	tended, err := lifecycleOccurrence(garden.VerbTend, seed.ID, garden.Ask{By: msg.Dispatcher, DirectlyNotified: delegate})
	if err != nil {
		return nil, err
	}
	occurrences := []seedEvents.Occurrence{tended}
	if noteCommit != nil {
		noted, eventErr := seedEvents.Occur(
			gardenSeedEventModel, gardenSeedEventVocabulary.NoteAdded, seed.ID,
			seedEvents.NoteAddedPayload{
				NoteID: note.ID, AttentionRequested: false, CausedBy: cause,
			},
		)
		if eventErr != nil {
			return nil, eventErr
		}
		occurrences = append(occurrences, noted)
	}
	events, err := encodeGardenSeedEvents(occurrences...)
	if err != nil {
		return nil, err
	}

	dispatches, err := d.handoverDispatchCommits(msg, operationID, sessionID, directory, agent, observed, fromChief, seed)
	if err != nil {
		return nil, err
	}
	commits := append([]store.DocumentCommit{seedCommit}, dispatches.commits...)
	if noteCommit != nil {
		commits = append(commits, *noteCommit)
	}
	written, eventSeqs, err := d.store.CommitGardenDispatchWritesWithEvents(
		commits, store.GardenPartyWatch{Watcher: delegate, SeedID: seed.ID}, events, d.gardenTime(),
	)
	if err == nil {
		err = d.discardAllIneligibleGardenSeedBellsLocked()
	}
	if err != nil {
		var conflict *docstore.ConflictError
		if errors.As(err, &conflict) {
			return nil, fmt.Errorf("%s changed while the new worker was starting; refresh it before handing it over", seed.ID)
		}
		return nil, err
	}
	announceGardenSeedEvents(d, eventSeqs)
	for i, commit := range commits {
		d.announceCommittedWrite(commit.Fact, written[i].Seq)
	}
	d.rememberDispatchProjection(sessionID, dispatches.newDispatch, written[1].Rev)
	if dispatches.oldExecutionID != "" {
		d.rememberDispatchProjection(dispatches.oldExecutionID, dispatches.oldDispatch, written[2].Rev)
	}
	if err := d.resolveGardenReviewAction(request.Review, seed.ID, "handover"); err != nil {
		d.logf("Garden review: settle %s after Handover: %v", seed.ID, err)
	}

	if noteCommit == nil {
		return nil, nil
	}
	noteSchema, err := d.notesCollection()
	if err != nil {
		return nil, err
	}
	noteDoc, found, err := d.store.GetDocument(*noteSchema, note.ID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("handoff note %s was committed but cannot be read", note.ID)
	}
	wire := d.noteWire(note, *noteDoc)

	return &wire, nil
}

type handoverDispatchCommits struct {
	commits        []store.DocumentCommit
	newDispatch    garden.Execution
	oldExecutionID protocol.SessionID
	oldDispatch    garden.Execution
}

func (d *Daemon) handoverDispatchCommits(
	msg *resolvedDelegationLaunch, operationID string, sessionID protocol.SessionID, directory string, agent string, observed garden.Execution, fromChief bool,
	seed garden.Seed,
) (handoverDispatchCommits, error) {
	var out handoverDispatchCommits
	dispatchSchema, err := d.dispatchesCollection()
	if err != nil {
		return out, err
	}
	newDispatch, newDoc, newFound, err := d.gardenDispatchDocument(sessionID)
	if err != nil {
		return out, err
	}
	if newFound {
		if crown := activeDispatchCrown(newDispatch); crown != "" && crown != seed.ID {
			return out, fmt.Errorf("handed-over session %s already reports to %s", sessionID, crown)
		}
		if owner := strings.TrimSpace(newDispatch.OperationID); owner != "" && owner != operationID {
			return out, fmt.Errorf("handed-over session %s belongs to another operation", sessionID)
		}
	}
	newDispatch = mergeGardenExecution(newDispatch, observed)
	newDispatch.Crown = seed.ID
	newDispatch.SupersededBy = ""
	newDispatch.Dispatcher = msg.Dispatcher
	newDispatch.FromChief = fromChief
	newDispatch.OperationID = operationID
	if newDispatch.Cwd == "" {
		newDispatch.Cwd = directory
	}
	if newDispatch.Agent == "" {
		newDispatch.Agent = agent
	}
	newBody, err := newDispatch.Encode()
	if err != nil {
		return out, err
	}
	newExpected := docstore.ExpectAbsent
	if newFound {
		newExpected = newDoc.Rev
	}
	out.newDispatch = newDispatch
	out.commits = []store.DocumentCommit{{
		Write: store.DocumentWrite{Schema: *dispatchSchema, ID: string(sessionID), Body: newBody, Expected: &newExpected},
		Fact:  documentChangedFact(garden.Namespace, garden.CollectionDispatches, string(sessionID), false),
	}}

	oldExecutionID := protocol.TrimID(seed.LastExecutionID)
	if oldExecutionID == "" || oldExecutionID == sessionID {
		return out, nil
	}
	oldDispatch, oldDoc, found, err := d.gardenDispatchDocument(oldExecutionID)
	if err != nil {
		return out, err
	}
	if !found || activeDispatchCrown(oldDispatch) != seed.ID {
		return out, nil
	}
	oldDispatch.SupersededBy = sessionID
	oldBody, err := oldDispatch.Encode()
	if err != nil {
		return out, err
	}
	oldExpected := oldDoc.Rev
	out.oldExecutionID, out.oldDispatch = oldExecutionID, oldDispatch
	out.commits = append(out.commits, store.DocumentCommit{
		Write: store.DocumentWrite{Schema: *dispatchSchema, ID: string(oldExecutionID), Body: oldBody, Expected: &oldExpected},
		Fact:  documentChangedFact(garden.Namespace, garden.CollectionDispatches, string(oldExecutionID), false),
	})
	return out, nil
}

func (d *Daemon) handoffNoteCommit(seedID string, msg *resolvedDelegationLaunch) (*store.DocumentCommit, garden.Note, error) {
	var note garden.Note
	handoff := strings.TrimSpace(protocol.Deref(msg.Handover.Handoff))
	if handoff == "" {
		return nil, note, nil
	}
	if err := garden.ValidateNote(handoff); err != nil {
		return nil, note, err
	}
	noteSchema, err := d.notesCollection()
	if err != nil {
		return nil, note, err
	}
	note.ID = strings.TrimSpace(protocol.Deref(msg.Handover.NoteID))
	if note.ID == "" {
		note.ID, err = d.mintNoteID()
		if err != nil {
			return nil, note, err
		}
	}
	note.Seed = seedID
	note.Kind = garden.NoteKindHandoff
	note.Body = handoff
	note.Author = msg.Dispatcher
	noteBody, err := note.Encode()
	if err != nil {
		return nil, note, err
	}
	noteExpected := docstore.ExpectAbsent
	return &store.DocumentCommit{
		Write: store.DocumentWrite{Schema: *noteSchema, ID: note.ID, Body: noteBody, Expected: &noteExpected},
		Fact:  documentChangedFact(garden.Namespace, garden.CollectionNotes, note.ID, false),
	}, note, nil
}
