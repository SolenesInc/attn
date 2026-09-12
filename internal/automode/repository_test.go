package automode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	attngit "github.com/victorarias/attn/internal/git"
)

func TestLoadRepositoryRulesFromCheckoutRoot(t *testing.T) {
	root := initRulesRepository(t)
	nested := filepath.Join(root, "nested", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRepositoryRules(t, root, `{
  "rules": [
    {"pattern":["go","test"],"decision":"prompt","sandbox":"bypass"},
    {"pattern":["git","status"],"decision":"allow","sandbox":"inherit"}
  ]
}`)

	loaded, err := LoadRepositoryRules(nested)
	if err != nil {
		t.Fatalf("load repository rules: %v", err)
	}
	if loaded.Path != filepath.Join(attngit.CanonicalizePath(root), RepositoryRulesFile) {
		t.Fatalf("path = %q, want checkout policy path", loaded.Path)
	}
	if len(loaded.Rules) != 2 || loaded.Rules[0].Sandbox != RuleSandboxBypass ||
		loaded.Rules[1].Sandbox != RuleSandboxInherit {
		t.Fatalf("rules = %+v", loaded.Rules)
	}
}

func TestLoadRepositoryRulesUsesLegacySandboxDefaults(t *testing.T) {
	root := initRulesRepository(t)
	writeRepositoryRules(t, root, `{"rules":[
  {"pattern":["go","test"],"decision":"allow"},
  {"pattern":["git","push"],"decision":"prompt"}
]}`)

	loaded, err := LoadRepositoryRules(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Rules[0].Sandbox != RuleSandboxBypass || loaded.Rules[1].Sandbox != RuleSandboxInherit {
		t.Fatalf("legacy defaults = %+v", loaded.Rules)
	}
}

func TestLoadRepositoryRulesNamesInvalidFileAndRule(t *testing.T) {
	root := initRulesRepository(t)
	writeRepositoryRules(t, root, `{"rules":[{"pattern":["go test"],"decision":"allow"}]}`)

	_, err := LoadRepositoryRules(root)
	if err == nil || !strings.Contains(err.Error(), filepath.Join(root, RepositoryRulesFile)) ||
		!strings.Contains(err.Error(), "rule 1") || !strings.Contains(err.Error(), "holds whitespace") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadRepositoryRulesRejectsABadExample(t *testing.T) {
	root := initRulesRepository(t)
	writeRepositoryRules(t, root, `{"rules":[{
  "pattern":["go","test"],"decision":"prompt","match":[["go","list","./..."]]
}]}`)

	_, err := LoadRepositoryRules(root)
	if err == nil || !strings.Contains(err.Error(), "rule 1") || !strings.Contains(err.Error(), "match example") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadRepositoryRulesOutsideRepositoryIsEmpty(t *testing.T) {
	loaded, err := LoadRepositoryRules(t.TempDir())
	if err != nil || loaded.Path != "" || len(loaded.Rules) != 0 {
		t.Fatalf("loaded = %+v, err = %v", loaded, err)
	}
}

func TestLoadRepositoryRulesReadsTheActiveWorktree(t *testing.T) {
	root := initRulesRepository(t)
	writeRepositoryRules(t, root, `{"rules":[{"pattern":["go","test"]}]}`)
	runRulesGit(t, root, "add", RepositoryRulesFile)
	runRulesGit(t, root, "-c", "user.name=attn test", "-c", "user.email=attn@example.test", "commit", "-qm", "rules")
	worktree := filepath.Join(t.TempDir(), "checkout")
	runRulesGit(t, root, "worktree", "add", "--detach", worktree)
	writeRepositoryRules(t, worktree, `{"rules":[{"pattern":["go","list"]}]}`)

	loaded, err := LoadRepositoryRules(worktree)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Path != filepath.Join(attngit.CanonicalizePath(worktree), RepositoryRulesFile) ||
		len(loaded.Rules) != 1 || loaded.Rules[0].Describe() != "go list" {
		t.Fatalf("worktree rules = %+v", loaded)
	}
}

func initRulesRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	command := exec.Command("git", "init", "--quiet", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	return root
}

func runRulesGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func writeRepositoryRules(t *testing.T, root, content string) {
	t.Helper()
	path := filepath.Join(root, RepositoryRulesFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
