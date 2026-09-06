package daemon

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func TestOrdinaryTenderSavesOneExecutionForEverySeedItTends(t *testing.T) {
	d := newGardenDaemon(t)
	cwd := t.TempDir()
	now := string(protocol.TimestampNow())
	d.store.Add(&protocol.Session{
		ID: "sess-a", Label: "ordinary worker", Agent: protocol.SessionAgentClaude,
		Directory: cwd, State: protocol.SessionStateIdle,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})

	plot := plant(t, d, protocol.SeedPlantMessage{Title: "the plot"})
	first := plant(t, d, protocol.SeedPlantMessage{Title: "first", PartOf: protocol.Ptr(plot.ID)})
	second := plant(t, d, protocol.SeedPlantMessage{Title: "second", PartOf: protocol.Ptr(plot.ID)})
	move(t, d, "sess-a", first.ID, garden.VerbTend, "", "")
	move(t, d, "sess-a", second.ID, garden.VerbTend, "", "")

	for _, seedID := range []string{first.ID, second.ID} {
		seed, _, err := d.readSeed(seedID)
		if err != nil {
			t.Fatal(err)
		}
		if seed.LastExecutionID != "sess-a" {
			t.Fatalf("%s last execution = %q, want sess-a", seedID, seed.LastExecutionID)
		}
		continuation := d.continuationForSeed(seed)
		if continuation == nil || continuation.Execution.Cwd != cwd || continuation.Execution.Agent != "claude" {
			t.Fatalf("%s continuation = %+v", seedID, continuation)
		}
	}
}

func TestSeedStateClockMovesOnlyWithLifecycle(t *testing.T) {
	d := newGardenDaemon(t)
	at := time.Date(2026, 8, 29, 8, 0, 0, 0, time.UTC)
	d.gardenNow = func() time.Time { return at }
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "clocked work", Body: protocol.Ptr("first body")})
	wantPlanted := formatGardenTime(at)
	if seed.StateChangedAt != wantPlanted || !seed.StateChangedAtExact {
		t.Fatalf("planted clock = %q exact=%v, want %q exact", seed.StateChangedAt, seed.StateChangedAtExact, wantPlanted)
	}

	at = at.Add(24 * time.Hour)
	edited := editSeed(t, d, seed.ID, "a newer body")
	if edited.StateChangedAt != wantPlanted {
		t.Fatalf("body edit moved lifecycle clock to %q", edited.StateChangedAt)
	}

	at = at.Add(time.Hour)
	claimed := move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "")
	if claimed.StateChangedAt != formatGardenTime(at) || !claimed.StateChangedAtExact {
		t.Fatalf("tend clock = %q exact=%v", claimed.StateChangedAt, claimed.StateChangedAtExact)
	}
}

func TestLegacySeedUsesItsDocumentUpdateAsConservativeStateEvidence(t *testing.T) {
	updated := time.Date(2026, 8, 22, 14, 30, 0, 0, time.UTC)
	wire := seedToProtocol(garden.Seed{ID: "s-legacy", Title: "old work", Status: garden.StatusPlanted}, docstore.Document{
		ID: "s-legacy", Rev: 4, CreatedAt: updated.Add(-time.Hour), UpdatedAt: updated,
	}, false)
	if wire.StateChangedAt != formatGardenTime(updated) || wire.StateChangedAtExact {
		t.Fatalf("legacy state evidence = %q exact=%v", wire.StateChangedAt, wire.StateChangedAtExact)
	}
}

func TestObservedExecutionDistinguishesLocalNonGitAndRemoteHosts(t *testing.T) {
	localDir := t.TempDir()
	local := observedGardenExecution(&protocol.Session{
		ID: "local", Directory: localDir, Agent: protocol.SessionAgentCopilot,
	}, "native-local", time.Now())
	if local.HostKind != garden.HostLocal || local.Cwd != localDir || local.Agent != "copilot" ||
		local.RepositoryRoot != "" || local.Branch != "" {
		t.Fatalf("local non-Git execution = %+v", local)
	}

	remote := observedGardenExecution(&protocol.Session{
		ID: "remote", Directory: "/srv/work", Agent: protocol.SessionAgentClaude,
		EndpointID: protocol.Ptr("outpost-a"), MainRepo: protocol.Ptr("/srv/repo"), Branch: protocol.Ptr("feature/a"),
	}, "native-remote", time.Now())
	if remote.HostKind != garden.HostRemote || remote.EndpointID != "outpost-a" ||
		remote.RepositoryRoot != "/srv/repo" || remote.Branch != "feature/a" {
		t.Fatalf("remote execution = %+v", remote)
	}
}

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
		ID: "sess-snipe", Label: "plugin worker", Agent: "snipe",
		Directory: cwd, State: protocol.SessionStateIdle,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "plugin work"})
	move(t, d, "sess-snipe", seed.ID, garden.VerbTend, "", "")
	d.persistResumeSessionID("sess-snipe", "snipe-conv-3")
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
