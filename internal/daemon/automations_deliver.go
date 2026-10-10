package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/ptybackend"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/garden"
	seedEvents "github.com/victorarias/attn/internal/garden/events"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

type retryableAutomationDeliveryError struct{ cause error }

func (e *retryableAutomationDeliveryError) Error() string { return e.cause.Error() }
func (e *retryableAutomationDeliveryError) Unwrap() error { return e.cause }
func (d *Daemon) deliverObservedAutomationRun(run *store.AutomationRun) error {
	if d.automationDeliveryHook != nil {
		return d.automationDeliveryHook(run)
	}
	return d.deliverAutomationRun(context.Background(), run)
}
func (d *Daemon) handleAutomationDeliveryError(run *store.AutomationRun, deliveryErr error) (*store.AutomationRun, error) {
	if _, launched := d.automationLaunchResults.Load(run.ID); launched {
		current, err := d.store.GetAutomationRun(run.ID)
		return current, errors.Join(deliveryErr, err)
	}
	var retryable *retryableAutomationDeliveryError
	message := strings.ToLower(deliveryErr.Error())
	diskFull := errors.Is(deliveryErr, syscall.ENOSPC) || store.IsStorageFull(deliveryErr) || strings.Contains(message, "no space left on device") || strings.Contains(message, "database or disk is full")
	// A delivery cut short by shutdown stays pending, and the next start delivers it again.
	if !diskFull && (errors.As(deliveryErr, &retryable) || d.stopping()) {
		current, err := d.store.GetAutomationRun(run.ID)
		return current, errors.Join(deliveryErr, err)
	}
	now := time.Now()
	var persistErr error
	if diskFull {
		d.automationLaunchFailures.Store(run.ID, deliveryErr)
	}
	if err := d.store.MarkAutomationRunFailed(run.ID, deliveryErr.Error(), diskFull, now); err != nil {
		persistErr = errors.Join(persistErr, fmt.Errorf("mark run failed: %w", err))
		if diskFull {
			d.broadcastAutomationsChanged(run.DefinitionID)
			return run, errors.Join(deliveryErr, persistErr)
		}
	} else {
		d.automationLaunchFailures.Delete(run.ID)
	}
	if err := d.recordAutomationRunSeedOutcome(run, automationFailureComment(run, deliveryErr.Error())); err != nil {
		persistErr = errors.Join(persistErr, err)
	}
	d.broadcastAutomationsChanged(run.DefinitionID)
	failed, err := d.store.GetAutomationRun(run.ID)
	if err != nil {
		persistErr = errors.Join(persistErr, fmt.Errorf("reload failed run: %w", err))
	}
	return failed, errors.Join(deliveryErr, persistErr)
}

func (d *Daemon) persistAutomationLaunchOutcomes() error {
	var persistErr error
	d.automationLaunchResults.Range(func(key, value any) bool {
		id, result := key.(string), value.(automation.DeliveryResult)
		run, err := d.store.GetAutomationRun(id)
		if err != nil {
			persistErr = errors.Join(persistErr, err)
			return true
		}
		if run == nil || run.State != store.AutomationRunStatePending {
			d.automationLaunchResults.Delete(id)
			return true
		}
		persistErr = errors.Join(persistErr, d.finalizeAutomationRun(run, result))
		return true
	})
	d.automationLaunchFailures.Range(func(key, value any) bool {
		id, cause := key.(string), value.(error)
		run, err := d.store.GetAutomationRun(id)
		if err != nil {
			persistErr = errors.Join(persistErr, err)
			return true
		}
		if run == nil || run.State != store.AutomationRunStatePending {
			d.automationLaunchFailures.Delete(id)
			return true
		}
		_, err = d.handleAutomationDeliveryError(run, cause)
		if _, pending := d.automationLaunchFailures.Load(id); pending {
			persistErr = errors.Join(persistErr, err)
		}
		return true
	})
	return persistErr
}
func (d *Daemon) stopping() bool { return d.life.Ended() }

func automationFailureComment(run *store.AutomationRun, message string) string {
	comment := "Automation delivery failed: " + message
	if run != nil {
		comment += " (automation run " + run.ID + ")"
	}
	return comment
}

func (d *Daemon) cancelAutomationRun(run *store.AutomationRun, reason, message string) (*store.AutomationRun, error) {
	if result, launched := d.automationLaunchResults.Load(run.ID); launched {
		persistErr := d.finalizeAutomationRun(run, result.(automation.DeliveryResult))
		current, err := d.store.GetAutomationRun(run.ID)
		return current, errors.Join(persistErr, err)
	}
	if cause, stopped := d.automationLaunchFailures.Load(run.ID); stopped {
		failed, err := d.handleAutomationDeliveryError(run, cause.(error))
		if _, pending := d.automationLaunchFailures.Load(run.ID); pending {
			return failed, err
		}
		return failed, nil
	}
	now := time.Now()
	var persistErr error
	if err := d.recordAutomationRunSeedOutcome(run, automationFailureComment(run, message)); err != nil {
		persistErr = errors.Join(persistErr, err)
	}
	if err := d.store.MarkAutomationRunCancelled(run.ID, reason, now); err != nil {
		persistErr = errors.Join(persistErr, fmt.Errorf("mark run cancelled: %w", err))
	}
	d.broadcastAutomationsChanged(run.DefinitionID)
	cancelled, err := d.store.GetAutomationRun(run.ID)
	if err != nil {
		persistErr = errors.Join(persistErr, fmt.Errorf("reload cancelled run: %w", err))
	}
	return cancelled, persistErr
}

func (d *Daemon) automationRunIsContinuation(run *store.AutomationRun) (bool, error) {
	origin, err := d.store.OriginAutomationRunIDForSeed(run.DefinitionID, run.SeedID)
	if err != nil {
		return false, err
	}
	return origin != "" && origin != run.ID, nil
}

func (d *Daemon) automationWorkReadyOccurrence(run *store.AutomationRun) (seedEvents.Occurrence, error) {
	continuation, err := d.automationRunIsContinuation(run)
	if err != nil {
		return seedEvents.Occurrence{}, err
	}
	cause := who.Attn()
	if !continuation {
		b, err := d.bindings()
		if err != nil {
			return seedEvents.Occurrence{}, err
		}
		if p, ok := b.PartyOf(run.SessionID); ok {
			cause = p.Actor()
		}
	}
	return seedEvents.Occur(gardenSeedEventModel, gardenSeedEventVocabulary.WorkReady, run.SeedID, seedEvents.WorkReadyPayload{AutomationRunID: run.ID, CausedBy: cause.Ref()})
}

func (d *Daemon) recordAutomationRunSeedOutcome(run *store.AutomationRun, body string) error {
	if run == nil || strings.TrimSpace(run.SeedID) == "" {
		return errors.New("record automation outcome: seed id missing")
	}
	continuation, err := d.automationRunIsContinuation(run)
	if err != nil {
		return fmt.Errorf("record automation outcome: load continuity: %w", err)
	}
	if _, _, err := d.readSeed(run.SeedID); err != nil {
		prompt, agent := "Automation delivery", ""
		var snapshot automation.Snapshot
		if json.Unmarshal([]byte(run.SnapshotJSON), &snapshot) == nil {
			prompt, agent = snapshot.Prompt, snapshot.Launch.Agent
		}
		req := automation.WorkRequest{RunID: run.ID, DefinitionID: run.DefinitionID, Prompt: prompt, Launch: automation.EffectiveLaunch{Agent: agent}, Location: automation.LocationSpec{}, IDs: automation.DeliveryIDs{SeedID: run.SeedID, SessionID: run.SessionID, ProfileID: run.ProfileID}}
		if _, _, ensureErr := d.ensureAutomationSeed(req); ensureErr != nil {
			return fmt.Errorf("record automation outcome: ensure seed: %w", ensureErr)
		}
	}
	notes, err := d.readNotesDomain(run.SeedID)
	if err != nil {
		return fmt.Errorf("record automation outcome: read notes: %w", err)
	}
	seen := false
	for _, note := range notes {
		seen = seen || note.Body == body
	}
	if !seen {
		author := who.Attn()
		if !continuation {
			b, err := d.bindings()
			if err != nil {
				return err
			}
			if p, ok := b.PartyOf(run.SessionID); ok {
				author = p.Actor()
			}
		}
		if _, err := d.appendSeedNote(run.SeedID, body, author, garden.NoteKindNote, nil, true); err != nil {
			return fmt.Errorf("record automation outcome: append note: %w", err)
		}
	}
	if continuation {
		return nil
	}
	seed, _, err := d.readSeed(run.SeedID)
	if err != nil || garden.Closed(seed.Status) {
		return err
	}
	_, _, _, err = d.applySeedMove(run.SeedID, garden.VerbWither, d.automationSeedMove(run.SeedID, run.SessionID, garden.Ask{By: who.Attn(), Reason: garden.TrimReason(body)}), "", 0)
	return err
}
func (d *Daemon) deliverAutomationRun(ctx context.Context, run *store.AutomationRun) error {
	if result, launched := d.automationLaunchResults.Load(run.ID); launched {
		return d.finalizeAutomationRun(run, result.(automation.DeliveryResult))
	}
	if cause, stopped := d.automationLaunchFailures.Load(run.ID); stopped {
		return cause.(error)
	}
	definition, err := d.store.GetAutomationDefinition(run.DefinitionID)
	if err != nil {
		return err
	}
	if definition == nil || !definition.Enabled {
		return errors.New("automation definition is disabled; refusing pending delivery")
	}
	var snapshot automation.Snapshot
	if err := json.Unmarshal([]byte(run.SnapshotJSON), &snapshot); err != nil {
		return err
	}
	snapshot.Launch = snapshot.Launch.WithLegacyDefaults()
	if err := snapshot.Launch.Validate(); err != nil {
		return fmt.Errorf("invalid unattended launch contract: %w", err)
	}
	occurrence, err := d.store.GetAutomationOccurrence(run.OccurrenceID)
	if err != nil {
		return err
	}
	if occurrence == nil {
		return errors.New("automation occurrence missing")
	}
	continuityKey := ""
	switch snapshot.Continuity {
	case "per_subject":
		continuityKey = occurrence.SubjectKey
	case "singleton":
		continuityKey = "singleton"
	}
	req := automation.WorkRequest{RunID: run.ID, DefinitionID: run.DefinitionID, SubjectKey: occurrence.SubjectKey, ContinuityKey: continuityKey, Provider: occurrence.Provider, Prompt: snapshot.Prompt, Context: json.RawMessage(occurrence.PayloadJSON), Launch: snapshot.Launch, Location: snapshot.Location, IDs: automation.DeliveryIDs{SeedID: run.SeedID, SessionID: run.SessionID, ProfileID: run.ProfileID}}
	if err := d.validateAutomationContinuation(req); err != nil {
		return err
	}
	result, err := d.materializeAutomationRun(ctx, req)
	if err != nil {
		return err
	}
	d.automationLaunchResults.Store(run.ID, result)
	return d.finalizeAutomationRun(run, result)
}

func (d *Daemon) finalizeAutomationRun(run *store.AutomationRun, result automation.DeliveryResult) error {
	ready, err := d.automationWorkReadyOccurrence(run)
	if err != nil {
		return err
	}
	events, err := encodeGardenSeedEvents(ready)
	if err != nil {
		return err
	}
	seq, inserted, err := d.store.MarkAutomationRunDeliveredWithEvent(
		run.ID, string(result.Resolved), events[0], time.Now(),
	)
	if err != nil {
		return err
	}
	if inserted {
		announceGardenSeedEvents(d, []int64{seq})
	}
	d.automationLaunchResults.Delete(run.ID)
	d.broadcastAutomationsChanged(run.DefinitionID)
	return nil
}

func (d *Daemon) materializeAutomationRun(ctx context.Context, req automation.WorkRequest) (automation.DeliveryResult, error) {
	continuation, restoreSeed, err := d.ensureAutomationSeed(req)
	if err != nil {
		return automation.DeliveryResult{}, fmt.Errorf("ensure seed: %w", err)
	}
	result, err := d.launchAutomationRun(ctx, req, continuation)
	if err != nil && restoreSeed != nil {
		return automation.DeliveryResult{}, errors.Join(err, restoreSeed())
	}
	return result, err
}

func (d *Daemon) launchAutomationRun(ctx context.Context, req automation.WorkRequest, continuation bool) (automation.DeliveryResult, error) {
	if continuation {
		if err := d.ensureAutomationOccurrenceNote(req); err != nil {
			return automation.DeliveryResult{}, fmt.Errorf("record occurrence: %w", err)
		}
	}
	location, err := d.prepareAutomationLocation(ctx, req)
	if err != nil {
		return automation.DeliveryResult{}, fmt.Errorf("prepare location: %w", err)
	}
	if err := d.bindAutomationSeedLocation(req, location); err != nil {
		return automation.DeliveryResult{}, fmt.Errorf("bind seed location: %w", err)
	}
	if err := d.ensureAutomationSession(ctx, req, location.Directory); err != nil {
		return automation.DeliveryResult{}, fmt.Errorf("ensure session: %w", err)
	}
	if err := d.verifyAutomationDelivery(ctx, req, location.Directory); err != nil {
		return automation.DeliveryResult{}, fmt.Errorf("verify delivery: %w", err)
	}
	return automation.DeliveryResult{SeedID: req.IDs.SeedID, SessionID: req.IDs.SessionID, ProfileID: req.IDs.ProfileID, Directory: location.Directory, Revision: location.Revision, Resolved: location.Resolved, Mode: "created"}, nil
}

func (d *Daemon) validateAutomationContinuation(req automation.WorkRequest) error {
	if req.ContinuityKey == "" {
		return nil
	}
	binding, err := d.store.GetActiveAutomationContinuityBinding(req.DefinitionID, req.ContinuityKey)
	if err != nil {
		return err
	}
	if binding == nil {
		return errors.New("automation continuity binding missing")
	}
	if binding.SeedID != req.IDs.SeedID || binding.SessionID != req.IDs.SessionID {
		return errors.New("automation run does not match its continuity binding")
	}
	if binding.OriginRunID == "" || binding.OriginRunID == req.RunID {
		return nil
	}
	origin, err := d.store.GetAutomationRun(binding.OriginRunID)
	if err != nil {
		return err
	}
	if origin == nil {
		return errors.New("continuity origin run missing")
	}
	var originSnapshot automation.Snapshot
	if err := json.Unmarshal([]byte(origin.SnapshotJSON), &originSnapshot); err != nil {
		return fmt.Errorf("continuity origin snapshot: %w", err)
	}
	reqContract := automation.NewContinuationContract(req.Prompt, req.Launch, req.Location)
	if !originSnapshot.ContinuationContract().Equal(reqContract) {
		return errors.New("automation reviewer contract changed; refusing to reuse a session with stale instructions")
	}
	if req.Provider == "github" {
		originOccurrence, err := d.store.GetAutomationOccurrence(origin.OccurrenceID)
		if err != nil || originOccurrence == nil {
			return errors.Join(errors.New("continuity origin occurrence missing"), err)
		}
		originPR, err := automation.ParsePullRequestInput(json.RawMessage(originOccurrence.PayloadJSON))
		if err != nil {
			return fmt.Errorf("continuity origin payload: %w", err)
		}
		currentPR, err := automation.ParsePullRequestInput(req.Context)
		if err != nil {
			return err
		}
		if originPR.SubjectKey() != currentPR.SubjectKey() || currentPR.SubjectKey() != req.ContinuityKey {
			return errors.New("automation reviewer pull-request identity changed; refusing to reuse its session")
		}
	}
	if canStartWithdrawnUndeliveredReviewer(origin) {
		return nil
	}
	if d.automationSessionIsLive(req.IDs.SessionID) {
		return nil
	}
	_, err = d.automationResumeSessionID(req)
	return err
}
func (d *Daemon) automationSessionIsLive(sessionID protocol.SessionID) bool {
	return d.sessionLive(context.Background(), sessionID)
}
func (d *Daemon) automationResumeSessionID(req automation.WorkRequest) (string, error) {
	resumeID := strings.TrimSpace(d.store.GetResumeSessionID(req.IDs.SessionID))
	if resumeID == "" {
		return "", errors.New("reviewer continuity cannot resume the stopped session without a recorded transcript")
	}
	driver := agentdriver.Get(req.Launch.Agent)
	if !d.conversationReady(driver, resumeID) {
		return "", errors.New("reviewer continuity transcript is unavailable; refusing an unattended fresh session")
	}
	return resumeID, nil
}
func (d *Daemon) ensureAutomationSeed(req automation.WorkRequest) (bool, func() error, error) {
	if err := d.requireHome(garden.Surface); err != nil {
		return false, nil, err
	}
	def, err := d.store.GetAutomationDefinitionIncludingDeleted(req.DefinitionID)
	if err != nil {
		return false, nil, err
	}
	if def == nil {
		return false, nil, fmt.Errorf("definition missing")
	}
	if req.IDs.SeedID == "" {
		return false, nil, errors.New("automation seed id missing")
	}
	continuation := false
	if req.ContinuityKey != "" {
		binding, err := d.store.GetActiveAutomationContinuityBinding(req.DefinitionID, req.ContinuityKey)
		if err != nil {
			return false, nil, err
		}
		if binding == nil || binding.SeedID != req.IDs.SeedID || binding.SessionID != req.IDs.SessionID {
			return false, nil, errors.New("automation seed does not match its continuity binding")
		}
		continuation = binding.OriginRunID != "" && binding.OriginRunID != req.RunID
	}
	title := strings.TrimSpace(def.Name)
	if _, _, reviewTitle, ok := automationReviewNames(req); ok {
		title = strings.TrimSpace(reviewTitle)
	}
	body := strings.TrimSpace(req.Prompt)
	var restore func() error
	if seed, _, readErr := d.readSeed(req.IDs.SeedID); readErr == nil {
		if err := d.requireSeedInProfile(seed.ID, req.IDs.ProfileID, false); err != nil {
			return false, nil, err
		}
		if garden.Closed(seed.Status) {
			if restore, err = d.activateAutomationContinuationSeed(req.IDs.SeedID, req.IDs.SessionID); err != nil {
				return false, nil, err
			}
		}
	} else {
		if err := garden.ValidatePlant(title, body); err != nil {
			return false, nil, err
		}
		schema, schemaErr := d.seedsCollection()
		if schemaErr != nil {
			d.ensureGardenCollections()
			schema, schemaErr = d.seedsCollection()
		}
		if schemaErr != nil {
			return false, nil, schemaErr
		}
		seed := d.initializeSeedLifecycle(garden.Seed{
			ProfileID: req.IDs.ProfileID, ID: req.IDs.SeedID, Title: title, Body: body,
			Status: garden.StatusPlanted, Planter: who.Attn(), StepSlug: garden.StepSlug(title), Edges: []garden.Edge{}, Vars: []garden.Var{},
		})
		if _, err := d.plantSeed(*schema, seed); err != nil {
			return false, nil, err
		}
	}
	if bound, ok := d.gardenDispatchCrown(req.IDs.SessionID); ok && bound != req.IDs.SeedID {
		return false, nil, fmt.Errorf("automation session is bound to %s, want %s", bound, req.IDs.SeedID)
	}
	return continuation, restore, nil
}

func (d *Daemon) activateAutomationContinuationSeed(seedID string, sessionID protocol.SessionID) (func() error, error) {
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return nil, fmt.Errorf("read automation continuation seed %s: %w", seedID, err)
	}
	quietMove := func(verb garden.Verb, reason string) error {
		_, _, _, err := d.applySeedMove(seedID, verb, d.automationSeedMove(seedID, sessionID, garden.Ask{By: who.Attn(), Reason: reason, SuppressNotification: true}), "", 0)
		return err
	}
	var restore func() error
	if garden.Closed(seed.Status) {
		closeVerb, reason := garden.VerbWither, seed.Reason
		if seed.Status == garden.StatusHarvested {
			closeVerb = garden.VerbHarvest
		}
		restore = func() error { return quietMove(closeVerb, reason) }
		if err := quietMove(garden.VerbReplant, ""); err != nil {
			return nil, fmt.Errorf("replant automation continuation seed %s: %w", seedID, err)
		}
	}
	return restore, nil
}

func (d *Daemon) automationSeedMove(seedID string, sessionID protocol.SessionID, ask garden.Ask) func(who.Bindings) (seedMoveAsk, error) {
	return func(b who.Bindings) (seedMoveAsk, error) {
		seed, _, err := d.readSeed(seedID)
		if err != nil {
			return seedMoveAsk{}, err
		}
		tender, claimed := seed.Claim.Lasts(b)
		self, live := b.PartyOf(sessionID)
		id, session := tender.Session()
		ask.Force = claimed && ((live && tender == self) || (session && id == sessionID))
		return seedMoveAsk{ask: ask}, nil
	}
}

func (d *Daemon) claimAutomationSeed(req automation.WorkRequest) error {
	d.lockGardenRoles()
	defer d.unlockGardenRoles()
	b, err := d.bindings()
	if err != nil {
		return err
	}
	party, ok := b.PartyOf(req.IDs.SessionID)
	if !ok {
		return fmt.Errorf("automation session %s has no ledger row", req.IDs.SessionID)
	}
	seed, doc, err := d.readSeed(req.IDs.SeedID)
	if err != nil {
		return err
	}
	tender, _ := seed.Claim.Tender()
	if tender != party || seed.Status != garden.StatusGrowing {
		next, err := garden.Tend(seed, party, garden.Ask{By: who.Attn()}, b)
		if err != nil {
			return d.seedMoveError(err, b)
		}
		if next.Status != seed.Status {
			next.StateChangedAt = formatGardenTime(d.gardenTime())
		}
		next.LastExecutionID = req.IDs.SessionID
		schema, err := d.seedsCollection()
		if err != nil {
			return err
		}
		tended, err := lifecycleOccurrence(garden.VerbTend, seed.ID, garden.Ask{By: who.Attn(), SuppressNotification: true})
		if err != nil {
			return err
		}
		if _, err := d.writeSeedWithEvents(*schema, next, doc.Rev, tended); err != nil {
			return err
		}
	}
	origin, err := d.automationContinuationOrigin(req)
	if err != nil {
		return err
	}
	if origin != nil {
		if _, err := d.store.SetGardenSeedWatch(party, seed.ID, true, d.gardenTime()); err != nil {
			return err
		}
	}
	return d.discardAllIneligibleGardenSeedBellsLocked()
}

func (d *Daemon) ensureAutomationOccurrenceNote(req automation.WorkRequest) error {
	inputPath, err := d.ensureAutomationOccurrenceInput(req)
	if err != nil {
		return err
	}
	body := "Accepted automation occurrence " + req.RunID + " for this thread. Structured occurrence input: " + inputPath
	notes, err := d.readNotesDomain(req.IDs.SeedID)
	if err != nil {
		return err
	}
	for _, note := range notes {
		if note.Body == body {
			return nil
		}
	}
	if _, err := d.appendSeedNote(req.IDs.SeedID, body, who.Attn(), garden.NoteKindNote, nil, false); err != nil {
		return err
	}
	return nil
}
func (d *Daemon) prepareAutomationLocation(ctx context.Context, req automation.WorkRequest) (automation.PreparedLocation, error) {
	if req.Location.Type == "directory" {
		directory, err := validateDelegationDirectory(req.Location.Path)
		if err != nil {
			return automation.PreparedLocation{}, err
		}
		if directory != filepath.Clean(req.Location.Path) {
			return automation.PreparedLocation{}, fmt.Errorf("automation location no longer resolves to its approved directory")
		}
		resolved, _ := json.Marshal(automation.ResolvedLocation{Type: "directory", Path: directory})
		return automation.PreparedLocation{Directory: directory, Resolved: resolved}, nil
	}
	if req.Location.Type != "repository_worktree" {
		return automation.PreparedLocation{}, fmt.Errorf("unsupported location %q", req.Location.Type)
	}
	pr, err := automation.ParsePullRequestInput(req.Context)
	if err != nil {
		return automation.PreparedLocation{}, err
	}
	originRun, err := d.automationContinuationOrigin(req)
	if err != nil {
		return automation.PreparedLocation{}, err
	}
	if originRun != nil {
		originOccurrence, err := d.store.GetAutomationOccurrence(originRun.OccurrenceID)
		if err != nil || originOccurrence == nil {
			return automation.PreparedLocation{}, errors.Join(errors.New("continuity origin occurrence missing"), err)
		}
		originPR, err := automation.ParsePullRequestInput(json.RawMessage(originOccurrence.PayloadJSON))
		if err != nil {
			return automation.PreparedLocation{}, fmt.Errorf("continuity origin payload: %w", err)
		}
		if originPR.SubjectKey() != pr.SubjectKey() || (req.ContinuityKey != "" && pr.SubjectKey() != req.ContinuityKey) {
			return automation.PreparedLocation{}, errors.New("automation reviewer pull-request identity changed; refusing to reuse its worktree")
		}
	}
	identity := pr.RepositoryIdentity()
	authorization := ""
	if d.ghRegistry != nil {
		if client, ok := d.ghRegistry.Get(pr.Host); ok {
			authorization = client.GitHTTPSAuthorizationHeader()
		}
	}
	d.automationRepoMu.Lock()
	if d.automationRepos == nil {
		d.automationRepos = make(map[string]*sync.Mutex)
	}
	repoLock := d.automationRepos[identity]
	if repoLock == nil {
		repoLock = &sync.Mutex{}
		d.automationRepos[identity] = repoLock
	}
	d.automationRepoMu.Unlock()
	repoLock.Lock()
	defer repoLock.Unlock()
	source := req.Location.RepositorySources.Default
	mainRepo := ""
	if override, ok := req.Location.RepositorySources.Overrides[identity]; ok {
		source = override
		type overrideInfo struct {
			mainRepo  string
			remoteURL string
		}
		override, gitErr := gitValue(ctx, d.gitExecution(), gitTask{Kind: gitTaskAutomation, Lane: gitDeferred}, func(runCtx context.Context, client *attngit.Client) (overrideInfo, error) {
			mainRepo, runErr := client.ValidateLocalClone(runCtx, source.Path, identity)
			if runErr != nil {
				return overrideInfo{}, runErr
			}
			remoteURL, runErr := client.Output(runCtx, attngit.OpMetadata, mainRepo, "remote", "get-url", "origin")
			return overrideInfo{mainRepo: mainRepo, remoteURL: string(remoteURL)}, runErr
		})
		mainRepo, err = override.mainRepo, gitErr
		if err != nil {
			return automation.PreparedLocation{}, fmt.Errorf("local repository override: %w", err)
		}
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(override.remoteURL)), "https://") && authorization == "" {
			return automation.PreparedLocation{}, &retryableAutomationDeliveryError{cause: fmt.Errorf("GitHub host %s is not authenticated", pr.Host)}
		}
	} else {
		if authorization == "" {
			return automation.PreparedLocation{}, &retryableAutomationDeliveryError{cause: fmt.Errorf("GitHub host %s is not authenticated", pr.Host)}
		}
		root := strings.TrimSpace(d.dataRoot)
		if root == "" {
			root = filepath.Dir(d.socketPath)
		}
		target := filepath.Join(root, "automation", "repos", attngit.RepositoryCacheKey(identity), "repo")
		cloneURL := "https://" + identity + ".git"
		mainRepo, err = gitValue(ctx, d.gitExecution(), gitTask{Kind: gitTaskAutomation, Lane: gitDeferred}, func(runCtx context.Context, client *attngit.Client) (string, error) {
			mainRepo, _, runErr := client.EnsureManagedClone(runCtx, cloneURL, target, identity, authorization)
			return mainRepo, runErr
		})
		if err != nil {
			return automation.PreparedLocation{}, &retryableAutomationDeliveryError{cause: fmt.Errorf("managed repository cache: %w", err)}
		}
	}
	if err := d.gitExecution().Run(ctx, gitTask{Kind: gitTaskAutomation, Lane: gitDeferred}, func(runCtx context.Context, client *attngit.Client) error {
		return client.EnsurePullRequestRevision(runCtx, mainRepo, "origin", pr.Number, pr.HeadSHA, authorization)
	}); err != nil {
		return automation.PreparedLocation{}, &retryableAutomationDeliveryError{cause: err}
	}
	repoName := pr.Repository
	root := strings.TrimSpace(d.dataRoot)
	if root == "" {
		root = filepath.Dir(d.socketPath)
	}
	worktree := filepath.Join(root, "automation", "worktrees", string(req.IDs.SessionID), repoName)
	sessionPersisted := false
	if d.store != nil {
		if existing := d.store.Get(req.IDs.SessionID); existing != nil {
			if filepath.Clean(existing.Directory) != filepath.Clean(worktree) || existing.Agent != req.Launch.Agent {
				return automation.PreparedLocation{}, fmt.Errorf("persisted session does not match automation snapshot")
			}
			sessionPersisted = true
		}
	}
	if originRun != nil && originRun.State == store.AutomationRunStateDelivered {
		dispatch, ok := d.gardenDispatch(req.IDs.SessionID)
		if !ok || activeDispatchCrown(dispatch) != req.IDs.SeedID || filepath.Clean(dispatch.Cwd) != filepath.Clean(worktree) {
			return automation.PreparedLocation{}, errors.New("reviewer continuity seed does not own the expected worktree")
		}
		if _, err := os.Stat(worktree); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return automation.PreparedLocation{}, errors.New("reviewer continuity worktree is missing; refusing to recreate it silently")
			}
			return automation.PreparedLocation{}, fmt.Errorf("inspect reviewer continuity worktree: %w", err)
		}
		sessionPersisted = true
	}
	if err := d.gitExecution().Run(ctx, gitTask{Kind: gitTaskAutomation, Lane: gitDeferred}, func(runCtx context.Context, client *attngit.Client) error {
		_, runErr := client.EnsureAutomationSessionWorktree(runCtx, mainRepo, worktree, pr.HeadSHA, authorization, sessionPersisted)
		return runErr
	}); err != nil {
		return automation.PreparedLocation{}, &retryableAutomationDeliveryError{cause: err}
	}
	resolved, _ := json.Marshal(automation.ResolvedLocation{
		Type: "repository_worktree", Repository: identity, ConfiguredSource: source,
		MainRepository: mainRepo, Worktree: worktree, Revision: pr.HeadSHA,
		ProviderRef: fmt.Sprintf("refs/pull/%d/head", pr.Number),
	})
	return automation.PreparedLocation{Directory: worktree, Revision: pr.HeadSHA, Resolved: resolved}, nil
}
func (d *Daemon) bindAutomationSeedLocation(req automation.WorkRequest, location automation.PreparedLocation) error {
	return d.recordGardenDispatch(req.IDs.SessionID, req.IDs.SeedID, location.Directory, req.Launch.Agent, false)
}
func (d *Daemon) ensureAutomationSession(ctx context.Context, req automation.WorkRequest, directory string) error {
	if err := req.Launch.Validate(); err != nil {
		return fmt.Errorf("invalid unattended launch contract: %w", err)
	}
	continuationRun, err := d.automationContinuationOrigin(req)
	if err != nil {
		return err
	}
	if existing := d.store.SessionLedgerEntry(req.IDs.SessionID); existing != nil {
		if filepath.Clean(existing.Directory) != filepath.Clean(directory) || existing.Agent != req.Launch.Agent {
			return fmt.Errorf("persisted session does not match automation snapshot")
		}
	}
	inputPath, err := d.ensureAutomationOccurrenceInput(req)
	if err != nil {
		return err
	}
	if d.automationSessionIsLive(req.IDs.SessionID) {
		if err := d.claimAutomationSeed(req); err != nil {
			return err
		}
		return d.verifyUnattendedLaunch(req)
	}
	if continuationRun != nil {
		if canStartWithdrawnUndeliveredReviewer(continuationRun) {
			return d.startAutomationSession(req, directory, inputPath)
		}
		return d.continueAutomationSession(ctx, req, directory)
	}
	return d.startAutomationSession(req, directory, inputPath)
}

func (d *Daemon) continueAutomationSession(ctx context.Context, req automation.WorkRequest, directory string) error {
	return d.continueAutomationSessionForeground(req, directory)
}

func (d *Daemon) continueAutomationSessionForeground(req automation.WorkRequest, directory string) error {
	if _, err := d.automationResumeSessionID(req); err != nil {
		return err
	}
	intent, ok := d.store.LaunchIntent(req.IDs.SessionID)
	if !ok {
		return errors.New("reviewer continuity cannot restore the stopped session without its ledger launch intent")
	}
	if err := intent.UnattendedLaunch.WithLegacyDefaults().Validate(); err != nil {
		return fmt.Errorf("reviewer continuity ledger launch contract is invalid: %w", err)
	}
	label := automationSessionLabel(req, directory)
	_, err := d.reopenSessionRuntime(sessionReopenPlan{
		SessionID: req.IDs.SessionID, Directory: directory, Title: label,
		ProfileID: req.IDs.ProfileID, afterSessionRecorded: func() error { return d.claimAutomationSeed(req) },
	}, d.newDelegationRollback(), nil)
	if err != nil {
		return err
	}
	if err := d.verifyUnattendedLaunch(req); err != nil {
		return err
	}
	d.announceBackgroundLaunch("automation", strconv.Itoa(req.DefinitionID), req.IDs.SessionID, "automation")
	return nil
}

func (d *Daemon) automationSessionLaunch(req automation.WorkRequest, directory, inputPath string) (string, string, error) {
	seed, _, err := d.readSeed(req.IDs.SeedID)
	if err != nil {
		return "", "", err
	}
	pullRequest, pullRequestErr := automation.ParsePullRequestInput(req.Context)
	var pullRequestTarget *automation.PullRequestInput
	if pullRequestErr == nil {
		pullRequestTarget = &pullRequest
	}
	definitionName := fmt.Sprintf("Automation %d", req.DefinitionID)
	if definition, err := d.store.GetAutomationDefinition(req.DefinitionID); err == nil && definition != nil {
		definitionName = definition.Name
	}
	prompt := automationSessionPrompt(req.Prompt, inputPath, req.IDs.SeedID, seed.Title, definitionName, pullRequestTarget, pullRequestErr == nil)
	return automationSessionLabel(req, directory), prompt, nil
}

func automationSessionLabel(req automation.WorkRequest, directory string) string {
	label := filepath.Base(directory)
	if _, reviewLabel, _, ok := automationReviewNames(req); ok {
		label = reviewLabel
	}
	return label
}

func (d *Daemon) startAutomationSession(req automation.WorkRequest, directory, inputPath string) error {
	label, prompt, err := d.automationSessionLaunch(req, directory, inputPath)
	if err != nil {
		return err
	}
	client := newInternalWSClient()
	message := &protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: req.IDs.SessionID, Cwd: directory, ProfileID: req.IDs.ProfileID, Agent: req.Launch.Agent, Cols: 80, Rows: 24, Label: protocol.Ptr(label), InitialPrompt: protocol.Ptr(prompt), Model: protocol.Ptr(req.Launch.Model), Effort: protocol.Ptr(req.Launch.Effort), Executable: protocol.Ptr(req.Launch.Executable)}
	d.handleSpawnSessionWithPolicy(client, message, internalSpawnPolicy{afterSessionRecorded: func() error { return d.claimAutomationSeed(req) }, unattendedLaunch: req.Launch, launchPlacement: &launchPlacement{kind: "automation", itemID: strconv.Itoa(req.DefinitionID)}})
	if _, err := readInternalActionResult(client); err != nil {
		return err
	}
	if err := d.verifyUnattendedLaunch(req); err != nil {
		return err
	}
	d.announceBackgroundLaunch("automation", strconv.Itoa(req.DefinitionID), req.IDs.SessionID, "automation")
	return nil
}
func canStartWithdrawnUndeliveredReviewer(origin *store.AutomationRun) bool {
	return origin != nil && origin.State == store.AutomationRunStateCancelled && origin.CancelReason == store.AutomationCancelReasonReviewWithdrawn
}
func (d *Daemon) automationContinuationOrigin(req automation.WorkRequest) (*store.AutomationRun, error) {
	if req.ContinuityKey == "" {
		return nil, nil
	}
	binding, err := d.store.GetActiveAutomationContinuityBinding(req.DefinitionID, req.ContinuityKey)
	if err != nil || binding == nil || binding.OriginRunID == "" || binding.OriginRunID == req.RunID {
		return nil, err
	}
	origin, err := d.store.GetAutomationRun(binding.OriginRunID)
	if err != nil {
		return nil, err
	}
	if origin == nil {
		return nil, errors.New("continuity origin run missing")
	}
	return origin, nil
}
func (d *Daemon) verifyUnattendedLaunch(req automation.WorkRequest) error {
	if err := d.passUnattendedLaunchGate(req); err != nil {
		return &retryableAutomationDeliveryError{cause: err}
	}
	return nil
}
func (d *Daemon) ensureAutomationOccurrenceInput(req automation.WorkRequest) (string, error) {
	if filepath.Base(req.RunID) != req.RunID || strings.TrimSpace(req.RunID) == "" {
		return "", errors.New("invalid automation run id")
	}
	root := strings.TrimSpace(d.dataRoot)
	if root == "" {
		root = filepath.Dir(d.socketPath)
	}
	dir := filepath.Join(root, "automation", "occurrences")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create automation occurrence directory: %w", err)
	}
	path := filepath.Join(dir, req.RunID+".json")
	if current, err := os.ReadFile(path); err == nil {
		if string(current) != string(req.Context) {
			return "", errors.New("automation occurrence artifact disagrees with durable payload")
		}
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read automation occurrence artifact: %w", err)
	}
	tmp, err := os.CreateTemp(dir, req.RunID+"-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create automation occurrence artifact: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(req.Context); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("publish automation occurrence artifact: %w", err)
	}
	return path, nil
}
func automationSessionPrompt(configuredPrompt, inputPath, seedID, seedTitle, definitionName string, pullRequest *automation.PullRequestInput, localOnlyReview bool) string {
	values := prompts.Values{
		"brief":        configuredPrompt,
		"input_path":   inputPath,
		"seed_id":      seedID,
		"seed_title":   seedTitle,
		"local_review": fmt.Sprint(localOnlyReview),
		"has_target":   fmt.Sprint(pullRequest != nil && localOnlyReview),
	}
	if pullRequest != nil && localOnlyReview {
		values["definition"] = fmt.Sprintf("%q", definitionName)
		values["repository"] = pullRequest.RepositoryIdentity()
		values["number"] = fmt.Sprint(pullRequest.Number)
		values["url"] = pullRequest.URL
		values["head_sha"] = pullRequest.HeadSHA
	}
	return prompts.RenderText("automation", "opening", values)
}

const codexDirectoryTrustPrompt = "Do you trust the contents of this directory?"

func stripANSIForPromptMatch(data []byte) string {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		if data[i] != 0x1b {
			out = append(out, data[i])
			i++
			continue
		}
		if i+1 >= len(data) {
			break
		}
		switch data[i+1] {
		case '[':
			i += 2
			for i < len(data) {
				if data[i] >= 0x40 && data[i] <= 0x7e {
					i++
					break
				}
				i++
			}
		case ']':
			i += 2
			for i < len(data) {
				if data[i] == 0x07 {
					i++
					break
				}
				if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		default:
			i += 2
		}
	}
	return string(out)
}

func (d *Daemon) passUnattendedLaunchGate(req automation.WorkRequest) error {
	if req.Launch.Agent != string(protocol.SessionAgentCodex) {
		return nil
	}
	snapshots, ok := d.ptyBackend.(ptybackend.ScreenSnapshotProvider)
	if !ok {
		return errors.New("automation launch cannot verify Codex directory trust gate")
	}
	deadline := time.Now().Add(10 * time.Second)
	acknowledged := false
	for time.Now().Before(deadline) && !d.stopping() {
		info, err := snapshots.ScreenSnapshot(context.Background(), d.primaryTerminal(req.IDs.SessionID))
		if err == nil {
			var payload []byte
			if info.Screen != nil {
				payload = info.Screen.Payload
			}
			screen := stripANSIForPromptMatch(payload)
			if strings.Contains(screen, codexDirectoryTrustPrompt) {
				if !acknowledged {
					if err := d.writeSessionPTY(req.IDs.SessionID, []byte("\r"), "automation"); err != nil {
						return fmt.Errorf("accept Codex directory trust: %w", err)
					}
					acknowledged = true
				}
			} else if acknowledged {
				return nil
			} else if time.Until(deadline) < 5*time.Second && len(payload) > 0 {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if acknowledged {
		return errors.New("automation launch: Codex directory trust prompt did not clear")
	}
	return errors.New("automation launch did not produce a verifiable Codex screen")
}
func (d *Daemon) verifyAutomationDelivery(_ context.Context, req automation.WorkRequest, directory string) error {
	seed, _, err := d.readSeed(req.IDs.SeedID)
	if err != nil {
		return err
	}
	if seed.ID != req.IDs.SeedID {
		return fmt.Errorf("seed links disagree")
	}
	if crown, ok := d.gardenDispatchCrown(req.IDs.SessionID); !ok || crown != req.IDs.SeedID {
		return fmt.Errorf("seed dispatch link missing")
	}
	session := d.store.Get(req.IDs.SessionID)
	if session == nil || session.ProfileID != req.IDs.ProfileID || filepath.Clean(session.Directory) != filepath.Clean(directory) {
		return fmt.Errorf("session links disagree")
	}
	return nil
}
