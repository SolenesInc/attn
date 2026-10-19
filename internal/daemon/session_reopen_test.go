package daemon

import (
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/launchcontract"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

type reopenSession struct {
	ID        string
	Directory string
	Branch    string
	Repo      string
	Agent     string
	Resume    string
	ClosedBy  string
	Reason    string
	NoIntent  bool
	Intent    *store.LaunchIntent
	ProfileID string
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

	closedBy := session.ClosedBy
	if closedBy == "" {
		closedBy = store.SessionClosedByUser
	}
	d.closeSession(protocol.SessionID(session.ID), store.SessionClose{By: closedBy, Reason: session.Reason})
	if !d.store.SessionClosed(protocol.SessionID(session.ID)) {
		t.Fatalf("session %s did not close into the ledger", session.ID)
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
