package daemon_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestMovingAnAgentWithItsSameDesktopDelegates(t *testing.T) {
	for _, withDelegates := range []bool{false, true} {
		t.Run(map[bool]string{false: "single pane", true: "with delegates"}[withDelegates], func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex)
			app, cli := w.App(), w.Client()
			cwd := w.Path("dispatcher")
			root, _, _ := w.RequestSpawn(app, fakeagent.Codex, cwd)
			shell, _, _ := w.RequestSpawn(app, shellHarness, w.Path("split-shell"), func(msg *protocol.SpawnSessionMessage) { msg.SpawnedFrom = protocol.Ptr(root.ID) })
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
			first := delegate(root.ID, "first")
			second := delegate(root.ID, "second")
			nested := delegate(first, "nested")
			elsewhere := delegate(root.ID, "elsewhere")
			unplaced := delegate(root.ID, "unplaced")
			behindElsewhere := delegate(elsewhere, "behind-elsewhere")
			source, _ := placedPane(t, w, string(unplaced))
			_, unplacedPane := placedPane(t, w, string(unplaced))
			mustProfileRequest(app, protocol.DesktopRemoveLeafMessage{Cmd: protocol.CmdDesktopRemoveLeaf, RequestID: "unplace", DesktopID: source.ID, LeafID: unplacedPane}, "unplace")
			target := createDesktop(app, app.SelectedProfile())
			move := func(session protocol.SessionID, target protocol.Desktop, flag bool, id string) protocol.ProfileActionResultMessage {
				t.Helper()
				source, pane := placedPane(t, w, string(session))
				return mustProfileRequest(app, protocol.DesktopMoveLeafMessage{
					Cmd: protocol.CmdDesktopMoveLeaf, RequestID: id, SourceDesktopID: source.ID, TargetDesktopID: protocol.Ptr(target.ID),
					LeafID: pane, Edge: protocol.Ptr(protocol.LayoutDockEdgeRight),
					WithDelegates: protocol.Ptr(flag),
				}, id)
			}
			move(elsewhere, target, false, "elsewhere")
			source, _ = placedPane(t, w, string(root.ID))
			target, _ = placedPane(t, w, string(elsewhere))
			result := move(root.ID, target, withDelegates, "move-root")
			var finalSource, finalTarget protocol.Desktop
			for _, desktop := range result.Desktops {
				if desktop.ID == source.ID {
					finalSource = desktop
				}
				if desktop.ID == target.ID {
					finalTarget = desktop
				}
			}
			if finalSource.Revision != source.Revision+1 || finalTarget.Revision != target.Revision+1 {
				t.Fatalf("group move revisions source %d->%d target %d->%d; want one update each", source.Revision, finalSource.Revision, target.Revision, finalTarget.Revision)
			}
			paneFor := func(desktop protocol.Desktop, session protocol.SessionID) string {
				for _, pane := range desktop.Panes {
					if pane.SessionID == session {
						return pane.PaneID
					}
				}
				return ""
			}
			for _, session := range []protocol.SessionID{first, second, nested} {
				destination := finalSource
				if withDelegates {
					destination = finalTarget
				}
				if paneFor(destination, session) == "" {
					t.Errorf("delegate %s missing from %s", session, destination.ID)
				}
			}
			for _, session := range []protocol.SessionID{shell.ID, behindElsewhere} {
				if paneFor(finalSource, session) == "" {
					t.Errorf("excluded session %s left the source", session)
				}
			}

			if paneFor(finalTarget, elsewhere) == "" {
				t.Error("other-desktop delegate moved")
			}
			if paneFor(finalSource, unplaced) != "" || paneFor(finalTarget, unplaced) != "" {
				t.Error("unplaced delegate acquired a placement")
			}
			if finalTarget.ActivePaneID != paneFor(finalTarget, root.ID) {
				t.Error("target did not keep root active")
			}
			if withDelegates {
				tree := desktopTree(t, finalTarget)
				want := []string{paneFor(finalTarget, elsewhere), paneFor(finalTarget, root.ID)}
				for _, leaf := range desktopTree(t, source).leafIDs() {
					for _, session := range []protocol.SessionID{first, second} {
						if paneFor(source, session) == leaf {
							want = append(want, paneFor(finalTarget, session))
							if session == first {
								want = append(want, paneFor(finalTarget, nested))
							}
						}
					}
				}
				if !slices.Equal(tree.leafIDs(), want) {
					t.Fatalf("target leaf order %v; want %v", tree.leafIDs(), want)
				}
				parent, _, ok := tree.splitHolding(paneFor(finalTarget, nested))
				if !ok || parent.Direction != "vertical" || !slices.Equal(parent.leafIDs(), []string{paneFor(finalTarget, first), paneFor(finalTarget, nested)}) {
					t.Fatalf("nested delegate did not land beside its dispatcher: %s", finalTarget.TreeJson)
				}
			}
		})
	}
}
