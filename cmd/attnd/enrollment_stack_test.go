package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessionref"
	"github.com/victorarias/attn/internal/testworld"
)

type enrollmentJSON struct {
	Status   string `json:"status"`
	DaemonID string `json:"daemon_id"`
	Role     string `json:"role"`
	HomeID   string `json:"home_id"`
	Replaced string `json:"home_daemon_id"`
	IsHome   bool   `json:"is_home"`
	Message  string `json:"message"`
}

func homeID(t *testing.T, s *testworld.Stack) string {
	t.Helper()
	var status enrollmentJSON
	s.Attn("enrollment", "status", "--json").JSON(t, &status)
	if !enrollment.ValidHomeID(status.HomeID) {
		t.Fatalf("enrollment status names no home id: %+v", status)
	}
	return status.HomeID
}

func shortID[ID ~string](session ID) string {
	return sessionref.Short(protocol.SessionID(session))
}

func requireLines(t *testing.T, what, text string, want ...string) {
	t.Helper()
	for _, line := range want {
		if !strings.Contains(text, line) {
			t.Errorf("%s does not say %q:\n%s", what, line, text)
		}
	}
}

func TestAnEnrolledDataFolderNamesItsEnrollmentAndLeaveRestoresIt(t *testing.T) {
	t.Parallel()
	const otherHome = "h-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	s := testworld.NewStack(t)
	requireLines(t, "enrollment --help", s.Attn("enrollment", "--help").Stdout, "status", "leave")

	s.Start()
	s.Stop()

	var own enrollmentJSON
	s.Attn("enrollment", "status", "--json").JSON(t, &own)
	if !own.IsHome || own.Role != "home" || own.DaemonID == "" || !enrollment.ValidHomeID(own.HomeID) {
		t.Fatalf("a fresh daemon's status = %+v, want its own home", own)
	}
	requireLines(t, "status on a home", s.Attn("enrollment", "status").Stdout, own.DaemonID, own.HomeID, "enrollment: home")

	record := `{"role": "outpost", "home_id": "` + otherHome + `", "home_name": "elsewhere"}` + "\n"
	if err := os.WriteFile(filepath.Join(s.Dir, enrollment.RecordFileName), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	requireLines(t, "status when enrolled to another daemon", s.Attn("enrollment", "status").Stdout,
		own.DaemonID, "enrollment: enrolled to elsewhere ("+otherHome+")", "attn enrollment leave")

	if left := s.Attn("enrollment", "leave"); left.Code != 0 {
		t.Fatalf("leave exited %d: %s", left.Code, left.Stderr)
	}
	var back enrollmentJSON
	s.Attn("enrollment", "status", "--json").JSON(t, &back)
	if !back.IsHome || back.DaemonID != own.DaemonID || !enrollment.ValidHomeID(back.HomeID) || back.HomeID == otherHome {
		t.Fatalf("after leave the status = %+v, want its own home again", back)
	}
}

func TestADaemonKeepsItsIDAcrossRestarts(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	daemonID := func() string {
		t.Helper()
		var status enrollmentJSON
		s.Attn("enrollment", "status", "--json").JSON(t, &status)
		return status.DaemonID
	}
	s.Start()
	s.Stop()
	first := daemonID()
	s.Start()
	s.Stop()
	if again := daemonID(); again != first || !enrollment.ValidDaemonID(first) {
		t.Fatalf("the daemon ID went from %q to %q across a restart, want one valid ID kept", first, again)
	}

}
