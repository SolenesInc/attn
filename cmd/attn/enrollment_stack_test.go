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

func TestEnrollmentNamesTheHomeAndRefusesToBeRehomedSilently(t *testing.T) {
	t.Parallel()
	const (
		firstHome  = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		secondHome = "d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	s := testworld.NewStack(t)
	requireLines(t, "enrollment --help", s.Attn("enrollment", "--help").Stdout, "status", "enroll --home", "leave")

	s.Start()
	s.Stop()

	var own enrollmentJSON
	s.Attn("enrollment", "status", "--json").JSON(t, &own)
	if !own.IsHome || own.DaemonID == "" || own.HomeDaemonID != own.DaemonID {
		t.Fatalf("a fresh daemon's status = %+v, want its own home", own)
	}
	requireLines(t, "status on a home", s.Attn("enrollment", "status").Stdout,
		own.DaemonID, "enrollment: home", "garden and crew: this daemon owns them")

	enrolled := s.Attn("enrollment", "enroll", "--home", firstHome)
	if enrolled.Code != 0 || enrolled.Stderr != "" || strings.Count(enrolled.Stdout, "\n") != 1 || !strings.Contains(enrolled.Stdout, firstHome) {
		t.Fatalf("enroll exited %d with stdout %q and stderr %q, want one line naming the home on stdout", enrolled.Code, enrolled.Stdout, enrolled.Stderr)
	}
	requireLines(t, "status on an outpost", s.Attn("enrollment", "status").Stdout,
		own.DaemonID, "outpost of "+firstHome, "garden and crew: refused here", "attn enrollment leave", enrollment.PlanPath)

	refused := s.Attn("enrollment", "enroll", "--home", secondHome, "--json")
	var result enrollmentJSON
	refused.JSON(t, &result)
	if refused.Code != 3 || result.Status != "refused" || result.HomeDaemonID != firstHome {
		t.Fatalf("re-homing exited %d with %+v, want exit 3 and a refusal naming the current home", refused.Code, result)
	}
	if !strings.Contains(refused.Stderr, result.Message) || result.Message == "" {
		t.Fatalf("the refusal wording %q is not on stderr, where the hub reads it: %q", result.Message, refused.Stderr)
	}

	if left := s.Attn("enrollment", "leave"); left.Code != 0 {
		t.Fatalf("leave exited %d: %s", left.Code, left.Stderr)
	}
	var back enrollmentJSON
	s.Attn("enrollment", "status", "--json").JSON(t, &back)
	if !back.IsHome || back.HomeDaemonID != own.DaemonID {
		t.Fatalf("after leave the status = %+v, want its own home again", back)
	}
}

func TestADaemonKeepsItsIDAcrossRestartsAndReplacesAnUnreadableOne(t *testing.T) {
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

	if err := os.WriteFile(filepath.Join(s.Dir, enrollment.DaemonIDFileName), []byte("corrupt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Start()
	s.Stop()
	if replaced := daemonID(); !enrollment.ValidDaemonID(replaced) || replaced == first {
		t.Fatalf("after its ID file was garbled the daemon reports %q, want a fresh valid ID", replaced)
	}
}
