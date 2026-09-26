package daemon_test

import (
	"encoding/json"
	"maps"
	"math"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

var workspaceShell = fakeagent.Harness(protocol.SessionAgentShell)

func TestTheAppsPaneOrderRegistersTheSessionAndClosingTheLastPaneRemovesItAll(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	cwd := w.Path("shop")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	registered := testworld.Request(app, protocol.RegisterWorkspaceMessage{
		Cmd: protocol.CmdRegisterWorkspace, ID: "ws-shop", Title: "shop", Directory: cwd,
	}, protocol.EventWorkspaceRegistered, func(e protocol.WorkspaceRegisteredMessage) bool { return e.Workspace.ID == "ws-shop" })
	if registered.Workspace.EndpointID != nil {
		t.Errorf("a local workspace was announced with endpoint %q", *registered.Workspace.EndpointID)
	}

	added := workspaceLayoutAction(app, protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: "ws-shop", SessionID: "session-shell",
		PaneID: protocol.Ptr("pane-shell"), Title: protocol.Ptr("shell"),
	}, protocol.CmdWorkspaceLayoutAddSessionPane, "ws-shop")
	if !added.Success || protocol.Deref(added.PaneID) != "pane-shell" {
		t.Fatalf("adding the pane answered success=%v pane=%q: %s", added.Success, protocol.Deref(added.PaneID), protocol.Deref(added.Error))
	}
	awaitWorkspacePane(app, "ws-shop", "pane-shell", protocol.WorkspaceLayoutPaneStatusSpawning)

	spawned := testworld.Request(app, protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: "session-shell", Agent: string(workspaceShell), Label: protocol.Ptr("shell"),
		Cwd: cwd, WorkspaceID: "ws-shop", Cols: 80, Rows: 24,
	}, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == "session-shell" })
	if !spawned.Success {
		t.Fatalf("spawning the shell failed: %s", protocol.Deref(spawned.Error))
	}
	awaitWorkspacePane(app, "ws-shop", "pane-shell", protocol.WorkspaceLayoutPaneStatusReady)
	session := testworld.Await(app, protocol.EventSessionRegistered, func(e protocol.SessionRegisteredMessage) bool {
		return e.Session.ID == "session-shell"
	}).Session
	if session.WorkspaceID != "ws-shop" || session.State != protocol.SessionStateIdle {
		t.Errorf("the shell registered in workspace %q as %s, want ws-shop and idle", session.WorkspaceID, session.State)
	}
	testworld.Await(app, protocol.EventWorkspaceStateChanged, func(e protocol.WorkspaceStateChangedMessage) bool {
		return e.Workspace.ID == "ws-shop" && e.Workspace.Status == protocol.WorkspaceStatusIdle
	})

	closed := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: "ws-shop", PaneID: "pane-shell",
	}, protocol.CmdWorkspaceLayoutClosePane, "ws-shop")
	if !closed.Success {
		t.Fatalf("closing the pane failed: %s", protocol.Deref(closed.Error))
	}
	testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.SessionUnregisteredMessage) bool { return e.Session.ID == "session-shell" })
	testworld.Await(app, protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool { return e.Workspace.ID == "ws-shop" })
	workIn(t, app, w.Path("after"))
	if got := workspaceEventCount(app, protocol.EventWorkspaceUnregistered, "ws-shop"); got != 1 {
		t.Errorf("the app was told %d times that the workspace went away, want once", got)
	}
	view := w.App().Initial
	if slices.ContainsFunc(view.Sessions, func(s protocol.Session) bool { return s.ID == "session-shell" }) {
		t.Error("the closed shell is still listed")
	}
	if slices.ContainsFunc(view.Workspaces, func(ws protocol.Workspace) bool { return ws.ID == "ws-shop" }) {
		t.Error("the emptied workspace is still listed")
	}
}

func TestASpawnAdoptsThePaneTheAppAddedOrAddsItsOwn(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	cwd := w.Path("shop")
	workIn(t, app, cwd)
	ws := workspaceIDFor(t, w, cwd)

	bare := workspaceSpawnShell(t, app, ws, cwd, "session-bare", "bare shell")
	layout := awaitWorkspacePane(app, ws, "", protocol.WorkspaceLayoutPaneStatusReady)
	if len(layout.Panes) != 1 || protocol.Deref(layout.Panes[0].SessionID) != bare || layout.Panes[0].Title != "bare shell" {
		t.Fatalf("a spawn without a pane left panes %+v, want one ready pane titled after the session", layout.Panes)
	}

	added := workspaceLayoutAction(app, protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: ws, SessionID: "session-adopted",
		PaneID: protocol.Ptr("pane-adopted"), Title: protocol.Ptr("custom title"),
	}, protocol.CmdWorkspaceLayoutAddSessionPane, ws)
	if !added.Success {
		t.Fatalf("adding the pane failed: %s", protocol.Deref(added.Error))
	}
	adopted := workspaceSpawnShell(t, app, ws, cwd, "session-adopted", "different label")
	layout = awaitWorkspacePane(app, ws, "pane-adopted", protocol.WorkspaceLayoutPaneStatusReady)
	if len(layout.Panes) != 2 {
		t.Fatalf("after spawning into the added pane the layout has panes %+v, want the two", layout.Panes)
	}
	pane := workspacePane(layout, "pane-adopted")
	if protocol.Deref(pane.SessionID) != adopted || pane.Title != "custom title" {
		t.Errorf("the adopted pane is %+v, want session %s and the title the app gave it", pane, adopted)
	}
	exitWorkspaceShells(app, bare, adopted)
}

func TestAFailedSpawnMarksItsPaneFailedAndLeavesNothingBehind(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	cwd := w.Path("shop")
	gone := w.Path("gone")

	failed, ws, pane := w.RequestSpawn(app, workspaceShell, cwd, func(m *protocol.SpawnSessionMessage) { m.Cwd = gone })
	if failed.Success {
		t.Fatalf("spawning a shell in the missing %s succeeded", gone)
	}
	layout := awaitWorkspacePane(app, ws, pane, protocol.WorkspaceLayoutPaneStatusFailed)
	if got := protocol.Deref(workspacePane(layout, pane).Error); got == "" {
		t.Error("the failed pane carries no error for the app to show")
	}

	bare := testworld.Request(app, protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: "session-bare", Agent: string(workspaceShell), Cwd: gone, WorkspaceID: ws, Cols: 80, Rows: 24,
	}, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == "session-bare" })
	if bare.Success {
		t.Fatal("a spawn without a pane into a missing directory succeeded")
	}

	existing := workspaceSpawnShell(t, app, ws, cwd, "session-existing", "kept label")
	exitWorkspaceShells(app, existing)
	respawn := testworld.Request(app, protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: existing, Agent: string(workspaceShell), Label: protocol.Ptr("replacement"),
		Cwd: gone, WorkspaceID: ws, Cols: 80, Rows: 24,
	}, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == existing })
	if respawn.Success {
		t.Fatal("respawning into a missing directory succeeded")
	}

	view := w.App().Initial
	for _, s := range view.Sessions {
		if s.ID == failed.ID || s.ID == "session-bare" {
			t.Errorf("the failed spawn left session %s behind", s.ID)
		}
		if s.ID == existing && (s.Label != "kept label" || s.Directory != cwd || s.WorkspaceID != ws) {
			t.Errorf("after a failed respawn the session is %q in %s (workspace %s), want it kept as %q in %s", s.Label, s.Directory, s.WorkspaceID, "kept label", cwd)
		}
	}
	if !slices.ContainsFunc(view.Sessions, func(s protocol.Session) bool { return s.ID == existing }) {
		t.Error("a failed respawn removed the session it was replacing")
	}
	layout = workspaceLayoutNow(t, w, ws)
	if slices.ContainsFunc(layout.Panes, func(p protocol.WorkspaceLayoutPane) bool { return protocol.Deref(p.SessionID) == "session-bare" }) {
		t.Error("the failed spawn without a pane left a pane behind")
	}
}

func TestClosingTheFailedPaneOfTheOnlySpawnRemovesTheWorkspaceAndFreesTheSessionID(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	refused := refuseSpawnLikeTheApp(w, app, workspaceShell, w.Path("shop"), func(m *protocol.SpawnSessionMessage) { m.Cwd = w.Path("gone") })
	retryDir := w.Path("retry")
	workIn(t, app, retryDir)
	retried := workspaceSpawnShell(t, app, workspaceIDFor(t, w, retryDir), retryDir, refused.ID, "retry")
	exitWorkspaceShells(app, retried)
}

func TestSpawnsAndPanesAreRefusedOutsideAKnownWorkspace(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	cwd := w.Path("shop")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		workspace string
		want      string
	}{
		{"", "missing workspace_id"},
		{"ws-never-registered", "unknown workspace"},
	} {
		id := uuid.NewString()
		app.Send(protocol.SpawnSessionMessage{
			Cmd: protocol.CmdSpawnSession, ID: id, Agent: string(workspaceShell), Cwd: cwd, WorkspaceID: c.workspace, Cols: 80, Rows: 24,
		})
		refusal := testworld.Refused(app)
		if protocol.Deref(refusal.Cmd) != protocol.CmdSpawnSession || !strings.Contains(protocol.Deref(refusal.Error), c.want) {
			t.Errorf("a shell spawn into workspace %q was refused with %q for %q, want %q", c.workspace, protocol.Deref(refusal.Error), protocol.Deref(refusal.Cmd), c.want)
		}
	}
	missing := workspaceLayoutAction(app, protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: "ws-never-registered", SessionID: "session-requested",
		PaneID: protocol.Ptr("pane-requested"),
	}, protocol.CmdWorkspaceLayoutAddSessionPane, "ws-never-registered")
	if missing.Success || protocol.Deref(missing.PaneID) != "pane-requested" {
		t.Errorf("adding a pane to an unknown workspace answered success=%v for pane %q, want a failure for pane-requested", missing.Success, protocol.Deref(missing.PaneID))
	}
	if view := w.App().Initial; len(view.Sessions) != 0 || len(view.Workspaces) != 0 {
		t.Errorf("refused requests left sessions %d and workspaces %d behind", len(view.Sessions), len(view.Workspaces))
	}
}

func TestClosingAPaneAnswersAtOnceEvenWhenItsProcessIgnoresSIGTERM(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	cwd := w.Path("shop")
	stubborn, ws, stubbornPane := w.RequestSpawn(app, workspaceShell, cwd)
	remaining := w.Spawn(app, workspaceShell, cwd)
	app.TypeLine(stubborn.ID, "trap '' TERM HUP; echo stubborn-$((6*7))")
	app.AwaitScreen(stubborn.ID, "stubborn-42")

	closed := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: ws, PaneID: stubbornPane,
	}, protocol.CmdWorkspaceLayoutClosePane, ws)
	if !closed.Success {
		t.Fatalf("closing the pane failed: %s", protocol.Deref(closed.Error))
	}
	layout := testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(m protocol.WorkspaceLayoutUpdatedMessage) bool {
		return m.WorkspaceLayout.WorkspaceID == ws && workspacePane(m.WorkspaceLayout, stubbornPane).PaneID == ""
	}).WorkspaceLayout
	if len(layout.Panes) != 1 || protocol.Deref(layout.Panes[0].SessionID) != remaining {
		t.Errorf("after the close the layout has panes %+v, want only %s", layout.Panes, remaining)
	}
	exitWorkspaceShells(app, remaining, stubborn.ID)
}

func TestPanesWhoseSessionDidNotSurviveARestartAreDropped(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	untouched, ws, untouchedPane := w.RequestSpawn(app, fakeagent.Claude, cwd)
	w.Launched(untouched.ID)
	if refused, _, failedPane := w.RequestSpawn(app, fakeagent.Harness("no-such-agent"), cwd, func(m *protocol.SpawnSessionMessage) {
		m.ID = "session-failed"
	}); refused.Success || failedPane != "pane-session-failed" {
		t.Fatalf("spawning an unknown agent answered success=%v in pane %s", refused.Success, failedPane)
	}
	pending := workspaceLayoutAction(app, protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: ws, SessionID: "session-pending", PaneID: protocol.Ptr("pane-pending"),
	}, protocol.CmdWorkspaceLayoutAddSessionPane, ws)
	if !pending.Success {
		t.Fatalf("adding the pane failed: %s", protocol.Deref(pending.Error))
	}

	w.restart()
	layout := workspaceLayoutNow(t, w, ws)
	statuses := map[string]protocol.WorkspaceLayoutPaneStatus{}
	for _, p := range layout.Panes {
		statuses[p.PaneID] = p.Status
	}
	want := map[string]protocol.WorkspaceLayoutPaneStatus{
		"pane-session-failed": protocol.WorkspaceLayoutPaneStatusFailed,
		"pane-pending":        protocol.WorkspaceLayoutPaneStatusSpawning,
	}
	if len(statuses) != len(want) || statuses["pane-session-failed"] != want["pane-session-failed"] || statuses["pane-pending"] != want["pane-pending"] {
		t.Errorf("after the restart the panes are %v, want %v without %s", statuses, want, untouchedPane)
	}
	if leaves := workspaceLayoutTree(t, layout).leafIDs(); slices.Contains(leaves, untouchedPane) {
		t.Errorf("the layout still places %s among %v", untouchedPane, leaves)
	}
}

func TestASplitRatioTheUserSetStaysLockedAsPanesComeAndGoAndAcrossARestart(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	cwd := w.Path("shop")
	first := w.Spawn(app, workspaceShell, cwd)
	second := w.Spawn(app, workspaceShell, cwd)
	ws := workspaceIDFor(t, w, cwd)
	split, _, ok := workspaceLayoutTree(t, workspaceLayoutNow(t, w, ws)).splitHolding("pane-" + second)
	if !ok {
		t.Fatal("two panes share no split")
	}

	for _, c := range []struct {
		split   string
		success bool
	}{
		{"split-that-does-not-exist", false},
		{split.SplitID, true},
	} {
		requestID := uuid.NewString()
		result := testworld.Request(app, protocol.WorkspaceLayoutSetSplitRatioMessage{
			Cmd: protocol.CmdWorkspaceLayoutSetSplitRatio, WorkspaceID: ws, SplitID: c.split, Ratio: 0.3, RequestID: protocol.Ptr(requestID),
		}, protocol.EventWorkspaceLayoutActionResult, func(r protocol.WorkspaceLayoutActionResultMessage) bool {
			return protocol.Deref(r.RequestID) == requestID
		})
		if result.Success != c.success || protocol.Deref(result.SplitID) != c.split {
			t.Fatalf("setting the ratio of %s answered success=%v for split %q, want %v", c.split, result.Success, protocol.Deref(result.SplitID), c.success)
		}
	}

	third := w.Spawn(app, workspaceShell, cwd)
	closed := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: ws, PaneID: "pane-" + third,
	}, protocol.CmdWorkspaceLayoutClosePane, ws)
	if !closed.Success {
		t.Fatalf("closing the third pane failed: %s", protocol.Deref(closed.Error))
	}
	exitWorkspaceShells(app, first, second)
	for _, when := range []string{"after a pane came and went", "after a restart"} {
		if when == "after a restart" {
			w.restart()
		}
		locked, ok := workspaceLayoutTree(t, workspaceLayoutNow(t, w, ws)).split(split.SplitID)
		if !ok || !locked.RatioLocked || locked.RatioMode != "preferred" || math.Abs(locked.Ratio-0.3) > 0.01 {
			t.Errorf("%s the split is %+v (found=%v), want it locked at the preferred 0.3", when, locked, ok)
		}
	}
}

func TestMovingAPaneWithinItsWorkspaceResplitsAroundTheDropTarget(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	first := w.Spawn(app, fakeagent.Claude, cwd)
	second := w.Spawn(app, fakeagent.Claude, cwd)
	w.Launched(first)
	w.Launched(second)
	ws := workspaceIDFor(t, w, cwd)
	before := workspaceLayoutNow(t, w, ws).LayoutJson

	selfDrop := workspaceLayoutAction(app, protocol.WorkspaceLayoutMoveLeafMessage{
		Cmd: protocol.CmdWorkspaceLayoutMoveLeaf, WorkspaceID: ws, LeafID: "pane-" + first, AnchorID: "pane-" + first,
		Edge: protocol.WorkspaceLayoutDockEdgeRight,
	}, protocol.CmdWorkspaceLayoutMoveLeaf, ws)
	if selfDrop.Success {
		t.Fatal("dropping a pane on itself was accepted")
	}
	if after := workspaceLayoutNow(t, w, ws).LayoutJson; after != before {
		t.Fatalf("a refused self-drop changed the layout from %s to %s", before, after)
	}

	moved := workspaceLayoutAction(app, protocol.WorkspaceLayoutMoveLeafMessage{
		Cmd: protocol.CmdWorkspaceLayoutMoveLeaf, WorkspaceID: ws, LeafID: "pane-" + first, AnchorID: "pane-" + second,
		Edge: protocol.WorkspaceLayoutDockEdgeBottom, Ratio: protocol.Ptr(0.5),
	}, protocol.CmdWorkspaceLayoutMoveLeaf, ws)
	if !moved.Success || protocol.Deref(moved.PaneID) != "pane-"+first {
		t.Fatalf("moving the pane answered success=%v for %q: %s", moved.Success, protocol.Deref(moved.PaneID), protocol.Deref(moved.Error))
	}
	updated := workspaceLayoutNow(t, w, ws)
	root := workspaceLayoutTree(t, updated)
	if root.Type != "split" || root.Direction != "horizontal" || !slices.Equal(root.leafIDs(), []string{"pane-" + second, "pane-" + first}) {
		t.Errorf("after dropping %s below %s the layout is %s, want a top/bottom split of the two", first, second, updated.LayoutJson)
	}
	if len(updated.Panes) != 2 {
		t.Errorf("after the move the panes are %+v, want both sessions kept", updated.Panes)
	}
}

func TestMovingAPaneToAnotherWorkspaceTakesItsSessionAlong(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	source := w.Spawn(app, fakeagent.Claude, w.Path("api"))
	target := w.Spawn(app, fakeagent.Claude, w.Path("web"))
	w.Launched(source)
	w.Launched(target)
	sourceWS, targetWS := workspaceIDFor(t, w, w.Path("api")), workspaceIDFor(t, w, w.Path("web"))
	from := len(app.Received())

	moved := workspaceLayoutAction(app, protocol.WorkspaceLayoutMoveLeafToWorkspaceMessage{
		Cmd: protocol.CmdWorkspaceLayoutMoveLeafToWorkspace, SourceWorkspaceID: sourceWS, TargetWorkspaceID: targetWS,
		LeafID: "pane-" + source, AnchorID: protocol.Ptr("pane-" + target), Edge: protocol.WorkspaceLayoutDockEdgeRight, Ratio: protocol.Ptr(0.4),
	}, protocol.CmdWorkspaceLayoutMoveLeafToWorkspace, sourceWS)
	if !moved.Success || protocol.Deref(moved.SourceWorkspaceID) != sourceWS || protocol.Deref(moved.TargetWorkspaceID) != targetWS ||
		protocol.Deref(moved.LeafID) != "pane-"+source || protocol.Deref(moved.FinalLeafID) != "pane-"+source {
		t.Fatalf("the move answered %+v (%s), want success from %s to %s keeping the pane id", moved, protocol.Deref(moved.Error), sourceWS, targetWS)
	}
	testworld.Await(app, protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool { return e.Workspace.ID == sourceWS })
	testworld.AwaitSession(app, source, func(s protocol.Session) bool { return s.WorkspaceID == targetWS })

	var order []string
	for _, e := range app.Received()[from:] {
		switch {
		case e.Event == protocol.EventWorkspaceLayoutUpdated && e.WorkspaceLayout != nil && e.WorkspaceLayout.WorkspaceID == targetWS &&
			workspacePane(*e.WorkspaceLayout, "pane-"+source).PaneID != "" && !slices.Contains(order, "target layout"):
			order = append(order, "target layout")
		case e.Session != nil && e.Session.ID == source && e.Session.WorkspaceID == targetWS && !slices.Contains(order, "session moved"):
			order = append(order, "session moved")
		}
	}
	if !slices.Equal(order, []string{"target layout", "session moved"}) {
		t.Errorf("the app heard of the move as %v, want the target layout before the session's new workspace", order)
	}
	if layout := workspaceLayoutNow(t, w, targetWS); !slices.Equal(workspacePaneIDs(layout), []string{"pane-" + target, "pane-" + source}) {
		t.Errorf("the target workspace has panes %v, want both", workspacePaneIDs(layout))
	}
}

func TestMovingAPaneToANewWorkspaceCreatesItBesideTheSource(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	leaving := w.Spawn(app, fakeagent.Claude, cwd)
	staying := w.Spawn(app, fakeagent.Claude, cwd)
	w.Launched(leaving)
	w.Launched(staying)
	source := workspaceIDFor(t, w, cwd)
	from := len(app.Received())

	created := workspaceLayoutAction(app, protocol.WorkspaceLayoutMoveLeafToNewWorkspaceMessage{
		Cmd: protocol.CmdWorkspaceLayoutMoveLeafToNewWorkspace, SourceWorkspaceID: source, LeafID: "pane-" + leaving,
	}, protocol.CmdWorkspaceLayoutMoveLeafToNewWorkspace, source)
	newWS := protocol.Deref(created.TargetWorkspaceID)
	if !created.Success || newWS == "" || newWS == source || protocol.Deref(created.LeafID) != "pane-"+leaving {
		t.Fatalf("the move answered %+v (%s), want success into a fresh workspace", created, protocol.Deref(created.Error))
	}
	registered := testworld.Await(app, protocol.EventWorkspaceRegistered, func(e protocol.WorkspaceRegisteredMessage) bool { return e.Workspace.ID == newWS }).Workspace
	testworld.AwaitSession(app, leaving, func(s protocol.Session) bool { return s.WorkspaceID == newWS })
	sourceView, _ := workspaceView(w, source)
	if registered.Directory != cwd || registered.Rank <= sourceView.Rank {
		t.Errorf("the new workspace is in %s ranked %q, want %s ranked after the source's %q", registered.Directory, registered.Rank, cwd, sourceView.Rank)
	}
	var order []string
	for _, e := range app.Received()[from:] {
		switch {
		case e.Event == protocol.EventWorkspaceRegistered && e.Workspace != nil && e.Workspace.ID == newWS:
			order = append(order, "registered")
		case e.Event == protocol.EventWorkspaceLayoutUpdated && e.WorkspaceLayout != nil && e.WorkspaceLayout.WorkspaceID == newWS && !slices.Contains(order, "layout"):
			order = append(order, "layout")
		}
	}
	if !slices.Equal(order, []string{"registered", "layout"}) {
		t.Errorf("the app heard of the new workspace as %v, want it registered before its layout", order)
	}
	if got := workspacePaneIDs(workspaceLayoutNow(t, w, newWS)); !slices.Equal(got, []string{"pane-" + leaving}) {
		t.Errorf("the new workspace has panes %v, want the moved one", got)
	}
	if got := workspacePaneIDs(workspaceLayoutNow(t, w, source)); !slices.Equal(got, []string{"pane-" + staying}) {
		t.Errorf("the source has panes %v, want the one that stayed", got)
	}

	emptied := workspaceLayoutAction(app, protocol.WorkspaceLayoutMoveLeafToNewWorkspaceMessage{
		Cmd: protocol.CmdWorkspaceLayoutMoveLeafToNewWorkspace, SourceWorkspaceID: source, LeafID: "pane-" + staying,
	}, protocol.CmdWorkspaceLayoutMoveLeafToNewWorkspace, source)
	if !emptied.Success {
		t.Fatalf("moving the last pane out failed: %s", protocol.Deref(emptied.Error))
	}
	testworld.Await(app, protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool { return e.Workspace.ID == source })
	testworld.AwaitSession(app, staying, func(s protocol.Session) bool { return s.WorkspaceID == protocol.Deref(emptied.TargetWorkspaceID) })

	refused, failedWS, failedPane := w.RequestSpawn(app, fakeagent.Harness("no-such-agent"), w.Path("broken"))
	if refused.Success {
		t.Fatal("spawning an unknown agent succeeded")
	}
	before := len(w.App().Initial.Workspaces)
	orphan := workspaceLayoutAction(app, protocol.WorkspaceLayoutMoveLeafToNewWorkspaceMessage{
		Cmd: protocol.CmdWorkspaceLayoutMoveLeafToNewWorkspace, SourceWorkspaceID: failedWS, LeafID: failedPane,
	}, protocol.CmdWorkspaceLayoutMoveLeafToNewWorkspace, failedWS)
	if orphan.Success {
		t.Fatal("a pane whose session never started was moved to a new workspace")
	}
	if after := w.App().Initial.Workspaces; len(after) != before {
		t.Errorf("the refused move left %d workspaces, want %d", len(after), before)
	}
	if got := workspacePaneIDs(workspaceLayoutNow(t, w, failedWS)); !slices.Equal(got, []string{failedPane}) {
		t.Errorf("the refused move left the source with panes %v, want %s", got, failedPane)
	}
}

func TestDockedTilesKeepTheirParamsAndBindingAsTheyMoveAndSurviveARestart(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	cwd := w.Path("shop")
	first := w.Spawn(app, workspaceShell, cwd)
	second := w.Spawn(app, workspaceShell, cwd)
	ws := workspaceIDFor(t, w, cwd)
	firstPane, secondPane := "pane-"+first, "pane-"+second
	notes := w.Path("notes.md")
	dock := func(tileID, kind, anchor string, edge protocol.WorkspaceLayoutDockEdge, params string) protocol.WorkspaceLayoutActionResultMessage {
		msg := protocol.WorkspaceLayoutDockTileMessage{
			Cmd: protocol.CmdWorkspaceLayoutDockTile, WorkspaceID: ws, AnchorPaneID: anchor, Edge: edge, TileID: tileID, TileKind: kind,
		}
		if params != "" {
			msg.TileParams = protocol.Ptr(params)
		}
		result := workspaceLayoutAction(app, msg, protocol.CmdWorkspaceLayoutDockTile, ws)
		if protocol.Deref(result.TileID) != tileID {
			t.Fatalf("docking %s answered for tile %q", tileID, protocol.Deref(result.TileID))
		}
		return result
	}
	update := func(tileID, params, session string) protocol.WorkspaceLayoutActionResultMessage {
		msg := protocol.WorkspaceLayoutUpdateTileMessage{
			Cmd: protocol.CmdWorkspaceLayoutUpdateTile, WorkspaceID: ws, TileID: tileID, TileParams: params, RequestID: uuid.NewString(),
		}
		if session != "" {
			msg.TileSessionID = protocol.Ptr(session)
		}
		return testworld.Request(app, msg, protocol.EventWorkspaceLayoutActionResult, func(r protocol.WorkspaceLayoutActionResultMessage) bool {
			return protocol.Deref(r.RequestID) == msg.RequestID
		})
	}
	tiles := func() map[string]workspaceLayoutNode {
		return workspaceLayoutTree(t, workspaceLayoutNow(t, w, ws)).tiles()
	}
	original, child := plantSeed(t, w, "Discount codes"), plantSeed(t, w, "Discount code validation")

	for _, step := range []struct {
		name    string
		do      func() protocol.WorkspaceLayoutActionResultMessage
		success bool
	}{
		{"dock a tile under a pane's id", func() protocol.WorkspaceLayoutActionResultMessage {
			return dock(secondPane, "markdown", firstPane, protocol.WorkspaceLayoutDockEdgeRight, notes)
		}, false},
		{"dock a markdown tile", func() protocol.WorkspaceLayoutActionResultMessage {
			return dock("tile-md", "markdown", firstPane, protocol.WorkspaceLayoutDockEdgeRight, notes)
		}, true},
		{"add a pane under a tile's id", func() protocol.WorkspaceLayoutActionResultMessage {
			return workspaceLayoutAction(app, protocol.WorkspaceLayoutAddSessionPaneMessage{
				Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: ws, SessionID: "session-colliding", PaneID: protocol.Ptr("tile-md"),
			}, protocol.CmdWorkspaceLayoutAddSessionPane, ws)
		}, false},
		{"move the markdown tile without params", func() protocol.WorkspaceLayoutActionResultMessage {
			return dock("tile-md", "markdown", secondPane, protocol.WorkspaceLayoutDockEdgeBottom, "")
		}, true},
		{"point the markdown tile at another file", func() protocol.WorkspaceLayoutActionResultMessage {
			return update("tile-md", w.Path("other.md"), "")
		}, false},
		{"bind the markdown tile to the second session", func() protocol.WorkspaceLayoutActionResultMessage {
			return update("tile-md", notes, second)
		}, true},
		{"bind the markdown tile to a session nobody spawned", func() protocol.WorkspaceLayoutActionResultMessage {
			return update("tile-md", notes, "session-nobody-spawned")
		}, false},
		{"dock a seed tile", func() protocol.WorkspaceLayoutActionResultMessage {
			return dock("tile-seed", "seed", firstPane, protocol.WorkspaceLayoutDockEdgeRight, original.ID)
		}, true},
		{"point the seed tile at another seed", func() protocol.WorkspaceLayoutActionResultMessage {
			return update("tile-seed", child.ID, "")
		}, true},
		{"point the seed tile at a seed that does not exist", func() protocol.WorkspaceLayoutActionResultMessage {
			return update("tile-seed", "s-gone03", "")
		}, false},
		{"dock a browser tile", func() protocol.WorkspaceLayoutActionResultMessage {
			return dock("tile-browser", "browser", secondPane, protocol.WorkspaceLayoutDockEdgeRight, "https://example.com/")
		}, true},
		{"navigate the browser tile", func() protocol.WorkspaceLayoutActionResultMessage {
			return update("tile-browser", "https://example.com/docs", "")
		}, true},
		{"navigate and rebind the browser tile at once", func() protocol.WorkspaceLayoutActionResultMessage {
			return update("tile-browser", "https://example.com/combined", second)
		}, true},
		{"navigate the browser tile to a local file", func() protocol.WorkspaceLayoutActionResultMessage {
			return update("tile-browser", "file:///tmp/private.txt", "")
		}, false},
		{"dock an empty notebook tile", func() protocol.WorkspaceLayoutActionResultMessage {
			return dock("tile-notebook", "notebook", firstPane, protocol.WorkspaceLayoutDockEdgeLeft, "")
		}, true},
		{"open a note in the notebook tile", func() protocol.WorkspaceLayoutActionResultMessage {
			return update("tile-notebook", "/notes/knowledge/decisions.md", "")
		}, true},
	} {
		if result := step.do(); result.Success != step.success {
			t.Fatalf("%s: success=%v (%s), want %v", step.name, result.Success, protocol.Deref(result.Error), step.success)
		}
	}
	if got := workspacePaneIDs(workspaceLayoutNow(t, w, ws)); !slices.Equal(got, []string{firstPane, secondPane}) {
		t.Errorf("after docking tiles the panes are %v, want the two sessions' panes only", got)
	}

	want := map[string]workspaceLayoutNode{
		"tile-md":       {TileKind: "markdown", TileParams: notes, TileSessionID: second},
		"tile-seed":     {TileKind: "seed", TileParams: child.ID},
		"tile-browser":  {TileKind: "browser", TileParams: "https://example.com/combined", TileSessionID: second},
		"tile-notebook": {TileKind: "notebook", TileParams: "/notes/knowledge/decisions.md"},
	}
	exitWorkspaceShells(app, first, second)
	for _, when := range []string{"before a restart", "after a restart"} {
		if when == "after a restart" {
			w.restart()
			app = w.App()
		}
		docked := tiles()
		if len(docked) != len(want) {
			t.Errorf("%s the tiles are %v, want %v", when, slices.Sorted(maps.Keys(docked)), slices.Sorted(maps.Keys(want)))
		}
		for id, tile := range want {
			got := docked[id]
			if got.TileKind != tile.TileKind || got.TileParams != tile.TileParams || got.TileSessionID != tile.TileSessionID {
				t.Errorf("%s tile %s is %s %q bound to %q, want %s %q bound to %q", when, id,
					got.TileKind, got.TileParams, got.TileSessionID, tile.TileKind, tile.TileParams, tile.TileSessionID)
			}
		}
	}

	undock := func() protocol.WorkspaceLayoutActionResultMessage {
		return workspaceLayoutAction(app, protocol.WorkspaceLayoutUndockTileMessage{
			Cmd: protocol.CmdWorkspaceLayoutUndockTile, WorkspaceID: ws, TileID: "tile-md",
		}, protocol.CmdWorkspaceLayoutUndockTile, ws)
	}
	if result := undock(); !result.Success {
		t.Fatalf("undocking the markdown tile failed: %s", protocol.Deref(result.Error))
	}
	if _, ok := tiles()["tile-md"]; ok {
		t.Error("the undocked tile is still in the layout")
	}
	if got := workspacePaneIDs(workspaceLayoutNow(t, w, ws)); !slices.Equal(got, []string{firstPane, secondPane}) {
		t.Errorf("after undocking the panes are %v, want both kept", got)
	}
	if result := undock(); result.Success {
		t.Error("undocking a tile that is gone succeeded")
	}
}

type workspaceLayoutNode struct {
	Type          string                `json:"type"`
	PaneID        string                `json:"pane_id"`
	TileID        string                `json:"tile_id"`
	TileKind      string                `json:"tile_kind"`
	TileParams    string                `json:"tile_params"`
	TileSessionID string                `json:"tile_session_id"`
	SplitID       string                `json:"split_id"`
	Direction     string                `json:"direction"`
	Ratio         float64               `json:"ratio"`
	RatioLocked   bool                  `json:"ratio_locked"`
	RatioMode     string                `json:"ratio_mode"`
	Children      []workspaceLayoutNode `json:"children"`
}

func workspaceLayoutTree(t *testing.T, layout protocol.WorkspaceLayout) workspaceLayoutNode {
	t.Helper()
	var root workspaceLayoutNode
	if err := json.Unmarshal([]byte(layout.LayoutJson), &root); err != nil {
		t.Fatalf("layout of %s is not a node tree: %v: %s", layout.WorkspaceID, err, layout.LayoutJson)
	}
	return root
}

func (n workspaceLayoutNode) leafIDs() []string {
	switch n.Type {
	case "pane":
		return []string{n.PaneID}
	case "tile":
		return []string{n.TileID}
	}
	var ids []string
	for _, child := range n.Children {
		ids = append(ids, child.leafIDs()...)
	}
	return ids
}

func (n workspaceLayoutNode) tiles() map[string]workspaceLayoutNode {
	found := map[string]workspaceLayoutNode{}
	if n.Type == "tile" {
		found[n.TileID] = n
	}
	for _, child := range n.Children {
		for id, tile := range child.tiles() {
			found[id] = tile
		}
	}
	return found
}

func (n workspaceLayoutNode) split(id string) (workspaceLayoutNode, bool) {
	if n.Type == "split" && n.SplitID == id {
		return n, true
	}
	for _, child := range n.Children {
		if found, ok := child.split(id); ok {
			return found, true
		}
	}
	return workspaceLayoutNode{}, false
}

func (n workspaceLayoutNode) splitHolding(leafID string) (split workspaceLayoutNode, index int, ok bool) {
	for i, child := range n.Children {
		if slices.Equal(child.leafIDs(), []string{leafID}) {
			return n, i, true
		}
		if found, at, ok := child.splitHolding(leafID); ok {
			return found, at, true
		}
	}
	return workspaceLayoutNode{}, 0, false
}

func workspaceLayoutAction(app *testworld.Peer, cmd any, action, workspaceID string) protocol.WorkspaceLayoutActionResultMessage {
	app.T.Helper()
	return testworld.Request(app, cmd, protocol.EventWorkspaceLayoutActionResult, func(r protocol.WorkspaceLayoutActionResultMessage) bool {
		return r.Action == action && r.WorkspaceID == workspaceID
	})
}

func awaitWorkspacePane(app *testworld.Peer, workspaceID, paneID string, status protocol.WorkspaceLayoutPaneStatus) protocol.WorkspaceLayout {
	app.T.Helper()
	return testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(m protocol.WorkspaceLayoutUpdatedMessage) bool {
		if m.WorkspaceLayout.WorkspaceID != workspaceID {
			return false
		}
		return slices.ContainsFunc(m.WorkspaceLayout.Panes, func(p protocol.WorkspaceLayoutPane) bool {
			return (paneID == "" || p.PaneID == paneID) && p.Status == status
		})
	}).WorkspaceLayout
}

func workspacePane(layout protocol.WorkspaceLayout, paneID string) protocol.WorkspaceLayoutPane {
	for _, p := range layout.Panes {
		if p.PaneID == paneID {
			return p
		}
	}
	return protocol.WorkspaceLayoutPane{}
}

func workspacePaneIDs(layout protocol.WorkspaceLayout) []string {
	ids := make([]string, 0, len(layout.Panes))
	for _, p := range layout.Panes {
		ids = append(ids, p.PaneID)
	}
	return ids
}

func workspaceView(w *world, id string) (protocol.Workspace, bool) {
	for _, ws := range w.App().Initial.Workspaces {
		if ws.ID == id {
			return ws, true
		}
	}
	return protocol.Workspace{}, false
}

func workspaceLayoutNow(t *testing.T, w *world, id string) protocol.WorkspaceLayout {
	t.Helper()
	ws, ok := workspaceView(w, id)
	if !ok || ws.Layout == nil {
		t.Fatalf("workspace %s is not listed with a layout (listed=%v)", id, ok)
	}
	return *ws.Layout
}

func workspaceIDFor(t *testing.T, w *world, dir string) string {
	t.Helper()
	for _, ws := range w.App().Initial.Workspaces {
		if ws.Directory == dir {
			return ws.ID
		}
	}
	t.Fatalf("no workspace is listed for %s", dir)
	return ""
}

func workspaceEventCount(p *testworld.Peer, event, workspaceID string) int {
	count := 0
	for _, e := range p.Received() {
		if e.Event == event && e.Workspace != nil && e.Workspace.ID == workspaceID {
			count++
		}
	}
	return count
}

func workspaceSpawnShell(t *testing.T, app *testworld.Peer, workspaceID, cwd, id, label string) string {
	t.Helper()
	result := testworld.Request(app, protocol.SpawnSessionMessage{
		Cmd: protocol.CmdSpawnSession, ID: id, Agent: string(workspaceShell), Label: protocol.Ptr(label),
		Cwd: cwd, WorkspaceID: workspaceID, Cols: 80, Rows: 24,
	}, protocol.EventSpawnResult, func(r protocol.SpawnResultMessage) bool { return r.ID == id })
	if !result.Success {
		t.Fatalf("spawning shell %s failed: %s", id, protocol.Deref(result.Error))
	}
	return id
}

func exitWorkspaceShells(app *testworld.Peer, ids ...string) {
	app.T.Helper()
	for _, id := range ids {
		app.TypeLine(id, "exit")
		testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == id })
	}
}
