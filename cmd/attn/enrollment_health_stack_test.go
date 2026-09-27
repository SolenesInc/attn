package main_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type enrollmentHealth struct {
	DaemonInstanceID string `json:"daemon_instance_id"`
	Enrollment       string `json:"enrollment"`
	HomeDaemonID     string `json:"home_daemon_id"`
}

func readEnrollmentHealth(t *testing.T, s *testworld.Stack) enrollmentHealth {
	t.Helper()
	resp, err := http.Get("http://" + s.WSAddr + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var health enrollmentHealth
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	return health
}

func TestTheDaemonReportsItsHomeAndFencesHomeStateWhenItCannotTell(t *testing.T) {
	t.Parallel()
	const home = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	s := testworld.NewStack(t)
	s.Start()
	own := readEnrollmentHealth(t, s)
	if own.Enrollment != "home" || own.DaemonInstanceID == "" || own.HomeDaemonID != own.DaemonInstanceID {
		t.Errorf("a fresh daemon's health = %+v, want it to be its own home", own)
	}
	if initial := s.App().Initial; protocol.Deref(initial.HomeDaemonID) != own.DaemonInstanceID {
		t.Errorf("a fresh daemon tells the app its home is %q, want its own id %s", protocol.Deref(initial.HomeDaemonID), own.DaemonInstanceID)
	}
	s.Stop()

	if enrolled := s.Attn("enrollment", "enroll", "--home", home); enrolled.Code != 0 {
		t.Fatalf("enroll exited %d: %s", enrolled.Code, enrolled.Stderr)
	}
	s.Start()
	if outpost := readEnrollmentHealth(t, s); outpost.Enrollment != "outpost of "+home || outpost.HomeDaemonID != home || outpost.DaemonInstanceID != own.DaemonInstanceID {
		t.Errorf("an outpost's health = %+v, want it the outpost of %s under its own id %s", outpost, home, own.DaemonInstanceID)
	}
	if initial := s.App().Initial; protocol.Deref(initial.HomeDaemonID) != home || protocol.Deref(initial.DaemonInstanceID) != own.DaemonInstanceID {
		t.Errorf("an outpost tells the app it is %q with home %q, want %s with home %s", protocol.Deref(initial.DaemonInstanceID), protocol.Deref(initial.HomeDaemonID), own.DaemonInstanceID, home)
	}
	crewFenced := s.Attn("crew", "list")
	if crewFenced.Code == 0 {
		t.Errorf("attn crew list answered on an outpost: %s", crewFenced.Stdout)
	}
	requireLines(t, "attn crew list on an outpost", crewFenced.Stderr, "the crew", own.DaemonInstanceID, home, "attn enrollment leave", enrollment.PlanPath)

	if err := os.WriteFile(filepath.Join(s.Dir, enrollment.RecordFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if unknown := readEnrollmentHealth(t, s); unknown.Enrollment != "unknown" || unknown.HomeDaemonID != "" {
		t.Errorf("with an unreadable enrollment record health = %+v, want enrollment unknown and no home", unknown)
	}
	if initial := s.App().Initial; protocol.Deref(initial.HomeDaemonID) != "" {
		t.Errorf("with an unreadable enrollment record the app is told the home is %q, want none", protocol.Deref(initial.HomeDaemonID))
	}
	for _, command := range [][]string{{"seed", "ls"}, {"crew", "list"}} {
		if fenced := s.Attn(command...); fenced.Code == 0 {
			t.Errorf("attn %v answered with an unreadable enrollment record: %s", command, fenced.Stdout)
		}
	}
}

func TestAnOutpostEnrolledBeforeItsFirstStartComesUpAsThatOutpost(t *testing.T) {
	t.Parallel()
	const home = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	s := testworld.NewStack(t)
	if left := s.Attn("enrollment", "leave"); left.Code == 0 || !strings.Contains(left.Stderr, "daemon id") {
		t.Errorf("leave before any start exited %d with stderr %q, want a refusal saying there is no daemon id yet", left.Code, left.Stderr)
	}
	if malformed := s.Attn("enrollment", "enroll", "--home", "not-a-daemon-id"); malformed.Code == 0 {
		t.Errorf("enroll into a malformed home exited 0: %s", malformed.Stdout)
	}
	for _, want := range []string{"enrolled", "unchanged"} {
		var result enrollmentJSON
		s.Attn("enrollment", "enroll", "--home", home, "--json").JSON(t, &result)
		if result.Status != want || result.HomeDaemonID != home {
			t.Errorf("enroll into %s before the first start = %+v, want %s", home, result, want)
		}
	}

	s.Start()
	outpost := readEnrollmentHealth(t, s)
	if outpost.Enrollment != "outpost of "+home || !enrollment.ValidDaemonID(outpost.DaemonInstanceID) {
		t.Fatalf("the first start after enrolling reports %+v, want an outpost of %s with its own id", outpost, home)
	}
	s.Stop()
	if self := s.Attn("enrollment", "enroll", "--home", outpost.DaemonInstanceID); self.Code == 0 || !strings.Contains(self.Stderr, "cannot enroll to itself") {
		t.Errorf("enroll into its own id exited %d with stderr %q, want a refusal", self.Code, self.Stderr)
	}
	var left enrollmentJSON
	s.Attn("enrollment", "leave", "--json").JSON(t, &left)
	var again enrollmentJSON
	s.Attn("enrollment", "leave", "--json").JSON(t, &again)
	if left.Status != "left" || again.Status != "unchanged" {
		t.Errorf("leaving twice reported %q then %q, want left then unchanged", left.Status, again.Status)
	}
}
