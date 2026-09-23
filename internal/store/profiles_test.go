package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

func openProfileStore(t *testing.T) (*Store, func() *Store) {
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

func addProfileSession(t *testing.T, s *Store, id, profileID string) {
	t.Helper()
	now := string(protocol.TimestampNow())
	if err := s.AddChecked(&protocol.Session{
		ID: id, Label: id, Agent: protocol.SessionAgentCodex, Directory: "/tmp/project", WorkspaceID: "workspace-" + id,
		State: protocol.SessionStateIdle, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	}); err != nil {
		t.Fatalf("adding session %s: %v", id, err)
	}
	if profileID == "" {
		return
	}
	if err := s.AssignSessionProfile(id, profileID); err != nil {
		t.Fatalf("assigning session %s to profile %s: %v", id, profileID, err)
	}
}

func mustCreateProfile(t *testing.T, s *Store, name string) (profiles.Profile, profiles.Desktop) {
	t.Helper()
	profile, desktop, err := s.CreateProfile(name)
	if err != nil {
		t.Fatalf("CreateProfile(%q): %v", name, err)
	}
	return profile, desktop
}

func mustPlace(t *testing.T, s *Store, desktopID, sessionID string) (profiles.Desktop, string) {
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

func wantCode(t *testing.T, err error, code profiles.Code) *profiles.Error {
	t.Helper()
	var profileErr *profiles.Error
	if !errors.As(err, &profileErr) {
		t.Fatalf("error = %v, want a profiles error with code %s", err, code)
	}
	if profileErr.Code != code {
		t.Fatalf("error code = %s (%s), want %s", profileErr.Code, profileErr.Message, code)
	}
	return profileErr
}

func assertStoredDesktopsHoldTheirInvariants(t *testing.T, s *Store, profileID string) {
	t.Helper()
	profile, desktops, err := s.ProfileArrangement(profileID)
	if err != nil {
		t.Fatalf("ProfileArrangement(%s): %v", profileID, err)
	}
	current := false
	sessions := make(map[string]string)
	for _, desktop := range desktops {
		if err := profiles.CheckDesktop(desktop); err != nil {
			t.Fatalf("stored desktop broke an invariant: %v", err)
		}
		current = current || desktop.ID == profile.CurrentDesktopID
		for _, pane := range desktop.Panes {
			if other, dup := sessions[pane.SessionID]; dup {
				t.Fatalf("session %s is placed on desktops %s and %s", pane.SessionID, other, desktop.ID)
			}
			sessions[pane.SessionID] = desktop.ID
		}
	}
	if !profile.Deleted() && !current {
		t.Fatalf("profile %s names current desktop %q, which is not one of its desktops", profile.ID, profile.CurrentDesktopID)
	}
}

func TestProfileArrangementSurvivesRestart(t *testing.T) {
	s, restart := openProfileStore(t)
	profile, first := mustCreateProfile(t, s, "Main")
	if first.ShortcutSlot != 1 || profile.CurrentDesktopID != first.ID {
		t.Fatalf("new profile = %+v with desktop %+v, want its first desktop current in slot 1", profile, first)
	}
	_, second, err := s.CreateDesktop(profile.ID, "", 0, true)
	if err != nil {
		t.Fatalf("CreateDesktop: %v", err)
	}
	addProfileSession(t, s, "agent-a", profile.ID)
	addProfileSession(t, s, "agent-b", profile.ID)
	mustPlace(t, s, second.ID, "agent-a")
	placed, paneB := mustPlace(t, s, second.ID, "agent-b")
	ratioed, err := s.SetDesktopSplitRatio(second.ID, placed.Tree.SplitID, 0.3, placed.Revision)
	if err != nil {
		t.Fatalf("SetDesktopSplitRatio: %v", err)
	}
	paneA := layouttree.PaneIDs(ratioed.Tree)[0]
	if _, err := s.SetCurrentDesktop(profile.ID, second.ID); err != nil {
		t.Fatalf("SetCurrentDesktop: %v", err)
	}
	if _, _, err := s.SetActivePane(second.ID, paneA); err != nil {
		t.Fatalf("SetActivePane: %v", err)
	}
	if paneA == paneB {
		t.Fatalf("both agents landed in pane %s", paneA)
	}

	s = restart()
	gotProfile, desktops, err := s.ProfileArrangement(profile.ID)
	if err != nil {
		t.Fatalf("ProfileArrangement after restart: %v", err)
	}
	if gotProfile.CurrentDesktopID != second.ID {
		t.Fatalf("current desktop after restart = %s, want %s", gotProfile.CurrentDesktopID, second.ID)
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
	assertStoredDesktopsHoldTheirInvariants(t, s, profile.ID)
}

func ratioedWithActive(desktop profiles.Desktop, paneID string) profiles.Desktop {
	desktop.ActivePaneID = paneID
	return desktop
}

func TestSelectionDoesNotStaleAStructuralEdit(t *testing.T) {
	s, _ := openProfileStore(t)
	profile, desktop := mustCreateProfile(t, s, "Main")
	addProfileSession(t, s, "agent-a", profile.ID)
	addProfileSession(t, s, "agent-b", profile.ID)
	placed, paneA := mustPlace(t, s, desktop.ID, "agent-a")

	if _, _, err := s.SetActivePane(desktop.ID, paneA); err != nil {
		t.Fatalf("SetActivePane: %v", err)
	}
	if _, err := s.SetCurrentDesktop(profile.ID, desktop.ID); err != nil {
		t.Fatalf("SetCurrentDesktop: %v", err)
	}
	after, err := s.GetProfile(profile.ID)
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if after.Revision != profile.Revision {
		t.Fatalf("profile revision moved from %d to %d on a selection change", profile.Revision, after.Revision)
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
	s, _ := openProfileStore(t)
	profile, first := mustCreateProfile(t, s, "Main")
	_, second, err := s.CreateDesktop(profile.ID, "", 0, true)
	if err != nil {
		t.Fatalf("CreateDesktop: %v", err)
	}
	addProfileSession(t, s, "agent-a", profile.ID)
	_, paneA := mustPlace(t, s, first.ID, "agent-a")

	_, _, err = s.SetActivePane(second.ID, paneA)
	wantCode(t, err, profiles.CodeNotFound)

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
	assertStoredDesktopsHoldTheirInvariants(t, s, profile.ID)
}

func TestAnAgentHasAtMostOnePlacementAcrossTheDaemon(t *testing.T) {
	s, _ := openProfileStore(t)
	profile, first := mustCreateProfile(t, s, "Main")
	_, second, err := s.CreateDesktop(profile.ID, "", 0, true)
	if err != nil {
		t.Fatalf("CreateDesktop: %v", err)
	}
	addProfileSession(t, s, "agent-a", profile.ID)
	mustPlace(t, s, first.ID, "agent-a")

	target, _ := s.GetDesktop(second.ID)
	_, _, err = s.PlaceSession(SessionPlacementRequest{DesktopID: second.ID, ExpectedRevision: target.Revision, SessionID: "agent-a", Direction: layouttree.DirectionVertical})
	wantCode(t, err, profiles.CodeAlreadyPlaced)

	source, _ := s.GetDesktop(first.ID)
	_, _, err = s.PlaceSession(SessionPlacementRequest{DesktopID: first.ID, ExpectedRevision: source.Revision, SessionID: "agent-a", Direction: layouttree.DirectionVertical})
	wantCode(t, err, profiles.CodeAlreadyPlaced)

	_, _, err = s.PlaceSession(SessionPlacementRequest{DesktopID: second.ID, ExpectedRevision: target.Revision, SessionID: "ghost", Direction: layouttree.DirectionVertical})
	wantCode(t, err, profiles.CodeNotFound)

	unchanged, _ := s.GetDesktop(second.ID)
	if unchanged.Revision != target.Revision || len(unchanged.Panes) != 0 {
		t.Fatalf("a refused placement changed desktop %+v", unchanged)
	}
	assertStoredDesktopsHoldTheirInvariants(t, s, profile.ID)
}

func TestLayoutWritesNeverChangeMembership(t *testing.T) {
	s, _ := openProfileStore(t)
	work, workDesktop := mustCreateProfile(t, s, "Work")
	home, homeDesktop := mustCreateProfile(t, s, "Home")
	addProfileSession(t, s, "work-agent", work.ID)
	addProfileSession(t, s, "home-agent", home.ID)
	addProfileSession(t, s, "unowned-agent", "")
	placedWork, workPane := mustPlace(t, s, workDesktop.ID, "work-agent")

	_, _, err := s.PlaceSession(SessionPlacementRequest{DesktopID: workDesktop.ID, ExpectedRevision: placedWork.Revision, SessionID: "home-agent", Direction: layouttree.DirectionVertical})
	wantCode(t, err, profiles.CodeCrossProfile)
	_, _, err = s.PlaceSession(SessionPlacementRequest{DesktopID: workDesktop.ID, ExpectedRevision: placedWork.Revision, SessionID: "unowned-agent", Direction: layouttree.DirectionVertical})
	wantCode(t, err, profiles.CodeCrossProfile)

	_, err = s.UpdateDesktopArrangement(workDesktop.ID, placedWork.Revision, func(desktop profiles.Desktop) (profiles.Desktop, error) {
		desktop.Panes[0].SessionID = "home-agent"
		return desktop, nil
	})
	wantCode(t, err, profiles.CodeCrossProfile)

	_, err = s.MoveLeaf(LeafMoveRequest{
		SourceDesktopID: workDesktop.ID, TargetDesktopID: homeDesktop.ID, LeafID: workPane,
		Direction: layouttree.DirectionVertical, ExpectedSourceRevision: placedWork.Revision, ExpectedTargetRevision: homeDesktop.Revision,
	})
	wantCode(t, err, profiles.CodeCrossProfile)

	if profileID, _ := s.SessionProfileID("work-agent"); profileID != work.ID {
		t.Fatalf("work-agent profile = %s, want %s", profileID, work.ID)
	}
	if err := s.AssignSessionProfile("work-agent", home.ID); err == nil {
		t.Fatal("assigning a second profile to a session succeeded; membership changes only through a move")
	}
}

func TestCorruptArrangementsAreRefusedNotNormalized(t *testing.T) {
	s, _ := openProfileStore(t)
	profile, desktop := mustCreateProfile(t, s, "Main")
	addProfileSession(t, s, "agent-a", profile.ID)
	placed, paneA := mustPlace(t, s, desktop.ID, "agent-a")

	cases := []struct {
		name    string
		corrupt func(profiles.Desktop) profiles.Desktop
	}{
		{"leaf without a pane row", func(d profiles.Desktop) profiles.Desktop {
			d.Tree, _ = layouttree.Split(d.Tree, paneA, "pane-orphan", "split-1", layouttree.DirectionVertical, 0.5)
			return d
		}},
		{"pane row without a leaf", func(d profiles.Desktop) profiles.Desktop {
			d.Panes = append(d.Panes, profiles.Pane{PaneID: "pane-extra", Kind: profiles.PaneKindAgent, SessionID: "agent-a", Status: profiles.PaneStatusReady})
			return d
		}},
		{"ratio outside the open interval", func(d profiles.Desktop) profiles.Desktop {
			d.Tree = layouttree.Node{Type: "split", SplitID: "split-1", Direction: layouttree.DirectionVertical, Ratio: 1.5, Children: []layouttree.Node{d.Tree, {Type: "tile", TileID: "tile-1", TileKind: "markdown"}}}
			return d
		}},
		{"split with one child", func(d profiles.Desktop) profiles.Desktop {
			d.Tree = layouttree.Node{Type: "split", SplitID: "split-1", Direction: layouttree.DirectionVertical, Ratio: 0.5, Children: []layouttree.Node{d.Tree}}
			return d
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.UpdateDesktopArrangement(desktop.ID, placed.Revision, func(d profiles.Desktop) (profiles.Desktop, error) {
				return tc.corrupt(d), nil
			})
			wantCode(t, err, profiles.CodeInvalid)
			got, _ := s.GetDesktop(desktop.ID)
			if !reflect.DeepEqual(got, placed) {
				t.Fatalf("a refused write changed the desktop:\n got: %+v\nwant: %+v", got, placed)
			}
		})
	}
}

func TestMoveBetweenDesktopsCommitsSourceAndTargetTogether(t *testing.T) {
	s, restart := openProfileStore(t)
	profile, first := mustCreateProfile(t, s, "Main")
	_, second, err := s.CreateDesktop(profile.ID, "", 0, true)
	if err != nil {
		t.Fatalf("CreateDesktop: %v", err)
	}
	for _, id := range []string{"agent-a", "agent-b", "agent-c"} {
		addProfileSession(t, s, id, profile.ID)
	}
	mustPlace(t, s, first.ID, "agent-a")
	source, paneB := mustPlace(t, s, first.ID, "agent-b")
	target, paneC := mustPlace(t, s, second.ID, "agent-c")

	_, err = s.MoveLeaf(LeafMoveRequest{
		SourceDesktopID: first.ID, TargetDesktopID: second.ID, LeafID: paneB, AnchorID: "pane-that-is-not-there",
		Direction: layouttree.DirectionHorizontal, ExpectedSourceRevision: source.Revision, ExpectedTargetRevision: target.Revision,
	})
	wantCode(t, err, profiles.CodeInvalid)
	_, err = s.MoveLeaf(LeafMoveRequest{
		SourceDesktopID: first.ID, TargetDesktopID: second.ID, LeafID: paneB, AnchorID: paneC,
		Direction: layouttree.DirectionHorizontal, ExpectedSourceRevision: source.Revision, ExpectedTargetRevision: target.Revision - 1,
	})
	wantCode(t, err, profiles.CodeStaleRevision)
	for _, want := range []profiles.Desktop{source, target} {
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
	assertStoredDesktopsHoldTheirInvariants(t, s, profile.ID)
}

func TestStaleRevisionFromASecondWriterIsRefused(t *testing.T) {
	s, _ := openProfileStore(t)
	profile, desktop := mustCreateProfile(t, s, "Main")
	addProfileSession(t, s, "agent-a", profile.ID)
	addProfileSession(t, s, "agent-b", profile.ID)
	seenByBoth := desktop.Revision

	winner, _, err := s.PlaceSession(SessionPlacementRequest{DesktopID: desktop.ID, ExpectedRevision: seenByBoth, SessionID: "agent-a", Direction: layouttree.DirectionVertical})
	if err != nil {
		t.Fatalf("first writer: %v", err)
	}
	_, _, err = s.PlaceSession(SessionPlacementRequest{DesktopID: desktop.ID, ExpectedRevision: seenByBoth, SessionID: "agent-b", Direction: layouttree.DirectionVertical})
	stale := wantCode(t, err, profiles.CodeStaleRevision)
	if want := profiles.Stale("desktop", desktop.ID, seenByBoth, winner.Revision).Message; stale.Message != want {
		t.Fatalf("stale message = %q, want %q", stale.Message, want)
	}
	if _, _, err := s.PlaceSession(SessionPlacementRequest{DesktopID: desktop.ID, ExpectedRevision: winner.Revision, SessionID: "agent-b", Direction: layouttree.DirectionVertical}); err != nil {
		t.Fatalf("the loser re-read revision %d and was still refused: %v", winner.Revision, err)
	}

	_, err = s.RenameProfile(profile.ID, "Renamed", profile.Revision+7)
	wantCode(t, err, profiles.CodeStaleRevision)
	_, err = s.RenameDesktop(desktop.ID, "Main", seenByBoth)
	wantCode(t, err, profiles.CodeStaleRevision)
	_, err = s.DeleteDesktop(desktop.ID, seenByBoth)
	wantCode(t, err, profiles.CodeStaleRevision)
}

func TestProfileIDsSurviveRenameAndDeletedNamesAreReusable(t *testing.T) {
	s, restart := openProfileStore(t)
	work, _ := mustCreateProfile(t, s, "Work")
	home, _ := mustCreateProfile(t, s, "Home")
	converted, err := s.MostRecentlyUsedProfile()
	if err != nil || converted.Name != DefaultProfileName {
		t.Fatalf("converted profile = %+v, %v; want %s", converted, err, DefaultProfileName)
	}
	if _, err := s.DeleteProfile(converted.ID, converted.Revision, home.ID); err != nil {
		t.Fatalf("deleting the converted Default profile: %v", err)
	}
	addProfileSession(t, s, "live-agent", work.ID)
	addProfileSession(t, s, "closed-agent", work.ID)
	if _, err := s.CloseSession("closed-agent", SessionClose{}, time.Now()); err != nil {
		t.Fatalf("closing closed-agent: %v", err)
	}
	wantCode(t, s.AssignSessionProfile("closed-agent", work.ID), profiles.CodeSessionClosed)

	_, _, err = s.CreateProfile("  Home ")
	wantCode(t, err, profiles.CodeNameTaken)
	_, err = s.RenameProfile(work.ID, "Home", work.Revision)
	wantCode(t, err, profiles.CodeNameTaken)

	renamed, err := s.RenameProfile(work.ID, "Office", work.Revision)
	if err != nil {
		t.Fatalf("RenameProfile: %v", err)
	}
	if renamed.ID != work.ID || renamed.Revision != work.Revision+1 {
		t.Fatalf("renamed profile = %+v, want the same id at the next revision", renamed)
	}
	if profileID, _ := s.SessionProfileID("live-agent"); profileID != work.ID {
		t.Fatalf("live-agent profile after rename = %s, want %s", profileID, work.ID)
	}

	_, err = s.DeleteProfile(work.ID, renamed.Revision, "")
	wantCode(t, err, profiles.CodeInvalid)
	_, err = s.DeleteProfile(work.ID, renamed.Revision, work.ID)
	wantCode(t, err, profiles.CodeDestinationSame)
	deletion, err := s.DeleteProfile(work.ID, renamed.Revision, home.ID)
	if err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	if !reflect.DeepEqual(deletion.MovedSessionIDs, []string{"live-agent"}) {
		t.Fatalf("moved sessions = %v, want only the live agent", deletion.MovedSessionIDs)
	}
	if profileID, _ := s.SessionProfileID("closed-agent"); profileID != work.ID {
		t.Fatalf("closed-agent profile = %s, want its history kept at %s", profileID, work.ID)
	}
	_, err = s.DeleteProfile(home.ID, home.Revision, work.ID)
	wantCode(t, err, profiles.CodeLastProfile)

	s = restart()
	reborn, _, err := s.CreateProfile("Office")
	if err != nil {
		t.Fatalf("reusing a deleted profile's name: %v", err)
	}
	if reborn.ID == work.ID {
		t.Fatalf("profile id %s was reused for a new profile", work.ID)
	}
	tombstone, err := s.GetProfile(work.ID)
	if err != nil || !tombstone.Deleted() {
		t.Fatalf("deleted profile = %+v, %v; want it kept as history", tombstone, err)
	}
	live, _ := s.ListProfiles(false)
	var names []string
	for _, profile := range live {
		names = append(names, profile.Name)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"Home", "Office"}) {
		t.Fatalf("live profiles = %v, want Home and the new Office", names)
	}
	_, err = s.RenameProfile(work.ID, "Back", tombstone.Revision)
	wantCode(t, err, profiles.CodeProfileDeleted)
}

func TestMovingAnAgentToAnotherProfileRemovesItsPlacementOnly(t *testing.T) {
	s, _ := openProfileStore(t)
	work, workDesktop := mustCreateProfile(t, s, "Work")
	home, _ := mustCreateProfile(t, s, "Home")
	addProfileSession(t, s, "agent-a", work.ID)
	addProfileSession(t, s, "agent-b", work.ID)
	mustPlace(t, s, workDesktop.ID, "agent-a")
	before, _ := mustPlace(t, s, workDesktop.ID, "agent-b")

	move, err := s.MoveSessionToProfile("agent-b", home.ID)
	if err != nil {
		t.Fatalf("MoveSessionToProfile: %v", err)
	}
	if move.FromProfileID != work.ID || move.SourceDesktop == nil || move.SourceDesktop.Revision != before.Revision+1 {
		t.Fatalf("move = %+v, want the source desktop rewritten once", move)
	}
	if _, found, _ := s.SessionPlacement("agent-b"); found {
		t.Fatal("agent-b kept a placement after changing profiles")
	}
	if profileID, _ := s.SessionProfileID("agent-b"); profileID != home.ID {
		t.Fatalf("agent-b profile = %s, want %s", profileID, home.ID)
	}
	if session := s.Get("agent-b"); session == nil {
		t.Fatal("moving profiles closed the agent")
	}
	_, err = s.MoveSessionToProfile("agent-b", home.ID)
	wantCode(t, err, profiles.CodeDestinationSame)
	assertStoredDesktopsHoldTheirInvariants(t, s, work.ID)
}

func TestDeletingADesktopUnplacesItsAgentsAndKeepsSlots(t *testing.T) {
	s, _ := openProfileStore(t)
	profile, first := mustCreateProfile(t, s, "Main")
	_, second, _ := s.CreateDesktop(profile.ID, "", 0, true)
	_, third, _ := s.CreateDesktop(profile.ID, "", 0, true)
	addProfileSession(t, s, "agent-a", profile.ID)
	mustPlace(t, s, second.ID, "agent-a")
	if _, err := s.SetCurrentDesktop(profile.ID, second.ID); err != nil {
		t.Fatalf("SetCurrentDesktop: %v", err)
	}
	_, _, err := s.CreateDesktop(profile.ID, "", 3, false)
	wantCode(t, err, profiles.CodeSlotTaken)

	current, _ := s.GetDesktop(second.ID)
	deletion, err := s.DeleteDesktop(second.ID, current.Revision)
	if err != nil {
		t.Fatalf("DeleteDesktop: %v", err)
	}
	if !reflect.DeepEqual(deletion.UnplacedSessionID, []string{"agent-a"}) || s.Get("agent-a") == nil {
		t.Fatalf("deletion = %+v, want agent-a unplaced and still open", deletion)
	}
	if deletion.Profile.CurrentDesktopID != third.ID {
		t.Fatalf("current desktop after deleting it = %s, want the next one %s", deletion.Profile.CurrentDesktopID, third.ID)
	}
	keptThird, _ := s.GetDesktop(third.ID)
	if keptThird.ShortcutSlot != 3 {
		t.Fatalf("third desktop slot = %d after deleting slot 2, want slots never renumbered", keptThird.ShortcutSlot)
	}
	_, refill, err := s.CreateDesktop(profile.ID, "", 0, true)
	if err != nil || refill.ShortcutSlot != 2 {
		t.Fatalf("new desktop = %+v, %v; want it to take the freed slot 2", refill, err)
	}
	moved, err := s.ReorderDesktop(refill.ID, "", first.ID, refill.Revision)
	if err != nil {
		t.Fatalf("ReorderDesktop: %v", err)
	}
	_, desktops, _ := s.ProfileArrangement(profile.ID)
	if desktops[0].ID != moved.ID {
		t.Fatalf("first desktop = %s, want the reordered %s", desktops[0].ID, moved.ID)
	}

	only, onlyDesktop := mustCreateProfile(t, s, "Solo")
	_, err = s.DeleteDesktop(onlyDesktop.ID, onlyDesktop.Revision)
	wantCode(t, err, profiles.CodeLastDesktop)
	_ = only
}

func TestMostRecentlyUsedProfileFollowsSelection(t *testing.T) {
	s, restart := openProfileStore(t)
	work, _ := mustCreateProfile(t, s, "Work")
	home, homeDesktop := mustCreateProfile(t, s, "Home")
	if _, err := s.SelectProfile(work.ID); err != nil {
		t.Fatalf("SelectProfile: %v", err)
	}
	if _, err := s.SetCurrentDesktop(home.ID, homeDesktop.ID); err != nil {
		t.Fatalf("SetCurrentDesktop: %v", err)
	}
	s = restart()
	recent, err := s.MostRecentlyUsedProfile()
	if err != nil || recent.ID != home.ID {
		t.Fatalf("most recently used profile = %+v, %v; want %s", recent, err, home.ID)
	}
}

func TestALaunchedSessionLandsBesideTheActivePaneOfTheCurrentDesktop(t *testing.T) {
	s, _ := openProfileStore(t)
	profile, desktop := mustCreateProfile(t, s, "Work")
	addProfileSession(t, s, "first", profile.ID)
	addProfileSession(t, s, "second", profile.ID)
	addProfileSession(t, s, "third", profile.ID)

	placed, firstPane, err := s.PlaceLaunchedSession(SessionPlacementRequest{SessionID: "first", Status: profiles.PaneStatusReady})
	if err != nil || placed.ID != desktop.ID {
		t.Fatalf("first launch placed on %s err=%v, want the current desktop %s", placed.ID, err, desktop.ID)
	}
	placed, secondPane, err := s.PlaceLaunchedSession(SessionPlacementRequest{SessionID: "second", Status: profiles.PaneStatusReady})
	if err != nil || placed.ActivePaneID != secondPane {
		t.Fatalf("second launch = %+v err=%v, want it focused", placed, err)
	}
	placed, _, err = s.PlaceLaunchedSession(SessionPlacementRequest{DesktopID: desktop.ID, SessionID: "third", AnchorPaneID: firstPane, Status: profiles.PaneStatusReady})
	if err != nil {
		t.Fatal(err)
	}
	if got := layouttree.PaneIDs(placed.Tree); len(got) != 3 || got[0] != firstPane {
		t.Fatalf("panes = %v, want the third split beside the first", got)
	}
	assertStoredDesktopsHoldTheirInvariants(t, s, profile.ID)
}

func TestALaunchedSessionIsNeverPlacedOutsideItsProfile(t *testing.T) {
	s, _ := openProfileStore(t)
	home, _ := mustCreateProfile(t, s, "Home")
	_, workDesktop := mustCreateProfile(t, s, "Work")
	addProfileSession(t, s, "homebody", home.ID)

	_, _, err := s.PlaceLaunchedSession(SessionPlacementRequest{DesktopID: workDesktop.ID, SessionID: "homebody", Status: profiles.PaneStatusReady})
	wantCode(t, err, profiles.CodeCrossProfile)
	if _, err := s.LaunchDesktop(home.ID, workDesktop.ID); err == nil {
		t.Fatal("LaunchDesktop accepted a desktop of another profile")
	}
	if _, placed, _ := s.SessionPlacement("homebody"); placed {
		t.Fatal("a refused placement left a pane")
	}
}

func TestReAddingASessionNeverChangesItsProfile(t *testing.T) {
	s, _ := openProfileStore(t)
	home, _ := mustCreateProfile(t, s, "Home")
	work, _ := mustCreateProfile(t, s, "Work")
	addProfileSession(t, s, "agent", home.ID)

	session := s.Get("agent")
	session.ProfileID = work.ID
	if err := s.AddChecked(session); err != nil {
		t.Fatal(err)
	}
	session.ProfileID = ""
	if err := s.AddChecked(session); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("agent").ProfileID; got != home.ID {
		t.Fatalf("profile after re-adds = %q, want %s: membership changes only through a move", got, home.ID)
	}
}

func TestReopeningIntoAnotherProfileIsUndoneWithTheClose(t *testing.T) {
	s, _ := openProfileStore(t)
	home, _ := mustCreateProfile(t, s, "Home")
	work, _ := mustCreateProfile(t, s, "Work")
	addProfileSession(t, s, "agent", home.ID)
	if _, err := s.CloseSession("agent", SessionClose{By: SessionClosedByUser}, time.Now()); err != nil {
		t.Fatal(err)
	}

	lifted, reopened, err := s.ReopenSession("agent", work.ID)
	if err != nil || !reopened || lifted.ProfileID != home.ID {
		t.Fatalf("reopen lifted=%+v reopened=%v err=%v, want the recorded profile %s lifted", lifted, reopened, err, home.ID)
	}
	if got := s.Get("agent").ProfileID; got != work.ID {
		t.Fatalf("reopened profile = %q, want %s", got, work.ID)
	}
	if restored, err := s.RestoreSessionClose("agent", lifted); err != nil || !restored {
		t.Fatalf("restore close restored=%v err=%v", restored, err)
	}
	if got, err := s.SessionProfileID("agent"); err != nil || got != home.ID {
		t.Fatalf("closed row profile = %q err=%v, want %s back as history", got, err, home.ID)
	}
}

func TestNothingJoinsADeletedProfileAndItsCrewMoveWithIt(t *testing.T) {
	s, _ := openProfileStore(t)
	doomed, _ := mustCreateProfile(t, s, "Doomed")
	kept, _ := mustCreateProfile(t, s, "Kept")
	if assigned, err := s.EnsureCrewProfile("mira", doomed.ID); err != nil || assigned != doomed.ID {
		t.Fatalf("EnsureCrewProfile = %q, %v; want %s", assigned, err, doomed.ID)
	}
	if assigned, err := s.EnsureCrewProfile("mira", kept.ID); err != nil || assigned != doomed.ID {
		t.Fatalf("a second EnsureCrewProfile = %q, %v; want the first assignment %s kept", assigned, err, doomed.ID)
	}
	addProfileSession(t, s, "closed-agent", doomed.ID)
	if _, err := s.CloseSession("closed-agent", SessionClose{}, time.Now()); err != nil {
		t.Fatal(err)
	}

	deletion, err := s.DeleteProfile(doomed.ID, doomed.Revision, kept.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(deletion.MovedCrewIDs, []string{"mira"}) {
		t.Fatalf("moved crew = %v, want [mira]", deletion.MovedCrewIDs)
	}
	if profileID, _ := s.CrewProfile("mira"); profileID != kept.ID {
		t.Fatalf("mira's profile after the delete = %q, want %s", profileID, kept.ID)
	}

	_, err = s.EnsureCrewProfile("nell", doomed.ID)
	wantCode(t, err, profiles.CodeProfileDeleted)
	now := string(protocol.TimestampNow())
	err = s.AddChecked(&protocol.Session{
		ID: "late-spawn", Label: "late", Agent: protocol.SessionAgentCodex, Directory: "/tmp/project", ProfileID: doomed.ID,
		State: protocol.SessionStateLaunching, StateSince: now, StateUpdatedAt: now, LastSeen: now,
	})
	wantCode(t, err, profiles.CodeProfileDeleted)
	if s.Get("late-spawn") != nil {
		t.Fatal("a session joined the deleted profile")
	}
	_, err = s.UpsertAutomationDefinition("late-automation", "Late", `{}`, doomed.ID, time.Now())
	wantCode(t, err, profiles.CodeProfileDeleted)
	_, _, err = s.ReopenSession("closed-agent", "")
	wantCode(t, err, profiles.CodeProfileDeleted)
	if _, reopened, err := s.ReopenSession("closed-agent", kept.ID); err != nil || !reopened {
		t.Fatalf("reopening into the kept profile: reopened=%v err=%v", reopened, err)
	}
}
