package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automation"
	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/garden"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/store"
)

type automationResumeBackend struct {
	*fakeSpawnBackend
	snapshotCalls int
}

func writeCodexRolloutFixture(t *testing.T, resumeID string) {
	t.Helper()
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	sessionsDir := filepath.Join(codexHome, "sessions", "2026", "07", "20")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir Codex sessions dir: %v", err)
	}
	rollout := []byte(`{"type":"session_meta","payload":{"id":"` + resumeID + `","cwd":"/tmp"}}` + "\n")
	if err := os.WriteFile(filepath.Join(sessionsDir, "rollout-"+resumeID+".jsonl"), rollout, 0o644); err != nil {
		t.Fatalf("write Codex rollout fixture: %v", err)
	}
}

func (b *automationResumeBackend) ScreenSnapshot(context.Context, string) (pty.ScreenSnapshotInfo, error) {
	b.snapshotCalls++
	payload := []byte("reviewer ready")
	if b.snapshotCalls == 1 {
		payload = []byte(codexDirectoryTrustPrompt)
	}
	return pty.ScreenSnapshotInfo{Screen: &pty.ViewportSnapshot{Payload: payload}}, nil
}

func TestStripANSIForPromptMatch_PreservesStyledSplitPrompt(t *testing.T) {
	stream := []byte("\x1b[2J\x1b[H\x1b[1;36mDo you trust \x1b[0m\x1b[8;4Hthe contents \x1b]8;;https://example.com\x1b\\of this directory?\x1b]8;;\x1b\\\x1b7")
	if got := stripANSIForPromptMatch(stream); !strings.Contains(got, codexDirectoryTrustPrompt) {
		t.Fatalf("stripped stream = %q, want prompt %q", got, codexDirectoryTrustPrompt)
	}
}

func testAutomationLaunch(agent string) automation.EffectiveLaunch {
	driverMode := launchcontract.ApprovalAuto
	if agent == string(protocol.SessionAgentCodex) {
		driverMode = launchcontract.ApprovalAutoReview
	}
	return automation.EffectiveLaunch{
		Agent: agent, Model: "review-model", Effort: "high",
		ApprovalProductMode: launchcontract.ApprovalAuto,
		ApprovalDriverMode:  driverMode,
		DirectoryTrust:      launchcontract.TrustConfiguredDirectory,
		Recovery:            launchcontract.RecoveryAdoptOrRestartFresh,
	}
}

func baselineGitHubReviewAutomation(t *testing.T, s *store.Store, definitionID, host string, at time.Time) {
	t.Helper()
	if candidates, err := s.ReconcileAutomationReviewRequests(definitionID, host, nil, at); err != nil || len(candidates) != 0 {
		t.Fatalf("establish review automation baseline: candidates=%#v err=%v", candidates, err)
	}
}

const manualAutomationYAML = `api_version: attn.dev/automations/v1alpha1
id: manual-check
name: Manual check
trigger: {type: manual}
prompt: Check locally.
launch: {driver: codex}
location: {type: directory, path: "%s"}
`

func setupContinuationWorktree(t *testing.T) (*Daemon, automation.WorkRequest, string, string) {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitDaemon(t, repo, "init")
	runGitDaemon(t, repo, "commit", "--allow-empty", "-m", "snapshot")
	runGitDaemon(t, repo, "remote", "add", "origin", "git@github.com:owner/repo.git")
	revisionBytes, err := attngit.NewClient().Output(context.Background(), attngit.OpMetadata, repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(automation.PullRequestInput{
		Provider: "github", Host: "github.com", Owner: "owner", Repository: "repo", Number: 42,
		URL: "https://github.com/owner/repo/pull/42", State: "open", HeadSHA: strings.TrimSpace(string(revisionBytes)),
	})
	location := automation.LocationSpec{Type: "repository_worktree", RepositorySources: automation.RepositorySources{
		Default: automation.RepositorySource{Type: "managed_cache"},
		Overrides: map[string]automation.RepositorySource{
			"github.com/owner/repo": {Type: "local_clone", Path: repo},
		},
	}}
	d := newEnrolledDaemon(t, "")
	d.dataRoot = filepath.Join(root, "instance")
	enrollHomeForTest(t, d)
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	def, err := d.store.UpsertAutomationDefinition("review", "Review", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	const subject = "github.com/owner/repo#42"
	baselineGitHubReviewAutomation(t, d.store, def.ID, "github.com", now)
	if _, err := d.store.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now); err != nil {
		t.Fatal(err)
	}
	origin, _, err := d.store.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, string(payload), `{}`, now, store.AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", SeedID: "s-at0001", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstReq := automation.WorkRequest{
		RunID: origin.ID, DefinitionID: def.ID, SubjectKey: subject, ContinuityKey: subject,
		Provider: "github", Prompt: "Review", Context: payload, Location: location,
		Launch: testAutomationLaunch("codex"), IDs: automation.DeliveryIDs{
			SeedID: origin.SeedID, SessionID: origin.SessionID, WorkspaceID: origin.WorkspaceID, PaneID: origin.PaneID,
		},
	}
	if _, _, err := d.ensureAutomationSeed(firstReq); err != nil {
		t.Fatal(err)
	}
	prepared, err := d.prepareAutomationLocation(context.Background(), firstReq)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.recordGardenDispatch(origin.SessionID, origin.SeedID, "", prepared.Directory, "codex", false); err != nil {
		t.Fatal(err)
	}
	if err := markAutomationRunDeliveredForTest(d.store, origin.ID, string(prepared.Resolved), now); err != nil {
		t.Fatal(err)
	}
	continuation := firstReq
	continuation.RunID = "run-2"
	return d, continuation, prepared.Directory, repo
}

func TestSuccessfulContinuationReopensBoundSeed(t *testing.T) {
	d := newGardenDaemon(t)
	const sessionID = "sess-a"
	seedID, err := d.bindDelegationSeed(sessionID, "", "Review the pull request.", "Review", "", t.TempDir(), "codex", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.applySeedTransition(seedID, garden.VerbHarvest, garden.Ask{Actor: garden.Tender{Session: sessionID}, Reason: "first review complete"}); err != nil {
		t.Fatal(err)
	}
	addGardenSession(t, d, "observer")
	watchSeed(t, d, "observer", seedID, false)
	req := automation.WorkRequest{
		RunID: "run-2", DefinitionID: "review", ContinuityKey: "github.com/owner/repo#42",
		IDs: automation.DeliveryIDs{SeedID: seedID, SessionID: sessionID},
	}
	if _, err := d.activateAutomationContinuationSeed(req.IDs.SeedID, req.IDs.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.activateAutomationContinuationSeed(req.IDs.SeedID, req.IDs.SessionID); err != nil {
		t.Fatal(err)
	}
	seed, _, err := d.readSeed(seedID)
	if err != nil {
		t.Fatal(err)
	}
	if seed.Status != garden.StatusGrowing || seed.TenderSession != sessionID {
		t.Fatalf("continued seed=%#v, want growing and tended by %s", seed, sessionID)
	}
	if queued := queuedSeedBells(t, d, "observer"); len(queued) != 0 {
		t.Fatalf("automation reactivation rang before work was ready: %q", queued)
	}
}

func TestAutomationContinuationRetendStaysQuiet(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, d *Daemon, seedID string)
	}{
		{
			name: "parked",
			setup: func(t *testing.T, d *Daemon, seedID string) {
				t.Helper()
				if _, _, err := d.applySeedTransition(seedID, garden.VerbPark, garden.Ask{Actor: garden.Tender{Session: "sess-a"}}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "abandoned tender",
			setup: func(t *testing.T, d *Daemon, seedID string) {
				t.Helper()
				addGardenSession(t, d, "stale")
				if _, _, err := d.applySeedTransition(seedID, garden.VerbTend, garden.Ask{Actor: garden.Tender{Session: "stale"}, Force: true}); err != nil {
					t.Fatal(err)
				}
				d.store.Remove("stale")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := newGardenDaemon(t)
			seedID, err := d.bindDelegationSeed("sess-a", "", "Review the pull request.", "Review", "", t.TempDir(), "codex", false)
			if err != nil {
				t.Fatal(err)
			}
			test.setup(t, d, seedID)
			addGardenSession(t, d, "observer")
			watchSeed(t, d, "observer", seedID, false)

			if _, err := d.activateAutomationContinuationSeed(seedID, "sess-a"); err != nil {
				t.Fatal(err)
			}
			if queued := queuedSeedBells(t, d, "observer"); len(queued) != 0 {
				t.Fatalf("automation retend rang before work was ready: %q", queued)
			}
		})
	}
}

type stoppedAutomationContinuationFixture struct {
	d         *Daemon
	backend   *automationResumeBackend
	origin    *store.AutomationRun
	req       automation.WorkRequest
	directory string
}

func setupStoppedAutomationContinuation(t *testing.T) stoppedAutomationContinuationFixture {
	t.Helper()
	d := newEnrolledDaemon(t, "")
	setupDelegationGarden(t, d)
	backend := &automationResumeBackend{fakeSpawnBackend: &fakeSpawnBackend{}}
	d.ptyBackend = backend
	now := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	def, err := d.store.UpsertAutomationDefinition("review", "Review", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	const subject = "github.com/owner/repo#42"
	baselineGitHubReviewAutomation(t, d.store, def.ID, "github.com", now)
	if _, err := d.store.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now); err != nil {
		t.Fatal(err)
	}
	origin, _, err := d.store.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{}`, `{}`, now, store.AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	d.handleRegisterWorkspace(nil, &protocol.RegisterWorkspaceMessage{Cmd: protocol.CmdRegisterWorkspace, ID: origin.WorkspaceID, Title: "review", Directory: directory})
	d.store.Add(&protocol.Session{ID: origin.SessionID, Agent: protocol.SessionAgentCodex, Directory: directory, WorkspaceID: origin.WorkspaceID})
	writeCodexRolloutFixture(t, "codex-rollout-1")
	d.store.SetResumeSessionID(origin.SessionID, "codex-rollout-1")

	req := automation.WorkRequest{
		RunID: "run-2", DefinitionID: def.ID, SubjectKey: subject, ContinuityKey: subject,
		Prompt: "Review locally", Context: json.RawMessage(`{}`), Launch: testAutomationLaunch("codex"),
		IDs: automation.DeliveryIDs{SeedID: origin.SeedID, SessionID: origin.SessionID, WorkspaceID: origin.WorkspaceID, PaneID: origin.PaneID},
	}
	d.store.SetLaunchIntent(origin.SessionID, store.LaunchIntent{
		ApprovalRoute:    launchcontract.ApprovalRouteReviewer,
		Executable:       req.Launch.Executable,
		Model:            req.Launch.Model,
		Effort:           req.Launch.Effort,
		UnattendedLaunch: req.Launch,
	})
	return stoppedAutomationContinuationFixture{
		d: d, backend: backend, origin: origin, req: req, directory: directory,
	}
}

func TestStoppedContinuationWaitsForClosingRuntimeBeforeReopening(t *testing.T) {
	fixture := setupStoppedAutomationContinuation(t)
	killEntered := make(chan struct{})
	releaseKill := make(chan struct{})
	fixture.backend.onKill = func() {
		close(killEntered)
		<-releaseKill
	}

	closing, err := fixture.d.beginSessionClose(
		fixture.origin.SessionID,
		store.SessionClose{By: store.SessionClosedByUser},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.d.finishSessionClose(fixture.origin.SessionID, closing)
	<-killEntered

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- fixture.d.ensureAutomationSession(context.Background(), fixture.req, fixture.directory)
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("continuation completed before the closing runtime exited: %v", err)
	default:
	}
	if got := spawnCount(fixture.backend.fakeSpawnBackend); got != 0 {
		t.Fatalf("spawn calls before teardown completed = %d, want 0", got)
	}

	close(releaseKill)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	fixture.d.waitForSessionTeardown(fixture.origin.SessionID)
	spawn, ok := fixture.backend.LastSpawn()
	if !ok || spawn.ResumeSessionID != "codex-rollout-1" || spawn.UnattendedLaunch != fixture.req.Launch {
		t.Fatalf("resume spawn=%#v ok=%v", spawn, ok)
	}
}

func TestWithdrawnBeforeLaunchReRequestCreatesFirstWorktree(t *testing.T) {
	d, req, worktree, _ := setupContinuationWorktree(t)
	binding, err := d.store.GetActiveAutomationContinuityBinding(req.DefinitionID, req.ContinuityKey)
	if err != nil || binding == nil {
		t.Fatalf("binding=%#v err=%v", binding, err)
	}
	if err := d.store.MarkAutomationRunCancelled(binding.OriginRunID, store.AutomationCancelReasonReviewWithdrawn, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}
	prepared, err := d.prepareAutomationLocation(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Directory != worktree {
		t.Fatalf("re-request worktree=%q want=%q", prepared.Directory, worktree)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("first worktree was not provisioned: %v", err)
	}
}

func TestReRequestCanStartReviewerWhenWithdrawnOriginNeverLaunched(t *testing.T) {
	s := store.New()
	now := time.Date(2026, 7, 19, 18, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	const subject = "github.com/owner/repo#42"
	const payload = `{"provider":"github","host":"github.com","owner":"owner","repository":"repo","number":42,"url":"https://github.com/owner/repo/pull/42","state":"open","head_sha":"0123456789abcdef0123456789abcdef01234567"}`
	const snapshot = `{"prompt":"Review","launch":{},"location":{}}`
	baselineGitHubReviewAutomation(t, s, def.ID, "github.com", now)
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, payload, snapshot, now, store.AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", SeedID: "s-seed01", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	d := newHomeDaemonForTest(t, s)
	d.ptyBackend = &fakeSpawnBackend{}
	if _, err := d.reconcileAutomationReviewRequests(def.ID, "github.com", nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	candidates, err := d.reconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(2*time.Minute))
	if err != nil || len(candidates) != 1 {
		t.Fatalf("re-request candidates=%#v err=%v", candidates, err)
	}
	second, _, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, candidates[0].Cycle, def.Revision, payload, snapshot, now.Add(2*time.Minute), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2"})
	if err != nil {
		t.Fatal(err)
	}
	req := automation.WorkRequest{
		RunID: second.ID, DefinitionID: def.ID, ContinuityKey: subject, Provider: "github", Prompt: "Review", Context: json.RawMessage(payload),
		IDs: automation.DeliveryIDs{SeedID: second.SeedID, SessionID: second.SessionID, WorkspaceID: second.WorkspaceID, PaneID: second.PaneID},
	}
	if err := d.validateAutomationContinuation(req); err != nil {
		t.Fatalf("withdrawn-before-launch re-request rejected: %v", err)
	}
}

func TestEnsureAutomationSeedRefusesOutpostDaemon(t *testing.T) {
	d := newEnrolledDaemon(t, "d-"+strings.Repeat("b", 32))
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	def, err := d.store.UpsertAutomationDefinition("nightly", "Nightly", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := d.store.ClaimScheduledAutomationRun(def.ID, "scheduled:one", "singleton", def.Revision, `{}`, `{"prompt":"Check locally."}`, now, store.AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", SeedID: "s-seed01", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := automation.WorkRequest{RunID: run.ID, DefinitionID: def.ID, ContinuityKey: "singleton", Prompt: "Check locally.", IDs: automation.DeliveryIDs{SeedID: run.SeedID, SessionID: run.SessionID, WorkspaceID: run.WorkspaceID, PaneID: run.PaneID}}
	var fenced *enrollment.FencedError
	if _, _, err := d.ensureAutomationSeed(req); !errors.As(err, &fenced) {
		t.Fatalf("ensureAutomationSeed err=%v, want FencedError", err)
	}
	if _, _, err := d.readSeed(run.SeedID); err == nil {
		t.Fatalf("seed %s planted on an outpost", run.SeedID)
	}
}

func newHomeDaemonForTest(t *testing.T, s *store.Store) *Daemon {
	t.Helper()
	d := &Daemon{store: s, wsHub: newWSHub(), dataRoot: t.TempDir()}
	enrollHomeForTest(t, d)
	return d
}

func enrollHomeForTest(t *testing.T, d *Daemon) {
	t.Helper()
	id, err := enrollment.EnsureDaemonID(d.dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	d.daemonInstanceID = id
	if err := d.ensureEnrollment(); err != nil {
		t.Fatal(err)
	}
}

func TestFailedContinuationDeliveryRestoresClosedSeedAndRingsItsSession(t *testing.T) {
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()
	stamp := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{ID: "session-1", Label: "nightly", State: "idle", StateSince: stamp, StateUpdatedAt: stamp, LastSeen: stamp})
	d.workspaces.register("workspace-1", "nightly", t.TempDir(), "n0", false, false)
	d.workspaces.associateSession("session-1", "workspace-1", "nightly")
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	def, err := d.store.UpsertAutomationDefinition("nightly", "Nightly", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := d.store.ClaimScheduledAutomationRun(def.ID, "scheduled:one", "singleton", def.Revision, `{}`, `{"prompt":"Check locally."}`, now, store.AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", SeedID: "s-seed01", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstReq := automation.WorkRequest{RunID: first.ID, DefinitionID: def.ID, ContinuityKey: "singleton", Prompt: "Check locally.", IDs: automation.DeliveryIDs{SeedID: first.SeedID, SessionID: first.SessionID, WorkspaceID: first.WorkspaceID, PaneID: first.PaneID}}
	if _, _, err := d.ensureAutomationSeed(firstReq); err != nil {
		t.Fatal(err)
	}
	if err := markAutomationRunDeliveredForTest(d.store, first.ID, `{}`, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.applySeedTransition(first.SeedID, garden.VerbHarvest, garden.Ask{Actor: garden.Tender{Session: first.SessionID}, Reason: "first check complete"}); err != nil {
		t.Fatal(err)
	}
	second, _, err := d.store.ClaimScheduledAutomationRun(def.ID, "scheduled:two", "singleton", def.Revision, `{}`, `{"prompt":"Check locally."}`, now.Add(time.Minute), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2"})
	if err != nil {
		t.Fatal(err)
	}
	secondReq := automation.WorkRequest{RunID: second.ID, DefinitionID: def.ID, ContinuityKey: "singleton", Prompt: "Check locally.", Context: json.RawMessage(`{}`), Location: automation.LocationSpec{Type: "directory", Path: filepath.Join(t.TempDir(), "deleted-worktree")}, IDs: automation.DeliveryIDs{SeedID: second.SeedID, SessionID: second.SessionID, WorkspaceID: second.WorkspaceID, PaneID: second.PaneID}}
	if _, err := d.materializeAutomationRun(context.Background(), secondReq); err == nil || !strings.Contains(err.Error(), "prepare location") {
		t.Fatalf("materialize err=%v, want the location step to fail", err)
	}
	seed, _, err := d.readSeed(second.SeedID)
	if err != nil || seed.Status != garden.StatusHarvested || seed.Reason != "first check complete" {
		t.Fatalf("seed=%#v err=%v, want harvested with its original reason restored", seed, err)
	}
	watching, err := d.store.GardenSeedWatching(second.SessionID, second.SeedID)
	if err != nil || !watching {
		t.Fatalf("continuation watch=%v err=%v, want its durable seed watch", watching, err)
	}
	if err := d.recordAutomationRunSeedOutcome(second, "continuation failed after rollback"); err != nil {
		t.Fatal(err)
	}
	assertOneSeedBell(t, d, second.SessionID, second.SeedID, "note.added")
}

func TestAutomationContinuationRollbackDoesNotRingAnUnchangedDependent(t *testing.T) {
	d := newEnrolledDaemon(t, "")
	d.ensureGardenCollections()
	addGardenSession(t, d, "session-1")
	addGardenSession(t, d, "session-2")
	blocker := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("session-1"), Title: "Run the automation"})
	dependent := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("session-2"), Title: "Use its result"})
	mustLink(t, d, blocker.ID, garden.EdgeBlocks, dependent.ID)
	move(t, d, "session-1", blocker.ID, garden.VerbTend, "", "")
	move(t, d, "session-2", dependent.ID, garden.VerbTend, "", "")
	move(t, d, "session-1", blocker.ID, garden.VerbHarvest, "complete", "")
	if _, _, err := d.store.ReadGardenSeedMailboxItems("session-2", dependent.ID, time.Now()); err != nil {
		t.Fatal(err)
	}

	restore, err := d.activateAutomationContinuationSeed(blocker.ID, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if restore == nil {
		t.Fatal("closed continuation did not provide a rollback")
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if queued := queuedSeedBells(t, d, "session-2"); len(queued) != 0 {
		t.Fatalf("rollback rang an unchanged dependent: %q", queued)
	}
}

func TestWithdrawnContinuationRingsItsSessionOnce(t *testing.T) {
	d := newEnrolledDaemon(t, "")
	addGardenSession(t, d, "session-1")
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	def, err := d.store.UpsertAutomationDefinition("nightly", "Nightly", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := d.store.ClaimScheduledAutomationRun(def.ID, "scheduled:one", "singleton", def.Revision, `{}`, `{"prompt":"Check locally."}`, now, store.AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", SeedID: "s-seed01", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstReq := automation.WorkRequest{RunID: first.ID, DefinitionID: def.ID, ContinuityKey: "singleton", Prompt: "Check locally.", IDs: automation.DeliveryIDs{SeedID: first.SeedID, SessionID: first.SessionID, WorkspaceID: first.WorkspaceID, PaneID: first.PaneID}}
	if _, _, err := d.ensureAutomationSeed(firstReq); err != nil {
		t.Fatal(err)
	}
	if err := markAutomationRunDeliveredForTest(d.store, first.ID, `{}`, now); err != nil {
		t.Fatal(err)
	}
	second, _, err := d.store.ClaimScheduledAutomationRun(def.ID, "scheduled:two", "singleton", def.Revision, `{}`, `{"prompt":"Check locally."}`, now.Add(time.Minute), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.store.MarkAutomationRunCancelled(second.ID, store.AutomationCancelReasonReviewWithdrawn, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	outcome := automationFailureComment(second, automationReviewWithdrawnMessage)
	if err := d.recordAutomationRunSeedOutcome(second, outcome); err != nil {
		t.Fatal(err)
	}
	assertOneSeedBell(t, d, second.SessionID, second.SeedID, "note.added")
	if _, _, err := d.store.ReadGardenSeedMailboxItems(second.SessionID, second.SeedID, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := d.recordAutomationRunSeedOutcome(second, outcome); err != nil {
			t.Fatalf("refresh %d: %v", i, err)
		}
	}
	if queued := queuedSeedBells(t, d, second.SessionID); len(queued) != 0 {
		t.Fatalf("a recorded withdrawal rang again: %q", queued)
	}
	notes, err := d.readNotesDomain(second.SeedID)
	if err != nil || len(notes) != 1 || notes[0].Body != outcome {
		t.Fatalf("notes=%#v err=%v, want the single withdrawal note", notes, err)
	}
}

func TestInitialAutomationOutcomeDoesNotRingItsOwnSession(t *testing.T) {
	d := newEnrolledDaemon(t, "")
	addGardenSession(t, d, "session-1")
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	def, err := d.store.UpsertAutomationDefinition("nightly", "Nightly", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := d.store.ClaimScheduledAutomationRun(def.ID, "scheduled:one", "singleton", def.Revision, `{}`, `{"prompt":"Check locally."}`, now, store.AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", SeedID: "s-seed01", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := automation.WorkRequest{RunID: run.ID, DefinitionID: def.ID, ContinuityKey: "singleton", Prompt: "Check locally.", IDs: automation.DeliveryIDs{SeedID: run.SeedID, SessionID: run.SessionID, WorkspaceID: run.WorkspaceID, PaneID: run.PaneID}}
	if _, _, err := d.ensureAutomationSeed(req); err != nil {
		t.Fatal(err)
	}
	if err := markAutomationRunDeliveredForTest(d.store, run.ID, `{}`, now); err != nil {
		t.Fatal(err)
	}
	if err := d.recordAutomationRunSeedOutcome(run, "initial run failed"); err != nil {
		t.Fatal(err)
	}
	if queued := queuedSeedBells(t, d, run.SessionID); len(queued) != 0 {
		t.Fatalf("initial outcome rang its own session: %q", queued)
	}
}

func TestAutomationWorkReadyExcludesOnlyAnInitialRunsOwnSession(t *testing.T) {
	d := newEnrolledDaemon(t, "")
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	def, err := d.store.UpsertAutomationDefinition("nightly", "Nightly", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := d.store.ClaimScheduledAutomationRun(def.ID, "scheduled:one", "singleton", def.Revision, `{}`, `{"prompt":"Check locally."}`, now, store.AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", SeedID: "s-seed01", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstReq := automation.WorkRequest{RunID: first.ID, DefinitionID: def.ID, ContinuityKey: "singleton", Prompt: "Check locally.", IDs: automation.DeliveryIDs{SeedID: first.SeedID, SessionID: first.SessionID, WorkspaceID: first.WorkspaceID, PaneID: first.PaneID}}
	if _, _, err := d.ensureAutomationSeed(firstReq); err != nil {
		t.Fatal(err)
	}
	assertWorkReadyCause := func(run *store.AutomationRun, want string) {
		t.Helper()
		occurrence, err := d.automationWorkReadyOccurrence(run)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := gardenSeedEventModel.Encode(occurrence)
		if err != nil {
			t.Fatal(err)
		}
		decision, err := gardenSeedEventModel.Interpret(encoded.Name, encoded.Subject, encoded.Payload)
		if err != nil || decision.CausedBySessionID() != want {
			t.Fatalf("work-ready cause = %q, want %q, err=%v", decision.CausedBySessionID(), want, err)
		}
	}
	assertWorkReadyCause(first, first.SessionID)
	if err := markAutomationRunDeliveredForTest(d.store, first.ID, `{}`, now); err != nil {
		t.Fatal(err)
	}
	second, _, err := d.store.ClaimScheduledAutomationRun(def.ID, "scheduled:two", "singleton", def.Revision, `{}`, `{"prompt":"Check locally."}`, now.Add(time.Minute), store.AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2"})
	if err != nil {
		t.Fatal(err)
	}
	assertWorkReadyCause(second, "")
}
