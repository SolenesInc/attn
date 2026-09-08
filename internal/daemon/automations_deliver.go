package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/garden"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/store"
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
	var retryable *retryableAutomationDeliveryError
	if errors.As(deliveryErr, &retryable) {
		current, err := d.store.GetAutomationRun(run.ID)
		return current, errors.Join(deliveryErr, err)
	}
	if errors.Is(deliveryErr, errAutomationReviewWithdrawn) {
		cancelled, cancelErr := d.cancelAutomationRun(run, store.AutomationCancelReasonReviewWithdrawn, deliveryErr.Error())
		return cancelled, errors.Join(deliveryErr, cancelErr)
	}
	failed, failErr := d.failAutomationRun(run, deliveryErr)
	return failed, errors.Join(deliveryErr, failErr)
}
func (d *Daemon) failAutomationRun(run *store.AutomationRun, deliveryErr error) (*store.AutomationRun, error) {
	now := time.Now()
	var persistErr error
	if err := d.store.MarkAutomationRunFailed(run.ID, deliveryErr.Error(), now); err != nil {
		persistErr = errors.Join(persistErr, fmt.Errorf("mark run failed: %w", err))
	}
	if err := d.recordAutomationRunSeedOutcome(run, automationFailureComment(run, deliveryErr.Error())); err != nil {
		persistErr = errors.Join(persistErr, err)
	}
	d.broadcastAutomationsChanged(run.DefinitionID)
	failed, err := d.store.GetAutomationRun(run.ID)
	if err != nil {
		persistErr = errors.Join(persistErr, fmt.Errorf("reload failed run: %w", err))
	}
	return failed, persistErr
}
func automationFailureComment(run *store.AutomationRun, message string) string {
	comment := "Automation delivery failed: " + message
	if run != nil {
		comment += " (automation run " + run.ID + ")"
	}
	return comment
}

func (d *Daemon) cancelAutomationRun(run *store.AutomationRun, reason, message string) (*store.AutomationRun, error) {
	now := time.Now()
	var persistErr error
	if err := d.store.MarkAutomationRunCancelled(run.ID, reason, now); err != nil {
		persistErr = errors.Join(persistErr, fmt.Errorf("mark run cancelled: %w", err))
	}
	if err := d.recordAutomationRunSeedOutcome(run, automationFailureComment(run, message)); err != nil {
		persistErr = errors.Join(persistErr, err)
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
		req := automation.WorkRequest{RunID: run.ID, DefinitionID: run.DefinitionID, Prompt: prompt, Launch: automation.EffectiveLaunch{Agent: agent}, Location: automation.LocationSpec{}, IDs: automation.DeliveryIDs{SeedID: run.SeedID, SessionID: run.SessionID, WorkspaceID: run.WorkspaceID, PaneID: run.PaneID}}
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
		if _, err := d.appendSeedNote(run.SeedID, body, run.SessionID, "", garden.NoteKindNote, nil); err != nil {
			return fmt.Errorf("record automation outcome: append note: %w", err)
		}
		d.ringSeedActivity(run.SeedID, "note", run.SessionID)
		if continuation {
			d.claimAndDeliverSeedBell(run.SessionID, run.SeedID, "note")
		}
	}
	if continuation {
		return nil
	}
	seed, _, err := d.readSeed(run.SeedID)
	if err != nil || garden.Closed(seed.Status) {
		return err
	}
	_, _, err = d.applySeedTransition(run.SeedID, garden.VerbWither, garden.Ask{Actor: garden.Tender{Session: run.SessionID}, Reason: garden.TrimReason(body)})
	return err
}
func (d *Daemon) deliverAutomationRun(ctx context.Context, run *store.AutomationRun) error {
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
	if occurrence.Provider == "github" {
		stillRequested, err := d.store.GitHubReviewAutomationRunStillRequested(run.ID)
		if err != nil {
			return err
		}
		if !stillRequested {
			return errAutomationReviewWithdrawn
		}
	}
	continuityKey := ""
	switch snapshot.Continuity {
	case "per_subject":
		continuityKey = occurrence.SubjectKey
	case "singleton":
		continuityKey = "singleton"
	}
	req := automation.WorkRequest{RunID: run.ID, DefinitionID: run.DefinitionID, SubjectKey: occurrence.SubjectKey, ContinuityKey: continuityKey, Provider: occurrence.Provider, Prompt: snapshot.Prompt, Context: json.RawMessage(occurrence.PayloadJSON), Launch: snapshot.Launch, Location: snapshot.Location, IDs: automation.DeliveryIDs{SeedID: run.SeedID, SessionID: run.SessionID, WorkspaceID: run.WorkspaceID, PaneID: run.PaneID}}
	if err := d.validateAutomationContinuation(req); err != nil {
		return err
	}
	result, err := d.materializeAutomationRun(ctx, req)
	if err != nil {
		return err
	}
	if err := d.store.MarkAutomationRunDelivered(run.ID, string(result.Resolved), time.Now()); err != nil {
		return err
	}
	// No unit-test coverage: pinned live by scenario-automation-surface.mjs
	// leg2_run_now_and_navigable.
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
	if err := d.ensureAutomationWorkspace(ctx, req, location.Directory); err != nil {
		return automation.DeliveryResult{}, fmt.Errorf("ensure workspace: %w", err)
	}
	if err := d.ensureAutomationPane(ctx, req); err != nil {
		return automation.DeliveryResult{}, fmt.Errorf("ensure pane: %w", err)
	}
	if err := d.ensureAutomationSession(ctx, req, location.Directory); err != nil {
		return automation.DeliveryResult{}, fmt.Errorf("ensure session: %w", err)
	}
	if err := d.verifyAutomationDelivery(ctx, req, location.Directory); err != nil {
		return automation.DeliveryResult{}, fmt.Errorf("verify delivery: %w", err)
	}
	if continuation {
		d.claimAndDeliverSeedBell(req.IDs.SessionID, req.IDs.SeedID, "note")
	}
	return automation.DeliveryResult{SeedID: req.IDs.SeedID, SessionID: req.IDs.SessionID, WorkspaceID: req.IDs.WorkspaceID, Directory: location.Directory, Revision: location.Revision, Resolved: location.Resolved, Mode: "created"}, nil
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
	if binding.SeedID != req.IDs.SeedID || binding.SessionID != req.IDs.SessionID || binding.WorkspaceID != req.IDs.WorkspaceID || binding.PaneID != req.IDs.PaneID {
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
	if d.canStartWithdrawnUndeliveredReviewer(origin, req.IDs.SessionID) {
		return nil
	}
	if d.automationSessionIsLive(req.IDs.SessionID) {
		return nil
	}
	_, err = d.automationResumeSessionID(req)
	return err
}
func (d *Daemon) automationSessionIsLive(sessionID string) bool {
	if d.ptyBackend == nil {
		return false
	}
	for _, liveID := range d.ptyBackend.SessionIDs(context.Background()) {
		if liveID == sessionID {
			return true
		}
	}
	return false
}
func (d *Daemon) automationResumeSessionID(req automation.WorkRequest) (string, error) {
	resumeID := strings.TrimSpace(d.store.GetResumeSessionID(req.IDs.SessionID))
	if resumeID == "" {
		resumeID = strings.TrimSpace(d.gardenDispatchResume(req.IDs.SessionID))
	}
	if resumeID == "" {
		return "", errors.New("reviewer continuity cannot resume the stopped session without a recorded transcript")
	}
	driver := agentdriver.Get(req.Launch.Agent)
	if !agentdriver.ResumeAvailable(driver, resumeID) {
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
		if seed.TenderSession != req.IDs.SessionID || seed.Status != garden.StatusGrowing {
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
			ID: req.IDs.SeedID, Title: title, Body: body,
			Status: garden.StatusPlanted, StepSlug: garden.StepSlug(title), Edges: []garden.Edge{}, Vars: []garden.Var{},
		})
		seed, err = garden.Transition(seed, garden.VerbTend, garden.Ask{Actor: garden.Tender{Session: req.IDs.SessionID}}, func(string) bool { return false })
		if err != nil {
			return false, nil, err
		}
		seed.LastExecutionID = req.IDs.SessionID
		if _, err := d.plantSeed(*schema, seed); err != nil {
			return false, nil, err
		}
	}
	if bound, ok := d.gardenDispatchCrown(req.IDs.SessionID); ok && bound != req.IDs.SeedID {
		return false, nil, fmt.Errorf("automation session is bound to %s, want %s", bound, req.IDs.SeedID)
	}
	return continuation, restore, nil
}

func (d *Daemon) activateAutomationContinuationSeed(seedID, sessionID string) (func() error, error) {
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		return nil, fmt.Errorf("read automation continuation seed %s: %w", seedID, err)
	}
	actor := garden.Tender{Session: sessionID}
	var restore func() error
	if garden.Closed(seed.Status) {
		closeVerb, closeReason := garden.VerbWither, seed.Reason
		if seed.Status == garden.StatusHarvested {
			closeVerb = garden.VerbHarvest
		}
		restore = func() error {
			_, _, err := d.applySeedTransition(seedID, closeVerb, garden.Ask{Actor: actor, Reason: closeReason})
			return err
		}
		if _, _, err := d.applySeedTransition(seedID, garden.VerbReplant, garden.Ask{Actor: actor}); err != nil {
			return nil, fmt.Errorf("replant automation continuation seed %s: %w", seedID, err)
		}
		seed.Status = garden.StatusPlanted
	}
	if seed.Status == garden.StatusGrowing && seed.TenderSession == sessionID {
		return restore, nil
	}
	if _, _, err := d.applySeedTransition(seedID, garden.VerbTend, garden.Ask{Actor: actor}); err != nil {
		return nil, fmt.Errorf("tend automation continuation seed %s: %w", seedID, err)
	}
	return restore, nil
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
	if _, err := d.appendSeedNote(req.IDs.SeedID, body, req.IDs.SessionID, "", garden.NoteKindNote, nil); err != nil {
		return err
	}
	d.ringSeedActivity(req.IDs.SeedID, "note", req.IDs.SessionID)
	return nil
}
func (d *Daemon) prepareAutomationLocation(_ context.Context, req automation.WorkRequest) (automation.PreparedLocation, error) {
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
		mainRepo, err = attngit.ValidateLocalClone(source.Path, identity)
		if err != nil {
			return automation.PreparedLocation{}, fmt.Errorf("local repository override: %w", err)
		}
		remoteURL, remoteErr := attngit.Output(attngit.OpMetadata, mainRepo, "remote", "get-url", "origin")
		if remoteErr != nil {
			return automation.PreparedLocation{}, fmt.Errorf("read local repository origin: %w", remoteErr)
		}
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(string(remoteURL))), "https://") && authorization == "" {
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
		mainRepo, _, err = attngit.EnsureManagedClone(cloneURL, target, identity, authorization)
		if err != nil {
			return automation.PreparedLocation{}, &retryableAutomationDeliveryError{cause: fmt.Errorf("managed repository cache: %w", err)}
		}
	}
	if err := attngit.EnsurePullRequestRevision(mainRepo, "origin", pr.Number, pr.HeadSHA, authorization); err != nil {
		return automation.PreparedLocation{}, &retryableAutomationDeliveryError{cause: err}
	}
	repoName := pr.Repository
	root := strings.TrimSpace(d.dataRoot)
	if root == "" {
		root = filepath.Dir(d.socketPath)
	}
	worktree := filepath.Join(root, "automation", "worktrees", req.IDs.SessionID, repoName)
	sessionPersisted := false
	if d.store != nil {
		if existing := d.store.Get(req.IDs.SessionID); existing != nil {
			if filepath.Clean(existing.Directory) != filepath.Clean(worktree) || existing.WorkspaceID != req.IDs.WorkspaceID || string(existing.Agent) != req.Launch.Agent {
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
		// A delivered origin proves this stable session owned the worktree: preserve
		// its commits, branch switch, and local changes when resuming.
		sessionPersisted = true
	}
	if _, err := attngit.EnsureAutomationSessionWorktree(mainRepo, worktree, pr.HeadSHA, authorization, sessionPersisted); err != nil {
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
	return d.recordGardenDispatch(req.IDs.SessionID, req.IDs.SeedID, "", location.Directory, req.Launch.Agent, false)
}
func (d *Daemon) ensureAutomationWorkspace(_ context.Context, req automation.WorkRequest, directory string) error {
	if existing := d.store.GetWorkspace(req.IDs.WorkspaceID); existing != nil {
		if filepath.Clean(existing.Directory) != filepath.Clean(directory) {
			return fmt.Errorf("workspace directory mismatch: %s", existing.Directory)
		}
		return nil
	}
	title := filepath.Base(directory)
	if reviewTitle, _, _, ok := automationReviewNames(req); ok {
		title = reviewTitle
	}
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{Cmd: protocol.CmdRegisterWorkspace, ID: req.IDs.WorkspaceID, Title: title, Directory: directory})
	if d.store.GetWorkspace(req.IDs.WorkspaceID) == nil {
		return fmt.Errorf("workspace was not persisted")
	}
	if _, msg := d.setWorkspaceMuted(req.IDs.WorkspaceID, false); msg != "" {
		return fmt.Errorf("make workspace visible: %s", msg)
	}
	return nil
}
func (d *Daemon) ensureAutomationPane(_ context.Context, req automation.WorkRequest) error {
	title := filepath.Base(req.Location.Path)
	if title == "." || title == "" {
		title = req.SubjectKey
	}
	if _, reviewTitle, _, ok := automationReviewNames(req); ok {
		title = reviewTitle
	}
	pane, _, err := d.addWorkspaceSessionPane(&protocol.WorkspaceLayoutAddSessionPaneMessage{Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: req.IDs.WorkspaceID, PaneID: protocol.Ptr(req.IDs.PaneID), SessionID: req.IDs.SessionID, Title: protocol.Ptr(title)})
	if err != nil {
		return err
	}
	if protocol.Deref(pane) != req.IDs.PaneID {
		return fmt.Errorf("session pane mismatch: got %s want %s", protocol.Deref(pane), req.IDs.PaneID)
	}
	return nil
}
func (d *Daemon) ensureAutomationSession(_ context.Context, req automation.WorkRequest, directory string) error {
	if err := req.Launch.Validate(); err != nil {
		return fmt.Errorf("invalid unattended launch contract: %w", err)
	}
	continuationRun, err := d.automationContinuationOrigin(req)
	if err != nil {
		return err
	}
	if existing := d.store.Get(req.IDs.SessionID); existing != nil {
		if filepath.Clean(existing.Directory) != filepath.Clean(directory) || existing.WorkspaceID != req.IDs.WorkspaceID || string(existing.Agent) != req.Launch.Agent {
			return fmt.Errorf("persisted session does not match automation snapshot")
		}
	}
	inputPath, err := d.ensureAutomationOccurrenceInput(req)
	if err != nil {
		return err
	}
	if d.automationSessionIsLive(req.IDs.SessionID) {
		// Worker recovery adopted the original launch; do not spawn the stable
		// session ID a second time.
		return d.verifyUnattendedLaunch(req)
	}
	if continuationRun != nil {
		if d.canStartWithdrawnUndeliveredReviewer(continuationRun, req.IDs.SessionID) {
			return d.startAutomationSession(req, directory, inputPath, "")
		}
		resumeID, err := d.automationResumeSessionID(req)
		if err != nil {
			return err
		}
		return d.startAutomationSession(req, directory, inputPath, resumeID)
	}
	return d.startAutomationSession(req, directory, inputPath, "")
}
func (d *Daemon) startAutomationSession(req automation.WorkRequest, directory, inputPath, resumeID string) error {
	pullRequest, pullRequestErr := automation.ParsePullRequestInput(req.Context)
	var pullRequestTarget *automation.PullRequestInput
	if pullRequestErr == nil {
		pullRequestTarget = &pullRequest
	}
	definitionName := req.DefinitionID
	if definition, err := d.store.GetAutomationDefinition(req.DefinitionID); err == nil && definition != nil {
		definitionName = definition.Name
	}
	prompt := automationSessionPrompt(req.Prompt, inputPath, req.IDs.SeedID, definitionName, pullRequestTarget, pullRequestErr == nil)
	label := filepath.Base(directory)
	if _, reviewLabel, _, ok := automationReviewNames(req); ok {
		label = reviewLabel
	}
	client := newInternalWSClient()
	message := &protocol.SpawnSessionMessage{Cmd: protocol.CmdSpawnSession, ID: req.IDs.SessionID, Cwd: directory, WorkspaceID: req.IDs.WorkspaceID, Agent: req.Launch.Agent, Cols: 80, Rows: 24, Label: protocol.Ptr(label), InitialPrompt: protocol.Ptr(prompt), Model: protocol.Ptr(req.Launch.Model), Effort: protocol.Ptr(req.Launch.Effort), Executable: protocol.Ptr(req.Launch.Executable)}
	if resumeID != "" {
		message.ResumeSessionID = protocol.Ptr(resumeID)
	}
	d.handleSpawnSessionWithPolicy(client, message, internalSpawnPolicy{unattendedLaunch: req.Launch})
	if _, err := readInternalActionResult(client); err != nil {
		return err
	}
	return d.verifyUnattendedLaunch(req)
}
func (d *Daemon) canStartWithdrawnUndeliveredReviewer(origin *store.AutomationRun, sessionID string) bool {
	return origin != nil && origin.State == store.AutomationRunStateCancelled && origin.CancelReason == store.AutomationCancelReasonReviewWithdrawn && d.store.Get(sessionID) == nil
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
func automationSessionPrompt(configuredPrompt, inputPath, seedID, definitionName string, pullRequest *automation.PullRequestInput, localOnlyReview bool) string {
	values := prompts.Values{
		"brief":        configuredPrompt,
		"input_path":   inputPath,
		"seed_id":      seedID,
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

// Applying the definition IS the user's authorization for that directory; exact
// screen matching keeps other prompts out.
func (d *Daemon) passUnattendedLaunchGate(req automation.WorkRequest) error {
	if req.Launch.Agent != string(protocol.SessionAgentCodex) {
		return nil
	}
	snapshots, ok := d.ptyBackend.(interface {
		ScreenSnapshot(context.Context, string) (pty.ScreenSnapshotInfo, error)
	})
	if !ok {
		return errors.New("automation launch cannot verify Codex directory trust gate")
	}
	deadline := time.Now().Add(10 * time.Second)
	acknowledged := false
	for time.Now().Before(deadline) {
		info, err := snapshots.ScreenSnapshot(context.Background(), req.IDs.SessionID)
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
	if seed.ID != req.IDs.SeedID || seed.TenderSession != req.IDs.SessionID || seed.Status != garden.StatusGrowing {
		return fmt.Errorf("seed links disagree")
	}
	if crown, ok := d.gardenDispatchCrown(req.IDs.SessionID); !ok || crown != req.IDs.SeedID {
		return fmt.Errorf("seed dispatch link missing")
	}
	session := d.store.Get(req.IDs.SessionID)
	if session == nil || session.WorkspaceID != req.IDs.WorkspaceID || filepath.Clean(session.Directory) != filepath.Clean(directory) {
		return fmt.Errorf("session links disagree")
	}
	return nil
}
