package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestSeedContinuationResumesAPluginTenderByCapability(t *testing.T) {
	d := newGardenDaemon(t)
	client, done := startPluginPipe(t, d, "snipe-plugin", nil)
	defer func() {
		_ = client.Close()
		<-done
	}()
	registerTestPluginDriver(t, client, "snipe", map[string]bool{"resume": true})
	cwd := t.TempDir()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: "sess-snipe", Label: "plugin worker", Agent: "snipe", ProfileID: defaultProfileID(t, d.store),
		Directory: cwd, State: protocol.SessionStateIdle,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "plugin work"})
	move(t, d, "sess-snipe", seed.ID, garden.VerbTend, "", "")
	d.persistResumeSessionID("sess-snipe", "snipe-conv-3")
	d.store.SetLaunchIntent("sess-snipe", store.LaunchIntent{})
	d.closeSession("sess-snipe", store.SessionClose{By: store.SessionClosedByUser})

	tended, _, err := d.readSeed(seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	continuation := d.continuationForSeed(tended)
	if continuation == nil || !continuation.ResumeAvailable || continuation.Execution.Resume != "snipe-conv-3" {
		t.Fatalf("continuation = %+v, want a resumable snipe-conv-3", continuation)
	}
}
