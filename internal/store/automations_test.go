package store

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
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
	def, err := s.UpsertAutomationDefinition("review", "Review", `{}`, "", now)
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
	run, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, `{}`, `{}`, now, AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1"})
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

func TestSetAutomationEnabledReenableBaselinesCurrentReviewDemand(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	const spec = `{"id":"review"}`
	def, err := s.UpsertAutomationDefinition("review", "Review", spec, "", now)
	if err != nil {
		t.Fatal(err)
	}
	const subject = "github.com/owner/repo#42"
	baselineGitHubReviewAutomation(t, s, def.ID, "github.com", now)
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now); err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1"}
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

func TestContinuityBindingIsNotReclaimedByAnAgentMovedToAnotherProfile(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	home, _, err := s.CreateProfile("Home")
	if err != nil {
		t.Fatal(err)
	}
	work, _, err := s.CreateProfile("Work")
	if err != nil {
		t.Fatal(err)
	}
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, home.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	firstIDs := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "seed-1", SessionID: "session-1"}
	first, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-20T03:00:00Z", "singleton", def.Revision, `{}`, `{}`, now, firstIDs)
	if err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	if err := markAutomationRunDeliveredForTest(s, first.ID, `{}`, now); err != nil {
		t.Fatal(err)
	}
	if err := s.AddChecked(&protocol.Session{ID: "session-1", Label: "nightly", Agent: "codex", Directory: "/tmp", ProfileID: home.ID, State: protocol.SessionStateIdle}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MoveSessionToProfile(SessionProfileMoveRequest{SessionID: "session-1", ExpectedProfileID: home.ID, DestinationProfileID: work.ID}); err != nil {
		t.Fatal(err)
	}

	secondIDs := AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", SeedID: "seed-2", SessionID: "session-2"}
	second, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-21T03:00:00Z", "singleton", def.Revision, `{}`, `{}`, now.Add(24*time.Hour), secondIDs)
	if err != nil || !created {
		t.Fatalf("second claim created=%v err=%v", created, err)
	}
	if second.SessionID != "session-2" || second.SeedID != "seed-2" || second.ProfileID != home.ID {
		t.Fatalf("second run reclaimed the moved agent: %#v", second)
	}
	var status, reason string
	if err := s.db.QueryRow(`SELECT status, released_reason FROM automation_continuity_bindings WHERE session_id = ?`, "session-1").Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != AutomationBindingStatusReleased || reason != AutomationBindingReleasedAgentMoved {
		t.Fatalf("moved agent's binding = %s/%s, want %s/%s", status, reason, AutomationBindingStatusReleased, AutomationBindingReleasedAgentMoved)
	}
}

func TestAClosedContinuityWorkerFollowsItsAutomationWhenItsProfileIsDeleted(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	doomed, _, err := s.CreateProfile("Doomed")
	if err != nil {
		t.Fatal(err)
	}
	kept, _, err := s.CreateProfile("Kept")
	if err != nil {
		t.Fatal(err)
	}
	def, err := s.UpsertAutomationDefinition("nightly", "Nightly", `{"id":"nightly"}`, doomed.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	first, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-20T03:00:00Z", "singleton", def.Revision, `{}`, `{}`, now,
		AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "seed-1", SessionID: "session-1"})
	if err != nil || !created {
		t.Fatalf("first claim created=%v err=%v", created, err)
	}
	if err := markAutomationRunDeliveredForTest(s, first.ID, `{}`, now); err != nil {
		t.Fatal(err)
	}
	if err := s.AddChecked(&protocol.Session{ID: "session-1", Label: "nightly", Agent: "codex", Directory: "/tmp", ProfileID: doomed.ID, State: protocol.SessionStateIdle}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CloseSession("session-1", SessionClose{}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteProfile(doomed.ID, doomed.Revision, kept.ID); err != nil {
		t.Fatal(err)
	}

	second, created, err := s.ClaimScheduledAutomationRun(def.ID, "scheduled:2026-07-21T03:00:00Z", "singleton", def.Revision, `{}`, `{}`, now.Add(24*time.Hour),
		AutomationRunReservation{RunID: "run-2", OccurrenceID: "occ-2", SeedID: "seed-2", SessionID: "session-2"})
	if err != nil || !created {
		t.Fatalf("second claim created=%v err=%v", created, err)
	}
	if second.SessionID != "session-1" || second.SeedID != "seed-1" || second.ProfileID != kept.ID {
		t.Fatalf("second run = %#v, want it to continue session-1 in %s", second, kept.ID)
	}
}
