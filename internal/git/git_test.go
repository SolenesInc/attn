package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Branch identity follows the nearest .git and its HEAD, including gitfiles.
// Detached labels use seven hex digits; unknown layouts retain git's behavior.
func TestBranchInfoFileLayouts(t *testing.T) {
	cases := []struct {
		name       string
		files      map[string]string
		cwd        string
		branch     string
		worktree   bool
		main       string
		repository string
	}{
		{name: "normal", files: map[string]string{"repo/.git/HEAD": "ref: refs/heads/topic/nested\n"}, cwd: "repo/src/deep", branch: "topic/nested", repository: "repo"},
		{name: "nearest repo", files: map[string]string{"repo/.git/HEAD": "ref: refs/heads/outer\n", "repo/inner/.git/HEAD": "ref: refs/heads/inner\n"}, cwd: "repo/inner", branch: "inner", repository: "repo/inner"},
		{name: "unborn", files: map[string]string{"repo/.git/HEAD": "ref: refs/heads/new\n"}, cwd: "repo", branch: "new", repository: "repo"},
		{name: "detached sha1", files: map[string]string{"repo/.git/HEAD": strings.Repeat("a", 40) + "\n"}, cwd: "repo", branch: "aaaaaaa", repository: "repo"},
		{name: "detached sha256", files: map[string]string{"repo/.git/HEAD": strings.Repeat("b", 64) + "\n"}, cwd: "repo", branch: "bbbbbbb", repository: "repo"},
		{name: "absolute worktree", files: map[string]string{"wt/.git": "gitdir: ROOT/main/.git/worktrees/wt\n", "main/.git/worktrees/wt/HEAD": "ref: refs/heads/topic\n", "main/.git/worktrees/wt/commondir": "../..\n"}, cwd: "wt/subdir", branch: "topic", worktree: true, main: "main", repository: "main"},
		{name: "relative worktree", files: map[string]string{"wt/.git": "gitdir: ../main/.git/worktrees/wt\n", "main/.git/worktrees/wt/HEAD": "ref: refs/heads/topic\n"}, cwd: "wt", branch: "topic", worktree: true, main: "main", repository: "../main"},
		{name: "submodule gitfile", files: map[string]string{"repo/sub/.git": "gitdir: ../.git/modules/sub\n", "repo/.git/modules/sub/HEAD": "ref: refs/heads/module\n"}, cwd: "repo/sub", branch: "module", repository: "repo/sub"},
		{name: "reftable stub", files: map[string]string{"repo/.git/HEAD": "ref: refs/heads/.invalid\n"}, cwd: "repo"},
		{name: "reftable directory", files: map[string]string{"repo/.git/HEAD": strings.Repeat("a", 40), "repo/.git/reftable/tables.list": ""}, cwd: "repo"},
		{name: "worktree own reftable", files: map[string]string{"wt/.git": "gitdir: ROOT/main/.git/worktrees/wt\n", "main/.git/worktrees/wt/HEAD": strings.Repeat("a", 40), "main/.git/worktrees/wt/commondir": "../..\n", "main/.git/worktrees/wt/reftable/tables.list": ""}, cwd: "wt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := canonicalFixturePath(t, t.TempDir())
			for path, content := range tc.files {
				path = filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(strings.ReplaceAll(content, "ROOT", root)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cwd := filepath.Join(root, tc.cwd)
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_CEILING_DIRECTORIES", "")
			t.Setenv("PATH", t.TempDir())
			got, err := NewClient().GetBranchInfo(context.Background(), cwd)
			want := BranchInfo{Branch: tc.branch, IsWorktree: tc.worktree, Repository: tc.repository}
			if tc.repository != "" && !strings.HasPrefix(tc.repository, "../") {
				want.Repository = filepath.Join(root, tc.repository)
			}
			if tc.main != "" {
				want.MainRepo = filepath.Join(root, tc.main)
			}
			if err != nil || *got != want {
				t.Fatalf("GetBranchInfo = %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

func TestBranchInfoGitParity(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(*testing.T, string) string
	}{
		{"normal", func(t *testing.T, repo string) string { return repo }},
		{"unborn", func(t *testing.T, repo string) string { runGit(t, repo, "checkout", "--orphan", "unborn"); return repo }},
		{"discovery ceiling", func(t *testing.T, repo string) string {
			dir := filepath.Join(repo, "src", "deep")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_CEILING_DIRECTORIES", canonicalFixturePath(t, filepath.Dir(dir)))
			return dir
		}},
		{"subdirectory", func(t *testing.T, repo string) string {
			dir := filepath.Join(repo, "src")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
		{"linked worktree", func(t *testing.T, repo string) string {
			dir := filepath.Join(filepath.Dir(repo), "wt")
			runGit(t, repo, "worktree", "add", "-b", "topic", dir)
			return dir
		}},
		{"detached", func(t *testing.T, repo string) string { runGit(t, repo, "checkout", "--detach"); return repo }},
		{"gitfile", func(t *testing.T, repo string) string {
			dir := filepath.Join(filepath.Dir(repo), "separate")
			if err := os.MkdirAll(filepath.Join(repo, ".git", "modules"), 0o755); err != nil {
				t.Fatal(err)
			}
			runGit(t, repo, "init", "--separate-git-dir", filepath.Join(repo, ".git", "modules", "sub"), dir)
			return dir
		}},
		{"bare", func(t *testing.T, repo string) string {
			dir := filepath.Join(filepath.Dir(repo), "bare")
			runGit(t, repo, "init", "--bare", dir)
			return dir
		}},
		{"git metadata directory", func(t *testing.T, repo string) string { return filepath.Join(repo, ".git", "objects") }},
		{"nested bare", func(t *testing.T, repo string) string {
			dir := filepath.Join(repo, "bare")
			runGit(t, repo, "init", "--bare", dir)
			return dir
		}},
		{"not a repo", func(t *testing.T, repo string) string { return filepath.Dir(repo) }},
		{"missing HEAD", func(t *testing.T, repo string) string {
			if err := os.Remove(filepath.Join(repo, ".git", "HEAD")); err != nil {
				t.Fatal(err)
			}
			return repo
		}},
		{"missing gitdir", func(t *testing.T, repo string) string {
			if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: missing\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return repo
		}},
		{"unknown HEAD", func(t *testing.T, repo string) string {
			if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("unknown\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return repo
		}},
		{"reftable", func(t *testing.T, repo string) string { initReftable(t, repo); return repo }},
		{"reftable worktree", func(t *testing.T, repo string) string {
			initReftable(t, repo)
			runGit(t, repo, "commit", "--allow-empty", "-m", "init")
			dir := filepath.Join(filepath.Dir(repo), "wt")
			runGit(t, repo, "worktree", "add", "-b", "topic", dir)
			return dir
		}},
		{"reftable detached", func(t *testing.T, repo string) string {
			initReftable(t, repo)
			runGit(t, repo, "commit", "--allow-empty", "-m", "init")
			runGit(t, repo, "checkout", "--detach")
			return repo
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := filepath.Join(t.TempDir(), "repo")
			if err := os.Mkdir(repo, 0o755); err != nil {
				t.Fatal(err)
			}
			runGit(t, repo, "init", "-b", "main")
			runGit(t, repo, "-c", "core.abbrev=7", "commit", "--allow-empty", "-m", "init")
			dir := tc.prepare(t, repo)
			client := NewClient()
			got, err := client.GetBranchInfo(context.Background(), dir)
			want := branchIdentityObservedByGit(t, dir)
			if err != nil || *got != want {
				t.Fatalf("GetBranchInfo = %+v, %v; git = %+v", got, err, want)
			}
		})
	}
}

func initReftable(t *testing.T, repo string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "--ref-format=reftable", "-b", "main")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(out), "unknown option") {
			t.Skip("git lacks reftable support")
		}
		t.Fatalf("git init reftable: %s: %v", out, err)
	}
}

func branchIdentityObservedByGit(t *testing.T, dir string) BranchInfo {
	t.Helper()
	observe := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	root, err := observe("rev-parse", "--show-toplevel")
	if err != nil {
		return BranchInfo{}
	}
	branch, err := observe("symbolic-ref", "--short", "HEAD")
	if err != nil {
		branch, err = observe("rev-parse", "--short=7", "HEAD")
		if err != nil {
			t.Fatal(err)
		}
	}
	gitDir, err := observe("rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatal(err)
	}
	common, err := observe("rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		t.Fatal(err)
	}
	want := BranchInfo{Branch: branch, Repository: canonicalFixturePath(t, root)}
	if canonicalFixturePath(t, common) != canonicalFixturePath(t, gitDir) {
		want.IsWorktree = true
		want.MainRepo = filepath.Dir(canonicalFixturePath(t, common))
		want.Repository = want.MainRepo
	}
	return want
}

func canonicalFixturePath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(resolved)
}
