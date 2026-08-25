package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspacelayout"
)

// addChiefWorkspaceLayout wires a workspace with two agent panes, so the guard is
// exercised against the chief pane while a sibling proves ordinary panes close.
func addChiefWorkspaceLayout(t *testing.T, d *Daemon, workspaceID, chiefSessionID, chiefPaneID, otherSessionID, otherPaneID string) {
	t.Helper()
	d.store.AddWorkspace(&protocol.Workspace{ID: workspaceID, Title: "shared", Directory: "/tmp/" + workspaceID})
	if err := d.store.SaveWorkspaceLayout(workspacelayout.WorkspaceLayout{
		WorkspaceID:  workspaceID,
		ActivePaneID: chiefPaneID,
		Layout: workspacelayout.Node{
			Type:      "split",
			SplitID:   "split-1",
			Direction: workspacelayout.DirectionVertical,
			Ratio:     0.5,
			Children: []workspacelayout.Node{
				{Type: "pane", PaneID: chiefPaneID},
				{Type: "pane", PaneID: otherPaneID},
			},
		},
		Panes: []workspacelayout.Pane{
			{PaneID: chiefPaneID, RuntimeID: chiefSessionID, SessionID: chiefSessionID, Kind: workspacelayout.PaneKindAgent, Title: "Chief"},
			{PaneID: otherPaneID, RuntimeID: otherSessionID, SessionID: otherSessionID, Kind: workspacelayout.PaneKindAgent, Title: "Worker"},
		},
	}); err != nil {
		t.Fatalf("SaveWorkspaceLayout() error = %v", err)
	}
}

func TestHandleUnregisterWS_RefusesChiefOfStaff(t *testing.T) {
	d, client := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "chief", "Chief")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}

	d.handleUnregisterWS(client, &protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: "chief"})

	if d.store.Get("chief") == nil {
		t.Fatal("chief-of-staff session was unregistered despite the close guard")
	}
	if got := d.chiefOfStaffSessionID(); got != "chief" {
		t.Fatalf("chief role after refused close = %q, want chief", got)
	}
	expectCommandError(t, client, protocol.CmdUnregister, chiefOfStaffProtectedError)
}

func TestHandleUnregisterWS_AllowsNonChiefWhileChiefExists(t *testing.T) {
	d, client := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "chief", "Chief")
	addChiefOfStaffTestSession(d, "worker", "Worker")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}

	d.handleUnregisterWS(client, &protocol.UnregisterMessage{Cmd: protocol.CmdUnregister, ID: "worker"})

	if d.store.Get("worker") != nil {
		t.Fatal("ordinary session was not unregistered while a chief existed")
	}
	if d.store.Get("chief") == nil {
		t.Fatal("chief-of-staff session must survive a sibling's close")
	}
}

func TestHandleWorkspaceLayoutClosePane_RefusesChiefOfStaff(t *testing.T) {
	d, client := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "chief", "Chief")
	addChiefOfStaffTestSession(d, "worker", "Worker")
	addChiefWorkspaceLayout(t, d, "workspace-shared", "chief", "pane-chief", "worker", "pane-worker")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}

	d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: "workspace-shared", PaneID: "pane-chief",
	})

	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, "workspace-shared", "pane-chief", false)
	if d.store.Get("chief") == nil {
		t.Fatal("chief-of-staff session was closed via its workspace pane despite the guard")
	}
	layout := d.store.GetWorkspaceLayout("workspace-shared")
	if layout == nil || len(layout.Panes) != 2 {
		t.Fatalf("chief pane was removed from layout: %+v", layout)
	}
}

func TestHandleWorkspaceLayoutClosePane_AllowsNonChiefPane(t *testing.T) {
	d, client := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "chief", "Chief")
	addChiefOfStaffTestSession(d, "worker", "Worker")
	addChiefWorkspaceLayout(t, d, "workspace-shared", "chief", "pane-chief", "worker", "pane-worker")
	if err := d.store.SetProfileRole(profileRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}

	d.handleWorkspaceLayoutClosePane(client, &protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: "workspace-shared", PaneID: "pane-worker",
	})

	expectWorkspaceLayoutActionResult(t, client, protocol.CmdWorkspaceLayoutClosePane, "workspace-shared", "pane-worker", true)
	if d.store.Get("worker") != nil {
		t.Fatal("ordinary pane's session was not closed")
	}
	if d.store.Get("chief") == nil {
		t.Fatal("chief-of-staff session must survive closing a sibling pane")
	}
}
