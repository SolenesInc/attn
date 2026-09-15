package store

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/protocol"
)

func TestSessionDelegationRolesSurviveRestartAndRoleDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roles.db")
	s, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := s.SaveDelegationPreferences(delegationprefs.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		id   string
		role delegationprefs.Resolved
	}{
		{"orchestrator", delegationprefs.Resolved{Builtin: protocol.Ptr(protocol.BuiltinDelegationRoleOrchestrator), RoleName: "Orchestrator"}},
		{"custom", delegationprefs.Resolved{RoleName: "Research partner", RoleIcon: "search"}},
		{"fallback", delegationprefs.Resolved{}},
		{"closed", delegationprefs.Resolved{RoleName: "Builder"}},
	} {
		if err := s.AddChecked(&protocol.Session{ID: fixture.id, Label: fixture.id, Agent: "codex", Directory: t.TempDir(), State: protocol.SessionStateIdle}); err != nil {
			t.Fatal(err)
		}
		fixture.role.Revision = cfg.Revision
		raw, err := json.Marshal(fixture.role)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.ClaimDelegationOperationWithPreferences("request-"+fixture.id, "op-"+fixture.id, fixture.id, "", "", `{}`, string(raw), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CloseSession("closed", SessionClose{By: SessionClosedByUser}, time.Now()); err != nil {
		t.Fatal(err)
	}
	cfg.Enabled = false
	if _, err := s.SaveDelegationPreferences(cfg); err != nil {
		t.Fatal(err)
	}
	want := map[string]*protocol.SessionDelegationRole{
		"orchestrator": {Name: "Orchestrator", Builtin: protocol.Ptr(protocol.BuiltinDelegationRoleOrchestrator)},
		"custom":       {Name: "Research partner", Icon: protocol.Ptr("search")},
	}
	got, err := s.SessionDelegationRoles()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("before restart: got %+v, error %v; want %+v", got, err, want)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, err = reopened.SessionDelegationRoles()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("after restart: got %+v, error %v; want %+v", got, err, want)
	}
}
