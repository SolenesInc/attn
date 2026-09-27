package daemon

import (
	"database/sql"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func crewSleepCall(t *testing.T, d *Daemon, member string) protocol.Response {
	t.Helper()
	return gardenCall(t, func(c net.Conn) {
		d.handleCrewSleep(c, &protocol.CrewSleepMessage{Cmd: protocol.CmdCrewSleep, Member: member})
	})
}

func persistentCrewSleepDaemon(t *testing.T) (*Daemon, *fakeSpawnBackend, *sql.DB) {
	t.Helper()
	d, backend, _ := newWakeableDaemon(t)
	d.stopEventBus()
	if err := d.store.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "crew-sleep.db")
	persistent, err := store.NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	d.store = persistent
	d.eventBus = nil
	d.ensureEventBus()
	d.ensureCrewCollections()
	d.importCrewHomes()
	direct, err := store.OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.stopAgentMailboxDoorbells()
		d.stopEventBus()
		_ = direct.Close()
		_ = persistent.Close()
	})
	return d, backend, direct
}

func TestCrewSleep_ADeadDayWithARestartPendingFailsTheRestart(t *testing.T) {
	d, backend, _ := newWakeableDaemon(t)
	runtime := &crewRuntimeBackend{fakeSpawnBackend: backend, running: make(map[string]bool)}
	d.ptyBackend = runtime
	woken, err := d.crewWake("alder", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if _, err := setCrewRestart(d, "alder", &crew.Restart{RequestID: "sleep-instead", SessionID: woken.SessionID, State: crew.RestartQueued}); err != nil {
		t.Fatalf("seed pending restart: %v", err)
	}
	runtime.running[woken.SessionID] = false

	resp := crewSleepCall(t, d, "alder")
	if !resp.Ok || !resp.CrewSleepResult.AlreadyAsleep {
		t.Fatalf("sleep = %+v / %v", resp.CrewSleepResult, protocol.Deref(resp.Error))
	}
	member := memberByID(t, crewList(t, d), "alder")
	if member.Restart == nil || member.Restart.State != protocol.CrewRestartStateFailed {
		t.Fatalf("restart = %+v, want failed because the user chose sleep", member.Restart)
	}
	if len(spawnedSessions(t, backend)) != 1 {
		t.Fatalf("spawns = %d, want no wake", len(spawnedSessions(t, backend)))
	}
}

func TestCrewSleep_LeavesTheRestartPendingWhenTheSleepPromptCannotCommit(t *testing.T) {
	d, _, db := persistentCrewSleepDaemon(t)
	woken, err := d.crewWake("trellis", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if _, err := setCrewRestart(d, "trellis", &crew.Restart{RequestID: "then-sleep", SessionID: woken.SessionID, State: crew.RestartRequested}); err != nil {
		t.Fatalf("seed pending restart: %v", err)
	}
	before, beforeDoc, err := d.crewMember("trellis")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER refuse_sleep_prompt BEFORE INSERT ON agent_mailbox_items BEGIN SELECT RAISE(ABORT, 'sleep mailbox unavailable'); END`); err != nil {
		t.Fatal(err)
	}

	failed := crewSleepCall(t, d, "trellis")
	if failed.Ok || !strings.Contains(protocol.Deref(failed.Error), "sleep mailbox unavailable") {
		t.Fatalf("sleep failure = %+v / %v", failed.CrewSleepResult, protocol.Deref(failed.Error))
	}
	after, afterDoc, err := d.crewMember("trellis")
	if err != nil || afterDoc.Rev != beforeDoc.Rev || after.Restart == nil || after.Restart.State != crew.RestartRequested || after.Restart.Withdrawn || after.BindingSession != woken.SessionID {
		t.Fatalf("member after failed sleep = %+v rev=%d, err=%v; want %+v rev=%d", after, afterDoc.Rev, err, before, beforeDoc.Rev)
	}
	items, err := d.store.UnreadAgentMailboxDeliveries(woken.SessionID)
	if err != nil || len(items) != 0 {
		t.Fatalf("sleep mailbox after failed commit = %+v, err=%v", items, err)
	}
	if _, err := db.Exec(`DROP TRIGGER refuse_sleep_prompt`); err != nil {
		t.Fatal(err)
	}
	if replay := crewRestartCall(t, d, "trellis", "restart-after-failed-sleep"); !replay.Ok {
		t.Fatalf("restart after failed sleep = %+v / %v", replay.CrewRestartResult, protocol.Deref(replay.Error))
	}
	succeeded := crewSleepCall(t, d, "trellis")
	if !succeeded.Ok {
		t.Fatalf("sleep retry: %v", protocol.Deref(succeeded.Error))
	}
	settled, _, err := d.crewMember("trellis")
	if err != nil || settled.Restart == nil || !settled.Restart.Withdrawn {
		t.Fatalf("restart after successful sleep = %+v, err=%v", settled.Restart, err)
	}
	items, err = d.store.UnreadAgentMailboxDeliveries(woken.SessionID)
	sleepPrompts := 0
	for _, item := range items {
		if item.Item.Prompt == crewRequestedSleepPrompt {
			sleepPrompts++
		}
	}
	if err != nil || sleepPrompts != 1 {
		t.Fatalf("sleep mailbox after retry = %+v, err=%v", items, err)
	}
}
