package store

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profilemigration"
	"github.com/victorarias/attn/internal/profiles"
)

func convertAgentWorkspaces(t *testing.T, count int) (*legacyFixture, *Store, ProfileMigrationView) {
	t.Helper()
	f := newLegacyFixture(t)
	for i := 1; i <= count; i++ {
		f.agentWorkspace(fmt.Sprintf("ws-%02d", i), fmt.Sprintf("agent-%02d", i))
	}
	s, view, _ := f.mustConvert()
	return f, s, view
}

func edit(t *testing.T, s *Store, revision int64, change func(profilemigration.Plan, []profilemigration.GroupState) (profilemigration.Plan, error)) ProfileMigrationView {
	t.Helper()
	view, err := s.EditProfileMigration(revision, change)
	if err != nil {
		t.Fatalf("EditProfileMigration: %v", err)
	}
	return view
}

func keepAll(plan profilemigration.Plan, live []profilemigration.GroupState) (profilemigration.Plan, error) {
	return plan.Keep(live, plan.Unconfirmed(live))
}

func TestTheSharedDraftSurvivesRestartAndRefusesStaleEdits(t *testing.T) {
	f, s, view := convertAgentWorkspaces(t, 11)
	moved := edit(t, s, view.State.Revision, func(plan profilemigration.Plan, live []profilemigration.GroupState) (profilemigration.Plan, error) {
		return plan.Move(live, "ws-10", view.Manifest.Groups[0].DesktopID, "", profilemigration.EdgeRight, 0)
	})
	if moved.State.Revision != view.State.Revision+1 {
		t.Fatalf("revision %d after one edit from %d", moved.State.Revision, view.State.Revision)
	}
	_, err := s.EditProfileMigration(view.State.Revision, keepAll)
	wantCode(t, err, profiles.CodeStaleRevision)

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	resumed, err := reopened.ProfileMigration()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resumed.Plan, moved.Plan) || resumed.State.Revision != moved.State.Revision {
		t.Fatalf("draft after restart = %+v at %d, want %+v at %d", resumed.Plan, resumed.State.Revision, moved.Plan, moved.State.Revision)
	}
}

func TestFinishAppliesTheDraftOnceAndKeepsAgentsLaunchedMeanwhile(t *testing.T) {
	_, s, view := convertAgentWorkspaces(t, 11)
	first, second := view.Manifest.Groups[0], view.Manifest.Groups[1]
	extra := view.Manifest.Groups[9]
	addProfileSession(t, s, "delegated-child", view.Manifest.ProfileID)
	source, err := s.GetDesktop(second.DesktopID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PlaceSession(SessionPlacementRequest{DesktopID: second.DesktopID, ExpectedRevision: source.Revision, SessionID: "delegated-child", AnchorPaneID: "pane-agent-02", Direction: layouttree.DirectionVertical}); err != nil {
		t.Fatalf("placing a delegated child during the picker: %v", err)
	}
	view = edit(t, s, view.State.Revision, func(plan profilemigration.Plan, live []profilemigration.GroupState) (profilemigration.Plan, error) {
		return plan.Move(live, second.ID, first.DesktopID, "", profilemigration.EdgeRight, 0)
	})
	view = edit(t, s, view.State.Revision, func(plan profilemigration.Plan, live []profilemigration.GroupState) (profilemigration.Plan, error) {
		return plan.Move(live, extra.ID, view.Manifest.Groups[4].DesktopID, "", profilemigration.EdgeRight, 0)
	})
	_, err = s.FinishProfileMigration(view.State.Revision)
	wantCode(t, err, profiles.CodeInvalid)
	view = edit(t, s, view.State.Revision, keepAll)

	finish, err := s.FinishProfileMigration(view.State.Revision)
	if err != nil || !finish.Finished {
		t.Fatalf("FinishProfileMigration = %+v, %v", finish, err)
	}
	if finish.View.State.Phase != profilemigration.PhaseComplete {
		t.Fatalf("phase after finish = %s", finish.View.State.Phase)
	}
	_, desktops, err := s.ProfileArrangement(view.Manifest.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	at := make(map[string]int)
	for _, d := range desktops {
		for _, p := range d.Panes {
			at[p.SessionID] = d.ShortcutSlot
		}
	}
	if at["agent-01"] != 1 || at["agent-02"] != 1 || at["delegated-child"] != 2 || at["agent-10"] != 5 || at["agent-11"] != 0 {
		t.Fatalf("placements after finish = %v, want agent-02 merged into slot 1, its delegated child left on slot 2, agent-10 merged into slot 5", at)
	}
	if _, err := s.GetDesktop(extra.DesktopID); err == nil {
		t.Fatalf("the emptied extra desktop %s survived the finish", extra.DesktopID)
	}

	again, err := s.FinishProfileMigration(view.State.Revision)
	if err != nil || again.Finished || again.View.State.Phase != profilemigration.PhaseComplete {
		t.Fatalf("a repeated finish = %+v, %v; want success without changes", again, err)
	}
	_, err = s.EditProfileMigration(again.View.State.Revision, keepAll)
	wantCode(t, err, profiles.CodeInvalid)
}

func TestAClosedImportRetiresAndFinishDoesNotBringItBack(t *testing.T) {
	_, s, view := convertAgentWorkspaces(t, 3)
	view = edit(t, s, view.State.Revision, func(plan profilemigration.Plan, live []profilemigration.GroupState) (profilemigration.Plan, error) {
		return plan.Move(live, "ws-03", view.Manifest.Groups[0].DesktopID, "", profilemigration.EdgeBottom, 0)
	})
	if _, err := s.CloseSession("agent-03", SessionClose{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	view, err := s.ProfileMigration()
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Live) != 2 {
		t.Fatalf("%d groups wait for placement after closing agent-03, want 2", len(view.Live))
	}
	view = edit(t, s, view.State.Revision, keepAll)
	if _, err := s.FinishProfileMigration(view.State.Revision); err != nil {
		t.Fatal(err)
	}
	_, desktops, err := s.ProfileArrangement(view.Manifest.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range desktops {
		for _, p := range d.Panes {
			if p.SessionID == "agent-03" {
				t.Fatalf("closed agent-03 was placed again on %s", d.ID)
			}
		}
	}
	if len(desktops) != 3 || !layouttree.LayoutEmpty(desktops[2].Tree) || desktops[2].ShortcutSlot != 3 {
		t.Fatalf("desktops = %+v, want slot 3 kept as an empty desktop", desktops)
	}
}

func TestTheProfileHoldingAPendingMigrationCannotBeDeleted(t *testing.T) {
	_, s, view := convertAgentWorkspaces(t, 2)
	other, _, err := s.CreateProfile("Work")
	if err != nil {
		t.Fatal(err)
	}
	converted, err := s.GetProfile(view.Manifest.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DeleteProfile(converted.ID, converted.Revision, other.ID)
	wantCode(t, err, profiles.CodeInvalid)

	view = edit(t, s, view.State.Revision, keepAll)
	if _, err := s.FinishProfileMigration(view.State.Revision); err != nil {
		t.Fatal(err)
	}
	converted, err = s.GetProfile(view.Manifest.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteProfile(converted.ID, converted.Revision, other.ID); err != nil {
		t.Fatalf("deleting Default after the migration finished: %v", err)
	}
}

func TestFinishKeepsADesktopOrderChangedDuringThePicker(t *testing.T) {
	_, s, view := convertAgentWorkspaces(t, 3)
	third, err := s.GetDesktop(view.Manifest.Groups[2].DesktopID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReorderDesktop(third.ID, "", view.Manifest.Groups[0].DesktopID, third.Revision); err != nil {
		t.Fatalf("moving desktop 3 first: %v", err)
	}
	view = edit(t, s, view.State.Revision, func(plan profilemigration.Plan, live []profilemigration.GroupState) (profilemigration.Plan, error) {
		return plan.Move(live, "ws-02", "slot-4", "", profilemigration.EdgeRight, 0)
	})
	view = edit(t, s, view.State.Revision, keepAll)
	if _, err := s.FinishProfileMigration(view.State.Revision); err != nil {
		t.Fatal(err)
	}
	_, desktops, err := s.ProfileArrangement(view.Manifest.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	var order []int
	for _, d := range desktops {
		order = append(order, d.ShortcutSlot)
	}
	if !reflect.DeepEqual(order, []int{3, 1, 2, 4}) {
		t.Fatalf("slots in desktop order = %v, want the reorder kept and the new slot-4 desktop last", order)
	}
}
