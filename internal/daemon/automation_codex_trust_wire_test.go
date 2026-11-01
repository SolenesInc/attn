package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestACodexAutomationRefusesAFolderTheUserIsNotKnownToTrust(t *testing.T) {
	for _, c := range []struct {
		name, saved    string
		shared, worked bool
	}{
		{name: "plain Codex in a folder nobody worked in"},
		{name: "shared Codex in a folder nobody worked in", shared: true},
		{name: "plain Codex in a folder saved untrusted", saved: "untrusted", worked: true},
		{name: "shared Codex in a folder saved untrusted", saved: "untrusted", worked: true, shared: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, fakeagent.Codex, fakeagent.Claude)
			app, cli := w.App(), w.Client()
			if c.shared {
				setSetting(t, app, "codex_shared_enabled", "true")
			}
			folder := w.Path("check")
			if err := os.MkdirAll(folder, 0o755); err != nil {
				t.Fatal(err)
			}
			if c.worked {
				w.Launched(w.Spawn(app, fakeagent.Claude, folder))
			}
			if c.saved != "" {
				w.SetCodexFolderTrust(folder, c.saved)
			}
			applyAutomation(t, cli, manualAutomation(w, "Check the code."))
			awaitAutomationChanged(app, 1)

			answered := runAutomationFromApp(app, "refused")
			if answered.Success || !strings.Contains(protocol.Deref(answered.Error), folder) || !strings.Contains(protocol.Deref(answered.Error), "trust") {
				t.Fatalf("the run answered %s, want it refused with a reason naming %s and its trust", describeRunAnswer(answered), folder)
			}
			if answered.Run != nil && protocol.Deref(answered.Run.SessionID) != "" {
				t.Errorf("the refused run started session %s, want no Codex", protocol.Deref(answered.Run.SessionID))
			}
			if got := w.CodexFolderTrust(folder); got != c.saved {
				t.Errorf("Codex saves %s as %q, want it left %q", folder, got, c.saved)
			}
			if servers := w.CodexServers(); len(servers) != 0 {
				t.Errorf("app-servers launched = %v, want none", servers)
			}
		})
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
			case "clone":
				folder = newRepo(t, "shop-review")
				runGit(t, folder, "remote", "add", "origin", "https://github.com/acme/shop.git")
				trusted = folder
			}
			if c.trustRepo {
				w.SetCodexFolderTrust(repo, "trusted")
			} else {
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
