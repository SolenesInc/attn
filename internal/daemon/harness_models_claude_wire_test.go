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
	found := testworld.Request(app, protocol.HarnessModelsMessage{Cmd: protocol.CmdHarnessModels, Harness: "claude", RequestID: "claude-models"},
		protocol.EventHarnessModelsResult, func(r protocol.HarnessModelsResultMessage) bool { return r.RequestID == "claude-models" })
	if !found.Success || len(found.Models) != len(fakeagent.ClaudeModels) {
		t.Fatalf("claude's models = %+v, want the %d its CLI reports", found, len(fakeagent.ClaudeModels))
	}
	for index, reported := range fakeagent.ClaudeModels {
		if found.Models[index].ID != reported.Value {
			t.Errorf("model at position %d = %s, want %s in harness reported order", index, found.Models[index].ID, reported.Value)
		}
		i := slices.IndexFunc(found.Models, func(m protocol.HarnessModel) bool { return m.ID == reported.Value })
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
