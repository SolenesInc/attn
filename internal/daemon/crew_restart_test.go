package daemon

import (
	"net"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
)

func crewRestartCall(t *testing.T, d *Daemon, member, requestID string) protocol.Response {
	t.Helper()
	current, doc, err := d.crewMember(member)
	expectedRevision := 0
	if err != nil {
		if strings.Contains(err.Error(), "outpost") {
			current.BindingSession = ""
		} else {
			t.Fatalf("read current crew day: %v", err)
		}
	} else {
		expectedRevision = int(doc.Rev)
		if !d.crewBindingLive(current) {
			current.BindingSession = ""
		}
	}
	msg := protocol.CrewRestartMessage{
		Cmd: protocol.CmdCrewRestart, Member: member, RequestID: requestID,
		ExpectedSessionID: protocol.Ptr(current.BindingSession), ExpectedRevision: protocol.Ptr(expectedRevision),
	}
	return gardenCall(t, func(c net.Conn) { d.handleCrewRestart(c, &msg) })
}

func setCrewRestart(d *Daemon, memberID string, restart *crew.Restart) (crew.Member, error) {
	return d.updateCrewMember(memberID, func(member *crew.Member) (bool, error) {
		copy := *restart
		member.Restart = &copy
		return true, nil
	})
}

func TestCrewRestart_ReconcileWakesAPendingRestartWhoseDayWasAlreadyReleased(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	runtime := &crewRuntimeBackend{fakeSpawnBackend: backend, running: make(map[string]bool)}
	d.ptyBackend = runtime
	woken, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	runtime.running[woken.SessionID] = false
	if _, err := d.releaseCrewBinding("alder", woken.SessionID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := setCrewRestart(d, "alder", &crew.Restart{RequestID: "released-day", SessionID: woken.SessionID, State: crew.RestartQueued}); err != nil {
		t.Fatalf("seed pending restart: %v", err)
	}

	d.reconcileCrewRestarts()

	member := memberByID(t, crewList(t, d), "alder")
	if member.Restart == nil || member.Restart.State != protocol.CrewRestartStateCompleted {
		t.Fatalf("reconciled restart = %+v, want completed by a wake", member.Restart)
	}
	if protocol.Deref(member.BindingSession) == "" || len(spawnedSessions(t, backend)) != 2 {
		t.Fatalf("binding/spawns = %q/%d, want a fresh day", protocol.Deref(member.BindingSession), len(spawnedSessions(t, backend)))
	}
}
