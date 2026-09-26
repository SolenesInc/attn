package main_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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
