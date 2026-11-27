package store

import (
	"path/filepath"
	"testing"
)

const beforeNotebookFoldersKeepTheirCopyName = 1795810551524491 - 1

func TestUpgradeKeepsTheCopyNameAKnownNotebookFolderAlreadyHas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attn.db")
	s, err := newStoreAtVersion(path, beforeNotebookFoldersKeepTheirCopyName)
	if err != nil {
		t.Fatal(err)
	}
	root := "/home/someone/Attn Notebook"
	if _, err := s.db.Exec(`INSERT INTO notebook_folders (id, folder_key, root_path, created_at) VALUES ('notebook-d-1-0011aabb', '0011aabb', ?, '2026-01-01T00:00:00Z')`, root); err != nil {
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
	keys, err := s.NotebookFolderKeys([]string{root}, func(string) NotebookFolderKey {
		t.Fatal("the upgrade forgot the known Notebook folder")
		return NotebookFolderKey{}
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := (NotebookFolderKey{ID: "notebook-d-1-0011aabb", Key: "0011aabb", CopyName: "attn-notebook"}); keys[root] != want {
		t.Fatalf("the known Notebook folder upgraded to %+v, want %+v", keys[root], want)
	}
}
