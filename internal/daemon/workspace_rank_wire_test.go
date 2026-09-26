package daemon_test

import (
	"cmp"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAMovedWorkspaceKeepsItsPlaceThroughReRegistrationAndRestart(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	for _, id := range []string{"ws-a", "ws-b", "ws-c"} {
		testworld.Request(app, protocol.RegisterWorkspaceMessage{
			Cmd: protocol.CmdRegisterWorkspace, ID: id, Title: id, Directory: w.Path(id),
		}, protocol.EventWorkspaceRegistered, func(e protocol.WorkspaceRegisteredMessage) bool { return e.Workspace.ID == id })
		testworld.Request(app, protocol.PinWorkspaceMessage{Cmd: protocol.CmdPinWorkspace, WorkspaceID: id, Pinned: true},
			protocol.EventWorkspaceStateChanged, func(e protocol.WorkspaceStateChangedMessage) bool { return e.Workspace.ID == id && e.Workspace.Pinned })
	}
	if got := workspaceRankOrder(t, w); !slices.Equal(got, []string{"ws-a", "ws-b", "ws-c"}) {
		t.Fatalf("registered workspaces are ordered %v, want the order they were registered in", got)
	}

	for _, move := range []struct {
		workspace, after, before string
		want                     []string
	}{
		{"ws-c", "", "ws-a", []string{"ws-c", "ws-a", "ws-b"}},
		{"ws-c", "ws-a", "ws-b", []string{"ws-a", "ws-c", "ws-b"}},
		{"ws-a", "ws-b", "", []string{"ws-c", "ws-b", "ws-a"}},
	} {
		msg := protocol.SetWorkspaceRankMessage{Cmd: protocol.CmdSetWorkspaceRank, WorkspaceID: move.workspace}
		if move.after != "" {
			msg.PrevWorkspaceID = protocol.Ptr(move.after)
		}
		if move.before != "" {
			msg.NextWorkspaceID = protocol.Ptr(move.before)
		}
		result := testworld.Request(app, msg, protocol.EventWorkspaceLayoutActionResult, func(r protocol.WorkspaceLayoutActionResultMessage) bool {
			return r.Action == protocol.CmdSetWorkspaceRank && r.WorkspaceID == move.workspace
		})
		if !result.Success {
			t.Fatalf("moving %s between %q and %q was refused: %s", move.workspace, move.after, move.before, protocol.Deref(result.Error))
		}
		if got := workspaceRankOrder(t, w); !slices.Equal(got, move.want) {
			t.Fatalf("after moving %s between %q and %q the order is %v, want %v", move.workspace, move.after, move.before, got, move.want)
		}
	}

	testworld.Request(app, protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "ws-a", Title: "ws-a", Directory: w.Path("ws-a"),
	}, protocol.EventWorkspaceStateChanged, func(e protocol.WorkspaceStateChangedMessage) bool { return e.Workspace.ID == "ws-a" })
	if got := workspaceRankOrder(t, w); !slices.Equal(got, []string{"ws-c", "ws-b", "ws-a"}) {
		t.Fatalf("after ws-a registered again the order is %v, want it kept last", got)
	}

	w.restart()
	if got := workspaceRankOrder(t, w); !slices.Equal(got, []string{"ws-c", "ws-b", "ws-a"}) {
		t.Fatalf("after a restart the order is %v, want the moved order kept", got)
	}
}

func workspaceRankOrder(t *testing.T, w *world) []string {
	t.Helper()
	listed, err := w.Client().List("")
	if err != nil {
		t.Fatalf("list workspaces: %v", err)
	}
	workspaces := slices.Clone(listed.Workspaces)
	slices.SortFunc(workspaces, func(a, b protocol.Workspace) int {
		return cmp.Or(cmp.Compare(a.Rank, b.Rank), cmp.Compare(a.ID, b.ID))
	})
	ids := make([]string, 0, len(workspaces))
	for _, ws := range workspaces {
		ids = append(ids, ws.ID)
	}
	return ids
}
