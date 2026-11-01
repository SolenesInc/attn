package store

import (
	"path/filepath"
	"testing"
)

func TestUpgradeKeepsEveryLiveProfileOnTheNotebookFolderItUsed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, 1793565205582816)
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
	if err := s.SetProfileSetting(owner.ID, "notebook.profile_root", own); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.DeleteProfile(gone.ID, gone.Revision, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = NewWithDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, want := range []struct{ profile, root string }{{plain.ID, shared}, {owner.ID, own}, {gone.ID, ""}} {
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
