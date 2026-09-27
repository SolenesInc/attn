package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const houseCatalog = `{"models":[
	{"harness":"house","provider":"first","id":"shared","access":"supported","effort_support":"supported","effort_levels":["low"]},
	{"harness":"house","provider":"second","id":"shared","access":"unsupported","effort_support":"unknown","detail":"second provider is unavailable"},
	{"harness":"house","provider":"third","id":"fixed","access":"supported","effort_support":"unsupported"},
	{"harness":"house","provider":"work","id":"custom","effort_support":"supported","effort_levels":["low","high"]}
]}`

func housePlugin(t *testing.T, capabilities string) {
	t.Setenv(fakeagent.PiAgentEnv, "house")
	t.Setenv(fakeagent.PiCapabilitiesEnv, capabilities)
	t.Setenv(fakeagent.PiModelsEnv, houseCatalog)
}

func TestAPluginDriverOffersTheModelsItDiscoversOnlyWhenItSaysItCan(t *testing.T) {
	for _, row := range []struct {
		capabilities string
		want         int
	}{
		{capabilities: "model_pin,effort_pin,model_discovery", want: 4},
		{capabilities: "model_pin,effort_pin", want: 0},
	} {
		t.Run(row.capabilities, func(t *testing.T) {
			housePlugin(t, row.capabilities)
			w := newWorld(t, fakeagent.Pi)
			app := w.App()
			pluginDriverSettings(app, "house")
			found := testworld.Request(app, protocol.DelegationModelsMessage{Cmd: protocol.CmdDelegationModels, Harness: "house", RequestID: "house-models"},
				protocol.EventDelegationModelsResult, func(r protocol.DelegationModelsResultMessage) bool { return r.RequestID == "house-models" })
			if !found.Success || len(found.Models) != row.want {
				t.Fatalf("the house plugin's models = %+v, want %d of them", found, row.want)
			}
			if row.want > 0 && (found.Models[3].Provider != "work" || found.Models[3].Access != protocol.ModelCapabilitySupportUnknown) {
				t.Errorf("a model the plugin did not vouch for is offered as %+v, want its provider kept and its access unknown", found.Models[3])
			}
		})
	}
}

func TestACrewMembersModelIsCheckedAgainstItsProvidersEntryInThePluginCatalog(t *testing.T) {
	housePlugin(t, "initial_prompt,model_pin,effort_pin,model_discovery")
	w := newCrewWorld(t, fakeagent.Pi)
	cli := w.Client()
	pluginDriverSettings(w.App(), "house")

	setCrew(t, cli, "trellis", protocol.CrewSetMessage{Agent: protocol.Ptr("house"), Model: protocol.Ptr("first/shared"), Effort: protocol.Ptr("low")})
	_, err := cli.CrewSet("trellis", nil, nil, protocol.Ptr("second/shared"), protocol.Ptr(""), nil)
	crewErrorContains(t, err, "second provider is unavailable")
	_, err = cli.CrewSet("trellis", nil, nil, protocol.Ptr("third/fixed"), protocol.Ptr("high"), nil)
	crewErrorContains(t, err, "does not support effort")
	if after := crewRosterMember(t, cli, "trellis"); protocol.Deref(after.Model) != "first/shared" || protocol.Deref(after.Effort) != "low" {
		t.Errorf("the refused choices changed trellis to %s at %s", protocol.Deref(after.Model), protocol.Deref(after.Effort))
	}
}
