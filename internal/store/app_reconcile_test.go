package store

import (
	"testing"
	"time"
)

func seedReconcileApp(t *testing.T, s *Store, now time.Time) AppVersion {
	t.Helper()
	v, _, err := s.CommitAppVersion(AppVersion{
		AppName: "approval-gate", ContentHash: "sha256:first",
		Declaration: `{"name":"approval-gate"}`, ArtifactPath: "bundle.js",
	}, now)
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if err := s.SaveBusConsumer(BusConsumer{
		Name: "app:approval-gate", Filter: "ticket.*", Enabled: true,
	}, now); err != nil {
		t.Fatalf("seed consumer: %v", err)
	}
	return v
}

func TestAppReconcileTriggerDuringRunRemainsOwedAndCompletionFencesCursor(t *testing.T) {
	s := New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	seedReconcileApp(t, s, now)
	if _, _, err := s.CommitAppVersion(AppVersion{
		AppName: "approval-gate", ContentHash: "sha256:second",
		Declaration: `{"name":"approval-gate"}`, ArtifactPath: "bundle-2.js",
	}, now); err != nil {
		t.Fatal(err)
	}
	first, err := s.AppReconcilePending("approval-gate")
	if err != nil || len(first.Requests) != 1 {
		t.Fatalf("first claim = %+v, %v", first, err)
	}
	if _, err := s.AppendBusEvent(BusEvent{Name: "ticket.updated"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestAppReconcileGap("approval-gate", 0, 2, now); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBusConsumerCursor("app:approval-gate", 9, now); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAppReconcile("approval-gate", first.ThroughRequestID, first.ThroughSeq, now); err != nil {
		t.Fatal(err)
	}
	consumer, _, err := s.GetBusConsumer("app:approval-gate")
	if err != nil || consumer.Cursor != 9 {
		t.Fatalf("completion rewound cursor: %+v, %v", consumer, err)
	}
	later, err := s.AppReconcilePending("approval-gate")
	if err != nil || len(later.Requests) != 1 || later.Requests[0].Reason != AppReconcileGap {
		t.Fatalf("later claim = %+v, %v", later, err)
	}
}
