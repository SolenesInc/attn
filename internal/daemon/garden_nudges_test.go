package daemon

import (
	"net"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/hub"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
)

func TestSeedNudges_RemoteTenderStopsAtTheHomeFence(t *testing.T) {
	d := newGardenDaemon(t)
	endpoint, err := d.store.AddEndpoint("gpu-box", "gpu.example.test", "")
	if err != nil {
		t.Fatalf("add outpost: %v", err)
	}
	d.hubManager = hub.NewManager(d.store, nil, nil, nil, nil, nil)
	if !d.hubManager.ReplaceRemoteSessions(endpoint.ID, []protocol.Session{{ID: "remote-worker"}}) {
		t.Fatal("remote session was not registered")
	}
	seed := garden.Seed{ID: "s-remote", TenderSession: "remote-worker"}

	sessionID, err := d.localGardenTenderSession(seed)
	if err == nil || sessionID != "" || !strings.Contains(err.Error(), "garden notifications are home-only") ||
		!strings.Contains(err.Error(), "remote-worker") || !strings.Contains(err.Error(), endpoint.ID) {
		t.Fatalf("remote tender = %q, %v; want a named home-only refusal", sessionID, err)
	}

	d.ringSeedUnblocked([]garden.Seed{seed})
	if queued := queuedSeedBells(t, d, "remote-worker"); len(queued) != 0 {
		t.Fatalf("home queued a bell for a remote tender: %q", queued)
	}
}

type seededNudgeGarden struct {
	d                  *Daemon
	crown, child, leaf protocol.Seed
}

func newSeededNudgeGarden(t *testing.T) seededNudgeGarden {
	t.Helper()
	d := newGardenDaemon(t)
	addGardenSession(t, d, "sess-b")
	addGardenSession(t, d, "sess-c")
	addGardenSession(t, d, "sess-d")
	crown := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "ship seed nudges"})
	child := plant(t, d, protocol.SeedPlantMessage{
		SourceSessionID: protocol.Ptr("sess-a"), Title: "daemon mechanics", PartOf: protocol.Ptr(crown.ID),
	})
	leaf := plant(t, d, protocol.SeedPlantMessage{
		SourceSessionID: protocol.Ptr("sess-a"), Title: "delivery proof", PartOf: protocol.Ptr(child.ID),
	})
	return seededNudgeGarden{d: d, crown: crown, child: child, leaf: leaf}
}

func watchSeed(t *testing.T, d *Daemon, sessionID, seedID string, unwatch bool) *protocol.SeedWatchResult {
	t.Helper()
	msg := protocol.SeedWatchMessage{
		Cmd: protocol.CmdSeedWatch, SourceSessionID: sessionID, SeedID: seedID,
	}
	if unwatch {
		msg.Unwatch = protocol.Ptr(true)
	}
	resp := gardenCall(t, func(c net.Conn) { d.handleSeedWatch(c, &msg) })
	if !resp.Ok {
		t.Fatalf("watch %s from %s: %v", seedID, sessionID, protocol.Deref(resp.Error))
	}
	return resp.SeedWatchResult
}

func queuedSeedBells(t *testing.T, d *Daemon, sessionID string) []string {
	t.Helper()
	messages, err := d.store.UnreadInboxDeliveries(inbox.ToSession(sessionID))
	if err != nil {
		t.Fatalf("queued bells for %s: %v", sessionID, err)
	}
	contents := make([]string, 0, len(messages))
	for _, delivery := range messages {
		contents = append(contents, mailboxItemContent(delivery))
	}
	return contents
}

func assertOneSeedBell(t *testing.T, d *Daemon, sessionID, seedID, event string) {
	t.Helper()
	queued := queuedSeedBells(t, d, sessionID)
	if len(queued) != 1 || !strings.Contains(queued[0], seedID+" moved: "+event) {
		t.Fatalf("queued bells for %s = %q, want one %s/%s doorbell", sessionID, queued, seedID, event)
	}
}

func TestSeedNudges_DispatcherHearsTheDelegatesHarvest(t *testing.T) {
	fixture := newSeededNudgeGarden(t)
	d := fixture.d
	move(t, d, "sess-b", fixture.leaf.ID, garden.VerbTend, "", "")
	if err := d.recordGardenDispatch("sess-b", fixture.leaf.ID, "sess-a", "/tmp/a", "codex", false); err != nil {
		t.Fatalf("record dispatch: %v", err)
	}

	move(t, d, "sess-b", fixture.leaf.ID, garden.VerbHarvest, "proof complete", "")
	assertOneSeedBell(t, d, "sess-a", fixture.leaf.ID, "harvested")
}
