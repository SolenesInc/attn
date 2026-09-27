package store

import (
	"testing"
	"time"
)

func markAutomationRunDeliveredForTest(s *Store, id, resolved string, now time.Time) error {
	_, _, err := s.MarkAutomationRunDeliveredWithEvent(id, resolved, BusEvent{
		Name: "garden.seed.work.ready", Subject: "s-test", Payload: `{"automation_run_id":"` + id + `"}`,
	}, now)
	return err
}

func baselineGitHubReviewAutomation(t *testing.T, s *Store, definitionID, host string, at time.Time) {
	t.Helper()
	if candidates, err := s.ReconcileAutomationReviewRequests(definitionID, host, nil, at); err != nil || len(candidates) != 0 {
		t.Fatalf("establish review automation baseline: candidates=%#v err=%v", candidates, err)
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
	if err := markAutomationRunDeliveredForTest(s, run.ID, `{}`, now); err != nil {
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
