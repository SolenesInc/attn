package daemon_test

import (
	"os"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestADelegatedSessionKeepsShowingItsRoleAfterTheRoleIsRemovedAndTheDaemonRestarts(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	codex := []protocol.DelegationChoice{{ID: "default", Name: "Everyday", Selection: protocol.DelegationSelection{Harness: "codex"}}}
	reviewer := protocol.BuiltinDelegationRoleReviewer
	requestID := uuid.NewString()
	installed := preferencesRequest(app, protocol.DelegationPreferencesSaveMessage{
		Cmd: protocol.CmdDelegationPreferencesSave, RequestID: requestID, InstallWorkflowSkill: protocol.Ptr(true),
		Preferences: protocol.DelegationPreferences{
			Enabled: true, WorkflowSkillEnabled: true,
			Roles: []protocol.DelegationRole{
				{ID: "review", Builtin: &reviewer, Enabled: true, DefaultChoiceID: "default", Choices: codex},
				{ID: "research", Name: "Research partner", Icon: "search", Enabled: true, DefaultChoiceID: "default", Choices: codex},
			},
			Fallback: protocol.DelegationFallback{Selection: protocol.DelegationSelection{Harness: "codex"}},
		},
	}, requestID)
	if !installed.Success {
		t.Fatalf("saving the roles: %s", protocol.Deref(installed.Error))
	}
	cwd := w.Path("api")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	var runs []*fakeagent.Run
	delegate := func(label string, configure func(*protocol.DelegateMessage)) string {
		t.Helper()
		request := brief(cwd, "look into the flaky checkout test")
		request.RequestID = uuid.NewString()
		request.Label = protocol.Ptr(label)
		configure(&request)
		accepted, err := cli.StartDelegation(request)
		if err != nil {
			t.Fatalf("delegating %s: %v", label, err)
		}
		runs = append(runs, w.Launched(accepted.SessionID))
		return accepted.SessionID
	}
	review := delegate("review", func(m *protocol.DelegateMessage) { m.Role = protocol.Ptr("review") })
	research := delegate("research", func(m *protocol.DelegateMessage) { m.Role = protocol.Ptr("research") })
	unmatched := delegate("unmatched", func(m *protocol.DelegateMessage) { m.Fallback = protocol.Ptr(true) })

	want := map[string]*protocol.SessionDelegationRole{
		research:  {Name: "Research partner", Icon: protocol.Ptr("search")},
		unmatched: nil,
	}
	reviewRole := queriedSession(t, cli, review).DelegationRole
	if reviewRole == nil || protocol.Deref(reviewRole.Builtin) != reviewer || reviewRole.Name == "" {
		t.Fatalf("the reviewer session shows role %+v, want the named built-in reviewer", reviewRole)
	}
	want[review] = reviewRole
	for id, role := range want {
		if got := queriedSession(t, cli, id).DelegationRole; !reflect.DeepEqual(got, role) {
			t.Errorf("session %s shows role %+v, want %+v", id, got, role)
		}
	}

	stripped := *installed.Preferences
	stripped.Roles = stripped.Roles[:1]
	stripped.Enabled = false
	if saved := savePreferences(app, stripped); !saved.Success {
		t.Fatalf("removing the research role: %s", protocol.Deref(saved.Error))
	}
	for _, run := range runs {
		run.Exit(0)
	}
	w.restart()
	for id, role := range want {
		if got := queriedSession(t, w.Client(), id).DelegationRole; !reflect.DeepEqual(got, role) {
			t.Errorf("after the restart session %s shows role %+v, want %+v", id, got, role)
		}
	}
}
