package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/testworld"
)

type enrollmentJSON struct {
	Status       string `json:"status"`
	DaemonID     string `json:"daemon_id"`
	HomeDaemonID string `json:"home_daemon_id"`
	IsHome       bool   `json:"is_home"`
	Message      string `json:"message"`
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
	const otherHome = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	s := testworld.NewStack(t)
	requireLines(t, "enrollment --help", s.Attn("enrollment", "--help").Stdout, "status", "leave")

	s.Start()
	s.Stop()

	var own enrollmentJSON
	s.Attn("enrollment", "status", "--json").JSON(t, &own)
	if !own.IsHome || own.DaemonID == "" || own.HomeDaemonID != own.DaemonID {
		t.Fatalf("a fresh daemon's status = %+v, want its own home", own)
	}
	requireLines(t, "status on a home", s.Attn("enrollment", "status").Stdout, own.DaemonID, "enrollment: home")

	record := `{"home_daemon_id": "` + otherHome + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(s.Dir, enrollment.RecordFileName), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	requireLines(t, "status when enrolled to another daemon", s.Attn("enrollment", "status").Stdout,
		own.DaemonID, "enrollment: enrolled to "+otherHome, "attn enrollment leave")

	if left := s.Attn("enrollment", "leave"); left.Code != 0 {
		t.Fatalf("leave exited %d: %s", left.Code, left.Stderr)
	}
	var back enrollmentJSON
	s.Attn("enrollment", "status", "--json").JSON(t, &back)
	if !back.IsHome || back.HomeDaemonID != own.DaemonID {
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
