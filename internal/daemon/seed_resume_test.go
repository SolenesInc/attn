package daemon

import (
	"strings"
	"syscall"
	"testing"

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func seedNoteCount(t *testing.T, d *Daemon, seedID string) int {
	t.Helper()
	notes, err := d.readNotesDomain(seedID)
	if err != nil {
		t.Fatalf("readNotesDomain(%s): %v", seedID, err)
	}
	return len(notes)
}

func delegateBoundSeed(t *testing.T, d *Daemon, backend *fakeSpawnBackend, sourceSessionID, agent string) (string, string) {
	t.Helper()
	if d.daemonInstanceID == "" {
		id, err := enrollment.EnsureDaemonID(d.dataRoot)
		if err != nil {
			t.Fatalf("prepare test Garden identity: %v", err)
		}
		d.daemonInstanceID = id
		if err := d.ensureEnrollment(); err != nil {
			t.Fatalf("prepare test Garden enrollment: %v", err)
		}
	}
	d.ensureGardenCollections()
	consumeDelegatedPrompt(t, backend)
	result, err := d.delegateResolved(&resolvedDelegationLaunch{
		Cmd:             protocol.CmdDelegate,
		SourceSessionID: protocol.Ptr(sourceSessionID),
		Brief:           protocol.Ptr("Investigate the tracked task."),
		Agent:           protocol.Ptr(agent),
	})
	if err != nil {
		t.Fatalf("delegate() error = %v", err)
	}
	seedID, bound := d.gardenDispatchCrown(result.SessionID)
	if !bound {
		t.Fatal("the delegation bound no seed to its session")
	}
	return result.SessionID, seedID
}

func TestSeedResumeRollsBackPaneWhenSpawnFails(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{
		SourceSessionID: protocol.Ptr("sess-a"), Title: "Resume me",
	})
	addGardenSession(t, d, "ghost-session")
	move(t, d, "ghost-session", seed.ID, garden.VerbTend, "", "")
	d.store.Remove("ghost-session")
	if err := d.recordGardenDispatch("ghost-session", seed.ID, "", t.TempDir(), "codex", false); err != nil {
		t.Fatalf("recordGardenDispatch: %v", err)
	}
	d.ptyBackend = &failingSpawnBackend{err: syscall.EPERM}

	if _, err := d.resumeSeed(seed.ID); err == nil {
		t.Fatal("resumeSeed succeeded, want spawn failure")
	}
	if _, placed, _ := d.store.SessionPlacement("ghost-session"); placed {
		t.Fatal("a pane survived a failed resume")
	}
}

func TestSeedResumeNeedsADestinationWhenTheTendersProfileWasDeleted(t *testing.T) {
	d, backend, sourceSessionID := newGardenDelegationDaemon(t)
	leafID, seedID := delegateBoundSeed(t, d, backend, sourceSessionID, "codex")
	writeCodexRolloutFixture(t, "codex-conv-orphan")
	d.persistResumeSessionID(leafID, "codex-conv-orphan")
	d.handleUnregister(drainedConn(t), &protocol.UnregisterMessage{ID: leafID})
	d.waitForSessionTeardown(leafID)
	kept := createTestProfile(t, d.store, "Kept")
	deleteTestProfile(t, d.store, defaultProfileID(t, d.store), kept.ID)
	since := spawnCount(backend)

	if _, err := d.resumeSeed(seedID); err == nil || !strings.Contains(err.Error(), "Sessions ledger") {
		t.Fatalf("resume without a destination = %v, want a refusal pointing to the Sessions ledger", err)
	}
	if spawnCount(backend) != since {
		t.Fatal("a refused resume spawned the agent")
	}

	client := spawnTestClient()
	client.selectProfile(kept.ID)
	d.handleSeedResume(client, &protocol.SeedResumeMessage{Cmd: protocol.CmdSeedResume, SeedID: seedID, RequestID: protocol.Ptr("resume-1")})
	var result protocol.SeedResumeResultMessage
	for _, payload := range drainClientPayloads(t, client) {
		if eventName(t, payload) == protocol.EventSeedResumeResult {
			decodeInto(t, payload, &result)
		}
	}
	if !result.Success || protocol.Deref(result.ProfileID) != kept.ID || d.store.Get(leafID).ProfileID != kept.ID {
		t.Fatalf("Garden Resume from a connection in %s = %+v, want the agent resumed there", kept.ID, result)
	}
}
