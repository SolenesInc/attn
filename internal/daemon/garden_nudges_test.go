package daemon

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/protocol"
)

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
	crown := plant(t, d, protocol.SeedPlantMessage{Body: protocol.Ptr("Complete the assigned work."), SourceSessionID: protocol.Ptr(protocol.SessionID("sess-a")), Title: "ship seed nudges"})
	child := plant(t, d, protocol.SeedPlantMessage{Body: protocol.Ptr("Complete the assigned work."),
		SourceSessionID: protocol.Ptr(protocol.SessionID("sess-a")), Title: "daemon mechanics", PartOf: protocol.Ptr(crown.ID),
	})
	leaf := plant(t, d, protocol.SeedPlantMessage{Body: protocol.Ptr("Complete the assigned work."),
		SourceSessionID: protocol.Ptr(protocol.SessionID("sess-a")), Title: "delivery proof", PartOf: protocol.Ptr(child.ID),
	})
	return seededNudgeGarden{d: d, crown: crown, child: child, leaf: leaf}
}

func queuedSeedBells(t *testing.T, d *Daemon, sessionID string) []string {
	t.Helper()
	messages, err := d.store.UnreadInboxDeliveries(inbox.ToSession(protocol.SessionID(sessionID)))
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
