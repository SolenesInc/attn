package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestRegisteringAWorkspaceAnnouncesItOnceAndRemembersItsDirectory(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	shop, blog := w.Path("shop"), w.Path("blog")
	for _, dir := range []string{shop, blog} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	register := func(id, title, dir, event string) protocol.Workspace {
		return testworld.Request(app, protocol.RegisterWorkspaceMessage{
			Cmd: protocol.CmdRegisterWorkspace, ID: id, Title: title, Directory: dir,
		}, event, func(e protocol.WorkspaceRegisteredMessage) bool { return e.Workspace.ID == id }).Workspace
	}

	if got := register("ws-shop", "shop", shop, protocol.EventWorkspaceRegistered); got.Title != "shop" || got.Directory != shop {
		t.Errorf("the app was told of workspace %q in %s, want shop in %s", got.Title, got.Directory, shop)
	}
	register("ws-shop", "shop again", shop, protocol.EventWorkspaceStateChanged)
	register("ws-blog", "blog", blog, protocol.EventWorkspaceRegistered)
	listed, err := w.Client().List("")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var ids []string
	for _, ws := range listed.Workspaces {
		ids = append(ids, ws.ID)
	}
	if slices.Sort(ids); !slices.Equal(ids, []string{"ws-blog", "ws-shop"}) {
		t.Errorf("the CLI lists workspaces %v, want ws-blog and ws-shop", ids)
	}

	app.Send(protocol.UnregisterWorkspaceMessage{Cmd: protocol.CmdUnregisterWorkspace, ID: "ws-never-registered"})
	testworld.Request(app, protocol.UnregisterWorkspaceMessage{Cmd: protocol.CmdUnregisterWorkspace, ID: "ws-blog"},
		protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool { return e.Workspace.ID == "ws-blog" })
	var told []string
	for _, e := range app.Received() {
		if (e.Event == protocol.EventWorkspaceRegistered || e.Event == protocol.EventWorkspaceUnregistered) && e.Workspace != nil {
			told = append(told, e.Event+" "+e.Workspace.ID)
		}
	}
	if want := []string{"workspace_registered ws-shop", "workspace_registered ws-blog", "workspace_unregistered ws-blog"}; !slices.Equal(told, want) {
		t.Errorf("the app was told %v, want %v", told, want)
	}

	w.restart()
	if got := locationPaths(recentLocations(w.App(), 0)); !slices.Contains(got, shop) || !slices.Contains(got, blog) {
		t.Errorf("after a restart the recent locations are %v, want both workspace directories", got)
	}
}

func TestMutingAWorkspaceMutesItsSessions(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	cwd := w.Path("shop")
	session := w.Spawn(app, fakeagent.Claude, cwd)
	w.Launched(session)
	ws := workspaceIDFor(t, w, cwd)
	if got := queriedSession(t, cli, session); got.WorkspaceID != ws || got.WorkspaceMuted != nil {
		t.Fatalf("the session is listed in workspace %q muted=%v, want %s and no mute", got.WorkspaceID, protocol.Deref(got.WorkspaceMuted), ws)
	}

	if err := cli.ToggleWorkspaceMute(ws); err != nil {
		t.Fatalf("mute %s: %v", ws, err)
	}
	testworld.Await(app, protocol.EventWorkspaceStateChanged, func(e protocol.WorkspaceStateChangedMessage) bool {
		return e.Workspace.ID == ws && e.Workspace.Muted
	})
	if got := queriedSession(t, cli, session); !protocol.Deref(got.WorkspaceMuted) {
		t.Error("the session of a muted workspace is not listed as muted")
	}
	if err := cli.ToggleWorkspaceMute("ws-never-registered"); err == nil {
		t.Error("muting a workspace that does not exist succeeded")
	}
}

func TestAWorkspaceTakesTheBusiestStateOfItsSessionsAndOnlyAnnouncesChanges(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	first, second := w.Spawn(app, fakeagent.Claude, cwd), w.Spawn(app, fakeagent.Claude, cwd)
	runs := map[string]*fakeagent.Run{first: w.Launched(first), second: w.Launched(second)}
	ws := workspaceIDFor(t, w, cwd)
	for id := range runs {
		testworld.AwaitSession(app, id, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })
	}
	announcedSinceWorkBegan := func() []protocol.WorkspaceStatus {
		var statuses []protocol.WorkspaceStatus
		for _, e := range app.Received() {
			if e.Event == protocol.EventWorkspaceStateChanged && e.Workspace != nil && e.Workspace.ID == ws &&
				(len(statuses) > 0 || e.Workspace.Status == protocol.WorkspaceStatusWorking) {
				statuses = append(statuses, e.Workspace.Status)
			}
		}
		return statuses
	}

	work := func(id string) protocol.Session {
		app.TypeLine(id, "run the tests")
		runs[id].Prompted()
		return testworld.AwaitSession(app, id, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	}
	finish := func(id string, working protocol.Session) {
		runs[id].Reply("All green. <!-- attn:state=idle -->")
		testworld.AwaitSession(app, id, func(s protocol.Session) bool {
			return s.State == protocol.SessionStateIdle && stateSince(t, s).After(stateSince(t, working))
		})
	}
	firstWorking := work(first)
	testworld.Await(app, protocol.EventWorkspaceStateChanged, func(e protocol.WorkspaceStateChangedMessage) bool {
		return e.Workspace.ID == ws && e.Workspace.Status == protocol.WorkspaceStatusWorking
	})
	finish(second, work(second))
	finish(first, firstWorking)
	for !slices.Contains(announcedSinceWorkBegan(), protocol.WorkspaceStatusIdle) {
		testworld.Await(app, protocol.EventWorkspaceStateChanged, func(e protocol.WorkspaceStateChangedMessage) bool {
			return e.Workspace.ID == ws && e.Workspace.Status == protocol.WorkspaceStatusIdle
		})
	}
	workIn(t, app, w.Path("barrier"))

	if statuses, want := announcedSinceWorkBegan(), []protocol.WorkspaceStatus{protocol.WorkspaceStatusWorking, protocol.WorkspaceStatusIdle}; !slices.Equal(statuses, want) {
		t.Errorf("while its sessions worked the workspace was announced as %v, want %v", statuses, want)
	}
}

func TestAWorkspaceLeavesWithItsLastSessionUnlessPinnedOrHoldingAFailedPane(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	spawn := func(dir string) (session, workspaceID string) {
		session = w.Spawn(app, fakeagent.Claude, dir)
		w.Launched(session)
		return session, workspaceIDFor(t, w, dir)
	}
	closePane := func(session, workspaceID string) {
		closed := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{
			Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: workspaceID, PaneID: "pane-" + session,
		}, protocol.CmdWorkspaceLayoutClosePane, workspaceID)
		if !closed.Success {
			t.Fatalf("closing the pane of %s failed: %s", session, protocol.Deref(closed.Error))
		}
		testworld.Await(app, protocol.EventSessionUnregistered, func(e protocol.SessionUnregisteredMessage) bool { return e.Session.ID == session })
	}

	unpinnedSession, unpinned := spawn(w.Path("shop"))
	pinnedSession, pinned := spawn(w.Path("notes"))
	testworld.Request(app, protocol.PinWorkspaceMessage{Cmd: protocol.CmdPinWorkspace, WorkspaceID: pinned, Pinned: true},
		protocol.EventWorkspaceStateChanged, func(e protocol.WorkspaceStateChangedMessage) bool {
			return e.Workspace.ID == pinned && e.Workspace.Pinned
		})
	failingDir := w.Path("broken")
	liveSession, failing := spawn(failingDir)
	refused, _, failedPane := w.RequestSpawn(app, fakeagent.Harness("no-such-agent"), failingDir)
	if refused.Success {
		t.Fatal("spawning an unknown agent succeeded")
	}

	closePane(unpinnedSession, unpinned)
	closePane(pinnedSession, pinned)
	closePane(liveSession, failing)
	testworld.Await(app, protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool { return e.Workspace.ID == unpinned })
	workIn(t, app, w.Path("barrier"))

	for _, c := range []struct {
		workspace string
		want      int
	}{{unpinned, 1}, {pinned, 0}, {failing, 0}} {
		if got := workspaceEventCount(app, protocol.EventWorkspaceUnregistered, c.workspace); got != c.want {
			t.Errorf("the app was told %d times that %s went away, want %d", got, c.workspace, c.want)
		}
	}
	listed, err := cli.List("")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byID := map[string]protocol.Workspace{}
	for _, ws := range listed.Workspaces {
		byID[ws.ID] = ws
	}
	if _, ok := byID[unpinned]; ok {
		t.Errorf("the emptied workspace %s is still listed", unpinned)
	}
	if got, ok := byID[pinned]; !ok || !got.Pinned || got.Status != protocol.WorkspaceStatusIdle {
		t.Errorf("the pinned workspace is listed=%v as %+v, want it kept pinned and idle", ok, got)
	}
	if view, ok := workspaceView(w, pinned); !ok || (view.Layout != nil && len(view.Layout.Panes) != 0) {
		t.Errorf("the emptied pinned workspace is shown=%v with layout %+v, want no panes left", ok, view.Layout)
	}
	if _, ok := byID[failing]; !ok {
		t.Errorf("the workspace showing a failed pane was removed")
	}
	if got := workspacePaneIDs(workspaceLayoutNow(t, w, failing)); !slices.Equal(got, []string{failedPane}) {
		t.Errorf("the workspace shows panes %v, want the failed %s", got, failedPane)
	}
	if slices.ContainsFunc(listed.Sessions, func(s protocol.Session) bool { return s.WorkspaceID == pinned || s.WorkspaceID == unpinned }) {
		t.Error("a closed session is still listed")
	}
}

func TestWorkspacesSurviveARestartUnlessEmptyAndUnpinned(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	shopDir := w.Path("shop")
	talked := w.Spawn(app, fakeagent.Claude, shopDir)
	run := w.Launched(talked)
	app.TypeLine(talked, "add a discount field")
	run.Prompted()
	run.Reply("Before or after tax? <!-- attn:state=waiting_input -->")
	shop := workspaceIDFor(t, w, shopDir)
	testworld.Await(app, protocol.EventWorkspaceStateChanged, func(e protocol.WorkspaceStateChangedMessage) bool {
		return e.Workspace.ID == shop && e.Workspace.Status == protocol.WorkspaceStatusWaitingInput
	})

	workIn(t, app, w.Path("notes"))
	notes := workspaceIDFor(t, w, w.Path("notes"))
	testworld.Request(app, protocol.PinWorkspaceMessage{Cmd: protocol.CmdPinWorkspace, WorkspaceID: notes, Pinned: true},
		protocol.EventWorkspaceStateChanged, func(e protocol.WorkspaceStateChangedMessage) bool {
			return e.Workspace.ID == notes && e.Workspace.Pinned
		})
	workIn(t, app, w.Path("scratch"))
	scratch := workspaceIDFor(t, w, w.Path("scratch"))
	workIn(t, app, w.Path("later"))
	later := workspaceIDFor(t, w, w.Path("later"))
	if added := workspaceLayoutAction(app, protocol.WorkspaceLayoutAddSessionPaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutAddSessionPane, WorkspaceID: later, SessionID: "session-about-to-spawn", PaneID: protocol.Ptr("pane-about-to-spawn"),
	}, protocol.CmdWorkspaceLayoutAddSessionPane, later); !added.Success {
		t.Fatalf("adding the pane failed: %s", protocol.Deref(added.Error))
	}
	refused, broken, _ := w.RequestSpawn(app, fakeagent.Harness("no-such-agent"), w.Path("broken"))
	if refused.Success {
		t.Fatal("spawning an unknown agent succeeded")
	}

	w.restart()
	view := w.App().Initial
	listed := map[string]protocol.Workspace{}
	for _, ws := range view.Workspaces {
		listed[ws.ID] = ws
	}
	if got, ok := listed[shop]; !ok || got.Status != protocol.WorkspaceStatusIdle {
		t.Errorf("the workspace of the talked-to session is listed=%v as %s, want it kept and idle like its recoverable session", ok, got.Status)
	}
	if i := slices.IndexFunc(view.Sessions, func(s protocol.Session) bool { return s.ID == talked }); i < 0 || view.Sessions[i].WorkspaceID != shop {
		t.Errorf("after the restart the talked-to session is not listed in %s", shop)
	}
	if got, ok := listed[notes]; !ok || !got.Pinned {
		t.Errorf("the pinned empty workspace is listed=%v pinned=%v, want it kept pinned", ok, got.Pinned)
	}
	if _, ok := listed[scratch]; ok {
		t.Error("the workspace that never had a session survived the restart")
	}
	for id, status := range map[string]protocol.WorkspaceLayoutPaneStatus{
		later:  protocol.WorkspaceLayoutPaneStatusSpawning,
		broken: protocol.WorkspaceLayoutPaneStatusFailed,
	} {
		ws, ok := listed[id]
		if !ok || ws.Layout == nil || len(ws.Layout.Panes) != 1 || ws.Layout.Panes[0].Status != status {
			t.Errorf("workspace %s is listed=%v with layout %+v, want its %s pane kept", id, ok, ws.Layout, status)
		}
	}
}

func TestClosingAWorkspaceClosesEachOfItsSessionsFirst(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	sessions := []string{w.Spawn(app, fakeagent.Claude, cwd), w.Spawn(app, fakeagent.Claude, cwd)}
	for _, id := range sessions {
		w.Launched(id)
	}
	ws := workspaceIDFor(t, w, cwd)

	testworld.Request(app, protocol.UnregisterWorkspaceMessage{Cmd: protocol.CmdUnregisterWorkspace, ID: ws},
		protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool { return e.Workspace.ID == ws })
	for _, id := range sessions {
		testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.ID == id })
	}

	for _, id := range sessions {
		var order []string
		for _, e := range app.Received() {
			switch {
			case e.Event == protocol.EventSessionClosed && e.SessionLedgerEntry != nil && e.SessionLedgerEntry.ID == id:
				order = append(order, "closed")
			case e.Event == protocol.EventSessionUnregistered && e.Session != nil && e.Session.ID == id:
				order = append(order, "unregistered")
			case e.Event == protocol.EventWorkspaceUnregistered && e.Workspace != nil && e.Workspace.ID == ws:
				order = append(order, "workspace gone")
			}
		}
		if want := []string{"closed", "unregistered", "workspace gone"}; !slices.Equal(order, want) {
			t.Errorf("the app heard of session %s as %v, want %v", id, order, want)
		}
	}
	if view := w.App().Initial; len(view.Sessions) != 0 || len(view.Workspaces) != 0 {
		t.Errorf("after closing the workspace %d sessions and %d workspaces are listed, want none", len(view.Sessions), len(view.Workspaces))
	}
}

func TestClosingTheLastPaneKeepsAWorkspaceThatStillShowsATile(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cwd := w.Path("shop")
	session := w.Spawn(app, fakeagent.Claude, cwd)
	w.Launched(session)
	ws := workspaceIDFor(t, w, cwd)
	notes := filepath.Join(cwd, "notes.md")
	if err := os.WriteFile(notes, []byte("# Notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if docked := workspaceLayoutAction(app, protocol.WorkspaceLayoutDockTileMessage{
		Cmd: protocol.CmdWorkspaceLayoutDockTile, WorkspaceID: ws, AnchorPaneID: "pane-" + session,
		Edge: protocol.WorkspaceLayoutDockEdgeRight, TileID: "tile-notes", TileKind: "markdown", TileParams: protocol.Ptr(notes),
	}, protocol.CmdWorkspaceLayoutDockTile, ws); !docked.Success {
		t.Fatalf("docking the tile failed: %s", protocol.Deref(docked.Error))
	}

	closed := workspaceLayoutAction(app, protocol.WorkspaceLayoutClosePaneMessage{
		Cmd: protocol.CmdWorkspaceLayoutClosePane, WorkspaceID: ws, PaneID: "pane-" + session,
	}, protocol.CmdWorkspaceLayoutClosePane, ws)
	if !closed.Success {
		t.Fatalf("closing the pane failed: %s", protocol.Deref(closed.Error))
	}
	tileOnly := func(layout protocol.WorkspaceLayout) bool {
		return len(layout.Panes) == 0 && slices.Equal(workspaceLayoutTree(t, layout).leafIDs(), []string{"tile-notes"})
	}
	testworld.Await(app, protocol.EventWorkspaceLayoutUpdated, func(m protocol.WorkspaceLayoutUpdatedMessage) bool {
		return m.WorkspaceLayout.WorkspaceID == ws && tileOnly(m.WorkspaceLayout)
	})

	w.restart()
	app = w.App()
	if layout := workspaceLayoutNow(t, w, ws); !tileOnly(layout) {
		t.Fatalf("after a restart the workspace shows %s with panes %v, want only the tile", layout.LayoutJson, workspacePaneIDs(layout))
	}
	undocked := workspaceLayoutAction(app, protocol.WorkspaceLayoutUndockTileMessage{
		Cmd: protocol.CmdWorkspaceLayoutUndockTile, WorkspaceID: ws, TileID: "tile-notes",
	}, protocol.CmdWorkspaceLayoutUndockTile, ws)
	if !undocked.Success {
		t.Fatalf("undocking the last tile failed: %s", protocol.Deref(undocked.Error))
	}
	testworld.Await(app, protocol.EventWorkspaceUnregistered, func(e protocol.WorkspaceUnregisteredMessage) bool { return e.Workspace.ID == ws })
	if _, ok := workspaceView(w, ws); ok {
		t.Error("the workspace is still listed after its last tile was undocked")
	}
}
