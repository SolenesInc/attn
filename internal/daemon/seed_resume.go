package daemon

import (
	"context"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
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
	continuation := d.continuationForSeedForeground(seed)
	if continuation == nil {
		if tender := protocol.TrimID(seed.TenderSession); tender != "" {
			return nil, fmt.Errorf("%s was tended by session %s, but no continuation was saved", seedID, tender)
		}
		return nil, fmt.Errorf("%s has no agent conversation to resume", seedID)
	}
	execution := continuation.Execution
	sessionID := protocol.TrimID(execution.SessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("%s has no agent conversation to resume", seedID)
	}
	actor := garden.Tender{Session: sessionID}
	if existing := d.gardenSession(sessionID); existing != nil && existing.ProfileID != seed.ProfileID {
		owner, _ := d.store.GetProfile(seed.ProfileID)
		caller, _ := d.store.GetProfile(existing.ProfileID)
		return nil, fmt.Errorf("seed %s belongs to profile %q; its previous agent now belongs to profile %q: hand the seed to a new agent in its own profile", seed.ID, owner.Name, caller.Name)
	}
	if existing := d.gardenSession(sessionID); existing != nil &&
		(execution.HostKind == garden.HostRemote || d.sessionHasLiveWorker(sessionID)) {
		if _, _, _, err := d.applySeedTransitionDetailedAsAtRevisionProtected(protection,
			seedID, garden.VerbTend, garden.Ask{Actor: actor}, "", d.sessionExists, expectedRev); err != nil {
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
	if _, _, _, err := d.applySeedTransitionDetailedAsAtRevisionProtected(protection,
		seedID, garden.VerbTend, garden.Ask{Actor: actor}, "", d.sessionExists, expectedRev); err != nil {
		return nil, err
	}
	reopened, err := d.reopenSessionRuntimeWithProtection(protection, sessionReopenPlan{
		SessionID: sessionID,
		Directory: execution.Cwd,
		Title:     seed.Title,
		ProfileID: profileID,
	}, d.newDelegationRollback())
	if err != nil {
		return nil, err
	}
	if err := d.resolveGardenReviewAction(review, seedID, "resume"); err != nil {
		d.logf("Garden review: settle %s after Resume: %v", seedID, err)
	}

	d.logf("resume: reopened seed %q as session %s", seedID, sessionID)
	return &seedResumeOutcome{SessionID: reopened.SessionID, ProfileID: reopened.ProfileID}, nil
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
