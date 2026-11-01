package store

import (
	"path/filepath"
	"testing"
)

func TestUpgradeKeepsEveryProfileOnTheNotebookFolderItUsed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, 1793565205582815)
	if err != nil {
		t.Fatal(err)
	}
	shared, own := "/notes/shared", "/notes/own"
	s.SetSetting("notebook.root", shared)
	plain, _, err := s.CreateProfile("Plain")
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := s.CreateProfile("Owner")
	if err != nil {
		t.Fatal(err)
	}
	gone, _, err := s.CreateProfile("Gone")
	if err != nil {
		t.Fatal(err)
	}
	goneOwner, _, err := s.CreateProfile("GoneOwner")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{owner.ID, goneOwner.ID} {
		if err := s.SetProfileSetting(id, "notebook.profile_root", own); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []struct {
		id       string
		revision int64
	}{{gone.ID, gone.Revision}, {goneOwner.ID, goneOwner.Revision}} {
		if _, _, err := s.DeleteProfile(p.id, p.revision, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, want := range []struct{ profile, root string }{{plain.ID, shared}, {owner.ID, own}, {gone.ID, shared}, {goneOwner.ID, own}} {
		if got := s.ProfileSetting(want.profile, "notebook.root"); got != want.root {
			t.Errorf("after the upgrade profile %s has notebook.root %q, want %q", want.profile, got, want.root)
		}
		if got := s.ProfileSetting(want.profile, "notebook.profile_root"); got != "" {
			t.Errorf("after the upgrade profile %s still has notebook.profile_root %q", want.profile, got)
		}
	}
	if got := s.GetSetting("notebook.root"); got != "" {
		t.Errorf("after the upgrade the machine notebook.root reads %q, want it gone", got)
	}
}
