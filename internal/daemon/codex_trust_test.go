package daemon

import (
	"fmt"
	"github.com/victorarias/attn/internal/git"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnUnattendedCodexRunTrustsOnlyAFolderTheUserIsKnownToTrust(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	origins := map[string]string{
		"shop":        "https://github.com/acme/shop.git",
		"shop-review": "git@github.com:Acme/Shop.git",
		"team-shop":   "https://gitlab.example/a/team/shop.git",
		"other-shop":  "https://gitlab.example/b/team/shop.git",
	}
	for _, repo := range []string{"shop", "shop-review", "team-shop", "other-shop", "warehouse"} {
		dir := filepath.Join(root, repo)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		runGitDaemon(t, dir, "init", "-b", "main")
		runGitDaemon(t, dir, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "start")
	}
	runGitDaemon(t, filepath.Join(root, "shop"), "worktree", "add", "-b", "nightly", filepath.Join(root, "shop--nightly"))
	for _, dir := range []string{"shop/pkg1", "shop/pkg2", "notes"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	originOf := func(repository string) string {
		rel, err := filepath.Rel(root, repository)
		if remote, ok := origins[rel]; err == nil && ok {
			return git.NormalizedOrigin(remote)
		}
		return ""
	}

	for _, c := range []struct {
		name    string
		folder  string
		worked  []string
		saved   map[string]string
		add     string
		refused bool
	}{
		{name: "the repository an agent worked in", folder: "shop", worked: []string{"shop"}, add: "shop"},
		{name: "a worktree of a repository an agent worked in", folder: "shop--nightly", worked: []string{"shop"}, add: "shop"},
		{name: "the repository of a worktree an agent worked in", folder: "shop", worked: []string{"shop--nightly"}, add: "shop"},
		{name: "a subfolder of a repository an agent worked in", folder: "shop/pkg2", worked: []string{"shop"}, add: "shop"},
		{name: "a subfolder beside the one an agent worked in", folder: "shop/pkg2", worked: []string{"shop/pkg1"}, add: "shop"},
		{name: "another clone of a repository an agent worked in", folder: "shop-review", worked: []string{"shop"}, add: "shop-review"},
		{name: "a clone of another repository whose origin ends the same way", folder: "other-shop", worked: []string{"team-shop"}, refused: true},
		{name: "a repository nobody worked in", folder: "shop", worked: []string{"warehouse", "notes"}, refused: true},
		{name: "a folder outside any repository an agent worked in", folder: "notes", worked: []string{"notes"}, add: "notes"},
		{name: "a folder outside any repository nobody worked in", folder: "notes", worked: []string{"shop"}, refused: true},
		{name: "a folder Codex trusts", folder: "notes", saved: map[string]string{"notes": "trusted"}},
		{name: "a subfolder of a repository Codex trusts", folder: "shop/pkg2", saved: map[string]string{"shop": "trusted"}},
		{name: "a folder Codex trusts inside a repository saved untrusted", folder: "shop/pkg2", saved: map[string]string{"shop/pkg2": "trusted", "shop": "untrusted"}},
		{name: "a folder saved untrusted that an agent worked in", folder: "shop", worked: []string{"shop"}, saved: map[string]string{"shop": "untrusted"}, refused: true},
		{name: "a subfolder of a repository saved untrusted that an agent worked in", folder: "shop/pkg2", worked: []string{"shop/pkg2"}, saved: map[string]string{"shop": "untrusted"}, refused: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			var raw strings.Builder
			for dir, level := range c.saved {
				fmt.Fprintf(&raw, "[projects.%s]\ntrust_level = %q\n", tomlBasicString(filepath.Join(root, dir)), level)
			}
			config, err := parseCodexTrust(filepath.Join(root, "config.toml"), []byte(raw.String()))
			if err != nil {
				t.Fatal(err)
			}
			var worked []string
			for _, dir := range c.worked {
				worked = append(worked, filepath.Join(root, dir))
			}
			folder := filepath.Join(root, c.folder)

			add, err := unattendedCodexTrustToAdd(folder, config, worked, originOf)
			if c.refused {
				if err == nil || !strings.Contains(err.Error(), folder) {
					t.Fatalf("the run was allowed (adding trust to %q, error %v), want it refused with a reason naming %s", add, err, folder)
				}
				return
			}
			if err != nil {
				t.Fatalf("the run was refused: %v", err)
			}
			want := ""
			if c.add != "" {
				want = filepath.Join(root, c.add)
			}
			if add != want {
				t.Errorf("the run adds trust to %q, want %q", add, want)
			}
		})
	}
}

func TestTwoRemotesNameTheSameRepositoryOnlyWhenTheirHostedLocationsMatch(t *testing.T) {
	for _, c := range []struct {
		name, a, b string
		same       bool
	}{
		{"https and scp-style SSH remotes of one repository", "https://github.com/acme/shop.git", "git@github.com:Acme/Shop.git", true},
		{"a trailing slash and a missing .git suffix", "https://github.com/acme/shop/", "https://github.com/acme/shop.git", true},
		{"another repository whose path ends the same way", "https://gitlab.example/a/team/shop.git", "https://gitlab.example/b/team/shop.git", false},
		{"local file remotes that differ only in case", "file:///srv/git/Team/shop.git", "file:///srv/git/team/shop.git", false},
		{"SSH remotes in different users' homes", "alice@example.org:shop.git", "bob@example.org:shop.git", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, b := git.NormalizedOrigin(c.a), git.NormalizedOrigin(c.b)
			if a == "" || b == "" {
				t.Fatalf("origins %q and %q normalize to %q and %q, want both named", c.a, c.b, a, b)
			}
			if (a == b) != c.same {
				t.Errorf("origins %q and %q match = %v, want %v", c.a, c.b, a == b, c.same)
			}
		})
	}
}
