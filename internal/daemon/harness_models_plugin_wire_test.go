package daemon_test

import (
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const houseCatalog = `{"models":[
	{"harness":"house","provider":"first","id":"first/shared","access":"supported","effort_support":"supported","effort_levels":["low"]},
	{"harness":"house","provider":"second","id":"second/shared","access":"unsupported","effort_support":"unknown","detail":"second provider is unavailable"},
	{"harness":"house","provider":"third","id":"third/fixed","access":"supported","effort_support":"unsupported"},
	{"harness":"house","provider":"work","id":"work/custom","family":"claude-haiku-4-5","effort_support":"supported","effort_levels":["low","high"]}
]}`

func housePlugin(t *testing.T, capabilities string) {
	t.Setenv(fakeagent.PlugAgentEnv, "house")
	t.Setenv(fakeagent.PlugCapabilitiesEnv, capabilities)
	t.Setenv(fakeagent.PlugModelsEnv, houseCatalog)
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
			w := newWorld(t, fakeagent.Plug)
			app := w.App()
			awaitHarnessOffered(app, "house")
			found := testworld.Request(app, protocol.HarnessModelsMessage{Cmd: protocol.CmdHarnessModels, Harness: "house", RequestID: "house-models"},
				protocol.EventHarnessModelsResult, func(r protocol.HarnessModelsResultMessage) bool { return r.RequestID == "house-models" })
			if !found.Success || len(found.Models) != row.want {
				t.Fatalf("the house plugin's models = %+v, want %d of them", found, row.want)
			}
			if row.want > 0 && (found.Models[3].Provider != "work" || found.Models[3].Access != protocol.ModelCapabilitySupportUnknown) {
				t.Errorf("a model the plugin did not vouch for is offered as %+v, want its provider kept and its access unknown", found.Models[3])
			}
			if row.want > 0 && (protocol.Deref(found.Models[3].Tier) != protocol.ModelTierLight || protocol.Deref(found.Models[0].Tier) != "") {
				t.Errorf("tiers = %v and %v, want light for the family claude-haiku-4-5 and none for first/shared", protocol.Deref(found.Models[3].Tier), protocol.Deref(found.Models[0].Tier))
			}
		})
	}
}

func TestACrewMembersModelIsCheckedAgainstItsEntryInThePluginCatalog(t *testing.T) {
	housePlugin(t, "initial_prompt,model_pin,effort_pin,model_discovery")
	w := newCrewWorld(t, fakeagent.Plug)
	cli := w.Client()
	awaitHarnessOffered(w.App(), "house")

	setCrew(t, cli, "trellis", protocol.CrewSetMessage{Agent: protocol.Ptr("house"), Model: protocol.Ptr("first/shared"), Effort: protocol.Ptr("low")})
	_, err := cli.CrewSet("trellis", nil, nil, protocol.Ptr("second/shared"), protocol.Ptr(""), nil)
	crewErrorContains(t, err, "second provider is unavailable")
	_, err = cli.CrewSet("trellis", nil, nil, protocol.Ptr("third/fixed"), protocol.Ptr("high"), nil)
	crewErrorContains(t, err, "does not support effort")
	if after := crewRosterMember(t, cli, "trellis"); protocol.Deref(after.Model) != "first/shared" || protocol.Deref(after.Effort) != "low" {
		t.Errorf("the refused choices changed trellis to %s at %s", protocol.Deref(after.Model), protocol.Deref(after.Effort))
	}
}

func TestADelegationRoleLaunchesThePluginCatalogIdExactly(t *testing.T) {
	housePlugin(t, "initial_prompt,model_pin,effort_pin,model_discovery")
	w := newWorld(t, fakeagent.Plug)
	app := w.App()
	awaitHarnessOffered(app, "house")
	roles := buildRole()
	roles.Roles[0].Choices[0].Selection = protocol.DelegationSelection{Harness: "house", Model: "work/custom", Effort: "high"}
	if saved := savePreferences(app, roles); !saved.Success {
		t.Fatalf("saving the build role on the house plugin's work/custom: %s", protocol.Deref(saved.Error))
	}
	cwd := w.Path("api")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	request := brief(cwd, "rename the checkout module")
	request.Role = protocol.Ptr("build")
	result, err := w.Client().Delegate(request)
	if err != nil {
		t.Fatalf("delegating with the build role: %v", err)
	}
	argv := w.Launched(string(result.SessionID)).Argv
	if model, _ := flagValue(argv, "--model"); model != "work/custom" {
		t.Errorf("the house plugin launched on %q, want the catalog id work/custom (argv %q)", model, argv)
	}
}

func TestAPluginCatalogKeepsTheFirstEntryOfARepeatedModelId(t *testing.T) {
	t.Setenv(fakeagent.PlugAgentEnv, "house")
	t.Setenv(fakeagent.PlugCapabilitiesEnv, "model_pin,model_discovery")
	t.Setenv(fakeagent.PlugModelsEnv, `{"models":[
	{"harness":"house","provider":"first","id":"shared","name":"First"},
	{"harness":"house","provider":"second","id":"shared","name":"Second"},
	{"harness":"house","provider":"third","id":"other","name":"Other"}
]}`)
	w := newWorld(t, fakeagent.Plug)
	app := w.App()
	awaitHarnessOffered(app, "house")
	found := testworld.Request(app, protocol.HarnessModelsMessage{Cmd: protocol.CmdHarnessModels, Harness: "house", RequestID: "house-models"},
		protocol.EventHarnessModelsResult, func(r protocol.HarnessModelsResultMessage) bool { return r.RequestID == "house-models" })
	var names []string
	for _, m := range found.Models {
		names = append(names, m.Name)
	}
	if !found.Success || strings.Join(names, ",") != "First,Other" {
		t.Errorf("a catalog listing shared twice answered %+v, want its first shared entry and other", found)
	}
}
