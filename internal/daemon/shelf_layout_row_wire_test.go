package daemon_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func columnsOf(node layoutNode) []layoutNode {
	if node.Type == "split" && node.Direction == "vertical" {
		return append(columnsOf(node.Children[0]), columnsOf(node.Children[1])...)
	}
	return []layoutNode{node}
}

func (n layoutNode) firstLeafWantedCols() int {
	if n.Type == "split" {
		return n.Children[0].firstLeafWantedCols()
	}
	return n.WantedCols
}

func rowOf(t *testing.T, desktop protocol.Desktop) string {
	t.Helper()
	names := map[string]string{}
	for _, pane := range desktop.Panes {
		names[pane.PaneID] = string(pane.SessionID)
	}
	var columns []string
	for _, column := range columnsOf(desktopTree(t, desktop)) {
		var tiles []string
		for _, id := range column.leafIDs() {
			if name, ok := names[id]; ok {
				id = name
			}
			tiles = append(tiles, id)
		}
		columns = append(columns, strings.Join(tiles, "/"))
	}
	return strings.Join(columns, " ")
}

func turnOnShelfLayout(t *testing.T, app *testworld.Peer) {
	t.Helper()
	setSetting(t, app, "shelf_layout_enabled", "true")
}

type rowDesktop struct {
	t         *testing.T
	w         *world
	app       *testworld.Peer
	profileID string
	desktopID string
}

func injectedRow(t *testing.T, w *world, sessions ...string) rowDesktop {
	t.Helper()
	app := w.App()
	turnOnShelfLayout(t, app)
	for _, id := range sessions {
		injectAgent(t, w, id)
	}
	profileID := app.SelectedProfile()
	desktop, _ := viewProfile(t, w, profileID).paneOf(t, sessions[0])
	return rowDesktop{t: t, w: w, app: app, profileID: profileID, desktopID: desktop.ID}
}

func (r rowDesktop) desktop() protocol.Desktop {
	r.t.Helper()
	return viewProfile(r.t, r.w, r.profileID).desktops[r.desktopID]
}

func (r rowDesktop) pane(session string) string {
	r.t.Helper()
	_, pane := viewProfile(r.t, r.w, r.profileID).paneOf(r.t, session)
	return pane
}

func (r rowDesktop) move(message protocol.DesktopMoveLeafMessage) protocol.ProfileActionResultMessage {
	r.t.Helper()
	message.Cmd, message.RequestID, message.SourceDesktopID = protocol.CmdDesktopMoveLeaf, uuid.NewString(), r.desktopID
	if message.TargetDesktopID == nil {
		message.TargetDesktopID = protocol.Ptr(r.desktopID)
	}
	return profileRequest(r.app, message, message.RequestID)
}

func (r rowDesktop) stack(session, under string) {
	r.t.Helper()
	moved := r.move(protocol.DesktopMoveLeafMessage{LeafID: r.pane(session), AnchorID: protocol.Ptr(r.pane(under)), Edge: protocol.Ptr(protocol.LayoutDockEdgeBottom)})
	if !moved.Success {
		r.t.Fatalf("stacking %s under %s: %s", session, under, protocol.Deref(moved.Error))
	}
}

func (r rowDesktop) requireRow(want string) {
	r.t.Helper()
	if got := rowOf(r.t, r.desktop()); got != want {
		r.t.Fatalf("the row is %q, want %q", got, want)
	}
}

func (r rowDesktop) requireActive(session string) {
	r.t.Helper()
	if got, want := r.desktop().ActivePaneID, r.pane(session); got != want {
		r.t.Fatalf("the active tile is %s, want %s (%s); the row is %q", got, want, session, rowOf(r.t, r.desktop()))
	}
}

func TestClosingATileFocusesByItsPositionInTheRow(t *testing.T) {
	for _, row := range []struct {
		name   string
		stacks [][2]string
		want   string
		visits []string
		close  string
		active string
	}{
		{name: "middle of the row takes the next column", want: "a b c", visits: []string{"c", "a", "b"}, close: "b", active: "c"},
		{name: "end of the row takes the previous column", want: "a b c", visits: []string{"a", "c"}, close: "c", active: "b"},
		{name: "a single tile between stacks takes the next column", stacks: [][2]string{{"d", "c"}}, want: "a b c/d", visits: []string{"d", "a", "b"}, close: "b", active: "d"},
		{name: "middle of a stack takes the next tile", stacks: [][2]string{{"b", "a"}, {"c", "b"}}, want: "a/b/c", visits: []string{"a", "c", "b"}, close: "b", active: "c"},
		{name: "end of a stack takes the previous tile", stacks: [][2]string{{"b", "a"}, {"c", "b"}}, want: "a/b/c d", visits: []string{"a", "d", "c"}, close: "c", active: "b"},
		{name: "entering a stack lands on its most recently used tile", stacks: [][2]string{{"c", "b"}, {"d", "c"}}, want: "a b/c/d", visits: []string{"d", "c", "a"}, close: "a", active: "c"},
		{name: "entering a stack leftward lands on its most recently used tile", stacks: [][2]string{{"b", "a"}, {"c", "b"}}, want: "a/b/c d", visits: []string{"c", "a", "b", "d"}, close: "d", active: "b"},
	} {
		t.Run(row.name, func(t *testing.T) {
			inBubble(t, func(t *testing.T, w *world) {
				sessions := []string{"a", "b", "c"}
				if strings.Contains(row.want, "d") {
					sessions = append(sessions, "d")
				}
				desktop := injectedRow(t, w, sessions...)
				for _, stack := range row.stacks {
					desktop.stack(stack[0], stack[1])
				}
				desktop.requireRow(row.want)
				for _, session := range row.visits {
					if result := requestShowSession(desktop.app, session); !result.Success {
						t.Fatalf("show %s: %s", session, protocol.Deref(result.Error))
					}
				}
				if result := closeTileFromApp(desktop.app, desktop.desktopID, desktop.pane(row.close)); !result.Success {
					t.Fatal(protocol.Deref(result.Error))
				}
				desktop.requireActive(row.active)
			})
		})
	}
}

func TestAnAgentExitFocusesByPositionLikeClosingItsTile(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	turnOnShelfLayout(t, app)
	var sessions []string
	for _, name := range []string{"a", "b", "c"} {
		sessions = append(sessions, w.Spawn(app, fakeagent.Claude, w.Path(name)))
	}
	for _, id := range []string{sessions[2], sessions[0], sessions[1]} {
		requestShowSession(app, id)
	}
	profileID := app.SelectedProfile()
	desktop, paneC := viewProfile(t, w, profileID).paneOf(t, sessions[2])
	watcher := w.AppOn(profileID)
	w.Launched(sessions[1]).Exit(0)
	testworld.Await(watcher, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return e.SessionID == protocol.SessionID(sessions[1]) })
	if result := closeFromApp(app, sessions[1]); result.Error != nil {
		t.Fatal(*result.Error)
	}
	if got := viewProfile(t, w, profileID).desktops[desktop.ID].ActivePaneID; got != paneC {
		t.Fatalf("closing the exited middle agent selected %s, want the next column %s", got, paneC)
	}
}

func TestAnAgentsTerminalGoesToTheRowsEndAndStacksOnlyWhenAskedTo(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	turnOnShelfLayout(t, app)
	first := w.Spawn(app, fakeagent.Claude, w.Path("first"))
	second := w.Spawn(app, fakeagent.Claude, w.Path("second"))
	requestShowSession(app, second)
	profileID := app.SelectedProfile()
	desktop, activePane := viewProfile(t, w, profileID).paneOf(t, second)
	open := func(caller string, direction *protocol.TerminalDirection) protocol.SessionID {
		t.Helper()
		answer, err := cli.TerminalCommand(context.Background(), protocol.TerminalOpenMessage{Cmd: protocol.CmdTerminalOpen, CallerSessionID: protocol.SessionID(caller), Direction: direction})
		if err != nil {
			t.Fatal(err)
		}
		return answer.TerminalOpenResult.SessionID
	}
	beside := open(first, nil)
	view := viewProfile(t, w, profileID).desktops[desktop.ID]
	if got, want := rowOf(t, view), fmt.Sprintf("%s %s %s", first, second, beside); got != want {
		t.Fatalf("the terminal opened into row %q, want %q", got, want)
	}
	if view.ActivePaneID != activePane {
		t.Fatalf("an agent's terminal took focus: %s, want %s", view.ActivePaneID, activePane)
	}
	another := open(first, nil)
	view = viewProfile(t, w, profileID).desktops[desktop.ID]
	if got, want := rowOf(t, view), fmt.Sprintf("%s %s %s %s", first, second, beside, another); got != want {
		t.Fatalf("a caller's second terminal made row %q, want %q", got, want)
	}
	below := open(second, protocol.Ptr(protocol.TerminalDirectionDown))
	view = viewProfile(t, w, profileID).desktops[desktop.ID]
	if got, want := rowOf(t, view), fmt.Sprintf("%s %s/%s %s %s", first, second, below, beside, another); got != want {
		t.Fatalf("--direction down made row %q, want %q", got, want)
	}
	if view.ActivePaneID != activePane {
		t.Fatalf("a stacked terminal took focus: %s, want %s", view.ActivePaneID, activePane)
	}
	exitShells(app, string(beside), string(another), string(below))
}

func TestATileOpenedFromTheCLILandsBesideItsAgentWithoutFocus(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		desktop := injectedRow(t, w, "a", "b", "c")
		desktop.stack("b", "a")
		requestShowSession(desktop.app, "c")
		cli := w.Client()

		cliFile := markdownFile(t, w, "cli.md")
		if err := cli.OpenMarkdown(cliFile, "a"); err != nil {
			t.Fatal(err)
		}
		desktop.requireActive("c")
		tiles := desktopTree(t, desktop.desktop()).tiles()
		if len(tiles) != 1 {
			t.Fatalf("opening one file left the tiles %+v", tiles)
		}
		for cliTile := range tiles {
			desktop.requireRow("a/b " + cliTile + " c")
		}

		if err := cli.OpenMarkdown(cliFile, "a"); err != nil {
			t.Fatal(err)
		}
		desktop.requireActive("c")

		if err := cli.OpenBrowser("https://example.com", "a"); err != nil {
			t.Fatal(err)
		}
		desktop.requireActive("c")

		appFile := markdownFile(t, w, "app.md")
		requestID := uuid.NewString()
		opened := testworld.Request(desktop.app, protocol.OpenMarkdownMessage{Cmd: protocol.CmdOpenMarkdown, Path: appFile, SessionID: protocol.Ptr(protocol.SessionID("c")), RequestID: protocol.Ptr(requestID)},
			protocol.EventOpenMarkdownResult, func(r protocol.OpenMarkdownResultMessage) bool { return protocol.Deref(r.RequestID) == requestID })
		if !opened.Success {
			t.Fatal(protocol.Deref(opened.Error))
		}
		if got := desktop.desktop().ActivePaneID; got != protocol.Deref(opened.TileID) {
			t.Fatalf("a tile opened from the app left %s active, want %s", got, protocol.Deref(opened.TileID))
		}
	})
}

func TestSideBySidePlacementsAnchorOnTheColumnOfAStackedTile(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		desktop := injectedRow(t, w, "a", "b", "c", "d")
		desktop.stack("b", "a")
		desktop.requireRow("a/b c d")

		moved := desktop.move(protocol.DesktopMoveLeafMessage{LeafID: desktop.pane("d"), AnchorID: protocol.Ptr(desktop.pane("b")), Edge: protocol.Ptr(protocol.LayoutDockEdgeRight)})
		if !moved.Success {
			t.Fatal(protocol.Deref(moved.Error))
		}
		desktop.requireRow("a/b d c")

		docked := dockOnDesktop(desktop.app, desktop.desktop(), protocol.DesktopDockTileMessage{
			AnchorID: protocol.Ptr(desktop.pane("a")), Edge: protocol.LayoutDockEdgeLeft, TileID: "left", TileKind: "browser", TileParams: protocol.Ptr("https://example.com"),
		})
		if !docked.Success {
			t.Fatal(protocol.Deref(docked.Error))
		}
		desktop.requireRow("left a/b d c")

		requestShowSession(desktop.app, "b")
		injectAgent(t, w, "placed")
		desktop.requireRow("left a/b placed d c")
	})
}

func TestMovingATerminalToADesktopAnchorsOnTheColumnOfItsActiveTile(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	turnOnShelfLayout(t, app)
	first := w.Spawn(app, fakeagent.Claude, w.Path("first"))
	second := w.Spawn(app, fakeagent.Claude, w.Path("second"))
	third := w.Spawn(app, fakeagent.Claude, w.Path("third"))
	profileID := app.SelectedProfile()
	desktop := rowDesktop{t: t, w: w, app: app, profileID: profileID}
	current, _ := viewProfile(t, w, profileID).paneOf(t, first)
	desktop.desktopID = current.ID
	desktop.stack(second, first)
	requestShowSession(app, first)
	call := func(message any) *protocol.Response {
		t.Helper()
		answer, err := cli.TerminalCommand(context.Background(), message)
		if err != nil {
			t.Fatal(err)
		}
		return answer
	}
	shell := call(protocol.TerminalOpenMessage{Cmd: protocol.CmdTerminalOpen, CallerSessionID: protocol.SessionID(third)}).TerminalOpenResult
	call(protocol.TerminalMoveMessage{Cmd: protocol.CmdTerminalMove, CallerSessionID: protocol.SessionID(third), Target: string(shell.TerminalID), Desktop: "8"})
	desktop.requireRow(fmt.Sprintf("%s/%s %s", first, second, third))
	back := call(protocol.TerminalMoveMessage{Cmd: protocol.CmdTerminalMove, CallerSessionID: protocol.SessionID(third), Target: string(shell.TerminalID), Desktop: current.ID}).TerminalMoveResult
	if back.DesktopID != current.ID {
		t.Fatalf("the terminal moved to %s, want %s", back.DesktopID, current.ID)
	}
	desktop.requireRow(fmt.Sprintf("%s/%s %s %s", first, second, shell.SessionID, third))
	desktop.requireActive(first)
	exitShells(app, string(shell.SessionID))
}

func TestWholeColumnMovesAStackWithItsOrderWithinAndBetweenDesktops(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		desktop := injectedRow(t, w, "a", "b", "c", "d")
		desktop.stack("c", "b")
		desktop.stack("d", "c")
		desktop.requireRow("a b/c/d")
		before := columnsOf(desktopTree(t, desktop.desktop()))[1]

		carried := desktop.move(protocol.DesktopMoveLeafMessage{
			LeafID: desktop.pane("c"), AnchorID: protocol.Ptr(desktop.pane("a")), Edge: protocol.Ptr(protocol.LayoutDockEdgeLeft),
			WholeColumn: protocol.Ptr(true), Activate: protocol.Ptr(false),
		})
		if !carried.Success {
			t.Fatal(protocol.Deref(carried.Error))
		}
		desktop.requireRow("b/c/d a")
		if got := columnsOf(desktopTree(t, desktop.desktop()))[0]; !reflect.DeepEqual(got, before) {
			t.Fatalf("the stack changed moving within its desktop: %+v, want %+v", got, before)
		}

		requestShowSession(desktop.app, "a")
		active := desktop.desktop().ActivePaneID
		again := desktop.move(protocol.DesktopMoveLeafMessage{
			LeafID: desktop.pane("c"), AnchorID: protocol.Ptr(desktop.pane("a")), Edge: protocol.Ptr(protocol.LayoutDockEdgeRight),
			WholeColumn: protocol.Ptr(true), Activate: protocol.Ptr(false),
		})
		if !again.Success {
			t.Fatal(protocol.Deref(again.Error))
		}
		desktop.requireRow("a b/c/d")
		if got := desktop.desktop().ActivePaneID; got != active {
			t.Fatalf("activate false moved focus to %s, want %s", got, active)
		}
		brought := desktop.move(protocol.DesktopMoveLeafMessage{
			LeafID: desktop.pane("d"), AnchorID: protocol.Ptr(desktop.pane("a")), Edge: protocol.Ptr(protocol.LayoutDockEdgeRight),
			WholeColumn: protocol.Ptr(true), Activate: protocol.Ptr(true),
		})
		if !brought.Success {
			t.Fatal(protocol.Deref(brought.Error))
		}
		desktop.requireActive("d")

		other := createDesktop(desktop.app, desktop.profileID)
		between := desktop.move(protocol.DesktopMoveLeafMessage{
			LeafID: desktop.pane("c"), TargetDesktopID: protocol.Ptr(other.ID), Edge: protocol.Ptr(protocol.LayoutDockEdgeRight),
			WholeColumn: protocol.Ptr(true),
		})
		if !between.Success {
			t.Fatal(protocol.Deref(between.Error))
		}
		view := viewProfile(t, w, desktop.profileID)
		if got := rowOf(t, view.desktops[other.ID]); got != "b/c/d" {
			t.Fatalf("the stack arrived as %q", got)
		}
		if got := columnsOf(desktopTree(t, view.desktops[other.ID]))[0]; !reflect.DeepEqual(got, before) {
			t.Fatalf("the stack changed moving between desktops: %+v, want %+v", got, before)
		}
		desktop.requireRow("a")
		desktop.requireActive("a")
		if got, want := view.desktops[other.ID].ActivePaneID, protocol.Deref(between.PaneID); got != want {
			t.Fatalf("the target's active tile is %s, want the moved %s", got, want)
		}
	})
}

func TestWholeColumnWithDelegatesMovesEachDelegateOnce(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app, cli := w.App(), w.Client()
	turnOnShelfLayout(t, app)
	cwd := w.Path("dispatcher")
	root, _, _ := w.RequestSpawn(app, fakeagent.Codex, cwd)
	bystander, _, _ := w.RequestSpawn(app, fakeagent.Codex, w.Path("bystander"))
	delegate := func(dispatcher protocol.SessionID, label string) protocol.SessionID {
		t.Helper()
		request := brief(cwd, "Work on "+label)
		request.Agent = protocol.Ptr("codex")
		request.SourceSessionID = protocol.Ptr(dispatcher)
		request.Label = protocol.Ptr(label)
		result, err := cli.Delegate(request)
		if err != nil {
			t.Fatal(err)
		}
		return result.SessionID
	}
	stacked := delegate(root.ID, "stacked")
	nested := delegate(stacked, "nested")
	profileID := app.SelectedProfile()
	source, _ := viewProfile(t, w, profileID).paneOf(t, string(root.ID))
	desktop := rowDesktop{t: t, w: w, app: app, profileID: profileID, desktopID: source.ID}
	desktop.stack(string(stacked), string(root.ID))
	desktop.requireRow(fmt.Sprintf("%s/%s %s %s", root.ID, stacked, bystander.ID, nested))

	other := createDesktop(app, profileID)
	moved := desktop.move(protocol.DesktopMoveLeafMessage{
		LeafID: desktop.pane(string(root.ID)), TargetDesktopID: protocol.Ptr(other.ID), Edge: protocol.Ptr(protocol.LayoutDockEdgeRight),
		WholeColumn: protocol.Ptr(true), WithDelegates: protocol.Ptr(true),
	})
	if !moved.Success {
		t.Fatal(protocol.Deref(moved.Error))
	}
	desktop.requireRow(string(bystander.ID))
	desktop.desktopID = other.ID
	desktop.requireRow(fmt.Sprintf("%s/%s %s", root.ID, stacked, nested))
}

func TestAMoveBetweenDesktopsWithActivateFalseLeavesTheTargetsActiveTile(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		desktop := injectedRow(t, w, "a", "b", "c")
		other := createDesktop(desktop.app, desktop.profileID)
		first := desktop.move(protocol.DesktopMoveLeafMessage{LeafID: desktop.pane("a"), TargetDesktopID: protocol.Ptr(other.ID), Edge: protocol.Ptr(protocol.LayoutDockEdgeRight)})
		if !first.Success {
			t.Fatal(protocol.Deref(first.Error))
		}
		second := desktop.move(protocol.DesktopMoveLeafMessage{
			LeafID: desktop.pane("b"), TargetDesktopID: protocol.Ptr(other.ID), AnchorID: first.PaneID, Edge: protocol.Ptr(protocol.LayoutDockEdgeRight), Activate: protocol.Ptr(false),
		})
		if !second.Success {
			t.Fatal(protocol.Deref(second.Error))
		}
		if got, want := viewProfile(t, w, desktop.profileID).desktops[other.ID].ActivePaneID, protocol.Deref(first.PaneID); got != want {
			t.Fatalf("activate false left %s active on the target, want %s", got, want)
		}
	})
}

func TestWantedColsLandOnTheColumnsFirstTileAndSurviveMovesAndARestart(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		turnOnShelfLayout(t, app)
		profileID := app.SelectedProfile()
		initial := viewProfile(t, w, profileID)
		desktop := rowDesktop{t: t, w: w, app: app, profileID: profileID, desktopID: initial.profile.CurrentDesktopID}
		for _, tile := range []struct {
			id, anchor string
			edge       protocol.LayoutDockEdge
		}{{"a", "", protocol.LayoutDockEdgeRight}, {"b", "a", protocol.LayoutDockEdgeRight}, {"c", "b", protocol.LayoutDockEdgeBottom}} {
			dock := protocol.DesktopDockTileMessage{TileID: tile.id, TileKind: "browser", TileParams: protocol.Ptr("https://example.com"), Edge: tile.edge}
			if tile.anchor != "" {
				dock.AnchorID = protocol.Ptr(tile.anchor)
			}
			if docked := dockOnDesktop(app, desktop.desktop(), dock); !docked.Success {
				t.Fatal(protocol.Deref(docked.Error))
			}
		}
		desktop.requireRow("a b/c")
		wanted := func(leaf string, cols int) protocol.ProfileActionResultMessage {
			t.Helper()
			id := uuid.NewString()
			return profileRequest(desktop.app, protocol.DesktopSetWantedColsMessage{Cmd: protocol.CmdDesktopSetWantedCols, RequestID: id, DesktopID: desktop.desktopID, LeafID: leaf, Cols: cols}, id)
		}
		firstTileCols := func(column int) int {
			t.Helper()
			return columnsOf(desktopTree(t, desktop.desktop()))[column].firstLeafWantedCols()
		}
		if result := wanted("c", 100); !result.Success {
			t.Fatal(protocol.Deref(result.Error))
		}
		if got := firstTileCols(1); got != 100 {
			t.Fatalf("the stack's first tile wants %d cols, want 100", got)
		}
		if refused := wanted("a", -1); refused.Success || !strings.Contains(protocol.Deref(refused.Error), "-1") {
			t.Fatalf("a negative width answered %+v", refused)
		}

		if result := wanted("a", 80); !result.Success {
			t.Fatal(protocol.Deref(result.Error))
		}
		redocked := dockOnDesktop(app, desktop.desktop(), protocol.DesktopDockTileMessage{AnchorID: protocol.Ptr("b"), TileID: "a", TileKind: "browser", TileParams: protocol.Ptr("https://example.com"), Edge: protocol.LayoutDockEdgeRight})
		if !redocked.Success {
			t.Fatal(protocol.Deref(redocked.Error))
		}
		desktop.requireRow("b/c a")
		if got := firstTileCols(1); got != 80 {
			t.Fatalf("after re-docking the tile wants %d cols, want 80", got)
		}

		w.restart()
		desktop.app = w.AppOn(profileID)
		if got := firstTileCols(0); got != 100 {
			t.Fatalf("after a restart the stack wants %d cols, want 100", got)
		}

		other := createDesktop(desktop.app, profileID)
		moved := desktop.move(protocol.DesktopMoveLeafMessage{LeafID: "b", TargetDesktopID: protocol.Ptr(other.ID), Edge: protocol.Ptr(protocol.LayoutDockEdgeRight), WholeColumn: protocol.Ptr(true)})
		if !moved.Success {
			t.Fatal(protocol.Deref(moved.Error))
		}
		desktop.requireRow("a")
		desktop.desktopID = other.ID
		desktop.requireRow("b/c")
		if got := firstTileCols(0); got != 100 {
			t.Fatalf("after moving desktops the stack wants %d cols, want 100", got)
		}
		above := dockOnDesktop(desktop.app, desktop.desktop(), protocol.DesktopDockTileMessage{AnchorID: protocol.Ptr("b"), TileID: "above", TileKind: "browser", TileParams: protocol.Ptr("https://example.com"), Edge: protocol.LayoutDockEdgeTop})
		if !above.Success {
			t.Fatal(protocol.Deref(above.Error))
		}
		desktop.requireRow("above/b/c")
		if result := wanted("b", 0); !result.Success {
			t.Fatal(protocol.Deref(result.Error))
		}
		removed := desktopAction(desktop.app, protocol.DesktopRemoveLeafMessage{Cmd: protocol.CmdDesktopRemoveLeaf, DesktopID: other.ID, LeafID: "above"})
		if !removed.Success {
			t.Fatal(protocol.Deref(removed.Error))
		}
		desktop.requireRow("b/c")
		if got := firstTileCols(0); got != 0 {
			t.Fatalf("a cleared width came back as %d cols when the tile above left", got)
		}
	})
}

func TestWithTheShelfLayoutOffTheRowOperationsAreRefusedByName(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		app := w.App()
		injectAgent(t, w, "a")
		injectAgent(t, w, "b")
		profileID := app.SelectedProfile()
		desktop, paneA := viewProfile(t, w, profileID).paneOf(t, "a")
		_, paneB := viewProfile(t, w, profileID).paneOf(t, "b")
		other := createDesktop(app, profileID)
		for _, target := range []string{desktop.ID, other.ID} {
			id := uuid.NewString()
			refused := profileRequest(app, protocol.DesktopMoveLeafMessage{
				Cmd: protocol.CmdDesktopMoveLeaf, RequestID: id, SourceDesktopID: desktop.ID, TargetDesktopID: protocol.Ptr(target),
				LeafID: paneA, AnchorID: protocol.Ptr(paneB), Edge: protocol.Ptr(protocol.LayoutDockEdgeRight), WholeColumn: protocol.Ptr(true),
			}, id)
			if refused.Success || !strings.Contains(protocol.Deref(refused.Error), "shelf_layout_enabled") {
				t.Fatalf("whole_column to %s with the setting off answered %+v", target, refused)
			}
		}
		id := uuid.NewString()
		refused := profileRequest(app, protocol.DesktopSetWantedColsMessage{Cmd: protocol.CmdDesktopSetWantedCols, RequestID: id, DesktopID: desktop.ID, LeafID: paneA, Cols: 100}, id)
		if refused.Success || !strings.Contains(protocol.Deref(refused.Error), "shelf_layout_enabled") {
			t.Fatalf("desktop_set_wanted_cols with the setting off answered %+v", refused)
		}
	})
}
