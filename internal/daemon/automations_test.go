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
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

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
	def, err := d.store.UpsertAutomationDefinition("review", "Review", `{}`, defaultProfileID(t, d.store), now)
	if err != nil {
		t.Fatal(err)
	}
	const subject = "github.com/owner/repo#42"
	baselineGitHubReviewAutomation(t, d.store, def.ID, "github.com", now)
	if _, err := d.store.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now); err != nil {
		t.Fatal(err)
	}
	origin, _, err := d.store.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, string(payload), `{}`, now, store.AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", SeedID: "s-at0001", SessionID: "session-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstReq := automation.WorkRequest{
		RunID: origin.ID, DefinitionID: def.ID, SubjectKey: subject, ContinuityKey: subject,
		Provider: "github", Prompt: "Review", Context: payload, Location: location,
		Launch: testAutomationLaunch("codex"), IDs: automation.DeliveryIDs{
			SeedID: origin.SeedID, SessionID: origin.SessionID,
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
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, defaultProfileID(t, s), now)
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
		RunID: "run-1", OccurrenceID: "occ-1", SeedID: "s-seed01", SessionID: "session-1",
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
		IDs: automation.DeliveryIDs{SeedID: second.SeedID, SessionID: second.SessionID},
	}
	if err := d.validateAutomationContinuation(req); err != nil {
		t.Fatalf("withdrawn-before-launch re-request rejected: %v", err)
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

const manualAutomationYAML = `api_version: attn.dev/automations/v1alpha1
id: manual-check
name: Manual check
trigger: {type: manual}
prompt: Check locally.
launch: {driver: codex}
location: {type: directory, path: "%s"}
`

func TestAnOutpostRefusesToCreateOrRunAutomations(t *testing.T) {
	const home = "d-cccccccccccccccccccccccccccccccc"
	d := newEnrolledDaemon(t, home)
	var fenced *enrollment.FencedError
	if _, err := d.automationApplyWithGuards(context.Background(), "id: nightly\nname: Nightly\n", defaultProfileID(t, d.store), nil, nil); !errors.As(err, &fenced) || !strings.Contains(err.Error(), home) {
		t.Fatalf("apply on an outpost = %v, want a refusal naming the home %s", err, home)
	}
	if defs, err := d.store.ListAutomationDefinitions(); err != nil || len(defs) != 0 {
		t.Fatalf("definitions after a refused apply = %v, %v; want none", defs, err)
	}
	def, err := d.store.UpsertAutomationDefinition("legacy", "Legacy", `{}`, defaultProfileID(t, d.store), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.automationRun(context.Background(), def.ID, "request-1", "{}"); !errors.As(err, &fenced) {
		t.Fatalf("run on an outpost = %v, want the home fence", err)
	}
	if _, err := d.automationSetEnabled(context.Background(), def.ID, true); !errors.As(err, &fenced) {
		t.Fatalf("enable on an outpost = %v, want the home fence", err)
	}
	if _, err := d.automationSetEnabled(context.Background(), def.ID, false); err != nil {
		t.Fatalf("disabling a leftover definition on an outpost: %v", err)
	}
	if err := d.automationDelete(context.Background(), def.ID); err != nil {
		t.Fatalf("deleting a leftover definition on an outpost: %v", err)
	}
}
