package supervise

import (
	"errors"
	"testing"
	"time"
)

func TestParkingStampsWhenItHappened(t *testing.T) {
	clock := newFakeClock()
	launcher := &fakeLauncher{}
	supervisor := New(Options{Clock: clock, GiveUpAfter: 1})
	_ = supervisor.Ensure("fixture", launcher.start)

	launcher.handle(0).exit(Exit{Error: "boom"})
	waitFor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.Phase == PhaseBackoff
	})
	clock.Advance(RestartBackoff[0])
	waitFor(t, func() bool { return launcher.count() == 2 })
	launcher.handle(1).exit(Exit{Error: "boom"})
	waitFor(t, func() bool {
		snapshot, _ := supervisor.Snapshot("fixture")
		return snapshot.Phase == PhaseParked
	})

	parked, _ := supervisor.Snapshot("fixture")
	if !parked.ParkedAt.Equal(clock.Now()) {
		t.Fatalf("ParkedAt=%s, want the give-up moment %s", parked.ParkedAt, clock.Now())
	}
	clock.Advance(time.Hour)
	if later, _ := supervisor.Snapshot("fixture"); !later.ParkedAt.Equal(parked.ParkedAt) {
		t.Fatalf("ParkedAt moved %s → %s while nothing happened", parked.ParkedAt, later.ParkedAt)
	}

	if err := supervisor.Ensure("fixture", launcher.start); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if revived, _ := supervisor.Snapshot("fixture"); !revived.ParkedAt.IsZero() {
		t.Fatalf("ParkedAt=%s after revival, want zero", revived.ParkedAt)
	}
}

func TestAdoptParkedRestoresWithoutLaunching(t *testing.T) {
	clock := newFakeClock()
	launcher := &fakeLauncher{}
	supervisor := New(Options{Clock: clock, GiveUpAfter: 10})

	parkedAt := clock.Now().Add(-3 * time.Hour)
	park := Park{
		ParkedAt:       parkedAt,
		RestartAttempt: 10,
		LastExit:       &Exit{At: parkedAt, ExitCode: intPtr(1)},
	}
	if err := supervisor.AdoptParked("fixture", park); err != nil {
		t.Fatalf("AdoptParked: %v", err)
	}

	snapshot, ok := supervisor.Snapshot("fixture")
	if !ok {
		t.Fatal("the adopted child is not supervised")
	}
	if snapshot.Phase != PhaseParked || snapshot.Running || !snapshot.NextRestartAt.IsZero() {
		t.Fatalf("snapshot=%+v, want parked with nothing running or scheduled", snapshot)
	}
	if snapshot.Desired != DesiredRunning {
		t.Fatalf("Desired=%q, want running — parking is the supervisor giving up, not the consumer", snapshot.Desired)
	}
	if !snapshot.ParkedAt.Equal(parkedAt) {
		t.Fatalf("ParkedAt=%s, want the original %s", snapshot.ParkedAt, parkedAt)
	}
	if snapshot.RestartAttempt != 10 {
		t.Fatalf("RestartAttempt=%d, want 10", snapshot.RestartAttempt)
	}
	if snapshot.LastExit == nil || snapshot.LastExit.ExitCode == nil || *snapshot.LastExit.ExitCode != 1 {
		t.Fatalf("LastExit=%+v, want the recorded exit code 1", snapshot.LastExit)
	}

	clock.Advance(24 * time.Hour)
	if got := launcher.count(); got != 0 {
		t.Fatalf("starts after adopting a park=%d, want 0", got)
	}

	if err := supervisor.EnsureUnlessParked("fixture", launcher.start); !errors.Is(err, ErrParked) {
		t.Fatalf("EnsureUnlessParked=%v, want ErrParked", err)
	}
	if got := launcher.count(); got != 0 {
		t.Fatalf("EnsureUnlessParked started the child %d time(s)", got)
	}
	if err := supervisor.Ensure("fixture", launcher.start); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	waitFor(t, func() bool { return launcher.count() == 1 })
	revived, _ := supervisor.Snapshot("fixture")
	if revived.Phase == PhaseParked || revived.RestartAttempt != 0 {
		t.Fatalf("revived=%+v, want a running child with its budget back", revived)
	}
}

func TestAdoptParkedIsSilent(t *testing.T) {
	changes, giveUps := 0, 0
	supervisor := New(Options{
		Clock:    newFakeClock(),
		OnChange: func(string) { changes++ },
		OnGiveUp: func(string, Snapshot) { giveUps++ },
	})
	if err := supervisor.AdoptParked("fixture", Park{ParkedAt: time.Now()}); err != nil {
		t.Fatalf("AdoptParked: %v", err)
	}
	if changes != 0 || giveUps != 0 {
		t.Fatalf("OnChange=%d OnGiveUp=%d, want 0 and 0", changes, giveUps)
	}
}

func TestAdoptParkedRefusesASupervisedChild(t *testing.T) {
	launcher := &fakeLauncher{}
	supervisor := New(Options{Clock: newFakeClock()})
	if err := supervisor.Ensure("fixture", launcher.start); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := supervisor.AdoptParked("fixture", Park{ParkedAt: time.Now()}); err == nil {
		t.Fatal("AdoptParked onto a running child succeeded")
	}
	if snapshot, _ := supervisor.Snapshot("fixture"); snapshot.Phase == PhaseParked {
		t.Fatalf("a refused adopt still parked the child: %+v", snapshot)
	}
	supervisor.Shutdown()
}
