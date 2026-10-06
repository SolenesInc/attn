package daemon

import (
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"

	seedEvents "github.com/victorarias/attn/internal/garden/events"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) bindDelegationAssignmentProtected(_ foregroundCleanupProtection, operationID string, sessionID protocol.SessionID, plannerSessionID protocol.SessionID, parentSeedID string, brief string, name string, seedID string, observed garden.Dispatch, fromChief, createSeed bool, profileID ...string) (string, error) {
	if err := d.requireHome(garden.Surface); err != nil {
		return "", err
	}
	if dispatch, found := d.gardenDispatch(sessionID); found && activeDispatchCrown(dispatch) != "" {
		bound := activeDispatchCrown(dispatch)
		if err := d.requireSeedInProfile(bound, firstProfile(profileID), false); err != nil {
			return "", err
		}
		if strings.TrimSpace(dispatch.OperationID) == strings.TrimSpace(operationID) || operationID == "" {
			return bound, nil
		}
		return "", fmt.Errorf("session %s is already bound by another delegation operation", sessionID)
	}
	seedSchema, err := d.seedsCollection()
	if err != nil {
		return "", err
	}
	dispatchSchema, err := d.dispatchesCollection()
	if err != nil {
		return "", err
	}

	var seed garden.Seed
	var seedExpected int64
	if createSeed {
		title := strings.TrimSpace(name)
		if title == "" {
			title = "delegated work"
		}
		body := strings.TrimSpace(brief)
		if err := garden.ValidatePlant(title, body); err != nil {
			return "", err
		}
		if strings.TrimSpace(seedID) == "" {
			return "", fmt.Errorf("new delegation seed identity was not reserved")
		}
		seed = garden.Seed{ProfileID: firstProfile(profileID), ID: seedID, Title: title, Body: body, Status: garden.StatusPlanted, StepSlug: garden.StepSlug(title), PlanterSession: plannerSessionID, PlanterMember: d.resolveTenderMember("", plannerSessionID), Edges: []garden.Edge{}, Vars: []garden.Var{}}
		if parent := strings.TrimSpace(parentSeedID); parent != "" {
			seed.Edges = append(seed.Edges, garden.Edge{Kind: garden.EdgePartOf, To: parent})
		}
		seedExpected = docstore.ExpectAbsent
	} else {
		var doc docstore.Document
		seed, doc, err = d.readSeed(seedID)
		if err != nil {
			return "", err
		}
		if garden.Closed(seed.Status) {
			return "", fmt.Errorf("%s is %s; replant it before delegating", seed.ID, seed.Status)
		}
		if holder := seed.Tender(); holder.Holds(d.sessionExists) {
			return "", fmt.Errorf("seed %s has active holder %s; use --handover to transfer it", seed.ID, holder.DisplayName())
		}
		seedExpected = doc.Rev
	}
	var profileErr error
	seed.ProfileID, profileErr = d.seedBirthProfile(seed)
	if profileErr != nil {
		return "", profileErr
	}
	previousStatus := seed.Status
	seed, err = garden.Transition(seed, garden.VerbTend, garden.Ask{Actor: garden.Tender{Session: sessionID}}, func(protocol.SessionID) bool { return false })
	if err != nil {
		return "", err
	}
	if createSeed || seed.Status != previousStatus {
		seed.StateChangedAt = formatGardenTime(d.gardenTime())
	}
	seed.LastExecutionID = sessionID
	seedBody, err := seed.Encode()
	if err != nil {
		return "", err
	}
	dispatch := observed
	dispatch.Crown = seed.ID
	dispatch.DispatcherSession = protocol.TrimID(plannerSessionID)
	dispatch.DispatcherMember = d.crewMembersBySession()[dispatch.DispatcherSession]
	dispatch.FromChief = fromChief
	dispatch.OperationID = strings.TrimSpace(operationID)
	dispatchBody, err := dispatch.Encode()
	if err != nil {
		return "", err
	}
	dispatchExpected := docstore.ExpectAbsent
	commits := []store.DocumentCommit{
		{Write: store.DocumentWrite{Schema: *seedSchema, ID: seed.ID, Body: seedBody, Expected: &seedExpected}, Fact: documentChangedFact(garden.Namespace, garden.CollectionSeeds, seed.ID, false)},
		{Write: store.DocumentWrite{Schema: *dispatchSchema, ID: string(sessionID), Body: dispatchBody, Expected: &dispatchExpected}, Fact: documentChangedFact(garden.Namespace, garden.CollectionDispatches, string(sessionID), false)},
	}
	occurrences := []seedEvents.Occurrence{}
	if createSeed {
		planted, eventErr := seedEvents.Occur(
			gardenSeedEventModel, gardenSeedEventVocabulary.Planted, seed.ID,
			seedEvents.CausePayload{CausedBySessionID: plannerSessionID},
		)
		if eventErr != nil {
			return "", eventErr
		}
		occurrences = append(occurrences, planted)
		for _, edge := range seed.Edges {
			linked, eventErr := seedEvents.Occur(
				gardenSeedEventModel, gardenSeedEventVocabulary.EdgeLinked, seed.ID,
				seedEvents.EdgePayload{
					EdgeKind: edge.Kind, TargetSeedID: edge.To,
					CausedBySessionID: plannerSessionID,
				},
			)
			if eventErr != nil {
				return "", eventErr
			}
			occurrences = append(occurrences, linked)
		}
	}
	tended, err := gardenSeedLifecycleOccurrence(garden.VerbTend, seed.ID, plannerSessionID, string(sessionID))
	if err != nil {
		return "", err
	}
	occurrences = append(occurrences, tended)
	events, err := encodeGardenSeedEvents(occurrences...)
	if err != nil {
		return "", err
	}
	d.lockGardenRoles()
	written, eventSeqs, err := d.store.CommitGardenDispatchWritesWithEvents(
		commits, store.GardenSeedWatch{WatcherSessionID: sessionID, SeedID: seed.ID}, events, d.gardenTime(),
	)
	if err == nil {
		err = d.discardAllIneligibleGardenSeedBellsLocked()
	}
	d.unlockGardenRoles()
	if err != nil {
		var conflict *docstore.ConflictError
		if errors.As(err, &conflict) {
			return "", fmt.Errorf("seed or dispatch changed while binding delegation: %w", err)
		}
		return "", err
	}
	for i, commit := range commits {
		d.announceCommittedWrite(commit.Fact, written[i].Seq)
	}
	announceGardenSeedEvents(d, eventSeqs)
	d.rememberDispatchProjection(sessionID, dispatch, written[1].Rev)
	return seed.ID, nil
}
