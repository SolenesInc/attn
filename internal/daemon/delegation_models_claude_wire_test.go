package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestClaudeOffersTheModelsItsCLIReportsWithTheirEffortLevels(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	found := testworld.Request(app, protocol.DelegationModelsMessage{Cmd: protocol.CmdDelegationModels, Harness: "claude", RequestID: "claude-models"},
		protocol.EventDelegationModelsResult, func(r protocol.DelegationModelsResultMessage) bool { return r.RequestID == "claude-models" })
	if !found.Success || len(found.Models) != len(fakeagent.ClaudeModels) {
		t.Fatalf("claude's models = %+v, want the %d its CLI reports", found, len(fakeagent.ClaudeModels))
	}
	for _, reported := range fakeagent.ClaudeModels {
		i := slices.IndexFunc(found.Models, func(m protocol.DelegationModel) bool { return m.ID == reported.Value })
		if i < 0 {
			t.Errorf("claude's %s is not offered", reported.Value)
			continue
		}
		offered := found.Models[i]
		support := protocol.ModelCapabilitySupportUnsupported
		if reported.SupportsEffort {
			support = protocol.ModelCapabilitySupportSupported
		}
		if offered.EffortSupport != support || !slices.Equal(offered.EffortLevels, reported.SupportedEffortLevels) {
			t.Errorf("claude's %s is offered as %+v, want effort %s %v", reported.Value, offered, support, reported.SupportedEffortLevels)
		}
	}
}
