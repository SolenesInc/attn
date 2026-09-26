package daemon_test

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestThePickerBrowsesDirectoriesAndOnlyTheAppListsFiles(t *testing.T) {
	t.Setenv("ATTN_BROWSER_HOST_TOKEN", "picker-browser-host-token")
	w := newWorld(t)
	root := w.Path("browse")
	for _, dir := range []string{"docs/guides", ".claude", ".git"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"plan.md", "notes.txt", ".git/COMMIT_EDITMSG"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "plan.md"), filepath.Join(root, "linked.md")); err != nil {
		t.Fatal(err)
	}
	app := pickerApp(w)
	remote := w.Connect(helloWithToken(protocol.Ptr(config.ClientToken())), nil)
	testworld.Await[protocol.InitialStateMessage](remote, protocol.EventInitialState, nil)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name          string
		input         string
		extensions    []string
		wantDirectory string
		wantEntries   []string
	}{
		{"a trailing slash browses into the directory", root + "/", nil, root, []string{".claude", "docs"}},
		{"a partial name lists its parent filtered by it", root + "/do", nil, root, []string{"docs"}},
		{"a nested directory lists its own children", root + "/docs/", nil, filepath.Join(root, "docs"), []string{"guides"}},
		{"extensions add the matching files", root + "/", []string{"md"}, root, []string{".claude", "docs", "plan.md"}},
		{"the prefix filters directories", root + "/.cla", []string{"md"}, root, []string{".claude"}},
		{"the prefix filters files", root + "/pla", []string{"md"}, root, []string{"plan.md"}},
		{"a tilde browses the home directory", "~/attn-picker-no-such-prefix", nil, home, []string{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			listed := browseForPicker(app, c.input, c.extensions)
			if !listed.Success {
				t.Fatalf("browsing %s was refused: %s", c.input, protocol.Deref(listed.Error))
			}
			if listed.Directory != c.wantDirectory || protocol.Deref(listed.HomePath) != home {
				t.Errorf("browsed directory %s with home %q, want %s with home %s", listed.Directory, protocol.Deref(listed.HomePath), c.wantDirectory, home)
			}
			if got := pickerEntryNames(listed.Entries); !slices.Equal(got, c.wantEntries) {
				t.Errorf("entries = %v, want %v", got, c.wantEntries)
			}
			for _, entry := range listed.Entries {
				if wantDir := entry.Name != "plan.md"; entry.IsDir != wantDir || entry.Path != filepath.Join(c.wantDirectory, entry.Name) {
					t.Errorf("entry %+v, want is_dir=%v at %s", entry, wantDir, filepath.Join(c.wantDirectory, entry.Name))
				}
			}
		})
	}

	if refused := browseForPicker(remote, root+"/", []string{"md"}); refused.Success || len(refused.Entries) != 0 {
		t.Fatalf("a client that is not the app listed files: success=%v entries=%v", refused.Success, pickerEntryNames(refused.Entries))
	}
	if allowed := browseForPicker(remote, root+"/", nil); !allowed.Success || !slices.Equal(pickerEntryNames(allowed.Entries), []string{".claude", "docs"}) {
		t.Fatalf("a client that is not the app browsing directories got success=%v entries=%v (%s), want the directories",
			allowed.Success, pickerEntryNames(allowed.Entries), protocol.Deref(allowed.Error))
	}
}

func TestThePickerResolvesAPathToItsRealDirectoryAndRepository(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	repo := newRepo(t, "shop")
	worktree := filepath.Join(filepath.Dir(repo), "shop--feature")
	runGit(t, repo, "worktree", "add", "-b", "feature", worktree)
	subdir := filepath.Join(repo, ".claude")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(repo), "shop-link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name         string
		path         string
		wantResolved string
		wantRepoRoot string
	}{
		{"a repository", repo, repo, repo},
		{"a repository with a trailing slash", repo + "/", repo, repo},
		{"a worktree", worktree, worktree, repo},
		{"a directory inside a repository", subdir, subdir, ""},
		{"a symlink to a repository", link, repo, repo},
	} {
		t.Run(c.name, func(t *testing.T) {
			requestID := uuid.NewString()
			result := testworld.Request(app, protocol.InspectPathMessage{
				Cmd: protocol.CmdInspectPath, Path: c.path, RequestID: protocol.Ptr(requestID),
			}, protocol.EventInspectPathResult, func(r protocol.InspectPathResultMessage) bool { return protocol.Deref(r.RequestID) == requestID })
			if !result.Success || result.Inspection == nil {
				t.Fatalf("inspecting %s was refused: %s", c.path, protocol.Deref(result.Error))
			}
			inspection := result.Inspection
			if inspection.ResolvedPath != c.wantResolved || !inspection.Exists || !inspection.IsDirectory {
				t.Errorf("inspection resolved %s (exists=%v dir=%v), want the directory %s", inspection.ResolvedPath, inspection.Exists, inspection.IsDirectory, c.wantResolved)
			}
			if got := protocol.Deref(inspection.RepoRoot); got != c.wantRepoRoot {
				t.Errorf("repo root %q, want %q", got, c.wantRepoRoot)
			}
		})
	}
}

func pickerApp(w *world) *testworld.Peer {
	w.T.Helper()
	p := w.Connect(protocol.ClientHelloMessage{
		Cmd:              protocol.CmdClientHello,
		ClientKind:       "tauri-app",
		Version:          "protocol-" + protocol.ProtocolVersion,
		Capabilities:     []string{protocol.CapabilityWorkspaceSessions},
		ClientToken:      protocol.Ptr(config.ClientToken()),
		BrowserHostToken: protocol.Ptr(config.BrowserHostToken()),
	}, http.Header{"Origin": {"tauri://localhost"}})
	testworld.Await[protocol.InitialStateMessage](p, protocol.EventInitialState, nil)
	return p
}

func browseForPicker(p *testworld.Peer, input string, extensions []string) protocol.BrowseDirectoryResultMessage {
	p.T.Helper()
	requestID := uuid.NewString()
	return testworld.Request(p, protocol.BrowseDirectoryMessage{
		Cmd: protocol.CmdBrowseDirectory, InputPath: input, Extensions: extensions, RequestID: protocol.Ptr(requestID),
	}, protocol.EventBrowseDirectoryResult, func(r protocol.BrowseDirectoryResultMessage) bool { return protocol.Deref(r.RequestID) == requestID })
}

func pickerEntryNames(entries []protocol.DirectoryEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}
