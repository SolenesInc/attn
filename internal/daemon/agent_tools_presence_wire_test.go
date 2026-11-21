package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestThePresenceToolNamesTheCallersSessionAndTheSeedsItTends(t *testing.T) {
	s := newSessionTools(t)
	var planted protocol.SeedPlantResult
	structuredAs(t, s.agent.mustCall(t, "seed_plant", map[string]any{"title": "Prune the merged worktrees"}), &planted)
	s.agent.mustCall(t, "seed_tend", map[string]any{"seed": planted.ID})
	var present protocol.PresenceResult
	structuredAs(t, s.shapes.call("presence", map[string]any{}), &present)
	if present.SessionID != protocol.SessionID(s.session) || len(present.SeedIds) != 1 || present.SeedIds[0] != planted.ID {
		t.Errorf("presence answered %+v, want session %s tending %s", present, s.session, planted.ID)
	}
	s.shapes.finish("presence")
}

func TestThePresenceToolNamesNoSeedOfAnotherProfile(t *testing.T) {
	p := newTwoProfileTools(t)
	var planted protocol.SeedPlantResult
	structuredAs(t, p.theirs.mustCall(t, "seed_plant", map[string]any{"title": "The side profile's work"}), &planted)
	p.theirs.mustCall(t, "seed_tend", map[string]any{"seed": planted.ID})
	var present protocol.PresenceResult
	structuredAs(t, p.mine.mustCall(t, "presence", map[string]any{}), &present)
	if present.SessionID != protocol.SessionID(p.here) || len(present.SeedIds) != 0 {
		t.Errorf("presence through /mcp answered %+v, want only session %s with no seed", present, p.here)
	}
}
