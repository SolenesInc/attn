package store

import (
	"testing"
	"time"
)

func baselineGitHubReviewAutomation(t *testing.T, s *Store, definitionID int, host string, at time.Time) {
	t.Helper()
	if candidates, err := s.ReconcileAutomationReviewRequests(definitionID, host, nil, at); err != nil || len(candidates) != 0 {
		t.Fatalf("establish review automation baseline: candidates=%#v err=%v", candidates, err)
	}
}

func TestSetAutomationEnabledReenableBaselinesCurrentReviewDemand(t *testing.T) {
	s := New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	const spec = `{"id":1}`
	profile, _ := s.MostRecentlyUsedProfile()
	def, err := s.UpsertAutomationDefinition(0, "Review", spec, profile.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	const (
		subject = "github.com/owner/repo#42"
		payload = `{"head_sha":"0123456789abcdef0123456789abcdef01234567"}`
	)
	baselineGitHubReviewAutomation(t, s, def.ID, "github.com", now)
	if _, err := s.ReconcileAutomationReviewRequests(def.ID, "github.com", []string{subject}, now); err != nil {
		t.Fatal(err)
	}
	ids := AutomationRunReservation{RunID: "run-1", OccurrenceID: "occ-1", SeedID: "ticket-1", SessionID: "session-1"}
	if _, created, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 1, def.Revision, payload, `{}`, now, ids); err != nil || !created {
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
	if _, _, err := s.ClaimGitHubReviewAutomationRun(def.ID, subject, 2, def.Revision, payload, `{}`, now.Add(3*time.Minute), AutomationRunReservation{RunID: "suppressed"}); err == nil {
		t.Fatal("direct claim accepted a baselined cycle")
	}
}
