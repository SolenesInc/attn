package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestChangingAMembersHarnessClearsItsModelAndEffortPins(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude, fakeagent.Codex)
	cli := w.Client()
	setCrew(t, cli, "keel", protocol.CrewSetMessage{Model: protocol.Ptr("claude-sonnet-fake"), Effort: protocol.Ptr("high")})

	switched := setCrew(t, cli, "keel", protocol.CrewSetMessage{Agent: protocol.Ptr("codex")})
	if protocol.Deref(switched.Agent) != "codex" || switched.Model != nil || switched.Effort != nil {
		t.Fatalf("switching harness left agent %q model %q effort %q, want codex with both pins cleared", protocol.Deref(switched.Agent), protocol.Deref(switched.Model), protocol.Deref(switched.Effort))
	}

}

func TestAMembersCustomModelSurvivesDiscoveryAndAnEffortTheModelLacksIsRefused(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	cli := w.Client()

	custom := setCrew(t, cli, "alder", protocol.CrewSetMessage{Model: protocol.Ptr("private-model"), Effort: protocol.Ptr("high")})
	if protocol.Deref(custom.Model) != "private-model" || protocol.Deref(custom.Effort) != "high" {
		t.Fatalf("a model discovery does not list = model %q effort %q, want it kept as set", protocol.Deref(custom.Model), protocol.Deref(custom.Effort))
	}
	_, err := cli.CrewSet("alder", nil, nil, protocol.Ptr("claude-sonnet-fake"), protocol.Ptr("max"), nil)
	crewErrorContains(t, err, `does not support effort "max"`)
	if after := crewRosterMember(t, cli, "alder"); after.Revision != custom.Revision || protocol.Deref(after.Model) != "private-model" || protocol.Deref(after.Effort) != "high" {
		t.Fatalf("a refused model and effort changed alder: %+v", after)
	}
}
