package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setups"
)

func openSetupStore(t *testing.T) (*Store, func() *Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "attn.db")
	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	current := s
	t.Cleanup(func() { current.Close() })
	restart := func() *Store {
		t.Helper()
		if err := current.Close(); err != nil {
			t.Fatalf("closing store before restart: %v", err)
		}
		reopened, err := NewWithDB(dbPath)
		if err != nil {
			t.Fatalf("reopening store: %v", err)
		}
		current = reopened
		return reopened
	}
	return s, restart
}

func addSetupSession(t *testing.T, s *Store, id, setupID string) {
	t.Helper()
	now := string(protocol.TimestampNow())
	if err := s.AddChecked(&protocol.Session{
		ID: id, Label: id, Agent: protocol.SessionAgentCodex, Directory: "/tmp/project", WorkspaceID: "workspace-" + id,
		State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	}); err != nil {
		t.Fatalf("adding session %s: %v", id, err)
	}
	if setupID == "" {
		return
	}
	if err := s.AssignSessionSetup(id, setupID); err != nil {
		t.Fatalf("assigning session %s to setup %s: %v", id, setupID, err)
	}
}

func mustCreateSetup(t *testing.T, s *Store, name string) (setups.Setup, setups.Desktop) {
	t.Helper()
	setup, desktop, err := s.CreateSetup(name)
	if err != nil {
		t.Fatalf("CreateSetup(%q): %v", name, err)
	}
	return setup, desktop
}

func mustPlace(t *testing.T, s *Store, desktopID, sessionID string) (setups.Desktop, string) {
	t.Helper()
	desktop, err := s.GetDesktop(desktopID)
	if err != nil {
		t.Fatalf("GetDesktop(%s): %v", desktopID, err)
	}
	placed, paneID, err := s.PlaceSession(SessionPlacementRequest{
		DesktopID: desktopID, ExpectedRevision: desktop.Revision, SessionID: sessionID,
		Direction: layouttree.DirectionVertical, Title: sessionID,
	})
	if err != nil {
		t.Fatalf("PlaceSession(%s on %s): %v", sessionID, desktopID, err)
	}
	return placed, paneID
}

func wantCode(t *testing.T, err error, code setups.Code) *setups.Error {
	t.Helper()
	var setupErr *setups.Error
	if !errors.As(err, &setupErr) {
		t.Fatalf("error = %v, want a setups error with code %s", err, code)
	}
	if setupErr.Code != code {
		t.Fatalf("error code = %s (%s), want %s", setupErr.Code, setupErr.Message, code)
	}
	return setupErr
}

func assertStoredDesktopsHoldTheirInvariants(t *testing.T, s *Store, setupID string) {
	t.Helper()
	setup, desktops, err := s.SetupArrangement(setupID)
	if err != nil {
		t.Fatalf("SetupArrangement(%s): %v", setupID, err)
	}
	current := false
	sessions := make(map[string]string)
	for _, desktop := range desktops {
		if err := setups.CheckDesktop(desktop); err != nil {
			t.Fatalf("stored desktop broke an invariant: %v", err)
		}
		current = current || desktop.ID == setup.CurrentDesktopID
		for _, pane := range desktop.Panes {
			if other, dup := sessions[pane.SessionID]; dup {
				t.Fatalf("session %s is placed on desktops %s and %s", pane.SessionID, other, desktop.ID)
			}
			sessions[pane.SessionID] = desktop.ID
		}
	}
	if !setup.Deleted() && !current {
		t.Fatalf("setup %s names current desktop %q, which is not one of its desktops", setup.ID, setup.CurrentDesktopID)
	}
}

func TestSetupArrangementSurvivesRestart(t *testing.T) {
	s, restart := openSetupStore(t)
	setup, first := mustCreateSetup(t, s, "Default")
	if first.ShortcutSlot != 1 || setup.CurrentDesktopID != first.ID {
		t.Fatalf("new setup = %+v with desktop %+v, want its first desktop current in slot 1", setup, first)
	}
	_, second, err := s.CreateDesktop(setup.ID, "", 0, true)
	if err != nil {
		t.Fatalf("CreateDesktop: %v", err)
	}
	addSetupSession(t, s, "agent-a", setup.ID)
	addSetupSession(t, s, "agent-b", setup.ID)
	mustPlace(t, s, second.ID, "agent-a")
	placed, paneB := mustPlace(t, s, second.ID, "agent-b")
	ratioed, err := s.SetDesktopSplitRatio(second.ID, placed.Tree.SplitID, 0.3, placed.Revision)
	if err != nil {
		t.Fatalf("SetDesktopSplitRatio: %v", err)
	}
	paneA := layouttree.PaneIDs(ratioed.Tree)[0]
	if _, err := s.SetCurrentDesktop(setup.ID, second.ID); err != nil {
		t.Fatalf("SetCurrentDesktop: %v", err)
	}
	if _, _, err := s.SetActivePane(second.ID, paneA); err != nil {
		t.Fatalf("SetActivePane: %v", err)
	}
	if paneA == paneB {
		t.Fatalf("both agents landed in pane %s", paneA)
	}

	s = restart()
	gotSetup, desktops, err := s.SetupArrangement(setup.ID)
	if err != nil {
		t.Fatalf("SetupArrangement after restart: %v", err)
	}
	if gotSetup.CurrentDesktopID != second.ID {
		t.Fatalf("current desktop after restart = %s, want %s", gotSetup.CurrentDesktopID, second.ID)
	}
	if len(desktops) != 2 || desktops[1].ID != second.ID {
		t.Fatalf("desktops after restart = %+v, want the two created in order", desktops)
	}
	got := desktops[1]
	if got.ActivePaneID != paneA {
		t.Fatalf("active pane after restart = %s, want %s", got.ActivePaneID, paneA)
	}
	if got.ShortcutSlot != 2 {
		t.Fatalf("second desktop slot = %d, want the lowest free slot 2", got.ShortcutSlot)
	}
	if got.Tree.Ratio != 0.3 || got.Tree.RatioMode != layouttree.RatioModePreferred {
		t.Fatalf("split after restart = ratio %v mode %q, want the user's 0.3 kept as preferred", got.Tree.Ratio, got.Tree.RatioMode)
	}
	if !reflect.DeepEqual(got, ratioedWithActive(ratioed, paneA)) {
		t.Fatalf("desktop changed across restart:\n got: %+v\nwant: %+v", got, ratioedWithActive(ratioed, paneA))
	}
	assertStoredDesktopsHoldTheirInvariants(t, s, setup.ID)
}

func ratioedWithActive(desktop setups.Desktop, paneID string) setups.Desktop {
	desktop.ActivePaneID = paneID
	return desktop
}

func TestSelectionDoesNotStaleAStructuralEdit(t *testing.T) {
	s, _ := openSetupStore(t)
	setup, desktop := mustCreateSetup(t, s, "Default")
	addSetupSession(t, s, "agent-a", setup.ID)
	addSetupSession(t, s, "agent-b", setup.ID)
	placed, paneA := mustPlace(t, s, desktop.ID, "agent-a")

	if _, _, err := s.SetActivePane(desktop.ID, paneA); err != nil {
		t.Fatalf("SetActivePane: %v", err)
	}
	if _, err := s.SetCurrentDesktop(setup.ID, desktop.ID); err != nil {
		t.Fatalf("SetCurrentDesktop: %v", err)
	}
	after, err := s.GetSetup(setup.ID)
	if err != nil {
		t.Fatalf("GetSetup: %v", err)
	}
	if after.Revision != setup.Revision {
		t.Fatalf("setup revision moved from %d to %d on a selection change", setup.Revision, after.Revision)
	}
	if after.LastUsedAt == "" {
		t.Fatal("a selection did not record last_used_at")
	}
	if _, _, err := s.PlaceSession(SessionPlacementRequest{
		DesktopID: desktop.ID, ExpectedRevision: placed.Revision, SessionID: "agent-b", Direction: layouttree.DirectionVertical,
	}); err != nil {
		t.Fatalf("a split made against the revision seen before focusing was refused: %v", err)
	}
}

func TestActivePaneMustBelongToTheDesktop(t *testing.T) {
	s, _ := openSetupStore(t)
	setup, first := mustCreateSetup(t, s, "Default")
	_, second, err := s.CreateDesktop(setup.ID, "", 0, true)
	if err != nil {
		t.Fatalf("CreateDesktop: %v", err)
	}
	addSetupSession(t, s, "agent-a", setup.ID)
	_, paneA := mustPlace(t, s, first.ID, "agent-a")

	_, _, err = s.SetActivePane(second.ID, paneA)
	wantCode(t, err, setups.CodeNotFound)

	removed, err := s.RemoveSessionPlacement("agent-a")
	if err != nil || removed == nil {
		t.Fatalf("RemoveSessionPlacement = %+v, %v", removed, err)
	}
	if removed.ActivePaneID != "" || !layouttree.LayoutEmpty(removed.Tree) {
		t.Fatalf("emptied desktop = %+v, want no tree and no active pane", removed)
	}
	if session := s.Get("agent-a"); session == nil {
		t.Fatal("removing a placement closed the agent")
	}
	assertStoredDesktopsHoldTheirInvariants(t, s, setup.ID)
}

func TestAnAgentHasAtMostOnePlacementAcrossTheDaemon(t *testing.T) {
	s, _ := openSetupStore(t)
	setup, first := mustCreateSetup(t, s, "Default")
	_, second, err := s.CreateDesktop(setup.ID, "", 0, true)
	if err != nil {
		t.Fatalf("CreateDesktop: %v", err)
	}
	addSetupSession(t, s, "agent-a", setup.ID)
	mustPlace(t, s, first.ID, "agent-a")

	target, _ := s.GetDesktop(second.ID)
	_, _, err = s.PlaceSession(SessionPlacementRequest{DesktopID: second.ID, ExpectedRevision: target.Revision, SessionID: "agent-a", Direction: layouttree.DirectionVertical})
	wantCode(t, err, setups.CodeAlreadyPlaced)

	source, _ := s.GetDesktop(first.ID)
	_, _, err = s.PlaceSession(SessionPlacementRequest{DesktopID: first.ID, ExpectedRevision: source.Revision, SessionID: "agent-a", Direction: layouttree.DirectionVertical})
	wantCode(t, err, setups.CodeAlreadyPlaced)

	_, _, err = s.PlaceSession(SessionPlacementRequest{DesktopID: second.ID, ExpectedRevision: target.Revision, SessionID: "ghost", Direction: layouttree.DirectionVertical})
	wantCode(t, err, setups.CodeNotFound)

	unchanged, _ := s.GetDesktop(second.ID)
	if unchanged.Revision != target.Revision || len(unchanged.Panes) != 0 {
		t.Fatalf("a refused placement changed desktop %+v", unchanged)
	}
	assertStoredDesktopsHoldTheirInvariants(t, s, setup.ID)
}

func TestLayoutWritesNeverChangeMembership(t *testing.T) {
	s, _ := openSetupStore(t)
	work, workDesktop := mustCreateSetup(t, s, "Work")
	home, homeDesktop := mustCreateSetup(t, s, "Home")
	addSetupSession(t, s, "work-agent", work.ID)
	addSetupSession(t, s, "home-agent", home.ID)
	addSetupSession(t, s, "unowned-agent", "")
	placedWork, workPane := mustPlace(t, s, workDesktop.ID, "work-agent")

	_, _, err := s.PlaceSession(SessionPlacementRequest{DesktopID: workDesktop.ID, ExpectedRevision: placedWork.Revision, SessionID: "home-agent", Direction: layouttree.DirectionVertical})
	wantCode(t, err, setups.CodeCrossSetup)
	_, _, err = s.PlaceSession(SessionPlacementRequest{DesktopID: workDesktop.ID, ExpectedRevision: placedWork.Revision, SessionID: "unowned-agent", Direction: layouttree.DirectionVertical})
	wantCode(t, err, setups.CodeCrossSetup)

	_, err = s.UpdateDesktopArrangement(workDesktop.ID, placedWork.Revision, func(desktop setups.Desktop) (setups.Desktop, error) {
		desktop.Panes[0].SessionID = "home-agent"
		return desktop, nil
	})
	wantCode(t, err, setups.CodeCrossSetup)

	_, err = s.MoveLeaf(LeafMoveRequest{
		SourceDesktopID: workDesktop.ID, TargetDesktopID: homeDesktop.ID, LeafID: workPane,
		Direction: layouttree.DirectionVertical, ExpectedSourceRevision: placedWork.Revision, ExpectedTargetRevision: homeDesktop.Revision,
	})
	wantCode(t, err, setups.CodeCrossSetup)

	if setupID, _ := s.SessionSetupID("work-agent"); setupID != work.ID {
		t.Fatalf("work-agent setup = %s, want %s", setupID, work.ID)
	}
	if err := s.AssignSessionSetup("work-agent", home.ID); err == nil {
		t.Fatal("assigning a second setup to a session succeeded; membership changes only through a move")
	}
}

func TestCorruptArrangementsAreRefusedNotNormalized(t *testing.T) {
	s, _ := openSetupStore(t)
	setup, desktop := mustCreateSetup(t, s, "Default")
	addSetupSession(t, s, "agent-a", setup.ID)
	placed, paneA := mustPlace(t, s, desktop.ID, "agent-a")

	cases := []struct {
		name    string
		corrupt func(setups.Desktop) setups.Desktop
	}{
		{"leaf without a pane row", func(d setups.Desktop) setups.Desktop {
			d.Tree, _ = layouttree.Split(d.Tree, paneA, "pane-orphan", "split-1", layouttree.DirectionVertical, 0.5)
			return d
		}},
		{"pane row without a leaf", func(d setups.Desktop) setups.Desktop {
			d.Panes = append(d.Panes, setups.Pane{PaneID: "pane-extra", Kind: setups.PaneKindAgent, SessionID: "agent-a", Status: setups.PaneStatusReady})
			return d
		}},
		{"ratio outside the open interval", func(d setups.Desktop) setups.Desktop {
			d.Tree = layouttree.Node{Type: "split", SplitID: "split-1", Direction: layouttree.DirectionVertical, Ratio: 1.5, Children: []layouttree.Node{d.Tree, {Type: "tile", TileID: "tile-1", TileKind: "markdown"}}}
			return d
		}},
		{"split with one child", func(d setups.Desktop) setups.Desktop {
			d.Tree = layouttree.Node{Type: "split", SplitID: "split-1", Direction: layouttree.DirectionVertical, Ratio: 0.5, Children: []layouttree.Node{d.Tree}}
			return d
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.UpdateDesktopArrangement(desktop.ID, placed.Revision, func(d setups.Desktop) (setups.Desktop, error) {
				return tc.corrupt(d), nil
			})
			wantCode(t, err, setups.CodeInvalid)
			got, _ := s.GetDesktop(desktop.ID)
			if !reflect.DeepEqual(got, placed) {
				t.Fatalf("a refused write changed the desktop:\n got: %+v\nwant: %+v", got, placed)
			}
		})
	}
}

func TestMoveBetweenDesktopsCommitsSourceAndTargetTogether(t *testing.T) {
	s, restart := openSetupStore(t)
	setup, first := mustCreateSetup(t, s, "Default")
	_, second, err := s.CreateDesktop(setup.ID, "", 0, true)
	if err != nil {
		t.Fatalf("CreateDesktop: %v", err)
	}
	for _, id := range []string{"agent-a", "agent-b", "agent-c"} {
		addSetupSession(t, s, id, setup.ID)
	}
	mustPlace(t, s, first.ID, "agent-a")
	source, paneB := mustPlace(t, s, first.ID, "agent-b")
	target, paneC := mustPlace(t, s, second.ID, "agent-c")

	_, err = s.MoveLeaf(LeafMoveRequest{
		SourceDesktopID: first.ID, TargetDesktopID: second.ID, LeafID: paneB, AnchorID: "pane-that-is-not-there",
		Direction: layouttree.DirectionHorizontal, ExpectedSourceRevision: source.Revision, ExpectedTargetRevision: target.Revision,
	})
	wantCode(t, err, setups.CodeInvalid)
	_, err = s.MoveLeaf(LeafMoveRequest{
		SourceDesktopID: first.ID, TargetDesktopID: second.ID, LeafID: paneB, AnchorID: paneC,
		Direction: layouttree.DirectionHorizontal, ExpectedSourceRevision: source.Revision, ExpectedTargetRevision: target.Revision - 1,
	})
	wantCode(t, err, setups.CodeStaleRevision)
	for _, want := range []setups.Desktop{source, target} {
		got, _ := s.GetDesktop(want.ID)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("a refused move changed desktop %s:\n got: %+v\nwant: %+v", want.ID, got, want)
		}
	}

	moved, err := s.MoveLeaf(LeafMoveRequest{
		SourceDesktopID: first.ID, TargetDesktopID: second.ID, LeafID: paneB, AnchorID: paneC,
		Direction: layouttree.DirectionHorizontal, LeafShare: 0.25, ExpectedSourceRevision: source.Revision, ExpectedTargetRevision: target.Revision,
	})
	if err != nil {
		t.Fatalf("MoveLeaf: %v", err)
	}
	if moved.FinalLeafID != paneB {
		t.Fatalf("pane id changed from %s to %s across the move", paneB, moved.FinalLeafID)
	}
	if moved.Source.Revision != source.Revision+1 || moved.Target.Revision != target.Revision+1 {
		t.Fatalf("revisions after move = %d and %d, want both bumped once", moved.Source.Revision, moved.Target.Revision)
	}

	s = restart()
	placement, found, err := s.SessionPlacement("agent-b")
	if err != nil || !found || placement.DesktopID != second.ID || placement.PaneID != paneB {
		t.Fatalf("agent-b placement after restart = %+v found=%v err=%v, want pane %s on %s", placement, found, err, paneB, second.ID)
	}
	gotTarget, _ := s.GetDesktop(second.ID)
	if gotTarget.ActivePaneID != paneB {
		t.Fatalf("target active pane = %s, want the moved pane %s", gotTarget.ActivePaneID, paneB)
	}
	if gotTarget.Tree.Ratio != 0.75 || !gotTarget.Tree.RatioLocked {
		t.Fatalf("target split = ratio %v locked %v, want the dropped leaf to keep its 0.25 share", gotTarget.Tree.Ratio, gotTarget.Tree.RatioLocked)
	}
	gotSource, _ := s.GetDesktop(first.ID)
	if ids := layouttree.PaneIDs(gotSource.Tree); len(ids) != 1 || gotSource.ActivePaneID != ids[0] {
		t.Fatalf("source after move = panes %v active %q, want its one remaining pane active", ids, gotSource.ActivePaneID)
	}
	if session := s.Get("agent-b"); session == nil || session.ID != "agent-b" {
		t.Fatalf("session identity changed across the move: %+v", session)
	}
	assertStoredDesktopsHoldTheirInvariants(t, s, setup.ID)
}

func TestStaleRevisionFromASecondWriterIsRefused(t *testing.T) {
	s, _ := openSetupStore(t)
	setup, desktop := mustCreateSetup(t, s, "Default")
	addSetupSession(t, s, "agent-a", setup.ID)
	addSetupSession(t, s, "agent-b", setup.ID)
	seenByBoth := desktop.Revision

	winner, _, err := s.PlaceSession(SessionPlacementRequest{DesktopID: desktop.ID, ExpectedRevision: seenByBoth, SessionID: "agent-a", Direction: layouttree.DirectionVertical})
	if err != nil {
		t.Fatalf("first writer: %v", err)
	}
	_, _, err = s.PlaceSession(SessionPlacementRequest{DesktopID: desktop.ID, ExpectedRevision: seenByBoth, SessionID: "agent-b", Direction: layouttree.DirectionVertical})
	stale := wantCode(t, err, setups.CodeStaleRevision)
	if want := setups.Stale("desktop", desktop.ID, seenByBoth, winner.Revision).Message; stale.Message != want {
		t.Fatalf("stale message = %q, want %q", stale.Message, want)
	}
	if _, _, err := s.PlaceSession(SessionPlacementRequest{DesktopID: desktop.ID, ExpectedRevision: winner.Revision, SessionID: "agent-b", Direction: layouttree.DirectionVertical}); err != nil {
		t.Fatalf("the loser re-read revision %d and was still refused: %v", winner.Revision, err)
	}

	_, err = s.RenameSetup(setup.ID, "Renamed", setup.Revision+7)
	wantCode(t, err, setups.CodeStaleRevision)
	_, err = s.RenameDesktop(desktop.ID, "Main", seenByBoth)
	wantCode(t, err, setups.CodeStaleRevision)
	_, err = s.DeleteDesktop(desktop.ID, seenByBoth)
	wantCode(t, err, setups.CodeStaleRevision)
}

func TestSetupIDsSurviveRenameAndDeletedNamesAreReusable(t *testing.T) {
	s, restart := openSetupStore(t)
	work, _ := mustCreateSetup(t, s, "Work")
	home, _ := mustCreateSetup(t, s, "Home")
	addSetupSession(t, s, "live-agent", work.ID)
	addSetupSession(t, s, "closed-agent", work.ID)
	if _, err := s.CloseSession("closed-agent", SessionClose{}, time.Now()); err != nil {
		t.Fatalf("closing closed-agent: %v", err)
	}

	_, _, err := s.CreateSetup("  Home ")
	wantCode(t, err, setups.CodeNameTaken)
	_, err = s.RenameSetup(work.ID, "Home", work.Revision)
	wantCode(t, err, setups.CodeNameTaken)

	renamed, err := s.RenameSetup(work.ID, "Office", work.Revision)
	if err != nil {
		t.Fatalf("RenameSetup: %v", err)
	}
	if renamed.ID != work.ID || renamed.Revision != work.Revision+1 {
		t.Fatalf("renamed setup = %+v, want the same id at the next revision", renamed)
	}
	if setupID, _ := s.SessionSetupID("live-agent"); setupID != work.ID {
		t.Fatalf("live-agent setup after rename = %s, want %s", setupID, work.ID)
	}

	_, err = s.DeleteSetup(work.ID, renamed.Revision, "")
	wantCode(t, err, setups.CodeInvalid)
	_, err = s.DeleteSetup(work.ID, renamed.Revision, work.ID)
	wantCode(t, err, setups.CodeDestinationSame)
	deletion, err := s.DeleteSetup(work.ID, renamed.Revision, home.ID)
	if err != nil {
		t.Fatalf("DeleteSetup: %v", err)
	}
	if !reflect.DeepEqual(deletion.MovedSessionIDs, []string{"live-agent"}) {
		t.Fatalf("moved sessions = %v, want only the live agent", deletion.MovedSessionIDs)
	}
	if setupID, _ := s.SessionSetupID("closed-agent"); setupID != work.ID {
		t.Fatalf("closed-agent setup = %s, want its history kept at %s", setupID, work.ID)
	}
	_, err = s.DeleteSetup(home.ID, home.Revision, work.ID)
	wantCode(t, err, setups.CodeLastSetup)

	s = restart()
	reborn, _, err := s.CreateSetup("Office")
	if err != nil {
		t.Fatalf("reusing a deleted setup's name: %v", err)
	}
	if reborn.ID == work.ID {
		t.Fatalf("setup id %s was reused for a new setup", work.ID)
	}
	tombstone, err := s.GetSetup(work.ID)
	if err != nil || !tombstone.Deleted() {
		t.Fatalf("deleted setup = %+v, %v; want it kept as history", tombstone, err)
	}
	live, _ := s.ListSetups(false)
	var names []string
	for _, setup := range live {
		names = append(names, setup.Name)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"Home", "Office"}) {
		t.Fatalf("live setups = %v, want Home and the new Office", names)
	}
	_, err = s.RenameSetup(work.ID, "Back", tombstone.Revision)
	wantCode(t, err, setups.CodeSetupDeleted)
}

func TestMovingAnAgentToAnotherSetupRemovesItsPlacementOnly(t *testing.T) {
	s, _ := openSetupStore(t)
	work, workDesktop := mustCreateSetup(t, s, "Work")
	home, _ := mustCreateSetup(t, s, "Home")
	addSetupSession(t, s, "agent-a", work.ID)
	addSetupSession(t, s, "agent-b", work.ID)
	mustPlace(t, s, workDesktop.ID, "agent-a")
	before, _ := mustPlace(t, s, workDesktop.ID, "agent-b")

	move, err := s.MoveSessionToSetup("agent-b", home.ID)
	if err != nil {
		t.Fatalf("MoveSessionToSetup: %v", err)
	}
	if move.FromSetupID != work.ID || move.SourceDesktop == nil || move.SourceDesktop.Revision != before.Revision+1 {
		t.Fatalf("move = %+v, want the source desktop rewritten once", move)
	}
	if _, found, _ := s.SessionPlacement("agent-b"); found {
		t.Fatal("agent-b kept a placement after changing setups")
	}
	if setupID, _ := s.SessionSetupID("agent-b"); setupID != home.ID {
		t.Fatalf("agent-b setup = %s, want %s", setupID, home.ID)
	}
	if session := s.Get("agent-b"); session == nil {
		t.Fatal("moving setups closed the agent")
	}
	_, err = s.MoveSessionToSetup("agent-b", home.ID)
	wantCode(t, err, setups.CodeDestinationSame)
	assertStoredDesktopsHoldTheirInvariants(t, s, work.ID)
}

func TestDeletingADesktopUnplacesItsAgentsAndKeepsSlots(t *testing.T) {
	s, _ := openSetupStore(t)
	setup, first := mustCreateSetup(t, s, "Default")
	_, second, _ := s.CreateDesktop(setup.ID, "", 0, true)
	_, third, _ := s.CreateDesktop(setup.ID, "", 0, true)
	addSetupSession(t, s, "agent-a", setup.ID)
	mustPlace(t, s, second.ID, "agent-a")
	if _, err := s.SetCurrentDesktop(setup.ID, second.ID); err != nil {
		t.Fatalf("SetCurrentDesktop: %v", err)
	}
	_, _, err := s.CreateDesktop(setup.ID, "", 3, false)
	wantCode(t, err, setups.CodeSlotTaken)

	current, _ := s.GetDesktop(second.ID)
	deletion, err := s.DeleteDesktop(second.ID, current.Revision)
	if err != nil {
		t.Fatalf("DeleteDesktop: %v", err)
	}
	if !reflect.DeepEqual(deletion.UnplacedSessionID, []string{"agent-a"}) || s.Get("agent-a") == nil {
		t.Fatalf("deletion = %+v, want agent-a unplaced and still open", deletion)
	}
	if deletion.Setup.CurrentDesktopID != third.ID {
		t.Fatalf("current desktop after deleting it = %s, want the next one %s", deletion.Setup.CurrentDesktopID, third.ID)
	}
	keptThird, _ := s.GetDesktop(third.ID)
	if keptThird.ShortcutSlot != 3 {
		t.Fatalf("third desktop slot = %d after deleting slot 2, want slots never renumbered", keptThird.ShortcutSlot)
	}
	_, refill, err := s.CreateDesktop(setup.ID, "", 0, true)
	if err != nil || refill.ShortcutSlot != 2 {
		t.Fatalf("new desktop = %+v, %v; want it to take the freed slot 2", refill, err)
	}
	moved, err := s.ReorderDesktop(refill.ID, "", first.ID, refill.Revision)
	if err != nil {
		t.Fatalf("ReorderDesktop: %v", err)
	}
	_, desktops, _ := s.SetupArrangement(setup.ID)
	if desktops[0].ID != moved.ID {
		t.Fatalf("first desktop = %s, want the reordered %s", desktops[0].ID, moved.ID)
	}

	only, onlyDesktop := mustCreateSetup(t, s, "Solo")
	_, err = s.DeleteDesktop(onlyDesktop.ID, onlyDesktop.Revision)
	wantCode(t, err, setups.CodeLastDesktop)
	_ = only
}

func TestMostRecentlyUsedSetupFollowsSelection(t *testing.T) {
	s, restart := openSetupStore(t)
	work, _ := mustCreateSetup(t, s, "Work")
	home, homeDesktop := mustCreateSetup(t, s, "Home")
	if _, _, err := s.SelectSetup(work.ID); err != nil {
		t.Fatalf("SelectSetup: %v", err)
	}
	if _, err := s.SetCurrentDesktop(home.ID, homeDesktop.ID); err != nil {
		t.Fatalf("SetCurrentDesktop: %v", err)
	}
	s = restart()
	recent, err := s.MostRecentlyUsedSetup()
	if err != nil || recent.ID != home.ID {
		t.Fatalf("most recently used setup = %+v, %v; want %s", recent, err, home.ID)
	}
}

func TestSetupMigrationStateIsRevisioned(t *testing.T) {
	s, restart := openSetupStore(t)
	if _, found, err := s.GetSetupMigration(); err != nil || found {
		t.Fatalf("fresh migration state found=%v err=%v, want none", found, err)
	}
	first, err := s.SaveSetupMigration(setups.MigrationState{SchemaVersion: 150, Phase: "placement_required", ImportedGroups: `[{"id":"g1"}]`}, 0)
	if err != nil {
		t.Fatalf("SaveSetupMigration: %v", err)
	}
	first.Draft = `{"g1":"keep"}`
	second, err := s.SaveSetupMigration(first, first.Revision)
	if err != nil {
		t.Fatalf("saving a draft: %v", err)
	}
	_, err = s.SaveSetupMigration(first, first.Revision)
	wantCode(t, err, setups.CodeStaleRevision)

	s = restart()
	got, found, err := s.GetSetupMigration()
	if err != nil || !found || !reflect.DeepEqual(got, second) {
		t.Fatalf("migration state after restart = %+v found=%v err=%v, want %+v", got, found, err, second)
	}
}
