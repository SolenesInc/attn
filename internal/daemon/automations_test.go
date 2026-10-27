package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/automation"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

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

func baselineGitHubReviewAutomation(t *testing.T, s *store.Store, definitionID int, host string, at time.Time) {
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
	d := newDaemonForTest(t)
	d.dataRoot = filepath.Join(root, "instance")
	now := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	def, err := d.store.UpsertAutomationDefinition(0, "Review", `{}`, defaultProfileID(t, d.store), now)
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
			SeedID: origin.SeedID, SessionID: origin.SessionID, ProfileID: origin.ProfileID,
		},
	}
	if _, _, err := d.ensureAutomationSeed(firstReq); err != nil {
		t.Fatal(err)
	}
	prepared, err := d.prepareAutomationLocation(context.Background(), firstReq)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.recordGardenDispatch(origin.SessionID, origin.SeedID, prepared.Directory, "codex", false); err != nil {
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

const manualAutomationYAML = `api_version: attn.dev/automations/v1alpha1
name: Manual check
trigger: {type: manual}
prompt: Check locally.
launch: {driver: codex}
location: {type: directory, path: "%s"}
`
