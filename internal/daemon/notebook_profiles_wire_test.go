package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func notebookEffectiveRoot(p *testworld.Peer, want string) {
	p.T.Helper()
	testworld.Await(p, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool {
		return m.Settings["notebook.root.effective"] == want
	})
}

func notebookFileHolds(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("%s holds %q (%v), want %q", path, got, err, want)
	}
}

func TestEachProfileReadsAndWritesItsOwnNotebookFolder(t *testing.T) {
	w := newWorld(t)
	setup := w.App()
	defaultFolder := fsNotebookRoot(t, w)
	work, home, plain := createProfile(setup, "Work"), createProfile(setup, "Home"), createProfile(setup, "Plain")
	workApp, homeApp, plainApp := w.AppOn(work.ID), w.AppOn(home.ID), w.AppOn(plain.ID)
	workRoot, homeRoot := filepath.Join(w.Dir, "work-notebook"), filepath.Join(w.Dir, "home-notebook")

	if saved := notebookSetting(workApp, "notebook.root", workRoot); !protocol.Deref(saved.Success) {
		t.Fatalf("setting Work's Notebook folder: %s", protocol.Deref(saved.Error))
	}
	notebookEffectiveRoot(workApp, workRoot)
	if saved := notebookSetting(homeApp, "notebook.root", homeRoot); !protocol.Deref(saved.Success) {
		t.Fatalf("setting Home's Notebook folder: %s", protocol.Deref(saved.Error))
	}
	notebookEffectiveRoot(homeApp, homeRoot)
	if got := plainApp.Initial.Settings["notebook.root.effective"]; got != defaultFolder {
		t.Fatalf("Plain starts on Notebook folder %v, want the default folder %s", got, defaultFolder)
	}

	for _, write := range []struct {
		app  *testworld.Peer
		path string
	}{{workApp, "work.md"}, {homeApp, "home.md"}, {plainApp, "plain.md"}} {
		if saved := fsAskWrite(write.app, "", write.path, notebookNote(write.path), ""); !saved.Success {
			t.Fatalf("writing %s: %s", write.path, protocol.Deref(saved.Error))
		}
	}
	for _, event := range plainApp.Log() {
		var changed protocol.FsChangedMessage
		if json.Unmarshal(event, &changed) == nil && changed.Event == protocol.EventFsChanged && changed.Root != defaultFolder {
			t.Fatalf("Plain's window heard a change in another profile's Notebook: %+v", changed)
		}
	}
	notebookFileHolds(t, filepath.Join(workRoot, "work.md"), notebookNote("work.md"))
	notebookFileHolds(t, filepath.Join(homeRoot, "home.md"), notebookNote("home.md"))
	notebookFileHolds(t, filepath.Join(defaultFolder, "plain.md"), notebookNote("plain.md"))
	for _, listed := range []struct {
		app  *testworld.Peer
		want []string
	}{{workApp, []string{"work.md"}}, {homeApp, []string{"home.md"}}, {plainApp, []string{"plain.md"}}} {
		if got := fsAskList(listed.app, "", ""); !slices.Equal(fsEntryPaths(got.Entries), listed.want) {
			t.Fatalf("a profile lists %v, want only its own %v", fsEntryPaths(got.Entries), listed.want)
		}
	}

	cli := w.Client()
	for _, tc := range []struct{ profile, want string }{{"Work", workRoot}, {home.ID, homeRoot}, {"Plain", defaultFolder}} {
		entry, err := cli.Settings(client.SettingsCaller{Profile: tc.profile}, "notebook.root.effective", false)
		if err != nil || protocol.Deref(entry.Entries[0].Value) != tc.want || entry.Entries[0].Scope != protocol.SettingScopeProfile {
			t.Fatalf("settings --profile %s notebook.root.effective = %+v (%v), want %s", tc.profile, entry, err, tc.want)
		}
	}
	if _, err := cli.Settings(client.SettingsCaller{}, "notebook.root", false); err == nil || !strings.Contains(err.Error(), "--profile") {
		t.Fatalf("reading a profile setting without naming one of several profiles = %v, want a request for --profile", err)
	}
	for _, attempt := range []func() error{
		func() error {
			_, err := cli.Settings(client.SettingsCaller{Profile: "Work"}, "notebook.profile_root", false)
			return err
		},
		func() error {
			_, err := cli.SetSetting(client.SettingsCaller{Profile: "Work"}, "notebook.profile_root", filepath.Join(w.Dir, "elsewhere"))
			return err
		},
	} {
		if err := attempt(); err == nil || !strings.Contains(err.Error(), "unknown setting: notebook.profile_root") {
			t.Fatalf("using the retired notebook.profile_root = %v, want an unknown setting", err)
		}
	}
	outside := t.TempDir()
	if _, err := cli.SetSetting(client.SettingsCaller{Profile: "Plain"}, "notebook.root", outside); err == nil || !strings.Contains(err.Error(), "outside instance harness root") {
		t.Fatalf("pointing a harness profile outside the harness = %v, want a refusal", err)
	}

	workGarden := cli.WithGardenProfile(work.ID, "")
	seed := plantSeedAs(t, workGarden, "", "Work evidence")
	source := seedArtifactsWrite(t, t.TempDir(), "evidence.txt", []byte("proof"))
	if copied := seedArtifactsTransfer(t, workGarden, seed, "copy", source, "", ""); copied.DestinationPath != filepath.Join(workRoot, "seeds", seed, "evidence.txt") {
		t.Fatalf("Work's seed artifact landed at %s, want it in Work's Notebook folder", copied.DestinationPath)
	}

	movedRoot := filepath.Join(w.Dir, "moved-notebook")
	if err := os.Mkdir(movedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if saved := notebookSetting(workApp, "notebook.root", movedRoot); !protocol.Deref(saved.Success) {
		t.Fatalf("repointing Work's Notebook folder: %s", protocol.Deref(saved.Error))
	}
	notebookEffectiveRoot(workApp, movedRoot)
	if got := fsAskList(workApp, "", ""); len(got.Entries) != 0 {
		t.Fatalf("after repointing, Work lists %v, want the new empty folder", fsEntryPaths(got.Entries))
	}
	notebookFileHolds(t, filepath.Join(workRoot, "work.md"), notebookNote("work.md"))
	notebookFileHolds(t, filepath.Join(workRoot, "seeds", seed, "evidence.txt"), "proof")
	if entries, err := os.ReadDir(movedRoot); err != nil || len(entries) != 0 {
		t.Fatalf("repointing put %v (%v) in the new folder, want nothing moved", entries, err)
	}
	if got := fsAskRead(homeApp, "", "home.md"); !got.Success {
		t.Fatalf("Home lost its note when Work repointed: %s", protocol.Deref(got.Error))
	}

	if saved := notebookSetting(workApp, "notebook.root", ""); !protocol.Deref(saved.Success) {
		t.Fatalf("clearing Work's Notebook folder: %s", protocol.Deref(saved.Error))
	}
	notebookEffectiveRoot(workApp, defaultFolder)
	if got := fsAskRead(workApp, "", "plain.md"); !got.Success {
		t.Fatalf("after clearing its folder, Work cannot read the default folder: %s", protocol.Deref(got.Error))
	}
}
