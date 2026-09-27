package main_test

import (
	"encoding/json"
	"net/http"
	"testing"

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

func TestAFreshDaemonReportsItselfAsItsOwnHome(t *testing.T) {
	t.Parallel()
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

}
