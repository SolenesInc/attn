package daemon

import (
	"errors"
	"fmt"
	"github.com/victorarias/attn/internal/prompts"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type seedHandoverPlan struct {
	seed          garden.Seed
	doc           docstore.Document
	recreated     bool
	repositorySub string
	alreadyBound  bool
}

func handoverSessionName(title, sessionID string) string {
	suffix := strings.ReplaceAll(strings.TrimSpace(sessionID), "-", "")
	if len(suffix) > 4 {
		suffix = suffix[:4]
	}
	baseLimit := maxDelegationNameRunes - len(suffix) - 1
	base := []rune(strings.TrimSpace(title))
	if len(base) > baseLimit {
		base = base[:baseLimit]
	}
	name := strings.TrimRight(string(base), "-_. \t")
	if name == "" {
		name = "handover"
	}
	return name + "-" + suffix
}

func (d *Daemon) handoverAlreadyBound(operationID, sessionID, seedID string) bool {
	dispatch, ok := d.gardenDispatch(sessionID)
	if !ok || strings.TrimSpace(dispatch.OperationID) != strings.TrimSpace(operationID) ||
		activeDispatchCrown(dispatch) != strings.TrimSpace(seedID) {
		return false
	}
	seed, _, err := d.readSeed(seedID)
	return err == nil && seed.TenderSession == sessionID && seed.LastExecutionID == sessionID
}

func (d *Daemon) prepareSeedHandover(
	msg *protocol.DelegateMessage, operationID, sessionID string,
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
		msg.Brief = seed.Body
		msg.Plot = protocol.Ptr(seed.ID)
		msg.Placement = protocol.Ptr(delegationPlacementNew)
		if session := d.store.Get(sessionID); session != nil {
			msg.Cwd = protocol.Ptr(session.Directory)
			if msg.Agent == nil {
				msg.Agent = protocol.Ptr(string(session.Agent))
			}
			if msg.Label == nil {
				msg.Label = protocol.Ptr(session.Label)
			}
		}
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
	if request.ExpectedRev <= 0 {
		return nil, errors.New("handover needs the seed revision you reviewed")
	}
	if int(doc.Rev) != request.ExpectedRev ||
		seed.TenderSession != strings.TrimSpace(request.ExpectedTenderSession) ||
		seed.TenderMember != strings.TrimSpace(request.ExpectedTenderMember) {
		return nil, fmt.Errorf("%s changed since you opened it; refresh it before handing it over", seed.ID)
	}
	if strings.TrimSpace(msg.Brief) != "" || msg.Plot != nil || msg.WorkspaceID != nil ||
		msg.Placement != nil || msg.Worktree != nil || msg.AllowWorktreeReuse != nil {
		return nil, errors.New("handover owns its seed and placement; send only seed, handoff, worker options, and optional cwd")
	}

	continuation := d.continuationForSeed(seed)
	if explicit := strings.TrimSpace(protocol.Deref(msg.Cwd)); explicit != "" {
		if _, err := validateDelegationDirectory(explicit); err != nil {
			return nil, err
		}
		msg.Placement = protocol.Ptr(delegationPlacementNew)
		msg.Worktree = nil
		msg.AllowWorktreeReuse = protocol.Ptr(true)
	} else {
		if continuation == nil {
			return nil, fmt.Errorf("%s has no saved working directory; choose one with --cwd", seed.ID)
		}
		switch continuation.HandoverPlacement {
		case handoverReuseCwd:
			msg.Cwd = protocol.Ptr(continuation.Execution.Cwd)
			msg.Placement = protocol.Ptr(delegationPlacementNew)
			msg.Worktree = nil
			msg.AllowWorktreeReuse = protocol.Ptr(true)
		case handoverRecreateBranch:
			target, safe, reason := branchCanBeRecreated(continuation.Execution)
			if !safe {
				return nil, fmt.Errorf("%s can no longer restore its saved worktree: %s; choose one with --cwd", seed.ID, reason)
			}
			plan.recreated = true
			plan.repositorySub = continuation.Execution.RepositorySubdir
			msg.Placement = protocol.Ptr(delegationPlacementNew)
			msg.Worktree = &protocol.DelegateWorktreeRequest{
				Repo:           protocol.Ptr(continuation.Execution.RepositoryRoot),
				Branch:         continuation.Execution.Branch,
				Path:           protocol.Ptr(target),
				ExistingBranch: protocol.Ptr(true),
			}
		default:
			reason := strings.TrimSpace(continuation.PlacementReason)
			if reason == "" {
				reason = "the saved working context is incomplete"
			}
			return nil, fmt.Errorf("%s needs a directory for Handover: %s; choose one with --cwd", seed.ID, reason)
		}
	}
	if msg.Agent == nil && continuation != nil && strings.TrimSpace(continuation.Execution.Agent) != "" {
		msg.Agent = protocol.Ptr(continuation.Execution.Agent)
	}

	msg.Brief = seed.Body
	msg.Plot = protocol.Ptr(seed.ID)
	if strings.TrimSpace(protocol.Deref(msg.Label)) == "" {
		msg.Label = protocol.Ptr(handoverSessionName(seed.Title, sessionID))
	}
	return plan, nil
}

func handoverPrompt(body, handoff, seedID string) string {
	return prompts.RenderText("delegation", "handover-brief", prompts.Values{"brief": body, "handoff": handoff, "seed_id": seedID})
}

func (d *Daemon) gardenDispatchDocument(sessionID string) (garden.Dispatch, docstore.Document, bool, error) {
	schema, err := d.dispatchesCollection()
	if err != nil {
		return garden.Dispatch{}, docstore.Document{}, false, err
	}
	doc, found, err := d.store.GetDocument(*schema, strings.TrimSpace(sessionID))
	if err != nil || !found {
		return garden.Dispatch{}, docstore.Document{}, found, err
	}
	dispatch, err := garden.DecodeDispatch(doc.Body)
	return dispatch, *doc, true, err
}

func (d *Daemon) bindSeedHandover(
	msg *protocol.DelegateMessage,
	operationID, sessionID, directory, agent string,
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
	seed, doc, err := d.readSeed(request.SeedID)
	if err != nil {
		return nil, err
	}
	if int(doc.Rev) != request.ExpectedRev ||
		seed.TenderSession != strings.TrimSpace(request.ExpectedTenderSession) ||
		seed.TenderMember != strings.TrimSpace(request.ExpectedTenderMember) {
		return nil, fmt.Errorf("%s changed while the new worker was starting; refresh it before handing it over", seed.ID)
	}
	if garden.Closed(seed.Status) {
		return nil, fmt.Errorf("%s became %s while the new worker was starting", seed.ID, seed.Status)
	}

	unclaimed := seed
	unclaimed.TenderSession, unclaimed.TenderMember = "", ""
	next, err := garden.Transition(unclaimed, garden.VerbTend, garden.Ask{
		Actor: garden.Tender{Session: sessionID},
	}, func(string) bool { return false })
	if err != nil {
		return nil, err
	}
	if next.Status != seed.Status {
		next.StateChangedAt = formatGardenTime(d.gardenTime())
	}
	next.LastExecutionID = sessionID

	session := d.store.Get(sessionID)
	if session == nil {
		return nil, fmt.Errorf("handed-over session %s is not tracked", sessionID)
	}
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

	// The new worker's session-start hook and the old worker's metadata refresh
	// both rewrite dispatches while this runs; the seed itself moving is fatal.
	const attempts = 3
	var commits []store.DocumentCommit
	var written []store.DocumentWriteResult
	var dispatches handoverDispatchCommits
	for attempt := 1; ; attempt++ {
		dispatches, err = d.handoverDispatchCommits(msg, operationID, sessionID, directory, agent, fromChief, seed, session)
		if err != nil {
			return nil, err
		}
		commits = append([]store.DocumentCommit{seedCommit}, dispatches.commits...)
		if noteCommit != nil {
			commits = append(commits, *noteCommit)
		}
		if d.seedHandoverBeforeCommit != nil {
			d.seedHandoverBeforeCommit()
		}
		written, err = d.store.CommitDocumentWrites(commits, d.gardenTime())
		if err == nil {
			break
		}
		var conflict *docstore.ConflictError
		if !errors.As(err, &conflict) {
			return nil, err
		}
		if conflict.Collection != garden.CollectionDispatches {
			return nil, fmt.Errorf("%s changed while the new worker was starting; refresh it before handing it over", seed.ID)
		}
		if attempt == attempts {
			return nil, fmt.Errorf("dispatch %s changed under all %d Handover attempts: %w", conflict.ID, attempts, err)
		}
	}
	for i, commit := range commits {
		d.announceCommittedWrite(commit.Fact, written[i].Seq)
	}
	d.publishFact(FactGardenTended, seed.ID, nil)
	if noteCommit != nil {
		d.publishFact(FactGardenNoted, seed.ID, nil)
	}
	d.rememberDispatchProjection(sessionID, dispatches.newDispatch, written[1].Rev)
	if dispatches.oldExecutionID != "" {
		d.rememberDispatchProjection(dispatches.oldExecutionID, dispatches.oldDispatch, written[2].Rev)
	}
	d.ringSeedActivity(seed.ID, gardenRingEvents[garden.VerbTend], sessionID, msg.SourceSessionID)
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
	wire := noteToProtocol(note, *noteDoc)
	d.mirrorSeedNoteOntoTicket(msg.SourceSessionID, seed.ID, wire.Body)
	return &wire, nil
}

type handoverDispatchCommits struct {
	commits        []store.DocumentCommit
	newDispatch    garden.Dispatch
	oldExecutionID string
	oldDispatch    garden.Dispatch
}

// The new dispatch comes first and the superseded old one second, when the
// seed's last execution still claims it.
func (d *Daemon) handoverDispatchCommits(
	msg *protocol.DelegateMessage, operationID, sessionID, directory, agent string, fromChief bool,
	seed garden.Seed, session *protocol.Session,
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
	newDispatch = mergeGardenExecution(newDispatch, observedGardenExecution(session, d.store.GetResumeSessionID(sessionID), d.gardenTime()))
	newDispatch.Crown = seed.ID
	newDispatch.SupersededBy = ""
	newDispatch.DispatcherSession = strings.TrimSpace(msg.SourceSessionID)
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
		Write: store.DocumentWrite{Schema: *dispatchSchema, ID: sessionID, Body: newBody, Expected: &newExpected},
		Fact:  documentChangedFact(garden.Namespace, garden.CollectionDispatches, sessionID, false),
	}}

	oldExecutionID := strings.TrimSpace(seed.LastExecutionID)
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
		Write: store.DocumentWrite{Schema: *dispatchSchema, ID: oldExecutionID, Body: oldBody, Expected: &oldExpected},
		Fact:  documentChangedFact(garden.Namespace, garden.CollectionDispatches, oldExecutionID, false),
	})
	return out, nil
}

func (d *Daemon) handoffNoteCommit(seedID string, msg *protocol.DelegateMessage) (*store.DocumentCommit, garden.Note, error) {
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
	note.ID, err = d.mintNoteID()
	if err != nil {
		return nil, note, err
	}
	note.Seed = seedID
	note.Kind = garden.NoteKindHandoff
	note.Body = handoff
	note.AuthorSession = strings.TrimSpace(msg.SourceSessionID)
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

func (p *seedHandoverPlan) applyRecreatedSubdir(directory string) (string, error) {
	if p == nil || !p.recreated || strings.TrimSpace(p.repositorySub) == "" {
		return directory, nil
	}
	return validateDelegationDirectory(filepath.Join(directory, p.repositorySub))
}
