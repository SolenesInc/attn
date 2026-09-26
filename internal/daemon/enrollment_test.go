package daemon

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/enrollment"
)

func newEnrolledDaemon(t *testing.T, homeDaemonID string) *Daemon {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	id, err := enrollment.EnsureDaemonID(d.dataRoot)
	if err != nil {
		t.Fatalf("EnsureDaemonID: %v", err)
	}
	d.daemonInstanceID = id
	if err := d.ensureEnrollment(); err != nil {
		t.Fatalf("ensureEnrollment: %v", err)
	}
	if homeDaemonID != "" {
		if _, err := enrollment.Enroll(d.dataRoot, homeDaemonID); err != nil {
			t.Fatalf("Enroll: %v", err)
		}
	}
	return d
}

func TestDaemon_OutpostDoesNotEnrollRemotes(t *testing.T) {
	home := newEnrolledDaemon(t, "")
	if got := home.homeDaemonIDForEnrollment(); got != home.daemonInstanceID {
		t.Fatalf("home daemon offered %q as the enrolling home, want %q", got, home.daemonInstanceID)
	}

	outpost := newEnrolledDaemon(t, "d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if got := outpost.homeDaemonIDForEnrollment(); got != "" {
		t.Fatalf("outpost offered %q as the enrolling home, want no enrollment", got)
	}
}
