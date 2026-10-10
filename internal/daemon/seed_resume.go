package daemon

import (
	"context"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

type seedResumeOutcome struct {
	SessionID      protocol.SessionID
	ProfileID      string
	AlreadyRunning bool
}

func (d *Daemon) resumeSeedFromReview(
	seedID string,
	review *protocol.SeedReviewActionContext,
) (*seedResumeOutcome, error) {
	var outcome *seedResumeOutcome
	err := d.worktreeMaintenance.ProtectFromAutomaticCleanup(context.Background(), func(protection foregroundCleanupProtection) error {
		var resumeErr error
		outcome, resumeErr = d.resumeSeedFromReviewProtected(protection, seedID, review)
		return resumeErr
	})
	return outcome, err
}

func (d *Daemon) resumeSeedFromReviewProtected(
	protection foregroundCleanupProtection,
	seedID string,
	review *protocol.SeedReviewActionContext,
) (*seedResumeOutcome, error) {
	seedID = strings.TrimSpace(seedID)
	if seedID == "" {
		return nil, fmt.Errorf("seed_id is required")
	}
	if err := d.requireHome(garden.Surface); err != nil {
		return nil, err
	}
	expectedRev := int64(0)
	if review != nil {
		item, err := d.validateGardenReviewAction(review, seedID, "resume")
		if err != nil {
			return nil, err
		}
		expectedRev = item.SeedRev
	}
	seed, seedDoc, err := d.readSeed(seedID)
	if err != nil {
		return nil, err
	}
	if expectedRev > 0 && seedDoc.Rev != expectedRev {
		return nil, fmt.Errorf("%s changed since you reviewed it; refresh the garden", seedID)
	}
	if garden.Closed(seed.Status) {
		return nil, fmt.Errorf("%s is %s; replant it before resuming its agent", seed.ID, seed.Status)
	}
	b, err := d.bindings()
	if err != nil {
		return nil, err
	}
	tender, claimed := seed.Claim.Lasts(b)
	if key, member := tender.Member(); claimed && member {
		if err := b.Check(tender); err != nil {
			return nil, err
		}
		d.crewWakeMu.Lock()
		woke, err := d.crewWakeDayWithChargeLocked(key, "", false, nil, crewWakeRequest{UserStarted: true})
		d.crewWakeMu.Unlock()
		if err != nil {
			return nil, err
		}
		if err := d.resolveGardenReviewAction(review, seedID, "resume"); err != nil {
			d.logf("Garden review: settle %s after Resume: %v", seedID, err)
		}
		return &seedResumeOutcome{SessionID: woke.SessionID, ProfileID: seed.ProfileID, AlreadyRunning: woke.AlreadyAwake}, nil
	}
	continuation := d.continuationForSeedForeground(seed)
	if continuation == nil {
		if tender, stored := seed.Claim.Tender(); stored {
			return nil, fmt.Errorf("%s was tended by session %s, but no continuation was saved", seedID, tender)
		}
		return nil, fmt.Errorf("%s has no agent conversation to resume", seedID)
	}
	execution := continuation.Execution
	sessionID := protocol.TrimID(execution.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%s has no agent conversation to resume", seedID)
	}
	if claimed {
		id, _ := tender.Session()
		if id != sessionID {
			return nil, d.seedMoveError(&garden.TakeoverRefused{SeedID: seedID, Verb: garden.VerbTend, Tender: tender}, b)
		}
	}
	if existing := d.gardenSession(sessionID); existing != nil && existing.ProfileID != seed.ProfileID {
		owner, _ := d.store.GetProfile(seed.ProfileID)
		caller, _ := d.store.GetProfile(existing.ProfileID)
		return nil, fmt.Errorf("seed %s belongs to profile %q; its previous agent now belongs to profile %q: hand the seed to a new agent in its own profile", seed.ID, owner.Name, caller.Name)
	}
	if existing := d.gardenSession(sessionID); existing != nil &&
		(execution.HostKind == garden.HostRemote || d.sessionHasLiveWorker(sessionID)) {
		if _, _, _, err := d.applySeedMoveProtected(protection, seedID, garden.VerbTend, func(b who.Bindings) (seedMoveAsk, error) {
			r, err := d.requestFromSession(sessionID, b)
			if err != nil {
				return seedMoveAsk{}, err
			}
			party, err := d.claimantFor(r, "")
			if err != nil {
				return seedMoveAsk{}, err
			}
			return seedMoveAsk{ask: garden.Ask{By: r.Actor()}, claimant: party, execution: sessionID}, nil
		}, "", expectedRev); err != nil {
			return nil, err
		}
		if err := d.resolveGardenReviewAction(review, seedID, "resume"); err != nil {
			d.logf("Garden review: settle %s after Resume: %v", seedID, err)
		}
		return &seedResumeOutcome{
			SessionID: existing.ID, ProfileID: existing.ProfileID, AlreadyRunning: true,
		}, nil
	}
	if !continuation.ResumeAvailable {
		reason := strings.TrimSpace(continuation.ResumeReason)
		if reason == "" {
			reason = "the original conversation is unavailable"
		}
		return nil, fmt.Errorf("%s cannot resume: %s", seedID, reason)
	}
	recorded := sessionReopenVerdict{SessionID: sessionID}
	d.planReopenProfile(&recorded)
	if recorded.ProfileDeleted {
		return nil, fmt.Errorf("%s cannot resume: its profile was deleted", seedID)
	}
	profileID := recorded.ProfileID
	if profileID != seed.ProfileID {
		owner, _ := d.store.GetProfile(seed.ProfileID)
		target, _ := d.store.GetProfile(profileID)
		return nil, fmt.Errorf("seed %s belongs to profile %q; its conversation would reopen in profile %q: hand the seed to a new agent in its own profile", seed.ID, owner.Name, target.Name)
	}
	afterSpawn := func() error {
		if _, err := d.validateGardenReviewAction(review, seedID, "resume"); err != nil {
			return err
		}
		return d.bindResumedSeed(protection, seed, seedDoc, sessionID, strings.TrimSpace(execution.Cwd),
			strings.TrimSpace(execution.Agent), strings.TrimSpace(execution.Resume))
	}
	reopened, err := d.reopenSessionRuntimeWithProtection(protection, sessionReopenPlan{
		SessionID: sessionID,
		Directory: execution.Cwd,
		Title:     seed.Title,
		ProfileID: profileID,
	}, d.newDelegationRollback(), afterSpawn)
	if err != nil {
		return nil, err
	}
	if err := d.resolveGardenReviewAction(review, seedID, "resume"); err != nil {
		d.logf("Garden review: settle %s after Resume: %v", seedID, err)
	}

	d.logf("resume: reopened seed %q as session %s", seedID, sessionID)
	return &seedResumeOutcome{SessionID: reopened.SessionID, ProfileID: reopened.ProfileID}, nil
}

func (d *Daemon) bindResumedSeed(
	_ foregroundCleanupProtection,
	seed garden.Seed,
	seedDoc docstore.Document,
	sessionID protocol.SessionID, directory string, agent string, resumeID string,
) error {
	d.lockGardenRoles()
	defer d.unlockGardenRoles()
	b, err := d.bindings()
	if err != nil {
		return err
	}
	r, err := d.requestFromSession(sessionID, b)
	if err != nil {
		return err
	}
	party, err := d.claimantFor(r, "")
	if err != nil {
		return err
	}
	next, err := garden.Tend(seed, party, garden.Ask{By: r.Actor()}, b)
	if err != nil {
		return fmt.Errorf("reclaim %s after resume: %w", seed.ID, d.seedMoveError(err, b))
	}
	if next.Status != seed.Status {
		next.StateChangedAt = formatGardenTime(d.gardenTime())
	}
	next.LastExecutionID = sessionID

	seedSchema, err := d.seedsCollection()
	if err != nil {
		return err
	}
	dispatchSchema, err := d.dispatchesCollection()
	if err != nil {
		return err
	}
	seedBody, err := next.Encode()
	if err != nil {
		return err
	}

	dispatch, dispatchDoc, found, err := d.gardenDispatchDocument(sessionID)
	if err != nil {
		return err
	}
	session := d.store.Get(sessionID)
	if session == nil {
		return fmt.Errorf("resumed session %s is not tracked", sessionID)
	}
	dispatch = mergeGardenExecution(dispatch, d.observedGardenExecution(session, resumeID, d.gardenTime()))
	dispatch.SessionID = sessionID
	dispatch.Crown = seed.ID
	dispatch.SupersededBy = ""
	if dispatch.Cwd == "" {
		dispatch.Cwd = directory
	}
	if dispatch.Agent == "" {
		dispatch.Agent = agent
	}
	dispatch.Resume = resumeID
	dispatchBody, err := dispatch.Encode()
	if err != nil {
		return err
	}

	seedExpected := seedDoc.Rev
	dispatchExpected := docstore.ExpectAbsent
	if found {
		dispatchExpected = dispatchDoc.Rev
	}
	seedFact := documentChangedFact(garden.Namespace, garden.CollectionSeeds, seed.ID, false)
	dispatchFact := documentChangedFact(garden.Namespace, garden.CollectionDispatches, string(sessionID), false)
	commits := []store.DocumentCommit{
		{
			Write: store.DocumentWrite{Schema: *seedSchema, ID: seed.ID, Body: seedBody, Expected: &seedExpected},
			Fact:  seedFact,
		},
		{
			Write: store.DocumentWrite{Schema: *dispatchSchema, ID: string(sessionID), Body: dispatchBody, Expected: &dispatchExpected},
			Fact:  dispatchFact,
		},
	}
	tended, err := lifecycleOccurrence(garden.VerbTend, seed.ID, garden.Ask{By: r.Actor()})
	if err != nil {
		return err
	}
	events, err := encodeGardenSeedEvents(tended)
	if err != nil {
		return err
	}
	written, eventSeqs, err := d.store.CommitDocumentWritesWithEvents(commits, events, d.gardenTime())
	if err == nil {
		err = d.discardAllIneligibleGardenSeedBellsLocked()
	}
	if err != nil {
		if docstore.IsConflict(err) {
			return fmt.Errorf("%s changed while its conversation was resuming; refresh it and try again", seed.ID)
		}
		return err
	}
	d.announceCommittedWrite(seedFact, written[0].Seq)
	d.announceCommittedWrite(dispatchFact, written[1].Seq)
	announceGardenSeedEvents(d, eventSeqs)
	d.rememberDispatchProjection(sessionID, dispatch, written[1].Rev)
	return nil
}

func (d *Daemon) handleSeedResume(client *wsClient, msg *protocol.SeedResumeMessage) {
	requestID := protocol.Deref(msg.RequestID)
	outcome, err := d.resumeSeedFromReview(msg.SeedID, msg.Review)
	response := protocol.SeedResumeResultMessage{
		Event:     protocol.EventSeedResumeResult,
		RequestID: requestID,
		Success:   err == nil,
	}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
	} else {
		response.SessionID = protocol.Ptr(outcome.SessionID)
		response.ProfileID = protocol.Ptr(outcome.ProfileID)
		if outcome.AlreadyRunning {
			response.AlreadyRunning = protocol.Ptr(true)
		}
	}
	d.sendToClient(client, response)
}
