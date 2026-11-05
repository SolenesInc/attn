package daemon

import (
	"context"
	"strings"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
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
		return nil, protocol.Refuse(protocol.RefusalInvalidInput, "seed_id is required")
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
		return nil, protocol.Refuse(protocol.RefusalConflict, "%s changed since you reviewed it; refresh the garden", seedID)
	}
	if garden.Closed(seed.Status) {
		return nil, protocol.Refuse(protocol.RefusalWrongState, "%s is %s; replant it before resuming its agent", seed.ID, seed.Status)
	}
	return d.wakeSeedTender(protection, seed, tenderWakeCause{
		tend: func(claimant who.Party, actor who.Actor) error {
			_, _, _, err := d.applySeedTransitionDetailedAsAtRevisionProtected(protection,
				seedID, garden.VerbTend, garden.Ask{Actor: actor, Claimant: claimant}, "", expectedRev)
			return err
		},
		settle: func() {
			if err := d.resolveGardenReviewAction(review, seedID, "resume"); err != nil {
				d.logf("Garden review: settle %s after Resume: %v", seedID, err)
			}
		},
	})
}

type tenderWakeCause struct {
	tend   func(claimant who.Party, actor who.Actor) error
	settle func()
	mail   bool
}

type tenderWakeBlocked struct{ err error }

func (e tenderWakeBlocked) Error() string { return e.err.Error() }
func (e tenderWakeBlocked) Unwrap() error { return e.err }

func blockedWake(err error) error { return tenderWakeBlocked{err: err} }

func (d *Daemon) wakeSeedTender(protection foregroundCleanupProtection, seed garden.Seed, cause tenderWakeCause) (*seedResumeOutcome, error) {
	return d.wakeFromContinuation(protection, seed, d.continuationOnItsMachine(seed), cause)
}

func (d *Daemon) wakeFromContinuation(protection foregroundCleanupProtection, seed garden.Seed, continuation *seedContinuation, cause tenderWakeCause) (*seedResumeOutcome, error) {
	seedID := seed.ID
	tend, settle := cause.tend, cause.settle
	var executed protocol.SessionID
	if continuation != nil {
		executed = continuation.Execution.SessionID
	}
	if key, member := d.seedResumeMember(seed, executed); member {
		return d.resumeMemberSeed(seed, key, tend, settle, seed.Tender.IsZero())
	}
	if continuation == nil {
		if !seed.Tender.IsZero() {
			return nil, blockedWake(protocol.Refuse(protocol.RefusalWrongState, "%s was tended by %s, but no continuation was saved", seedID, d.partyName(seed.Tender)))
		}
		return nil, blockedWake(protocol.Refuse(protocol.RefusalWrongState, "%s has no agent conversation to resume", seedID))
	}
	execution := continuation.Execution
	sessionID := protocol.TrimID(execution.SessionID)
	if sessionID == "" {
		return nil, blockedWake(protocol.Refuse(protocol.RefusalWrongState, "%s has no agent conversation to resume", seedID))
	}
	if tender, _ := seed.Tender.Session(); cause.mail && tender != sessionID {
		return nil, blockedWake(protocol.Refuse(protocol.RefusalWrongState, "%s names session %s as its tender, but its saved conversation is session %s's; resume it from the Garden to choose", seedID, shortSessionID(tender), shortSessionID(sessionID)))
	}
	actor := who.Session(sessionID)
	if existing := d.store.Get(sessionID); existing != nil && existing.ProfileID != seed.ProfileID {
		owner, _ := d.store.GetProfile(seed.ProfileID)
		caller, _ := d.store.GetProfile(existing.ProfileID)
		return nil, blockedWake(protocol.Refuse(protocol.RefusalWrongState, "seed %s belongs to profile %q; its previous agent now belongs to profile %q: hand the seed to a new agent in its own profile", seed.ID, owner.Name, caller.Name))
	}
	if existing := d.store.Get(sessionID); existing != nil && d.sessionHasLiveWorker(sessionID) {
		if err := tend(actor, actor.Actor()); err != nil {
			return nil, err
		}
		settle()
		return &seedResumeOutcome{
			SessionID: existing.ID, ProfileID: existing.ProfileID, AlreadyRunning: true,
		}, nil
	}
	if !continuation.ResumeAvailable {
		reason := strings.TrimSpace(continuation.ResumeReason)
		if reason == "" {
			reason = "the original conversation is unavailable"
		}
		refusal := protocol.Refuse(protocol.RefusalWrongState, "%s cannot resume: %s", seedID, reason)
		if continuation.ResumeCheckFailed {
			return nil, refusal
		}
		return nil, blockedWake(refusal)
	}
	recorded := sessionReopenVerdict{SessionID: sessionID}
	d.planReopenProfile(&recorded)
	if recorded.ProfileDeleted {
		return nil, blockedWake(protocol.Refuse(protocol.RefusalWrongState, "%s cannot resume: its profile was deleted", seedID))
	}
	profileID := recorded.ProfileID
	if profileID != seed.ProfileID {
		owner, _ := d.store.GetProfile(seed.ProfileID)
		target, _ := d.store.GetProfile(profileID)
		return nil, blockedWake(protocol.Refuse(protocol.RefusalWrongState, "seed %s belongs to profile %q; its conversation would reopen in profile %q: hand the seed to a new agent in its own profile", seed.ID, owner.Name, target.Name))
	}
	if err := tend(actor, actor.Actor()); err != nil {
		return nil, err
	}
	settle()
	reopened, err := d.reopenSessionRuntimeWithProtection(protection, sessionReopenPlan{
		SessionID:       sessionID,
		Directory:       execution.Cwd,
		Title:           seed.Title,
		ProfileID:       profileID,
		StartsLaunching: cause.mail,
	})
	if err != nil {
		return nil, err
	}
	d.logf("resume: reopened seed %q as session %s", seedID, sessionID)
	return &seedResumeOutcome{SessionID: reopened.SessionID, ProfileID: reopened.ProfileID}, nil
}

func (d *Daemon) seedResumeMember(seed garden.Seed, executed protocol.SessionID) (who.MemberKey, bool) {
	if key, member := seed.Tender.Member(); member {
		return key, true
	}
	if !seed.Tender.IsZero() || protocol.TrimID(executed) == "" {
		return who.MemberKey{}, false
	}
	entry := d.store.SessionLedgerEntry(executed)
	if entry == nil {
		return who.MemberKey{}, false
	}
	key, err := who.ParseMemberKey(protocol.Deref(entry.MemberKey))
	return key, err == nil
}

func (d *Daemon) resumeMemberSeed(
	seed garden.Seed, key who.MemberKey, tend func(who.Party, who.Actor) error, settle func(), claim bool,
) (*seedResumeOutcome, error) {
	if retired, ok := d.retiredCrewMember(key.String()); ok {
		return nil, blockedWake(retiredMemberError(retired))
	}
	if claim {
		if err := tend(who.Member(key), who.User()); err != nil {
			return nil, err
		}
	}
	woken, err := d.crewWake(key.String(), "")
	if err != nil {
		return nil, err
	}
	settle()
	profileID := woken.ProfileID
	if profileID == "" {
		profileID = seed.ProfileID
	}
	return &seedResumeOutcome{SessionID: woken.SessionID, ProfileID: profileID, AlreadyRunning: woken.AlreadyAwake}, nil
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
