package daemon

import (
	"errors"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) bindDelegationAssignment(operationID, sessionID, plannerSessionID, parentSeedID, brief, name, seedID, cwd, agent string, fromChief, createSeed bool) (string, error) {
	if err := d.requireHome(garden.Surface); err != nil {
		return "", err
	}
	if bound, ok := d.gardenDispatchCrown(sessionID); ok {
		dispatch, _ := d.gardenDispatch(sessionID)
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
		seed = garden.Seed{ID: seedID, Title: title, Body: body, Status: garden.StatusPlanted, StepSlug: garden.StepSlug(title), PlanterSession: plannerSessionID, PlanterMember: d.resolveTenderMember("", plannerSessionID), Edges: []garden.Edge{}, Vars: []garden.Var{}}
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
	previousStatus := seed.Status
	seed, err = garden.Transition(seed, garden.VerbTend, garden.Ask{Actor: garden.Tender{Session: sessionID}}, func(string) bool { return false })
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
	dispatch := observedGardenExecution(&protocol.Session{ID: sessionID, Directory: cwd, Agent: protocol.SessionAgent(agent)}, "", d.gardenTime())
	dispatch.Crown = seed.ID
	dispatch.DispatcherSession = strings.TrimSpace(plannerSessionID)
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
		{Write: store.DocumentWrite{Schema: *dispatchSchema, ID: sessionID, Body: dispatchBody, Expected: &dispatchExpected}, Fact: documentChangedFact(garden.Namespace, garden.CollectionDispatches, sessionID, false)},
	}
	d.gardenWatchMu.Lock()
	written, err := d.store.CommitGardenDispatchWrites(commits, store.GardenSeedWatch{WatcherSessionID: sessionID, SeedID: seed.ID}, d.gardenTime())
	d.gardenWatchMu.Unlock()
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
	d.rememberDispatchProjection(sessionID, dispatch, written[1].Rev)
	d.publishFact(FactGardenTended, seed.ID, nil)
	d.reconcileRemoteGardenSeedBells()
	d.ringSeedActivity(seed.ID, gardenRingEvents[garden.VerbTend], sessionID, plannerSessionID)
	return seed.ID, nil
}

// A home without Garden support may launch locally; a failed binding must surface.
func (d *Daemon) bindDelegationSeed(sessionID, plannerSessionID, brief, name, crown, cwd, agent string, fromChief bool) (string, error) {
	seedID, err := d.bindDelegatedSeed(sessionID, plannerSessionID, brief, name, crown, cwd, agent, fromChief)
	switch {
	case err == nil:
		d.logf("delegate: bound seed %q to session %s", seedID, sessionID)
	default:
		return "", fmt.Errorf("bind delegation for session %s: %w", sessionID, err)
	}
	return seedID, nil
}

// Idempotent through the dispatch record: a delegation resumed after a daemon
// crash re-binds instead of planting a second seed.
func (d *Daemon) bindDelegatedSeed(sessionID, plannerSessionID, brief, name, crown, cwd, agent string, fromChief bool) (string, error) {
	if err := d.requireHome(garden.Surface); err != nil {
		return "", err
	}
	if bound, ok := d.gardenDispatchCrown(sessionID); ok {
		return bound, nil
	}
	if err := d.recordGardenDispatch(sessionID, "", plannerSessionID, cwd, agent, fromChief); err != nil {
		return "", fmt.Errorf("preserve session %s before binding it: %w", sessionID, err)
	}
	seedID := strings.TrimSpace(crown)
	if seedID == "" {
		seed, err := d.plantDelegatedSeed(sessionID, plannerSessionID, brief, name)
		if err != nil {
			return "", err
		}
		seedID = seed.ID
	} else if err := d.tendDispatchedSeed(sessionID, plannerSessionID, seedID); err != nil {
		return "", err
	}
	if err := d.recordGardenDispatch(sessionID, seedID, plannerSessionID, cwd, agent, fromChief); err != nil {
		return "", fmt.Errorf("bind %s to session %s: %w", seedID, sessionID, err)
	}
	d.ringSeedActivity(seedID, gardenRingEvents[garden.VerbTend], sessionID, plannerSessionID)
	return seedID, nil
}

// Planted already tended by its delegate: a seed that exists unheld for a moment
// is one `ready` can offer away.
func (d *Daemon) plantDelegatedSeed(sessionID, plannerSessionID, brief, name string) (garden.Seed, error) {
	title := strings.TrimSpace(name)
	if title == "" {
		title = "delegated work"
	}
	body := strings.TrimSpace(brief)
	if err := garden.ValidatePlant(title, body); err != nil {
		return garden.Seed{}, err
	}
	schema, err := d.seedsCollection()
	if err != nil {
		return garden.Seed{}, err
	}
	seed := garden.Seed{
		Title:          title,
		Body:           body,
		Status:         garden.StatusPlanted,
		StepSlug:       garden.StepSlug(title),
		PlanterSession: plannerSessionID,
		PlanterMember:  d.resolveTenderMember("", plannerSessionID),
		Edges:          []garden.Edge{},
		Vars:           []garden.Var{},
	}
	if parent, ok := d.gardenDispatchCrown(plannerSessionID); ok && parent != "" {
		seed.Edges = append(seed.Edges, garden.Edge{Kind: garden.EdgePartOf, To: parent})
	}
	tender := garden.Tender{Session: sessionID}
	// Nothing holds an unwritten seed: the liveness predicate is never consulted.
	seed, err = garden.Transition(seed, garden.VerbTend, garden.Ask{Actor: tender}, func(string) bool { return false })
	if err != nil {
		return garden.Seed{}, err
	}
	seed.LastExecutionID = sessionID
	seed, _, err = d.mintAndPlant(*schema, seed)
	return seed, err
}

// validateDispatchCrown already refused a seed held by a live session; this
// take-over through garden.Transition is the race backstop behind it.
func (d *Daemon) tendDispatchedSeed(sessionID, plannerSessionID, seedID string) error {
	actor := garden.Tender{Session: sessionID, Member: d.resolveTenderMember("", sessionID)}
	if _, _, err := d.applySeedTransitionAs(seedID, garden.VerbTend, garden.Ask{Actor: actor}, d.dispatchSessionLive(plannerSessionID)); err != nil {
		return fmt.Errorf("tend %s as session %s: %w", seedID, sessionID, err)
	}
	return nil
}

// Every session as it really is, except the delegating one, which is handing the
// seed over.
func (d *Daemon) dispatchSessionLive(plannerSessionID string) func(string) bool {
	planner := strings.TrimSpace(plannerSessionID)
	return func(sessionID string) bool {
		if planner != "" && strings.TrimSpace(sessionID) == planner {
			return false
		}
		return d.sessionExists(sessionID)
	}
}
