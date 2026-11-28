package store

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
)

func TestTheHomeDesktopMigrationGivesEveryLiveProfileOneHomeAtTheTop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	old, err := newStoreAtVersion(path, 1795819946755585-1)
	if err != nil {
		t.Fatal(err)
	}
	work, _ := mustCreateProfile(t, old, "work")
	if _, _, err := old.CreateDesktop(work.ID, "Ops", 0, false); err != nil {
		t.Fatal(err)
	}
	gone, _ := mustCreateProfile(t, old, "gone")
	if _, _, err := old.DeleteProfile(gone.ID, gone.Revision, 0); err != nil {
		t.Fatal(err)
	}
	before := map[string][]string{}
	live, err := old.ListProfiles(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range live {
		_, desktops, err := old.ProfileArrangement(profile.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, desktop := range desktops {
			before[profile.ID] = append(before[profile.ID], desktop.ID+" "+desktop.Name+" "+desktop.OrderKey)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.ListHomeDesktop(true)
	for _, profile := range live {
		_, desktops, err := s.ProfileArrangement(profile.ID)
		if err != nil {
			t.Fatal(err)
		}
		var homes []profiles.Desktop
		var kept []string
		for _, desktop := range desktops {
			if desktop.Kind == profiles.DesktopKindHome {
				homes = append(homes, desktop)
				continue
			}
			kept = append(kept, desktop.ID+" "+desktop.Name+" "+desktop.OrderKey)
		}
		if len(homes) != 1 || desktops[0].ID != homes[0].ID {
			t.Fatalf("profile %s lists %d Home desktops, first %s; want exactly one, at the top", profile.Name, len(homes), desktops[0].ID)
		}
		if !slices.Equal(kept, before[profile.ID]) {
			t.Errorf("profile %s desktops changed from %v to %v beside Home", profile.Name, before[profile.ID], kept)
		}
		columns := layouttree.Columns(homes[0].Tree)
		if len(columns) != 2 || !slices.Equal(layouttree.LeafIDs(columns[0]), []string{"tile-view-attn", "tile-view-sessions"}) || !slices.Equal(layouttree.LeafIDs(columns[1]), []string{"tile-view-pull-requests"}) {
			t.Errorf("profile %s Home holds %s, want attn above Sessions, then Pull requests", profile.Name, mustEncode(t, homes[0].Tree))
		}
	}
	if _, err := s.HomeDesktop(gone.ID); !profiles.IsNotFound(err) {
		t.Errorf("a deleted profile's Home answered %v, want none made", err)
	}
	s.ListHomeDesktop(false)
	_, desktops, err := s.ProfileArrangement(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(desktops, func(d profiles.Desktop) bool { return d.Kind == profiles.DesktopKindHome }) {
		t.Error("with Home unlisted the profile's desktops still hold Home")
	}
}

func mustEncode(t *testing.T, tree layouttree.Node) string {
	t.Helper()
	encoded, err := layouttree.EncodeLayout(tree)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
