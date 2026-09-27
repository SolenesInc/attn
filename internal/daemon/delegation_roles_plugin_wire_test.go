package daemon_test

import (
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAHarnessWithoutAWorkflowSkillDirectoryIsRefusedOnlyForAttnRoles(t *testing.T) {
	t.Setenv(fakeagent.PiAgentEnv, "house")
	w := newWorld(t, fakeagent.Pi)
	app, cli := w.App(), w.Client()
	pluginDriverSettings(app, "house")
	onHouse := protocol.DelegationSelection{Harness: "house"}
	attnRole := loadDelegationPreferences(app).Templates[0]
	attnRole.Enabled = true
	for i := range attnRole.Choices {
		attnRole.Choices[i].Selection = onHouse
	}
	saved := addAttnDelegationRoles(app, protocol.DelegationPreferences{
		Enabled: true, WorkflowSkillEnabled: true,
		Roles: []protocol.DelegationRole{
			{ID: "build", Name: "Builder", Enabled: true, Instructions: "Check it carefully", StoppingPoint: "Stop once the tests pass",
				DefaultChoiceID: "default", Choices: []protocol.DelegationChoice{{ID: "default", Name: "Everyday", Selection: onHouse}}},
			attnRole,
		},
		Fallback: protocol.DelegationFallback{Selection: onHouse},
	})
	if !saved.Success {
		t.Fatalf("saving the roles: %s", protocol.Deref(saved.Error))
	}
	cwd := w.Path("api")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, row := range []struct {
		name, role string
		fallback   bool
		refusal    string
	}{
		{name: "the user's own role", role: "build"},
		{name: "the fallback", fallback: true},
		{name: "an attn role", role: attnRole.ID, refusal: `harness "house" has no supported attn-workflow skill directory`},
	} {
		request := brief(cwd, "Implement the discount field")
		request.Label = protocol.Ptr(strings.ReplaceAll(row.name, " ", "-"))
		if row.role != "" {
			request.Role = protocol.Ptr(row.role)
		}
		if row.fallback {
			request.Fallback = protocol.Ptr(true)
		}
		result, err := cli.Delegate(request)
		if row.refusal == "" && err != nil {
			t.Errorf("delegating in %s to the house plugin: %v", row.name, err)
		}
		if row.refusal != "" && (err == nil || !strings.Contains(err.Error(), row.refusal)) {
			t.Errorf("delegating in %s to the house plugin = %+v, %v; want the refusal %q", row.name, result, err, row.refusal)
		}
	}
}
