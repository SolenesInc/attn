package enrollment

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	homeID    = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	otherHome = "d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func seedDaemonID(t *testing.T, root, id string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, DaemonIDFileName), []byte(id+"\n"), 0600); err != nil {
		t.Fatalf("seed daemon id: %v", err)
	}
}

func TestRefuseOutpost_AllowsAFreshDataRootAndItsOwnHome(t *testing.T) {
	root := t.TempDir()
	if err := RefuseOutpost(root); err != nil {
		t.Fatalf("fresh data root refused: %v", err)
	}
	seedDaemonID(t, root, homeID)
	if _, err := Ensure(root, homeID); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := RefuseOutpost(root); err != nil {
		t.Fatalf("home refused: %v", err)
	}
}

func TestRefuseOutpost_RefusesAnEnrolledOutpostNamingItsHome(t *testing.T) {
	root := t.TempDir()
	seedDaemonID(t, root, homeID)
	if _, err := Enroll(root, otherHome); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	err := RefuseOutpost(root)
	var outpost *OutpostError
	if !errors.As(err, &outpost) {
		t.Fatalf("RefuseOutpost = %v, want *OutpostError", err)
	}
	if outpost.HomeDaemonID != otherHome || outpost.DaemonID != homeID {
		t.Fatalf("outpost error = %+v", outpost)
	}
	if !strings.Contains(err.Error(), "attn enrollment leave") {
		t.Fatalf("refusal does not say how to leave: %v", err)
	}
}

func TestRefuseOutpost_RefusesARecordWithoutItsDaemonID(t *testing.T) {
	root := t.TempDir()
	seedDaemonID(t, root, homeID)
	if _, err := Ensure(root, homeID); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := os.Remove(filepath.Join(root, DaemonIDFileName)); err != nil {
		t.Fatal(err)
	}
	err := RefuseOutpost(root)
	var outpost *OutpostError
	if !errors.As(err, &outpost) {
		t.Fatalf("RefuseOutpost = %v, want *OutpostError", err)
	}
	if !strings.Contains(err.Error(), "restore "+DaemonIDFileName+" with "+homeID) {
		t.Fatalf("refusal %q does not say how to recover without a daemon id", err)
	}
}

func TestRefuseOutpost_FailsClosedOnAnUnreadableRecord(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, RecordFileName), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RefuseOutpost(root); err == nil || !strings.Contains(err.Error(), "cannot tell") {
		t.Fatalf("RefuseOutpost = %v, want an unreadable-record refusal", err)
	}
}
