package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/protocol"
)

func TestBroadcastPreservesAcceptedDelegationRole(t *testing.T) {
	d := newCrewDaemon(t)
	addSession(t, d, "role-session")
	addSession(t, d, "roleless-session")
	cfg, err := d.store.SaveDelegationPreferences(delegationprefs.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(delegationprefs.Resolved{Revision: cfg.Revision, RoleName: "Orchestrator", Builtin: protocol.Ptr(protocol.BuiltinDelegationRoleOrchestrator)})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.store.ClaimDelegationOperationWithPreferences("request-role", "op-role", "role-session", "", "", `{}`, string(raw), time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, session := range append(d.sessionsForBroadcast(d.store.List("")), *d.sessionForBroadcast(d.store.Get("role-session"))) {
		if session.ID == "role-session" {
			if session.DelegationRole == nil || session.DelegationRole.Name != "Orchestrator" || protocol.Deref(session.DelegationRole.Builtin) != protocol.BuiltinDelegationRoleOrchestrator {
				t.Fatalf("role missing from broadcast: %+v", session.DelegationRole)
			}
		} else if session.DelegationRole != nil {
			t.Fatalf("role invented for %s", session.ID)
		}
	}
	if d.store.Get("role-session").DelegationRole != nil {
		t.Fatal("broadcast decoration changed stored session")
	}
}
