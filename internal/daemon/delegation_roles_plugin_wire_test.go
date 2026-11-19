package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAHarnessWithoutAWorkflowSkillDirectoryIsRefusedOnlyForAttnRoles(t *testing.T) {
	t.Setenv(fakeagent.PlugAgentEnv, "house")
	w, attnRole, cwd := houseWithAttnRoles(t)
	cli := w.Client()
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

func houseWithAttnRoles(t *testing.T) (*world, protocol.DelegationRole, string) {
	t.Helper()
	w := newWorld(t, fakeagent.Plug)
	app := w.App()
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
			{ID: "build", Name: "Builder", Enabled: true, Instructions: "Check it carefully",
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

	return w, attnRole, cwd
}

func TestAnAttnRoleOnAPluginHarnessGetsTheWorkflowSkillWhereThePluginSaysItsHarnessLooks(t *testing.T) {
	t.Setenv(fakeagent.PlugAgentEnv, "house")
	serverHome := filepath.Join(t.TempDir(), "house-home")
	t.Setenv("HOUSE_HOME", serverHome)
	providerHome := filepath.Join(t.TempDir(), "house-provider")
	t.Setenv(fakeagent.PlugSkillDirsEnv, `[
		{"home_env": "HOUSE_HOME", "default_home": ".house"},
		{"home_env": "HOUSE_UNSET_CLAUDE_CONFIG", "default_home": ".claude"},
		{"default_home": "`+providerHome+`"}
	]`)
	w, attnRole, cwd := houseWithAttnRoles(t)
	skills := []string{
		filepath.Join(serverHome, "skills", "attn-workflow", "SKILL.md"),
		filepath.Join(os.Getenv("ATTN_TOOL_HOME"), ".claude", "skills", "attn-workflow", "SKILL.md"),
		filepath.Join(providerHome, "skills", "attn-workflow", "SKILL.md"),
	}
	removeSkills := func() {
		for _, skill := range skills {
			if err := os.RemoveAll(filepath.Dir(skill)); err != nil {
				t.Fatal(err)
			}
		}
	}
	expectSkills := func(when string) {
		for _, skill := range skills {
			if _, err := os.Stat(skill); err != nil {
				t.Errorf("%s, attn-workflow is missing where the plugin declared: %v", when, err)
			}
		}
	}

	removeSkills()
	request := brief(cwd, "Implement the discount field")
	request.Role = protocol.Ptr(attnRole.ID)
	if result, err := w.Client().Delegate(request); err != nil {
		t.Fatalf("delegating in an attn role to the house plugin = %+v, %v", result, err)
	}
	expectSkills("after the attn role launched")

	removeSkills()
	w.restart()
	pluginDriverSettings(w.App(), "house")
	expectSkills("after the daemon restarted and the plugin registered again")
}
