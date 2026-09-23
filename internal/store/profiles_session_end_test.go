package store

import (
	"math"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
)

func placedPair(t *testing.T) (*Store, profiles.Profile, profiles.Desktop, string, string) {
	t.Helper()
	s, _ := openProfileStore(t)
	profile, desktop := mustCreateProfile(t, s, "attn")
	addProfileSession(t, s, "agent-a", profile.ID)
	addProfileSession(t, s, "agent-b", profile.ID)
	_, paneA := mustPlace(t, s, desktop.ID, "agent-a")
	_, paneB := mustPlace(t, s, desktop.ID, "agent-b")
	return s, profile, desktop, paneA, paneB
}

func wantOnlyPane(t *testing.T, s *Store, desktopID, paneID string) profiles.Desktop {
	t.Helper()
	desktop, err := s.GetDesktop(desktopID)
	if err != nil {
		t.Fatalf("GetDesktop: %v", err)
	}
	if len(desktop.Panes) != 1 || desktop.Panes[0].PaneID != paneID || desktop.ActivePaneID != paneID {
		t.Fatalf("desktop holds panes %+v with active %q, want only %s and active", desktop.Panes, desktop.ActivePaneID, paneID)
	}
	if got := layouttree.PaneIDs(desktop.Tree); len(got) != 1 || got[0] != paneID {
		t.Fatalf("desktop tree holds panes %v, want only %s", got, paneID)
	}
	return desktop
}

func TestEndingASessionTakesItsPaneOffTheDesktop(t *testing.T) {
	endings := []struct {
		name string
		end  func(t *testing.T, s *Store)
	}{
		{"close", func(t *testing.T, s *Store) {
			if closed, err := s.CloseSession("agent-a", SessionClose{}, time.Now()); err != nil || !closed {
				t.Fatalf("CloseSession = %v, %v", closed, err)
			}
		}},
		{"remove", func(t *testing.T, s *Store) { s.Remove("agent-a") }},
	}
	for _, ending := range endings {
		t.Run(ending.name, func(t *testing.T) {
			s, profile, desktop, _, paneB := placedPair(t)
			ending.end(t, s)

			if _, placed, err := s.SessionPlacement("agent-a"); err != nil || placed {
				t.Fatalf("agent-a is still placed after it ended (placed=%v, err=%v)", placed, err)
			}
			wantOnlyPane(t, s, desktop.ID, paneB)
			addProfileSession(t, s, "agent-c", profile.ID)
			mustPlace(t, s, desktop.ID, "agent-c")
			assertStoredDesktopsHoldTheirInvariants(t, s, profile.ID)
		})
	}
}

func TestClearingSessionsEmptiesEveryDesktop(t *testing.T) {
	s, profile, desktop, _, _ := placedPair(t)
	s.ClearSessions()

	emptied, err := s.GetDesktop(desktop.ID)
	if err != nil {
		t.Fatalf("GetDesktop: %v", err)
	}
	if len(emptied.Panes) != 0 || emptied.ActivePaneID != "" || !layouttree.LayoutEmpty(emptied.Tree) {
		t.Fatalf("after clearing sessions the desktop still holds %+v", emptied)
	}
	addProfileSession(t, s, "agent-c", profile.ID)
	mustPlace(t, s, desktop.ID, "agent-c")
}

func TestRemovingADirectorysSessionsTakesTheirPanesOffTheDesktop(t *testing.T) {
	s, _, desktop, _, _ := placedPair(t)
	s.RemoveSessionsInDirectory("/tmp/project")

	emptied, err := s.GetDesktop(desktop.ID)
	if err != nil {
		t.Fatalf("GetDesktop: %v", err)
	}
	if len(emptied.Panes) != 0 {
		t.Fatalf("desktop still holds %+v", emptied.Panes)
	}
}

func TestAPaneWhoseSessionVanishedNeverBlocksItsDesktop(t *testing.T) {
	s, profile, desktop, paneA, paneB := placedPair(t)
	for _, id := range []string{"agent-a", "agent-b"} {
		if _, err := s.db.Exec(`UPDATE sessions SET closed_at = '2026-01-01T00:00:00Z' WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
	current, err := s.GetDesktop(desktop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetDesktopSplitRatio(desktop.ID, current.Tree.SplitID, 0.3, current.Revision); err != nil {
		t.Fatalf("a desktop holding closed agents refused a ratio change: %v", err)
	}
	if _, err := s.RemoveSessionPlacement("agent-a"); err != nil {
		t.Fatalf("removing closed agent-a while agent-b is also closed: %v", err)
	}
	wantOnlyPane(t, s, desktop.ID, paneB)

	if _, err := s.db.Exec(`DELETE FROM sessions WHERE id = 'agent-b'`); err != nil {
		t.Fatal(err)
	}
	addProfileSession(t, s, "agent-c", profile.ID)
	mustPlace(t, s, desktop.ID, "agent-c")
	current, err = s.GetDesktop(desktop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RemoveLeaf(desktop.ID, paneB, current.Revision); err != nil {
		t.Fatalf("removing the pane of a session that no longer exists: %v", err)
	}
	_ = paneA

	addProfileSession(t, s, "agent-d", profile.ID)
	if _, err := s.CloseSession("agent-d", SessionClose{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	current, _ = s.GetDesktop(desktop.ID)
	_, _, err = s.PlaceSession(SessionPlacementRequest{DesktopID: desktop.ID, ExpectedRevision: current.Revision, SessionID: "agent-d"})
	wantCode(t, err, profiles.CodeSessionClosed)
}

func TestPlacingWithAShareKeepsThatShare(t *testing.T) {
	s, _ := openProfileStore(t)
	profile, desktop := mustCreateProfile(t, s, "attn")
	addProfileSession(t, s, "agent-a", profile.ID)
	addProfileSession(t, s, "agent-b", profile.ID)
	first, paneA := mustPlace(t, s, desktop.ID, "agent-a")

	placed, _, err := s.PlaceSession(SessionPlacementRequest{
		DesktopID: desktop.ID, ExpectedRevision: first.Revision, SessionID: "agent-b",
		AnchorPaneID: paneA, Direction: layouttree.DirectionVertical, NewPaneShare: 0.25,
	})
	if err != nil {
		t.Fatalf("PlaceSession: %v", err)
	}
	if math.Abs(placed.Tree.Ratio-0.75) > 1e-9 {
		t.Fatalf("the existing pane keeps %.2f of the split, want 0.75 so the new pane gets its 0.25", placed.Tree.Ratio)
	}
	stored, err := s.GetDesktop(desktop.ID)
	if err != nil || math.Abs(stored.Tree.Ratio-0.75) > 1e-9 {
		t.Fatalf("stored ratio = %.2f (err %v), want 0.75", stored.Tree.Ratio, err)
	}
}

func TestAMoveWhoseTargetWriteFailsLeavesTheSourceUntouched(t *testing.T) {
	s, _ := openProfileStore(t)
	profile, source := mustCreateProfile(t, s, "attn")
	_, target, err := s.CreateDesktop(profile.ID, "", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	addProfileSession(t, s, "agent-a", profile.ID)
	addProfileSession(t, s, "agent-b", profile.ID)
	source, paneA := mustPlace(t, s, source.ID, "agent-a")
	target, _ = mustPlace(t, s, target.ID, "agent-b")
	if _, err := s.db.Exec(`UPDATE desktop_panes SET status = 'melted' WHERE session_id = 'agent-b'`); err != nil {
		t.Fatal(err)
	}

	_, err = s.MoveLeaf(LeafMoveRequest{
		SourceDesktopID: source.ID, TargetDesktopID: target.ID, LeafID: paneA, Direction: layouttree.DirectionVertical,
		ExpectedSourceRevision: source.Revision, ExpectedTargetRevision: target.Revision,
	})
	wantCode(t, err, profiles.CodeInvalid)

	after := wantOnlyPane(t, s, source.ID, paneA)
	if after.Revision != source.Revision {
		t.Fatalf("the failed move took the source from revision %d to %d", source.Revision, after.Revision)
	}
	if placement, placed, err := s.SessionPlacement("agent-a"); err != nil || !placed || placement.DesktopID != source.ID {
		t.Fatalf("agent-a is at %+v (placed=%v, err=%v), want the source desktop", placement, placed, err)
	}
}

func TestShortcutSlotsOutsideOneToNineAreRefusedByName(t *testing.T) {
	s, _ := openProfileStore(t)
	profile, desktop := mustCreateProfile(t, s, "attn")
	for _, slot := range []int{-1, 10} {
		_, _, err := s.CreateDesktop(profile.ID, "", slot, false)
		refusal := wantCode(t, err, profiles.CodeInvalid)
		if refusal.Message != profiles.ValidateShortcutSlot(slot).Error() {
			t.Fatalf("slot %d refused with %q", slot, refusal.Message)
		}
		_, err = s.SetDesktopShortcutSlot(desktop.ID, slot, desktop.Revision)
		wantCode(t, err, profiles.CodeInvalid)
	}
	if err := profiles.ValidateShortcutSlot(10); err == nil || err.Error() != "shortcut slot 10 is outside 1-9" {
		t.Fatalf("slot 10 refusal = %v, want it to name the slot and the 1-9 range", err)
	}
	for slot := profiles.FirstShortcutSlot; slot <= profiles.LastShortcutSlot; slot++ {
		if err := profiles.ValidateShortcutSlot(slot); err != nil {
			t.Fatalf("slot %d refused: %v", slot, err)
		}
	}
}
