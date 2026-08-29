package daemon

import (
	"errors"
	"fmt"
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
			plan.recreated = true
			plan.repositorySub = continuation.Execution.RepositorySubdir
			msg.Placement = protocol.Ptr(delegationPlacementNew)
			msg.Worktree = &protocol.DelegateWorktreeRequest{
				Repo:           protocol.Ptr(continuation.Execution.RepositoryRoot),
				Branch:         continuation.Execution.Branch,
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
	var prompt strings.Builder
	prompt.WriteString(strings.TrimSpace(body))
	if text := strings.TrimSpace(handoff); text != "" {
		if prompt.Len() > 0 {
			prompt.WriteString("\n\n")
		}
		prompt.WriteString("Handoff:\n")
		prompt.WriteString(text)
	}
	if prompt.Len() > 0 {
		prompt.WriteString("\n\n")
	}
	prompt.WriteString("---\nYour work is seed `")
	prompt.WriteString(seedID)
	prompt.WriteString("` in the garden. You are its new tender. Read its current body and log with:\n\n    attn seed show ")
	prompt.WriteString(seedID)
	prompt.WriteString("\n\nReport progress on the log and harvest it when its outcome and required verification are complete.")
	return prompt.String()
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
		return nil, nil
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
	newDispatch, newDoc, newFound, err := d.gardenDispatchDocument(sessionID)
	if err != nil {
		return nil, err
	}
	if newFound {
		if crown := activeDispatchCrown(newDispatch); crown != "" && crown != seed.ID {
			return nil, fmt.Errorf("handed-over session %s already reports to %s", sessionID, crown)
		}
		if owner := strings.TrimSpace(newDispatch.OperationID); owner != "" && owner != operationID {
			return nil, fmt.Errorf("handed-over session %s belongs to another operation", sessionID)
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

	seedSchema, err := d.seedsCollection()
	if err != nil {
		return nil, err
	}
	dispatchSchema, err := d.dispatchesCollection()
	if err != nil {
		return nil, err
	}
	seedBody, err := next.Encode()
	if err != nil {
		return nil, err
	}
	newBody, err := newDispatch.Encode()
	if err != nil {
		return nil, err
	}
	seedExpected := doc.Rev
	newExpected := docstore.ExpectAbsent
	if newFound {
		newExpected = newDoc.Rev
	}
	commits := []store.DocumentCommit{
		{
			Write: store.DocumentWrite{Schema: *seedSchema, ID: seed.ID, Body: seedBody, Expected: &seedExpected},
			Fact:  documentChangedFact(garden.Namespace, garden.CollectionSeeds, seed.ID, false),
		},
		{
			Write: store.DocumentWrite{Schema: *dispatchSchema, ID: sessionID, Body: newBody, Expected: &newExpected},
			Fact:  documentChangedFact(garden.Namespace, garden.CollectionDispatches, sessionID, false),
		},
	}

	oldExecutionID := strings.TrimSpace(seed.LastExecutionID)
	oldIndex := -1
	var oldDispatch garden.Dispatch
	if oldExecutionID != "" && oldExecutionID != sessionID {
		var oldDoc docstore.Document
		oldDispatch, oldDoc, found, readErr := d.gardenDispatchDocument(oldExecutionID)
		if readErr != nil {
			return nil, readErr
		}
		if found && activeDispatchCrown(oldDispatch) == seed.ID {
			oldDispatch.SupersededBy = sessionID
			oldBody, encodeErr := oldDispatch.Encode()
			if encodeErr != nil {
				return nil, encodeErr
			}
			oldExpected := oldDoc.Rev
			oldIndex = len(commits)
			commits = append(commits, store.DocumentCommit{
				Write: store.DocumentWrite{Schema: *dispatchSchema, ID: oldExecutionID, Body: oldBody, Expected: &oldExpected},
				Fact:  documentChangedFact(garden.Namespace, garden.CollectionDispatches, oldExecutionID, false),
			})
		}
	}

	noteIndex := -1
	var note garden.Note
	if handoff := strings.TrimSpace(protocol.Deref(request.Handoff)); handoff != "" {
		if err := garden.ValidateNote(handoff); err != nil {
			return nil, err
		}
		noteSchema, err := d.notesCollection()
		if err != nil {
			return nil, err
		}
		note.ID, err = d.mintNoteID()
		if err != nil {
			return nil, err
		}
		note.Seed = seed.ID
		note.Kind = garden.NoteKindHandoff
		note.Body = handoff
		note.AuthorSession = strings.TrimSpace(msg.SourceSessionID)
		noteBody, err := note.Encode()
		if err != nil {
			return nil, err
		}
		noteExpected := docstore.ExpectAbsent
		noteIndex = len(commits)
		commits = append(commits, store.DocumentCommit{
			Write: store.DocumentWrite{Schema: *noteSchema, ID: note.ID, Body: noteBody, Expected: &noteExpected},
			Fact:  documentChangedFact(garden.Namespace, garden.CollectionNotes, note.ID, false),
		})
	}

	written, err := d.store.CommitDocumentWrites(commits, d.gardenTime())
	if err != nil {
		return nil, err
	}
	for i, commit := range commits {
		d.announceCommittedWrite(commit.Fact, written[i].Seq)
	}
	d.publishFact(FactGardenTended, seed.ID, nil)
	if noteIndex >= 0 {
		d.publishFact(FactGardenNoted, seed.ID, nil)
	}
	d.rememberDispatchSeed(sessionID, seed.ID)
	d.rememberDispatchFromChief(sessionID, fromChief)
	if oldIndex >= 0 {
		d.rememberDispatchSeed(oldExecutionID, "")
		d.rememberDispatchFromChief(oldExecutionID, oldDispatch.FromChief)
	}
	d.ringSeedActivity(seed.ID, gardenRingEvents[garden.VerbTend], sessionID, msg.SourceSessionID)

	if noteIndex < 0 {
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

func (p *seedHandoverPlan) applyRecreatedSubdir(directory string) (string, error) {
	if p == nil || !p.recreated || strings.TrimSpace(p.repositorySub) == "" {
		return directory, nil
	}
	return validateDelegationDirectory(filepath.Join(directory, p.repositorySub))
}
