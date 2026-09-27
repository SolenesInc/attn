package daemon

import (
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
	if ws := d.store.GetWorkspace("workspace-ghost-session"); ws != nil {
		t.Fatalf("workspace survived a failed resume: %+v", ws)
	}
}
