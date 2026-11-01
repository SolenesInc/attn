package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestUpgradeShowsAStoredSessionFailureOnlyInItsSessionsProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, 1793575730807813)
	if err != nil {
		t.Fatal(err)
	}
	side, _ := mustCreateProfile(t, s, "Side")
	other, _ := mustCreateProfile(t, s, "Other")
	addProfileSession(t, s, "side-agent", side.ID)
	if _, err := s.db.Exec(`INSERT INTO notifications(id,kind,title,source_kind,source_id,created_at) VALUES('before-actions','task_failed','Couldn’t name session early','task','early-job','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO jobs(id,kind,state,scheduled_at,created_at,updated_at) VALUES('review-job','garden_review_classify','failed','','','')`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	add := func(rec NotificationRecord) string {
		t.Helper()
		added, err := s.AddNotification(rec, now)
		if err != nil {
			t.Fatal(err)
		}
		return added.ID
	}
	openSession := func(id protocol.SessionID) []NotificationAction {
		return []NotificationAction{{Kind: "open_session", Label: "Open session", TargetID: id}, {Kind: "retry_task", Label: "Retry", TargetID: "job"}}
	}
	sideFailure := add(NotificationRecord{Kind: "task_failed", Title: "Couldn’t name \"side-agent\"", Actions: openSession("side-agent"), SourceKind: "task", SourceID: "title-job"})
	add(NotificationRecord{Kind: "task_failed", Title: "Couldn’t name session gone", Actions: openSession("gone"), SourceKind: "task", SourceID: "gone-job"})
	add(NotificationRecord{Kind: "task_failed", Title: "Couldn’t classify \"Old work\"", SourceKind: "task", SourceID: "review-job"})
	unknownFailure := add(NotificationRecord{Kind: "task_failed", Title: "Background job sweep failed", SourceKind: "task", SourceID: "sweep-job"})
	pluginStopped := add(NotificationRecord{Kind: "plugin_parked", Title: "Plugin stopped: alpha", SourceKind: "plugin", SourceID: "alpha"})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	listed := func(profileID string) []string {
		t.Helper()
		list, err := s.ListNotifications(profileID)
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, rec := range list {
			ids = append(ids, rec.ID)
		}
		return ids
	}
	sameSet := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		seen := map[string]bool{}
		for _, id := range got {
			seen[id] = true
		}
		for _, id := range want {
			if !seen[id] {
				return false
			}
		}
		return true
	}
	if got, want := listed(side.ID), []string{sideFailure, unknownFailure, pluginStopped, "before-actions"}; !sameSet(got, want) {
		t.Errorf("after the upgrade Side lists %v, want its session's failure and the daemon-wide %v", got, want)
	}
	if got, want := listed(other.ID), []string{unknownFailure, pluginStopped, "before-actions"}; !sameSet(got, want) {
		t.Errorf("after the upgrade Other lists %v, want only the daemon-wide %v", got, want)
	}
}
