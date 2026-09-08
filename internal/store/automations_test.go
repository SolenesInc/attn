package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func baselineGitHubReviewAutomation(t *testing.T, s *Store, definitionID, host string, at time.Time) {
	t.Helper()
	if candidates, err := s.ReconcileAutomationReviewRequests(definitionID, host, nil, at); err != nil || len(candidates) != 0 {
		t.Fatalf("establish review automation baseline: candidates=%#v err=%v", candidates, err)
	}
}

func TestGitHubReviewActivationBaselinesExistingDemandPerHost(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	const subjectA = "github.com/owner/repo#1"
	const subjectB = "ghe.example.com/owner/repo#2"
	for _, observation := range []struct {
		host    string
		subject string
	}{
		{host: "github.com", subject: subjectA},
		{host: "ghe.example.com", subject: subjectB},
	} {
		candidates, err := s.ReconcileAutomationReviewRequests(def.ID, observation.host, []string{observation.subject}, now)
		if err != nil || len(candidates) != 0 {
			t.Fatalf("first %s observation candidates=%#v err=%v", observation.host, candidates, err)
		}
		candidates, err = s.ReconcileAutomationReviewRequests(def.ID, observation.host, []string{observation.subject}, now.Add(time.Minute))
		if err != nil || len(candidates) != 0 {
			t.Fatalf("unchanged %s observation candidates=%#v err=%v", observation.host, candidates, err)
		}
	}
	if needs, err := s.AutomationReviewRequestNeedsClaim(def.ID, subjectA, 1); err != nil || needs {
		t.Fatalf("baselined request needs claim=%v err=%v", needs, err)
	}
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", nil, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subjectA}, now.Add(3*time.Minute))
	if err != nil || len(candidates) != 1 || candidates[0].Cycle != 2 {
		t.Fatalf("later request candidates=%#v err=%v", candidates, err)
	}
}

func TestGitHubReviewActivationBaselineSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	const subject = "github.com/owner/repo#42"
	if candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now); err != nil || len(candidates) != 0 {
		t.Fatalf("activation candidates=%#v err=%v", candidates, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(time.Minute)); err != nil || len(candidates) != 0 {
		t.Fatalf("post-restart candidates=%#v err=%v", candidates, err)
	}
}

func TestGitHubReviewLiveReapplyDoesNotRearm(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	const spec = `{"id":"review"}`
	def, err := s.UpsertAutomationDefinition("review", "Review", spec, now)
	if err != nil {
		t.Fatal(err)
	}
	baselineGitHubReviewAutomation(t, s, def.ID, "github.com", now)
	const subject = "github.com/owner/repo#42"
	if candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(time.Minute)); err != nil || len(candidates) != 1 {
		t.Fatalf("new request candidates=%#v err=%v", candidates, err)
	}
	if _, err := s.UpsertAutomationDefinition(def.ID, def.Name, spec, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(3*time.Minute)); err != nil || len(candidates) != 1 || candidates[0].Cycle != 1 {
		t.Fatalf("live reapply swallowed request: candidates=%#v err=%v", candidates, err)
	}
}

func TestAutomationClaimIsIdempotentAndSnapshotsRevision(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("cleanup", "Cleanup", `{"id":"cleanup"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "auto-run-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	first, created, err := s.ClaimManualAutomationRun("cleanup", "request-1", "github.com/owner/repo#42", `{"scope":"tmp"}`, def.Revision, `{"prompt":"first"}`, now, ids)
	if err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	other := AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", SeedID: "auto-run-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"}
	second, created, err := s.ClaimManualAutomationRun("cleanup", "request-1", "", `{"scope":"changed"}`, def.Revision, `{"prompt":"changed"}`, now.Add(time.Minute), other)
	if err != nil || created {
		t.Fatalf("duplicate claim created=%v err=%v", created, err)
	}
	if second.ID != first.ID || second.SeedID != first.SeedID || second.SnapshotJSON != `{"prompt":"first"}` {
		t.Fatalf("duplicate returned different run: %#v", second)
	}
	occurrence, err := s.GetAutomationOccurrence(first.OccurrenceID)
	if err != nil || occurrence == nil || occurrence.SubjectKey != "github.com/owner/repo#42" || occurrence.PayloadJSON != `{"scope":"tmp"}` {
		t.Fatalf("occurrence = %#v err=%v", occurrence, err)
	}
}

func TestAutomationProvenanceRecordsAreNewestFirstAndJoined(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Requested PR review - GPT Sol medium", `{"trigger":{"type":"github_review_requested"}}`, now)
	if err != nil {
		t.Fatal(err)
	}
	firstIDs := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	if _, created, err := s.ClaimManualAutomationRun(def.ID, "request-1", "ghe.spotify.net/owner/repo#1", `{"cycle":1}`, def.Revision, `{}`, now, firstIDs); err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	secondIDs := AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", SeedID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	if _, created, err := s.ClaimManualAutomationRun(def.ID, "request-2", "ghe.spotify.net/owner/repo#1", `{"cycle":2}`, def.Revision, `{}`, now.Add(time.Minute), secondIDs); err != nil || !created {
		t.Fatalf("second claim created=%v err=%v", created, err)
	}

	records, err := s.ListLatestAutomationProvenanceRecords()
	if err != nil || len(records) != 1 {
		t.Fatalf("records = %#v err=%v", records, err)
	}
	if records[0].RunID != "run-2" || records[0].DefinitionName != def.Name || records[0].SessionID != "session-1" || records[0].PayloadJSON != `{"cycle":2}` {
		t.Fatalf("latest record = %#v", records[0])
	}
	loaded, err := s.GetAutomationProvenanceRecord("run-1")
	if err != nil || loaded == nil || loaded.RunID != "run-1" || loaded.DefinitionSpecJSON != def.SpecJSON {
		t.Fatalf("loaded record = %#v err=%v", loaded, err)
	}
	latestForSession, err := s.GetLatestAutomationProvenanceRecordForSession("session-1")
	if err != nil || latestForSession == nil || latestForSession.RunID != "run-2" {
		t.Fatalf("latest session record = %#v err=%v", latestForSession, err)
	}
	latestForSeed, err := s.GetLatestAutomationProvenanceRecordForSeed("ticket-1")
	if err != nil || latestForSeed == nil || latestForSeed.RunID != "run-2" {
		t.Fatalf("latest seed record = %#v err=%v", latestForSeed, err)
	}
}

func TestScheduledAutomationClaimIsIdempotent(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	key := "scheduled:2026-07-20T03:00:00Z"
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "auto-run-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	first, created, err := s.ClaimScheduledAutomationRun(def.ID, key, "", def.Revision, `{"provider":"schedule"}`, `{"prompt":"sweep"}`, now, ids)
	if err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	other := AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", SeedID: "auto-run-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"}
	second, created, err := s.ClaimScheduledAutomationRun(def.ID, key, "", def.Revision, `{"provider":"schedule","changed":true}`, `{"prompt":"changed"}`, now.Add(time.Minute), other)
	if err != nil || created {
		t.Fatalf("duplicate claim created=%v err=%v", created, err)
	}
	if second.ID != first.ID || second.SeedID != first.SeedID || second.SnapshotJSON != `{"prompt":"sweep"}` {
		t.Fatalf("duplicate returned different run: %#v", second)
	}
	var occurrenceCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM automation_occurrences WHERE definition_id=? AND provider='schedule'`, def.ID).Scan(&occurrenceCount); err != nil {
		t.Fatal(err)
	}
	if occurrenceCount != 1 {
		t.Fatalf("occurrence count=%d, want 1", occurrenceCount)
	}
}

func TestScheduledAutomationClaimRejectsStaleRevision(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly","edited":true}`, now); err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	if _, _, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-20T03:00:00Z", "", def.Revision, `{}`, `{}`, now, ids); err == nil {
		t.Fatal("expected stale revision claim to be rejected")
	}
	if occurrence, err := s.GetAutomationOccurrence("occ-1"); err != nil || occurrence != nil {
		t.Fatalf("rejected claim left an occurrence: %#v err=%v", occurrence, err)
	}
	if run, err := s.GetAutomationRun("run-1"); err != nil || run != nil {
		t.Fatalf("rejected claim persisted a run: %#v err=%v", run, err)
	}
}

func TestScheduledAutomationClaimRejectsDisabledDefinition(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetAutomationEnabled(def.ID, false, now); err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	if _, _, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-20T03:00:00Z", "", def.Revision, `{}`, `{}`, now, ids); err == nil {
		t.Fatal("expected a disabled definition's claim to be rejected")
	}
	if occurrence, err := s.GetAutomationOccurrence("occ-1"); err != nil || occurrence != nil {
		t.Fatalf("rejected claim left an occurrence: %#v err=%v", occurrence, err)
	}
	if run, err := s.GetAutomationRun("run-1"); err != nil || run != nil {
		t.Fatalf("rejected claim persisted a run: %#v err=%v", run, err)
	}
}

func TestScheduledAutomationDifferentInstantsClaimDifferentRuns(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	first, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-20T03:00:00Z", "", def.Revision, `{}`, `{}`, now, AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	second, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-21T03:00:00Z", "", def.Revision, `{}`, `{}`, now.Add(24*time.Hour), AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", SeedID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"})
	if err != nil || !created {
		t.Fatalf("second claim created=%v err=%v", created, err)
	}
	if second.ID == first.ID {
		t.Fatalf("different instants claimed the same run: %#v", second)
	}
}

func TestScheduledAutomationSingletonContinuityReusesBinding(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	firstIDs := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	first, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-20T03:00:00Z", "singleton", def.Revision, `{}`, `{}`, now, firstIDs)
	if err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	secondIDs := AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", SeedID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"}
	if _, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-21T03:00:00Z", "singleton", def.Revision, `{}`, `{}`, now.Add(24*time.Hour), secondIDs); err == nil || created {
		t.Fatalf("second claim created=%v err=%v, want refused while the first run is still pending", created, err)
	}
	if err := s.MarkAutomationRunDelivered(first.ID, `{}`, now); err != nil {
		t.Fatal(err)
	}
	second, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-21T03:00:00Z", "singleton", def.Revision, `{}`, `{}`, now.Add(24*time.Hour), secondIDs)
	if err != nil || !created {
		t.Fatalf("second claim created=%v err=%v", created, err)
	}
	if second.ID == first.ID {
		t.Fatal("second occurrence should claim a distinct run")
	}
	if second.SeedID != first.SeedID || second.SessionID != first.SessionID || second.WorkspaceID != first.WorkspaceID || second.PaneID != first.PaneID {
		t.Fatalf("singleton continuity did not reuse binding IDs: first=%#v second=%#v", first, second)
	}
}

func TestAutomationContinuityBindingLifecycleReleaseThenReclaim(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	firstIDs := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	if _, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:1", "singleton", def.Revision, `{}`, `{}`, now, firstIDs); err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	binding, err := s.GetActiveAutomationContinuityBinding(def.ID, "singleton")
	if err != nil || binding == nil || binding.SeedID != firstIDs.SeedID || binding.Status != AutomationBindingStatusActive {
		t.Fatalf("active binding = %#v err=%v", binding, err)
	}
	firstBindingID := binding.ID

	if err := s.MarkAutomationRunDelivered(firstIDs.RunID, `{}`, now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}

	if err := s.ReleaseAutomationContinuityBinding(def.ID, "singleton", AutomationBindingReleasedTicketSwept, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if released, err := s.GetActiveAutomationContinuityBinding(def.ID, "singleton"); err != nil || released != nil {
		t.Fatalf("expected no active binding after release, got %#v err=%v", released, err)
	}
	if err := s.ReleaseAutomationContinuityBinding(def.ID, "singleton", AutomationBindingReleasedTicketSwept, now.Add(90*time.Second)); err != nil {
		t.Fatalf("re-release of an already-released binding errored: %v", err)
	}

	secondIDs := AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", SeedID: "ticket-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"}
	second, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2", "singleton", def.Revision, `{}`, `{}`, now.Add(2*time.Minute), secondIDs)
	if err != nil || !created {
		t.Fatalf("second claim created=%v err=%v", created, err)
	}
	if second.SeedID != secondIDs.SeedID {
		t.Fatalf("second claim reused a released binding instead of a fresh one: %#v", second)
	}
	fresh, err := s.GetActiveAutomationContinuityBinding(def.ID, "singleton")
	if err != nil || fresh == nil || fresh.ID == firstBindingID || fresh.SeedID != secondIDs.SeedID {
		t.Fatalf("fresh active binding = %#v err=%v (first id %s)", fresh, err, firstBindingID)
	}

	var releasedStatus, releasedReason string
	if err := s.db.QueryRow(`SELECT status,released_reason FROM automation_continuity_bindings WHERE id=?`, firstBindingID).Scan(&releasedStatus, &releasedReason); err != nil {
		t.Fatal(err)
	}
	if releasedStatus != AutomationBindingStatusReleased || releasedReason != AutomationBindingReleasedTicketSwept {
		t.Fatalf("original binding row = status=%s reason=%s, want released/%s", releasedStatus, releasedReason, AutomationBindingReleasedTicketSwept)
	}
}

func TestAutomationContinuityBindingUniqueActiveIndexRejectsSecondActiveRow(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	nowRaw := formatTicketTime(now)
	if _, err := s.db.Exec(`INSERT INTO automation_continuity_bindings(id,definition_id,continuity_key,ticket_id,session_id,workspace_id,pane_id,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		"binding-1", def.ID, "singleton", "ticket-1", "session-1", "workspace-1", "pane-1", AutomationBindingStatusActive, nowRaw, nowRaw); err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`INSERT INTO automation_continuity_bindings(id,definition_id,continuity_key,ticket_id,session_id,workspace_id,pane_id,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		"binding-2", def.ID, "singleton", "ticket-2", "session-2", "workspace-2", "pane-2", AutomationBindingStatusActive, nowRaw, nowRaw)
	if err == nil {
		t.Fatal("expected a second active binding row for the same (definition, continuity_key) to be rejected")
	}
}

func TestMarkAutomationRunCancelledSetsStateAndReason(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	run, created, err := s.ClaimManualAutomationRun(def.ID, "request-1", "", `{}`, def.Revision, `{}`, now, ids)
	if err != nil || !created {
		t.Fatalf("claim created=%v err=%v", created, err)
	}
	if err := s.MarkAutomationRunCancelled(run.ID, AutomationCancelReasonDefinitionDisabled, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	cancelled, err := s.GetAutomationRun(run.ID)
	if err != nil || cancelled == nil || cancelled.State != AutomationRunStateCancelled || cancelled.CancelReason != AutomationCancelReasonDefinitionDisabled {
		t.Fatalf("cancelled run = %#v err=%v", cancelled, err)
	}
	if cancelled.LastError != "" {
		t.Fatalf("cancellation set last_error: %q", cancelled.LastError)
	}
}

func TestListPrunableAndTerminalAutomationRunsIncludeCancelledRuns(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("cleanup", "Cleanup", `{"id":"cleanup"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	run, created, err := s.ClaimManualAutomationRun(def.ID, "request-1", "", `{}`, def.Revision, `{}`, now, ids)
	if err != nil || !created {
		t.Fatalf("claim created=%v err=%v", created, err)
	}
	if err := s.MarkAutomationRunCancelled(run.ID, AutomationCancelReasonDefinitionDisabled, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	terminal, err := s.ListTerminalAutomationRuns(def.ID)
	if err != nil || len(terminal) != 1 || terminal[0].ID != run.ID {
		t.Fatalf("terminal runs = %#v err=%v", terminal, err)
	}
	far := now.Add(365 * 24 * time.Hour)
	prunable, err := s.ListPrunableAutomationRuns(def.ID, 0, far)
	if err != nil || len(prunable) != 1 || prunable[0].ID != run.ID {
		t.Fatalf("prunable runs = %#v err=%v", prunable, err)
	}
}

func TestListWithdrawnGitHubReviewUndeliveredRunsIncludesCancelledReviewWithdrawnNotFailed(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	const subjectA = "github.com/owner/repo#1"
	const subjectB = "github.com/owner/repo#2"
	baselineGitHubReviewAutomation(t, s, def.ID, "github.com", now)
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subjectA, subjectB}, now); err != nil {
		t.Fatal(err)
	}
	runA, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subjectA, 1, def.Revision, `{}`, `{}`, now, AutomationRunReservation{
		RunID: "run-a", OccurrenceID: "occ-a", SeedID: "ticket-a", SessionID: "session-a", WorkspaceID: "workspace-a", PaneID: "pane-a",
	})
	if err != nil || !created {
		t.Fatalf("claim A created=%v err=%v", created, err)
	}
	runB, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subjectB, 1, def.Revision, `{}`, `{}`, now, AutomationRunReservation{
		RunID: "run-b", OccurrenceID: "occ-b", SeedID: "ticket-b", SessionID: "session-b", WorkspaceID: "workspace-b", PaneID: "pane-b",
	})
	if err != nil || !created {
		t.Fatalf("claim B created=%v err=%v", created, err)
	}
	if err := s.MarkAutomationRunFailed(runB.ID, "spawn unavailable", now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunCancelled(runA.ID, AutomationCancelReasonReviewWithdrawn, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	withdrawn, err := s.ListWithdrawnGitHubReviewUndeliveredRuns(def.ID, "github.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(withdrawn) != 1 || withdrawn[0].ID != runA.ID {
		t.Fatalf("withdrawn runs = %#v, want only cancelled run %s", withdrawn, runA.ID)
	}
}

func TestAutomationScheduleCursorGetSetRoundtrip(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetAutomationScheduleCursor(def.ID); err != nil || ok {
		t.Fatalf("missing cursor ok=%v err=%v", ok, err)
	}
	instant := time.Date(2026, 7, 20, 3, 0, 0, 123000000, time.UTC)
	if err := s.SetAutomationScheduleCursor(def.ID, instant); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetAutomationScheduleCursor(def.ID)
	if err != nil || !ok {
		t.Fatalf("roundtrip cursor ok=%v err=%v", ok, err)
	}
	if !got.Equal(instant) {
		t.Fatalf("cursor=%v, want %v", got, instant)
	}
	later := instant.Add(time.Hour)
	if err := s.SetAutomationScheduleCursor(def.ID, later); err != nil {
		t.Fatal(err)
	}
	got, ok, err = s.GetAutomationScheduleCursor(def.ID)
	if err != nil || !ok || !got.Equal(later) {
		t.Fatalf("advanced cursor=%v ok=%v err=%v", got, ok, err)
	}
}

func TestListAutomationRunsWithOccurrenceKeysOrdersNewestFirstWithLimit(t *testing.T) {
	s := New()
	base := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("cleanup", "Cleanup", `{"id":"cleanup"}`, base)
	if err != nil {
		t.Fatal(err)
	}
	seed := func(requestID string, at time.Time) *AutomationRun {
		ids := AutomationRunReservation{
			RunID:        "run-" + requestID,
			OccurrenceID: "occ-" + requestID,
			SeedID:       "ticket-" + requestID,
			SessionID:    "session-" + requestID,
			WorkspaceID:  "workspace-" + requestID,
			PaneID:       "pane-" + requestID,
		}
		run, created, err := s.ClaimManualAutomationRun(def.ID, requestID, "", `{}`, def.Revision, `{}`, at, ids)
		if err != nil || !created {
			t.Fatalf("claim %s created=%v err=%v", requestID, created, err)
		}
		return run
	}
	// Distinct clock-injected timestamps, not wall-clock spacing, so ordering
	// is deterministic regardless of test execution speed.
	seed("req-1", base)
	second := seed("req-2", base.Add(time.Minute))
	third := seed("req-3", base.Add(2*time.Minute))

	runs, err := s.ListAutomationRunsWithOccurrenceKeys(def.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("len(runs)=%d, want 2: %#v", len(runs), runs)
	}
	if runs[0].ID != third.ID || runs[0].OccurrenceKey != "manual:req-3" {
		t.Fatalf("newest run = %#v, want run %s with occurrence_key manual:req-3", runs[0], third.ID)
	}
	if runs[1].ID != second.ID || runs[1].OccurrenceKey != "manual:req-2" {
		t.Fatalf("second-newest run = %#v, want run %s with occurrence_key manual:req-2", runs[1], second.ID)
	}
	if !runs[0].CreatedAt.After(runs[1].CreatedAt) {
		t.Fatalf("runs not newest-first by created_at: %v then %v", runs[0].CreatedAt, runs[1].CreatedAt)
	}
}

func TestLatestAutomationRunPerDefinitionPicksNewestPerDefinitionAndOmitsZeroRunDefinitions(t *testing.T) {
	s := New()
	base := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	defA, err := s.UpsertAutomationDefinition("def-a", "A", `{"id":"def-a"}`, base)
	if err != nil {
		t.Fatal(err)
	}
	defB, err := s.UpsertAutomationDefinition("def-b", "B", `{"id":"def-b"}`, base)
	if err != nil {
		t.Fatal(err)
	}
	defEmpty, err := s.UpsertAutomationDefinition("def-empty", "Empty", `{"id":"def-empty"}`, base)
	if err != nil {
		t.Fatal(err)
	}
	_ = defEmpty
	seed := func(defID, requestID string, at time.Time) *AutomationRun {
		ids := AutomationRunReservation{
			RunID:        "run-" + defID + "-" + requestID,
			OccurrenceID: "occ-" + defID + "-" + requestID,
			SeedID:       "ticket-" + defID + "-" + requestID,
			SessionID:    "session-" + defID + "-" + requestID,
			WorkspaceID:  "workspace-" + defID + "-" + requestID,
			PaneID:       "pane-" + defID + "-" + requestID,
		}
		run, created, err := s.ClaimManualAutomationRun(defID, requestID, "", `{}`, 1, `{}`, at, ids)
		if err != nil || !created {
			t.Fatalf("claim %s/%s created=%v err=%v", defID, requestID, created, err)
		}
		return run
	}
	seed(defA.ID, "a-1", base)
	newestA := seed(defA.ID, "a-2", base.Add(time.Minute))
	newestB := seed(defB.ID, "b-1", base.Add(30*time.Second))

	latest, err := s.LatestAutomationRunPerDefinition()
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 2 {
		t.Fatalf("len(latest)=%d, want 2: %#v", len(latest), latest)
	}
	got, ok := latest[defA.ID]
	if !ok || got.ID != newestA.ID || got.OccurrenceKey != "manual:a-2" {
		t.Fatalf("def-a latest = %#v (ok=%v), want run %s with occurrence_key manual:a-2", got, ok, newestA.ID)
	}
	got, ok = latest[defB.ID]
	if !ok || got.ID != newestB.ID || got.OccurrenceKey != "manual:b-1" {
		t.Fatalf("def-b latest = %#v (ok=%v), want run %s with occurrence_key manual:b-1", got, ok, newestB.ID)
	}
	if _, ok := latest[defEmpty.ID]; ok {
		t.Fatalf("def-empty should have no entry, got %#v", latest[defEmpty.ID])
	}
}

func TestListPendingAutomationRunsIncludesScheduledProvider(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	run, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-20T03:00:00Z", "", def.Revision, `{}`, `{}`, now, AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil || !created {
		t.Fatalf("claim created=%v err=%v", created, err)
	}
	pending, err := s.ListPendingAutomationRuns()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range pending {
		if r.ID == run.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("pending runs=%#v, missing claimed schedule run %s", pending, run.ID)
	}
}

func TestGitHubReviewEdgeRetriesThenReusesContinuityBinding(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{"id":"review"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	subject := "github.com/owner/repo#42"
	baselineGitHubReviewAutomation(t, s, def.ID, "github.com", now)
	candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now)
	if err != nil || len(candidates) != 1 || candidates[0].Cycle != 1 {
		t.Fatalf("first reconcile = %#v err=%v", candidates, err)
	}
	candidates, err = s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(time.Minute))
	if err != nil || len(candidates) != 1 || candidates[0].Cycle != 1 {
		t.Fatalf("retry reconcile = %#v err=%v", candidates, err)
	}
	firstIDs := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "auto-run-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	first, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{"head_sha":"one"}`, `{"prompt":"review"}`, now, firstIDs)
	if err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	if first.SeedID != firstIDs.SeedID || first.SessionID != firstIDs.SessionID {
		t.Fatalf("first run links = %#v", first)
	}
	if err := s.MarkAutomationRunDelivered(first.ID, `{}`, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	candidates, err = s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(2*time.Minute))
	if err != nil || len(candidates) != 0 {
		t.Fatalf("duplicate poll candidates = %#v err=%v", candidates, err)
	}
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", nil, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	stale, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(2*time.Minute))
	if err != nil || len(stale) != 0 {
		t.Fatalf("stale observation candidates = %#v err=%v", stale, err)
	}
	candidates, err = s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(4*time.Minute))
	if err != nil || len(candidates) != 1 || candidates[0].Cycle != 2 {
		t.Fatalf("re-request candidates = %#v err=%v", candidates, err)
	}
	secondIDs := AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", SeedID: "auto-run-2", SessionID: "session-2", WorkspaceID: "workspace-2", PaneID: "pane-2"}
	second, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 2, def.Revision, `{"head_sha":"two"}`, `{"prompt":"review"}`, now.Add(4*time.Minute), secondIDs)
	if err != nil || !created {
		t.Fatalf("second claim created=%v err=%v", created, err)
	}
	if second.ID != secondIDs.RunID || second.SeedID != first.SeedID || second.SessionID != first.SessionID || second.WorkspaceID != first.WorkspaceID || second.PaneID != first.PaneID {
		t.Fatalf("continuation did not reuse binding: first=%#v second=%#v", first, second)
	}
	occurrence, err := s.GetAutomationOccurrence(second.OccurrenceID)
	if err != nil || occurrence == nil || occurrence.OccurrenceKey != "review_requested:github.com/owner/repo#42:2:two" {
		t.Fatalf("second occurrence = %#v err=%v", occurrence, err)
	}
}

func TestGitHubReviewChangedHeadsAreDurableAndPendingRunCannotBeOvertaken(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{"id":"review"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	const (
		subject   = "github.com/owner/repo#42"
		headOne   = "0123456789abcdef0123456789abcdef01234567"
		headTwo   = "89abcdef0123456789abcdef0123456789abcdef"
		headThree = "fedcba9876543210fedcba9876543210fedcba98"
	)
	baselineGitHubReviewAutomation(t, s, def.ID, "github.com", now)
	reconcile := func(at time.Time, head string) []AutomationReviewRequestCandidate {
		t.Helper()
		candidates, err := s.ReconcileAutomationReviewRequestHeads(def.ID, "github.com", []AutomationReviewRequestObservation{{SubjectKey: subject, HeadSHA: head}}, at)
		if err != nil {
			t.Fatal(err)
		}
		return candidates
	}
	claim := func(at time.Time, head string, ids AutomationRunReservation) (*AutomationRun, bool) {
		t.Helper()
		run, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{"head_sha":"`+head+`"}`, `{}`, at, ids)
		if err != nil {
			t.Fatal(err)
		}
		return run, created
	}

	if candidates := reconcile(now, headOne); len(candidates) != 1 || candidates[0].Cycle != 1 || candidates[0].HeadSHA != headOne {
		t.Fatalf("first-head candidates=%#v", candidates)
	}
	first, created := claim(now, headOne, AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if !created {
		t.Fatal("first head was not claimed")
	}
	if err := s.MarkAutomationRunDelivered(first.ID, `{}`, now); err != nil {
		t.Fatal(err)
	}
	if candidates := reconcile(now.Add(time.Minute), headOne); len(candidates) != 0 {
		t.Fatalf("same-head candidates=%#v", candidates)
	}

	if candidates := reconcile(now.Add(2*time.Minute), headTwo); len(candidates) != 1 || candidates[0].Cycle != 1 || candidates[0].HeadSHA != headTwo {
		t.Fatalf("changed-head candidates=%#v", candidates)
	}
	second, created := claim(now.Add(2*time.Minute), headTwo, AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2"})
	if !created || second.SeedID != first.SeedID || second.SessionID != first.SessionID {
		t.Fatalf("changed-head run=%#v created=%v first=%#v", second, created, first)
	}
	occurrence, err := s.GetAutomationOccurrence(second.OccurrenceID)
	if err != nil || occurrence == nil || occurrence.OccurrenceKey != "review_requested:"+subject+":1:"+headTwo {
		t.Fatalf("changed-head occurrence=%#v err=%v", occurrence, err)
	}

	if candidates := reconcile(now.Add(3*time.Minute), headThree); len(candidates) != 1 || candidates[0].HeadSHA != headThree {
		t.Fatalf("pending predecessor candidates=%#v", candidates)
	}
	retried, created := claim(now.Add(3*time.Minute), headThree, AutomationRunReservation{RunID: "run-3", OccurrenceID: "occ-3"})
	if created || retried.ID != second.ID {
		t.Fatalf("newer head overtook pending run: retried=%#v created=%v pending=%#v", retried, created, second)
	}
	if err := s.MarkAutomationRunDelivered(second.ID, `{}`, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if candidates := reconcile(now.Add(4*time.Minute), headThree); len(candidates) != 1 || candidates[0].HeadSHA != headThree {
		t.Fatalf("latest catch-up candidates=%#v", candidates)
	}
	third, created := claim(now.Add(4*time.Minute), headThree, AutomationRunReservation{RunID: "run-3", OccurrenceID: "occ-3"})
	if !created || third.ID == second.ID || third.SeedID != first.SeedID || third.SessionID != first.SessionID {
		t.Fatalf("latest catch-up run=%#v created=%v", third, created)
	}
}

func TestGitHubReviewLegacyCycleOccurrenceUsesPayloadHeadWithoutReplay(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	const (
		subject = "github.com/owner/repo#42"
		headOne = "0123456789abcdef0123456789abcdef01234567"
		headTwo = "89abcdef0123456789abcdef0123456789abcdef"
	)
	baselineGitHubReviewAutomation(t, s, def.ID, "github.com", now)
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now); err != nil {
		t.Fatal(err)
	}
	run, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{}`, `{}`, now, AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"})
	if err != nil || !created {
		t.Fatalf("legacy claim created=%v err=%v", created, err)
	}
	if _, err := s.db.Exec(`UPDATE automation_occurrences SET payload_json=? WHERE id=?`, `{"head_sha":"`+headOne+`"}`, run.OccurrenceID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(run.ID, `{}`, now); err != nil {
		t.Fatal(err)
	}
	observed := func(head string, at time.Time) []AutomationReviewRequestCandidate {
		t.Helper()
		candidates, err := s.ReconcileAutomationReviewRequestHeads(def.ID, "github.com", []AutomationReviewRequestObservation{{SubjectKey: subject, HeadSHA: head}}, at)
		if err != nil {
			t.Fatal(err)
		}
		return candidates
	}
	if candidates := observed(headOne, now.Add(time.Minute)); len(candidates) != 0 {
		t.Fatalf("legacy same-head replayed: %#v", candidates)
	}
	if candidates := observed(headTwo, now.Add(2*time.Minute)); len(candidates) != 1 || candidates[0].Cycle != 1 {
		t.Fatalf("legacy changed head candidates=%#v", candidates)
	}
}

func TestGitHubReviewChangedHeadsKeepDefinitionsIndependent(t *testing.T) {
	s := New()
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	const (
		subject = "github.com/owner/repo#42"
		headOne = "0123456789abcdef0123456789abcdef01234567"
		headTwo = "89abcdef0123456789abcdef0123456789abcdef"
	)
	var seeds, sessions []string
	for index, definitionID := range []string{"review-sol", "review-secondary"} {
		def, err := s.UpsertAutomationDefinition(definitionID, definitionID, `{}`, now)
		if err != nil {
			t.Fatal(err)
		}
		baselineGitHubReviewAutomation(t, s, def.ID, "github.com", now)
		observe := func(head string, at time.Time) []AutomationReviewRequestCandidate {
			t.Helper()
			candidates, err := s.ReconcileAutomationReviewRequestHeads(def.ID, "github.com", []AutomationReviewRequestObservation{{SubjectKey: subject, HeadSHA: head}}, at)
			if err != nil {
				t.Fatal(err)
			}
			return candidates
		}
		if candidates := observe(headOne, now); len(candidates) != 1 {
			t.Fatalf("%s first candidates=%#v", definitionID, candidates)
		}
		first, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{"head_sha":"`+headOne+`"}`, `{}`, now, AutomationRunReservation{
			RunID: fmt.Sprintf("run-%d-1", index), OccurrenceID: fmt.Sprintf("occ-%d-1", index), SeedID: fmt.Sprintf("seed-%d", index), SessionID: fmt.Sprintf("session-%d", index), WorkspaceID: fmt.Sprintf("workspace-%d", index), PaneID: fmt.Sprintf("pane-%d", index),
		})
		if err != nil || !created {
			t.Fatalf("%s first claim created=%v err=%v", definitionID, created, err)
		}
		seeds = append(seeds, first.SeedID)
		sessions = append(sessions, first.SessionID)
		if err := s.MarkAutomationRunDelivered(first.ID, `{}`, now); err != nil {
			t.Fatal(err)
		}
		if candidates := observe(headTwo, now.Add(time.Minute)); len(candidates) != 1 || candidates[0].Cycle != 1 {
			t.Fatalf("%s changed-head candidates=%#v", definitionID, candidates)
		}
		second, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{"head_sha":"`+headTwo+`"}`, `{}`, now.Add(time.Minute), AutomationRunReservation{RunID: fmt.Sprintf("run-%d-2", index), OccurrenceID: fmt.Sprintf("occ-%d-2", index)})
		if err != nil || !created || second.SeedID != first.SeedID || second.SessionID != first.SessionID {
			t.Fatalf("%s continuation=%#v created=%v err=%v", definitionID, second, created, err)
		}
	}
	if len(seeds) != 2 || seeds[0] == seeds[1] || sessions[0] == sessions[1] {
		t.Fatalf("definitions shared continuity: seeds=%#v sessions=%#v", seeds, sessions)
	}
}

func TestGitHubReviewAcceptedPendingRunRemainsRetryableWhileDemandIsActive(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{"id":"review"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	subject := "github.com/owner/repo#42"
	baselineGitHubReviewAutomation(t, s, def.ID, "github.com", now)
	candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("first reconcile = %#v err=%v", candidates, err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	run, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{}`, `{}`, now, ids)
	if err != nil || !created {
		t.Fatalf("claim created=%v err=%v", created, err)
	}

	candidates, err = s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(time.Minute))
	if err != nil || len(candidates) != 1 || candidates[0].Cycle != 1 {
		t.Fatalf("pending retry candidates = %#v err=%v", candidates, err)
	}
	needsClaim, err := s.AutomationReviewRequestNeedsClaim(def.ID, subject, 1)
	if err != nil || !needsClaim {
		t.Fatalf("pending run needs claim=%v err=%v", needsClaim, err)
	}
	retried, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{}`, `{}`, now.Add(time.Minute), AutomationRunReservation{RunID: "unused"})
	if err != nil || created || retried.ID != run.ID {
		t.Fatalf("retry run=%#v created=%v err=%v", retried, created, err)
	}

	if err := s.MarkAutomationRunDelivered(run.ID, `{}`, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	candidates, err = s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(3*time.Minute))
	if err != nil || len(candidates) != 0 {
		t.Fatalf("delivered duplicate candidates = %#v err=%v", candidates, err)
	}
}

func TestReapplyWhileDisabledPreservesReviewActivationBaseline(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	const spec = `{"id":"review"}`
	def, err := s.UpsertAutomationDefinition("review", "Review", spec, now)
	if err != nil {
		t.Fatal(err)
	}
	const subject = "github.com/owner/repo#42"
	baselineGitHubReviewAutomation(t, s, def.ID, "github.com", now)
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now); err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	if _, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{}`, `{}`, now, ids); err != nil || !created {
		t.Fatalf("initial claim created=%v err=%v", created, err)
	}
	if _, _, err := s.SetAutomationEnabled(def.ID, false, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if reapplied, err := s.UpsertAutomationDefinition(def.ID, def.Name, spec, now.Add(90*time.Second)); err != nil {
		t.Fatal(err)
	} else if reapplied.Enabled {
		t.Fatalf("re-apply re-enabled a disabled definition: %#v", reapplied)
	}
	if _, _, err := s.SetAutomationEnabled(def.ID, true, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	stale, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(90*time.Second))
	if err != nil || len(stale) != 0 {
		t.Fatalf("pre-enable observation crossed enable fence: candidates=%#v err=%v", stale, err)
	}
	candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(3*time.Minute))
	if err != nil || len(candidates) != 0 {
		t.Fatalf("re-enabled baseline candidates=%#v err=%v", candidates, err)
	}
	candidates, err = s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(4*time.Minute))
	if err != nil || len(candidates) != 0 {
		t.Fatalf("unchanged baseline candidates=%#v err=%v", candidates, err)
	}
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", nil, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	candidates, err = s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(6*time.Minute))
	if err != nil || len(candidates) != 1 || candidates[0].Cycle != 3 {
		t.Fatalf("post-enable re-request candidates=%#v err=%v", candidates, err)
	}
}

func TestGitHubReviewCursorOrdersObservationsWithinOneSecond(t *testing.T) {
	s := New()
	base := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, base)
	if err != nil {
		t.Fatal(err)
	}
	const subject = "github.com/owner/repo#42"
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, base.Add(100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", nil, base.Add(200*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, base.Add(150*time.Millisecond))
	if err != nil || len(candidates) != 0 {
		t.Fatalf("stale same-second candidates=%#v err=%v", candidates, err)
	}
	var active int
	if err := s.db.QueryRow(`SELECT active FROM automation_review_request_edges WHERE definition_id=? AND subject_key=?`, def.ID, subject).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatal("stale same-second observation reactivated withdrawn demand")
	}
}

func TestSetAutomationEnabledFlipsStateAndIsIdempotent(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("daily-check", "Daily check", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}

	got, changed, err := s.SetAutomationEnabled(def.ID, true, now.Add(time.Minute))
	if err != nil || changed || got == nil || !got.Enabled {
		t.Fatalf("no-op enable: def=%#v changed=%v err=%v", got, changed, err)
	}
	if !got.UpdatedAt.Equal(def.UpdatedAt) {
		t.Fatalf("no-op enable touched updated_at: got %v, want %v", got.UpdatedAt, def.UpdatedAt)
	}

	disabled, changed, err := s.SetAutomationEnabled(def.ID, false, now.Add(2*time.Minute))
	if err != nil || !changed || disabled == nil || disabled.Enabled {
		t.Fatalf("disable: def=%#v changed=%v err=%v", disabled, changed, err)
	}

	againNoOp, changed, err := s.SetAutomationEnabled(def.ID, false, now.Add(3*time.Minute))
	if err != nil || changed || againNoOp == nil || againNoOp.Enabled {
		t.Fatalf("no-op disable: def=%#v changed=%v err=%v", againNoOp, changed, err)
	}

	reenabled, changed, err := s.SetAutomationEnabled(def.ID, true, now.Add(4*time.Minute))
	if err != nil || !changed || reenabled == nil || !reenabled.Enabled {
		t.Fatalf("re-enable: def=%#v changed=%v err=%v", reenabled, changed, err)
	}

	missing, changed, err := s.SetAutomationEnabled("does-not-exist", true, now)
	if err != nil || changed || missing != nil {
		t.Fatalf("unknown id: def=%#v changed=%v err=%v", missing, changed, err)
	}
}

func TestSetAutomationEnabledReenableBaselinesCurrentReviewDemand(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	const spec = `{"id":"review"}`
	def, err := s.UpsertAutomationDefinition("review", "Review", spec, now)
	if err != nil {
		t.Fatal(err)
	}
	const subject = "github.com/owner/repo#42"
	baselineGitHubReviewAutomation(t, s, def.ID, "github.com", now)
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now); err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1"}
	if _, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{}`, `{}`, now, ids); err != nil || !created {
		t.Fatalf("initial claim created=%v err=%v", created, err)
	}
	if _, changed, err := s.SetAutomationEnabled(def.ID, false, now.Add(time.Minute)); err != nil || !changed {
		t.Fatalf("disable: changed=%v err=%v", changed, err)
	}
	if _, changed, err := s.SetAutomationEnabled(def.ID, true, now.Add(2*time.Minute)); err != nil || !changed {
		t.Fatalf("re-enable: changed=%v err=%v", changed, err)
	}
	stale, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(90*time.Second))
	if err != nil || len(stale) != 0 {
		t.Fatalf("pre-enable observation crossed enable fence: candidates=%#v err=%v", stale, err)
	}
	candidates, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now.Add(3*time.Minute))
	if err != nil || len(candidates) != 0 {
		t.Fatalf("re-enabled baseline candidates=%#v err=%v", candidates, err)
	}
	if needs, err := s.AutomationReviewRequestNeedsClaim(def.ID, subject, 2); err != nil || needs {
		t.Fatalf("baselined cycle needs claim=%v err=%v", needs, err)
	}
	if _, _, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 2, def.Revision, `{}`, `{}`, now.Add(3*time.Minute), AutomationRunReservation{RunID: "suppressed"}); err == nil {
		t.Fatal("direct claim accepted a baselined cycle")
	}
}

func TestSetAutomationEnabledNeverTouchesSpecOrRevision(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	const spec = `{"id":"nightly-sweep"}`
	def, err := s.UpsertAutomationDefinition("nightly-sweep", "Nightly sweep", spec, now)
	if err != nil {
		t.Fatal(err)
	}
	startRevision := def.Revision

	disabled, changed, err := s.SetAutomationEnabled(def.ID, false, now.Add(time.Minute))
	if err != nil || !changed || disabled == nil || disabled.Enabled {
		t.Fatalf("disable: def=%#v changed=%v err=%v", disabled, changed, err)
	}
	if disabled.Revision != startRevision {
		t.Fatalf("disable bumped revision: got %d, want unchanged %d", disabled.Revision, startRevision)
	}
	if disabled.SpecJSON != spec {
		t.Fatalf("disable touched spec_json: got %q, want unchanged %q", disabled.SpecJSON, spec)
	}

	noop, changed, err := s.SetAutomationEnabled(def.ID, false, now.Add(2*time.Minute))
	if err != nil || changed || noop == nil {
		t.Fatalf("no-op disable: def=%#v changed=%v err=%v", noop, changed, err)
	}
	if noop.Revision != disabled.Revision {
		t.Fatalf("no-op bumped revision: got %d, want %d", noop.Revision, disabled.Revision)
	}
}

func TestSetAutomationEnabledDegradesGracefullyOnCorruptSpecJSON(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	const corruptJSON = `not-json`
	def, err := s.UpsertAutomationDefinition("corrupt-spec", "Corrupt spec", corruptJSON, now)
	if err != nil {
		t.Fatal(err)
	}

	disabled, changed, err := s.SetAutomationEnabled(def.ID, false, now.Add(time.Minute))
	if err != nil || !changed || disabled == nil || disabled.Enabled {
		t.Fatalf("disable must still succeed on a corrupt spec: def=%#v changed=%v err=%v", disabled, changed, err)
	}
	if disabled.Revision != def.Revision {
		t.Fatalf("disable bumped revision: got %d, want unchanged %d", disabled.Revision, def.Revision)
	}
	if disabled.SpecJSON != corruptJSON {
		t.Fatalf("corrupt spec_json was touched instead of left alone: %s", disabled.SpecJSON)
	}
}

func TestListPrunableAutomationRunsProtectsBoundThreadOrigin(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-origin", OccurrenceID: "occ-origin", SeedID: "ticket-origin", SessionID: "session-origin", WorkspaceID: "workspace-origin", PaneID: "pane-origin"}
	origin, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:1", "singleton", def.Revision, `{}`, `{}`, now, ids)
	if err != nil || !created {
		t.Fatalf("claim origin created=%v err=%v", created, err)
	}
	if err := s.MarkAutomationRunDelivered(origin.ID, "{}", now); err != nil {
		t.Fatal(err)
	}

	far := now.Add(365 * 24 * time.Hour)
	prunable, err := s.ListPrunableAutomationRuns(def.ID, 0, far)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range prunable {
		if r.ID == origin.ID {
			t.Fatalf("expected the bound thread's origin run to be protected, got it in the prunable set: %#v", prunable)
		}
	}
}

func TestListPrunableAutomationRunsStillPrunesNonContinuityRuns(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("cleanup", "Cleanup", `{"id":"cleanup"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-manual", OccurrenceID: "occ-manual", SeedID: "ticket-manual", SessionID: "session-manual", WorkspaceID: "workspace-manual", PaneID: "pane-manual"}
	run, created, err := s.ClaimManualAutomationRun(def.ID, "request-1", "", `{}`, def.Revision, `{}`, now, ids)
	if err != nil || !created {
		t.Fatalf("claim created=%v err=%v", created, err)
	}
	if err := s.MarkAutomationRunDelivered(run.ID, "{}", now); err != nil {
		t.Fatal(err)
	}

	far := now.Add(365 * 24 * time.Hour)
	prunable, err := s.ListPrunableAutomationRuns(def.ID, 0, far)
	if err != nil {
		t.Fatal(err)
	}
	if len(prunable) != 1 || prunable[0].ID != run.ID {
		t.Fatalf("expected the non-continuity run to remain prunable, got %#v", prunable)
	}
}

func TestUpsertAutomationDefinitionBumpsRevisionOnSpecJSONChangeOnly(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now)
	if err != nil {
		t.Fatal(err)
	}
	if def.Revision != 1 {
		t.Fatalf("initial revision = %d, want 1", def.Revision)
	}

	noop, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if noop.Revision != def.Revision {
		t.Fatalf("no-op reapply revision = %d, want unchanged %d", noop.Revision, def.Revision)
	}
	if !noop.Enabled {
		t.Fatalf("no-op reapply disturbed enabled: %#v", noop)
	}

	edited, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly","edited":true}`, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if edited.Revision != def.Revision+1 {
		t.Fatalf("spec_json edit revision = %d, want %d", edited.Revision, def.Revision+1)
	}
}

func TestOriginAutomationRunIDForSeedSurvivesBindingRotation(t *testing.T) {
	s := New()
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{}`, now)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:one", "singleton", def.Revision, `{}`, `{}`, now, AutomationRunReservation{
		RunID: "run-1", OccurrenceID: "occ-1", SeedID: "s-seed01", SessionID: "session-1", WorkspaceID: "workspace-1", PaneID: "pane-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutomationRunDelivered(first.ID, `{}`, now); err != nil {
		t.Fatal(err)
	}
	second, _, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:two", "singleton", def.Revision, `{}`, `{}`, now.Add(time.Minute), AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2"})
	if err != nil || second.SeedID != first.SeedID {
		t.Fatalf("second run=%#v err=%v, want shared seed %s", second, err, first.SeedID)
	}
	if err := s.ReleaseAutomationContinuityBindings(def.ID, AutomationBindingReleasedContractRotated, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if active, err := s.GetActiveAutomationContinuityBinding(def.ID, "singleton"); err != nil || active != nil {
		t.Fatalf("active binding after rotation=%#v err=%v, want none", active, err)
	}
	origin, err := s.OriginAutomationRunIDForSeed(def.ID, first.SeedID)
	if err != nil || origin != first.ID {
		t.Fatalf("origin run for %s = %q err=%v, want %s", first.SeedID, origin, err, first.ID)
	}
	if origin, err := s.OriginAutomationRunIDForSeed(def.ID, "s-nobody"); err != nil || origin != "" {
		t.Fatalf("origin run for an unbound seed = %q err=%v, want none", origin, err)
	}
}
