package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSingletonNotebookSyncUpgradePreservesFilesAndBacksUpOnce(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t)
	homeRoot := filepath.Join(home.Dir, "notebook")
	writeNotebookFile(t, homeRoot, "keep.md", "home source")
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	home.Stop()
	outpost.Stop()
	statePath := filepath.Join(outpost.Dir, "notebook-sync", "state.json")
	currentBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	current := map[string]any{}
	if err := json.Unmarshal(currentBytes, &current); err != nil {
		t.Fatal(err)
	}
	legacyBytes, err := os.ReadFile("testdata/notebook-singleton.json")
	if err != nil {
		t.Fatal(err)
	}
	legacy := map[string]any{}
	if err := json.Unmarshal(legacyBytes, &legacy); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(outpost.Dir, "notebook")
	legacy["Home"], legacy["Root"], legacy["PreviousRoot"] = current["Home"], parent, ""
	if err := os.RemoveAll(parent); err != nil {
		t.Fatal(err)
	}
	writeNotebookFile(t, parent, "legacy.md", "old singleton outpost content")
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	outpost.Start()
	home.Start()
	pair.App = home.App()
	state := awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	previous := protocol.Deref(state.PreviousRoot)
	if previous == "" {
		t.Fatal("singleton upgrade did not name its backup")
	}
	saved, err := os.ReadFile(filepath.Join(previous, "legacy.md"))
	if err != nil || string(saved) != "old singleton outpost content" {
		t.Fatalf("singleton backup: %q %v", saved, err)
	}
	root := notebookProfileRoot(t, outpost, pair.App.SelectedProfile())
	if data, err := os.ReadFile(filepath.Join(root, "keep.md")); err != nil || string(data) != "home source" {
		t.Fatalf("upgraded outpost: %q %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(homeRoot, "keep.md")); err != nil || string(data) != "home source" {
		t.Fatalf("home changed: %q %v", data, err)
	}
	before, err := filepath.Glob(parent + ".before-sync-*")
	if err != nil {
		t.Fatal(err)
	}
	home.Stop()
	outpost.Stop()
	outpost.Start()
	home.Start()
	pair.App = home.App()
	again := awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	after, err := filepath.Glob(parent + ".before-sync-*")
	if err != nil {
		t.Fatal(err)
	}
	if protocol.Deref(again.PreviousRoot) != previous || len(after) != len(before) {
		t.Fatalf("restart backed up again: before=%v after=%v state=%+v", before, after, again)
	}
	t.Logf("real main singleton format upgraded; backup=%s; home retained; new folder synced; restart made no second backup", previous)
}
