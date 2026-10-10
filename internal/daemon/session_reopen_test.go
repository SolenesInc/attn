package daemon

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/hub"
	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/who"
)

func actionNames(actions []protocol.SessionReopenAction) []string {
	names := make([]string, 0, len(actions))
	for _, action := range actions {
		names = append(names, string(action))
	}
	return names
}

func wantReopenVerdict(
	t *testing.T, verdict *sessionReopenVerdict, reopenable bool, actions []protocol.SessionReopenAction,
) {
	t.Helper()
	if verdict.Reopenable != reopenable {
		t.Errorf("reopenable = %v, want %v (reason %q)", verdict.Reopenable, reopenable, verdict.Reason)
	}
	if !slices.Equal(actionNames(verdict.Actions), actionNames(actions)) {
		t.Errorf("actions = %v, want %v", actionNames(verdict.Actions), actionNames(actions))
	}
	if !reopenable && strings.TrimSpace(verdict.Reason) == "" {
		t.Error("a verdict that refuses a reopen carries no reason; an agent cannot act on that")
	}
}

type reopenSession struct {
	ID         string
	Directory  string
	Branch     string
	Repo       string
	Agent      string
	Resume     string
	ClosedBy   string
	Reason     string
	CostCursor string
	NoIntent   bool
	Intent     *store.LaunchIntent
	ProfileID  string
}

func closeReopenSession(t *testing.T, d *Daemon, session reopenSession) {
	t.Helper()
	now := protocol.TimestampNow().String()
	if session.ProfileID == "" {
		session.ProfileID = defaultProfileID(t, d.store)
	}
	entry := &protocol.Session{
		ID: protocol.SessionID(session.ID), Label: session.ID,
		Agent:     protocol.SessionAgent(session.Agent),
		Directory: session.Directory, ProfileID: session.ProfileID,
		State:      protocol.SessionStateIdle,
		StateSince: now, StateUpdatedAt: now, LastSeen: now,
	}
	if session.Branch != "" {
		entry.Branch = protocol.Ptr(session.Branch)
	}
	if session.Repo != "" {
		entry.IsWorktree = protocol.Ptr(true)
		entry.MainRepo = protocol.Ptr(session.Repo)
	}
	d.store.Add(entry)
	if !session.NoIntent {
		intent := store.LaunchIntent{ApprovalRoute: launchcontract.ApprovalRouteUser}
		if session.Intent != nil {
			intent = *session.Intent
		}
		d.store.SetLaunchIntent(protocol.SessionID(session.ID), intent)
	}
	if session.Resume != "" {
		d.persistResumeSessionID(protocol.SessionID(session.ID), session.Resume)
	}
	if session.CostCursor != "" {
		if err := d.store.SetSessionCostCursor(protocol.SessionID(session.ID), session.CostCursor); err != nil {
			t.Fatalf("set the cost cursor of %s: %v", session.ID, err)
		}
	}
	closedBy := who.User()
	if session.ClosedBy != "" {
		var err error
		closedBy, err = who.ParseActor("session:" + session.ClosedBy)
		if err != nil {
			t.Fatal(err)
		}
	}
	d.closeSession(protocol.SessionID(session.ID), store.SessionClose{By: closedBy, Reason: session.Reason})
	if !d.store.SessionClosed(protocol.SessionID(session.ID)) {
		t.Fatalf("session %s did not close into the ledger", session.ID)
	}
}

func TestReopenVerdictRefusesEveryRemoteSessionWithTheReleaseReason(t *testing.T) {
	endpoints := []protocol.EndpointInfo{
		{ID: "outpost-7", Name: "big-linux", Status: hub.StatusUnsupported},
	}
	cases := map[string]struct {
		endpointID string
		wantHost   string
	}{
		"saved":   {endpointID: "outpost-7", wantHost: "big-linux"},
		"removed": {endpointID: "outpost-nobody-configured", wantHost: "outpost-nobody-configured"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			verdict := &sessionReopenVerdict{
				SessionID: "remote-one",
				Execution: garden.Execution{HostKind: garden.HostRemote, EndpointID: tc.endpointID},
			}
			if decideReopenHost(verdict, endpoints) {
				t.Fatal("a remote session went on being decided on this daemon")
			}
			wantReopenVerdict(t, verdict, false, nil)
			if !strings.Contains(verdict.Reason, tc.wantHost) || !strings.Contains(verdict.Reason, hub.UnsupportedReason) {
				t.Errorf("reason = %q, want %s named with the release reason", verdict.Reason, tc.wantHost)
			}
			if strings.Contains(verdict.Reason, "retry") {
				t.Errorf("reason = %q offers a retry that cannot succeed", verdict.Reason)
			}
		})
	}
}

func TestReopenVerdictLandsInTheRecordedProfile(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-profile")
	closeReopenSession(t, d, reopenSession{
		ID: "in-profile", Directory: t.TempDir(), Agent: "codex", Resume: "conv-profile",
	})

	verdict := decidedReopenVerdict(t, d, "in-profile")
	if verdict.ProfileID != defaultProfileID(t, d.store) || verdict.ProfileDeleted {
		t.Errorf("profile = %q (deleted %v), want the recorded default profile", verdict.ProfileID, verdict.ProfileDeleted)
	}
}

func TestReopenVerdictNamesADeletedProfile(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "attn.sock"))
	writeCodexRolloutFixture(t, "conv-gone-profile")
	work := createTestProfile(t, d.store, "Work")
	closeReopenSession(t, d, reopenSession{
		ID: "gone-profile", Directory: t.TempDir(), Agent: "codex", Resume: "conv-gone-profile", ProfileID: work.ID,
	})
	deleteTestProfile(t, d.store, work.ID)

	verdict := decidedReopenVerdict(t, d, "gone-profile")
	if verdict.ProfileID != work.ID || !verdict.ProfileDeleted {
		t.Errorf("profile = %q (deleted %v), want the deleted Work profile named", verdict.ProfileID, verdict.ProfileDeleted)
	}
}
