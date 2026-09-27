package daemon_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/automode"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

func TestRepositoryRulesComeFromTheCheckoutADirectoryBelongsTo(t *testing.T) {
	w := newWorld(t)
	cli := w.Client()
	repo := w.Path("widgets")
	autoModeGitRepo(t, repo, "")
	writeAutoModeRepositoryRules(t, repo, `{"rules":[{"pattern":["go","test"],"decision":"prompt","sandbox":"bypass"}]}`)
	runGit(t, repo, "add", automode.RepositoryRulesFile)
	runGit(t, repo, "-c", "user.name=attn", "-c", "user.email=attn@example.test", "commit", "-qm", "rules")
	nested := filepath.Join(repo, "nested", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	worktree := w.Path("widgets-review")
	runGit(t, repo, "worktree", "add", "-q", "--detach", worktree)
	writeAutoModeRepositoryRules(t, worktree, `{"rules":[{"pattern":["go","list"],"decision":"prompt","sandbox":"bypass"}]}`)

	for _, tc := range []struct{ dir, root, rule string }{
		{nested, repo, "[[go] [test]]"},
		{worktree, worktree, "[[go] [list]]"},
	} {
		shown, err := cli.AutoModeShow(tc.dir)
		if err != nil {
			t.Fatalf("automode show %s: %v", tc.dir, err)
		}
		want := filepath.Join(attngit.CanonicalizePath(tc.root), automode.RepositoryRulesFile)
		if protocol.Deref(shown.RepositoryRulesPath) != want || len(shown.RepositoryRules) != 1 || fmt.Sprint(shown.RepositoryRules[0].Pattern) != tc.rule {
			t.Errorf("rules for %s = %+v from %q, want %q from %s", tc.dir, shown.RepositoryRules, protocol.Deref(shown.RepositoryRulesPath), tc.rule, want)
		}
	}

	broken := w.Path("broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, ".git"), []byte("gitdir: missing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.AutoModeShow(broken); err == nil || !strings.Contains(err.Error(), "discover repository auto-mode rules") {
		t.Errorf("automode show in a checkout git cannot read answered %v, want a refusal naming rule discovery", err)
	}
}
