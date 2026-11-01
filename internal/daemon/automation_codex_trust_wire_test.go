package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestACodexAutomationRefusesAFolderTheUserIsNotKnownToTrust(t *testing.T) {
	inRepo := func(sub string) func(t *testing.T, repo string) string {
		return func(t *testing.T, repo string) string {
			folder := filepath.Join(repo, sub)
			if err := os.MkdirAll(folder, 0o755); err != nil {
				t.Fatal(err)
			}
			return folder
		}
	}
	for _, c := range []struct {
		name, saved, worked, repoOrigin string
		shared, otherProfile            bool
		folder                          func(t *testing.T, repo string) string
	}{
		{name: "plain Codex in a repository nobody worked in", folder: inRepo("")},
		{name: "shared Codex in a repository nobody worked in", shared: true, folder: inRepo("")},
		{name: "plain Codex in a folder saved untrusted", saved: "untrusted", worked: ".", folder: inRepo("")},
		{name: "shared Codex in a folder saved untrusted", saved: "untrusted", worked: ".", shared: true, folder: inRepo("")},
		{name: "a clone of another repository whose origin ends the same way", worked: ".", folder: cloneWithOrigin("https://gitlab.example/b/team/shop.git")},
		{name: "a clone whose local origin differs only in case", worked: ".", repoOrigin: "file:///srv/git/Team/shop.git", folder: cloneWithOrigin("file:///srv/git/team/shop.git")},
		{name: "a clone whose origin is another SSH user's home", worked: ".", repoOrigin: "alice@example.org:shop.git", folder: cloneWithOrigin("bob@example.org:shop.git")},
		{name: "a repository an agent worked in from another profile", worked: "pkg1", otherProfile: true, folder: inRepo("pkg2")},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex, fakeagent.Claude)
			app := w.App()
			if c.shared {
				setSetting(t, app, "codex_shared_enabled", "true")
			}
			repo := newRepo(t, "shop")
			origin := "https://gitlab.example/a/team/shop.git"
			if c.repoOrigin != "" {
				origin = c.repoOrigin
			}
			runGit(t, repo, "remote", "add", "origin", origin)
			if c.worked != "" {
				w.Launched(w.Spawn(app, fakeagent.Claude, filepath.Join(repo, c.worked)))
			}
			folder := c.folder(t, repo)
			if c.saved != "" {
				w.SetCodexFolderTrust(folder, c.saved)
			}
			spec := strings.Replace(manualAutomation(w, "Check the code."), strconv.Quote(w.Path("check")), strconv.Quote(folder), 1)
			if c.otherProfile {
				side := createProfile(app, "Side")
				selectProfile(app, side.ID)
				applied := testworld.Request(app, protocol.AutomationApplyMessage{Cmd: protocol.CmdAutomationApply, RequestID: protocol.Ptr("side"), ProfileID: protocol.Ptr(side.ID), DefinitionYaml: spec}, protocol.EventAutomationApplyResult, automationAnswer[protocol.AutomationApplyResultMessage]("side"))
				if !applied.Success {
					t.Fatalf("apply in Side: %+v", applied)
				}
			} else {
				applyAutomation(t, w.Client(), spec)
			}
			awaitAutomationChanged(app, 1)

			answered := runAutomationFromApp(app, "refused")
			if answered.Success || !strings.Contains(protocol.Deref(answered.Error), folder) || !strings.Contains(protocol.Deref(answered.Error), "trust") {
				t.Fatalf("the run answered %s, want it refused with a reason naming %s and its trust", describeRunAnswer(answered), folder)
			}
			if answered.Run != nil && protocol.Deref(answered.Run.SessionID) != "" {
				t.Errorf("the refused run started session %s, want no Codex", protocol.Deref(answered.Run.SessionID))
			}
			for _, dir := range []string{folder, repo} {
				if got := w.CodexFolderTrust(dir); dir != folder && got != "" || dir == folder && got != c.saved {
					t.Errorf("Codex saves %s as %q, want it left as the user saved it", dir, got)
				}
			}
			if servers := w.CodexServers(); len(servers) != 0 {
				t.Errorf("app-servers launched = %v, want none", servers)
			}
		})
	}
}

func cloneWithOrigin(origin string) func(t *testing.T, repo string) string {
	return func(t *testing.T, _ string) string {
		clone := newRepo(t, "shop-review")
		runGit(t, clone, "remote", "add", "origin", origin)
		return clone
	}
}

func TestAPlainCodexAutomationStartsInsideARepositoryTheUserTrusts(t *testing.T) {
	for _, c := range []struct {
		name      string
		place     string
		trustRepo bool
	}{
		{name: "a worktree of a repository an agent worked in", place: "worktree"},
		{name: "a subfolder of a repository an agent worked in", place: "subfolder"},
		{name: "a subfolder of a repository Codex trusts", place: "subfolder", trustRepo: true},
		{name: "a subfolder beside the one an agent worked in", place: "sibling"},
		{name: "another clone of a repository an agent worked in", place: "clone"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex, fakeagent.Claude)
			app, cli := w.App(), w.Client()
			repo, worktree := newWorktree(t, "shop", "nightly")
			runGit(t, repo, "remote", "add", "origin", "https://github.com/acme/shop.git")
			folder, trusted := worktree, repo
			switch c.place {
			case "subfolder":
				folder = filepath.Join(repo, "pkg")
				if err := os.MkdirAll(folder, 0o755); err != nil {
					t.Fatal(err)
				}
			case "sibling":
				folder = filepath.Join(repo, "pkg2")
				if err := os.MkdirAll(folder, 0o755); err != nil {
					t.Fatal(err)
				}
			case "clone":
				folder = newRepo(t, "shop-review")
				runGit(t, folder, "remote", "add", "origin", "git@github.com:Acme/Shop.git")
				trusted = folder
			}
			switch {
			case c.trustRepo:
				w.SetCodexFolderTrust(repo, "trusted")
			case c.place == "sibling":
				w.Launched(w.Spawn(app, fakeagent.Claude, filepath.Join(repo, "pkg1")))
			default:
				w.Launched(w.Spawn(app, fakeagent.Claude, repo))
			}
			applyAutomation(t, cli, fmt.Sprintf(`api_version: attn.dev/automations/v1alpha1
name: Nightly check
trigger: {type: manual}
prompt: Report the flaky tests.
launch: {driver: codex}
location: {type: directory, path: %q}
`, folder))
			awaitAutomationChanged(app, 1)

			answered := runAutomationFromApp(app, "trusted")
			if !answered.Success || answered.Run == nil || protocol.Deref(answered.Run.SessionID) == "" {
				t.Fatalf("the run answered %s, want it started", describeRunAnswer(answered))
			}
			if got := w.CodexFolderTrust(trusted); got != "trusted" {
				t.Errorf("Codex saves the repository %s as %q, want trusted before Codex starts", trusted, got)
			}
			if got := w.CodexFolderTrust(folder); folder != trusted && got != "" {
				t.Errorf("Codex saves %s as %q, want the trust on its repository alone", folder, got)
			}
			if got := w.Launched(string(protocol.Deref(answered.Run.SessionID))).Prompted(); !strings.Contains(got, "Report the flaky tests.") {
				t.Errorf("codex received %q, want the automation's prompt", got)
			}
		})
	}
}
