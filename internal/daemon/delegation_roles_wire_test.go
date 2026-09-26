package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func loadDelegationPreferences(app *testworld.Peer) protocol.DelegationPreferencesResultMessage {
	app.T.Helper()
	id := uuid.NewString()
	return preferencesRequest(app, protocol.DelegationPreferencesGetMessage{Cmd: protocol.CmdDelegationPreferencesGet, RequestID: id}, id)
}

func addAttnDelegationRoles(app *testworld.Peer, preferences protocol.DelegationPreferences) protocol.DelegationPreferencesResultMessage {
	app.T.Helper()
	id := uuid.NewString()
	return preferencesRequest(app, protocol.DelegationPreferencesSaveMessage{
		Cmd: protocol.CmdDelegationPreferencesSave, RequestID: id, Preferences: preferences, InstallWorkflowSkill: protocol.Ptr(true),
	}, id)
}

func delegationWorkflowSkillIn(toolHome, harnessDir string) string {
	return filepath.Join(toolHome, harnessDir, "skills", "attn-workflow")
}

func TestAddingAttnRolesInstallsTheWorkflowSkillBeforeSavingThem(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	toolHome := os.Getenv("ATTN_TOOL_HOME")
	loaded := loadDelegationPreferences(app)
	if !loaded.Success || loaded.Preferences.Enabled || len(loaded.Preferences.Roles) != 0 {
		t.Fatalf("fresh delegation preferences = %+v; want them off with no roles", loaded)
	}
	var templates []string
	for _, template := range loaded.Templates {
		templates = append(templates, template.ID)
	}
	for _, want := range []string{"pathfinder", "builder", "reviewer", "orchestrator"} {
		if !slices.Contains(templates, want) {
			t.Errorf("the offered templates %v lack %s", templates, want)
		}
	}
	withAttnRoles := *loaded.Preferences
	withAttnRoles.WorkflowSkillEnabled = true
	withAttnRoles.Roles = loaded.Templates

	blocked := delegationWorkflowSkillIn(toolHome, ".claude")
	if err := os.MkdirAll(filepath.Dir(blocked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}
	partial := addAttnDelegationRoles(app, withAttnRoles)
	if partial.Success || !slices.Contains(partial.WorkflowSkillPaths, delegationWorkflowSkillIn(toolHome, ".agents")) || !slices.Contains(partial.WorkflowSkillPaths, blocked) {
		t.Fatalf("adding attn roles past a blocked skill directory = %+v; want a refusal naming every path", partial)
	}
	if after := savedPreferences(app); after.WorkflowSkillEnabled || len(after.Roles) != 0 || after.Revision != 0 {
		t.Fatalf("the refused install saved %+v", after)
	}

	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	installed := addAttnDelegationRoles(app, withAttnRoles)
	if !installed.Success || !installed.Preferences.WorkflowSkillEnabled || len(installed.Preferences.Roles) != len(loaded.Templates) {
		t.Fatalf("adding attn roles = %+v", installed)
	}
	for _, path := range installed.WorkflowSkillPaths {
		if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
			t.Errorf("the reported skill at %s is not there: %v", path, err)
		}
	}
	if !slices.Contains(installed.WorkflowSkillPaths, blocked) {
		t.Errorf("the install reports %v; want the claude skill directory too", installed.WorkflowSkillPaths)
	}
	for _, role := range installed.Preferences.Roles {
		if role.Builtin == nil || role.Name != "" || role.Instructions != "" {
			t.Errorf("a saved attn role copied its maintained guidance: %+v", role)
		}
	}

	roles, err := w.Client().DelegationRoles()
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range roles.Roles {
		if role.Builtin == nil || role.Name == "" || role.Description == "" || role.Instructions == "" || role.StoppingPoint == "" {
			t.Errorf("agents are offered attn role %+v without its maintained guidance", role)
		}
	}
}

func TestADelegateLaunchedInARoleGetsOnlyThatRolesGuidance(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	toolHome := os.Getenv("ATTN_TOOL_HOME")
	everyday := []protocol.DelegationChoice{{ID: "default", Name: "Everyday", Selection: protocol.DelegationSelection{Harness: "codex"}}}
	preferences := protocol.DelegationPreferences{
		Enabled: true, WorkflowSkillEnabled: true,
		Roles: []protocol.DelegationRole{
			{ID: "build", Name: "Builder", Enabled: true, Instructions: "Check {{literal}} carefully", StoppingPoint: "Stop once the tests pass",
				DefaultChoiceID: "default", Choices: everyday},
			{ID: "inspect", Name: "Inspector", Enabled: true, Instructions: "Inspect every corner", StoppingPoint: "Stop at the report",
				DefaultChoiceID: "default", Choices: everyday},
		},
		Fallback: protocol.DelegationFallback{Selection: protocol.DelegationSelection{Harness: "codex"}},
	}
	saved := addAttnDelegationRoles(app, preferences)
	if !saved.Success {
		t.Fatalf("saving the roles: %s", protocol.Deref(saved.Error))
	}
	if offered, err := cli.DelegationRoles(); err != nil || len(offered.Roles) != 2 || offered.Roles[0].Instructions != "Check {{literal}} carefully" {
		t.Fatalf("agents are offered %+v, %v; want the roles with their instructions verbatim", offered, err)
	}
	skills := delegationWorkflowSkillIn(toolHome, ".agents")
	if err := os.RemoveAll(skills); err != nil {
		t.Fatal(err)
	}

	cwd := w.Path("api")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	request := brief(cwd, "Implement the discount field")
	request.Role = protocol.Ptr("build")
	result, err := cli.Delegate(request)
	if err != nil {
		t.Fatal(err)
	}
	launched := w.Launched(result.SessionID)
	prompt := launched.Prompted()
	for _, want := range []string{"Role: Builder", "Check {{literal}} carefully", "Stop once the tests pass", "attn seed show " + result.SeedID} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the delegate's prompt lacks %q: %s", want, prompt)
		}
	}
	for _, leaked := range []string{"Implement the discount field", "--preferences-revision", "Inspector", "Inspect every corner"} {
		if strings.Contains(prompt, leaked) {
			t.Errorf("the delegate's prompt carries %q: %s", leaked, prompt)
		}
	}
	if model, pinned := delegateLaunchFlag(launched.Argv, "--model"); pinned {
		t.Errorf("a role without a model launched codex with --model %q", model)
	}
	if _, err := os.Stat(filepath.Join(skills, "references", "planning.md")); err != nil {
		t.Errorf("the workflow skill was not refreshed before the delegate started: %v", err)
	}

	off := *saved.Preferences
	off.Enabled = false
	if turnedOff := savePreferences(app, off); !turnedOff.Success || turnedOff.Preferences.Fallback.Selection.Harness != "codex" {
		t.Errorf("turning the roles off = %+v; want the fallback kept", turnedOff)
	}
	if offered, err := cli.DelegationRoles(); err != nil || len(offered.Roles) != 0 || offered.Fallback != nil || offered.Guidance != "" {
		t.Errorf("with the roles off agents are offered %+v, %v; want nothing", offered, err)
	}
}
