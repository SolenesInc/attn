package automode

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryRuleExamplesMustBeCommandsTheRuleDecides(t *testing.T) {
	cases := []struct{ rules, refusal string }{
		{`{"pattern":["go","test"],"decision":"prompt","match":[["go","list","./..."]]}`, "match example"},
		{`{"pattern":["go","test"],"decision":"prompt","match":[[]]}`, "match example cannot be empty"},
		{`{"pattern":["go","test"],"decision":"prompt","not_match":[[]]}`, "not_match example cannot be empty"},
		{`{"pattern":["."],"decision":"prompt","match":[[""]]}`, "match example program cannot be blank"},
		{`{"pattern":["."],"decision":"prompt","not_match":[[""]]}`, "not_match example program cannot be blank"},
		{`{"pattern":["go test"],"decision":"allow"}`, "holds whitespace"},
	}
	for _, tc := range cases {
		t.Run(tc.refusal, func(t *testing.T) {
			root := t.TempDir()
			if output, err := exec.Command("git", "init", "--quiet", root).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v: %s", err, output)
			}
			path := filepath.Join(root, RepositoryRulesFile)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(`{"rules":[`+tc.rules+`]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadRepositoryRules(root)
			if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "rule 1") || !strings.Contains(err.Error(), tc.refusal) {
				t.Fatalf("loading %s = %v, want a refusal naming the file, rule 1 and %q", tc.rules, err, tc.refusal)
			}
		})
	}
}

func TestARepositoryRuleMatchesAProgramByItsExecutableName(t *testing.T) {
	rule := NormalizeRule(Rule{Pattern: Tokens("rm"), Decision: DecisionPrompt})
	for program, want := range map[string]bool{
		"~/bin/rm":           true,
		"/usr/bin/../bin/rm": true,
		"../rm":              true,
		"bin/./rm":           true,
		"/..":                false,
		"dir/..":             false,
		"/usr/bin/rmdir":     false,
	} {
		if got := repositoryRuleMatches(rule, []string{program}); got != want {
			t.Errorf("rm matches %q = %v, want %v", program, got, want)
		}
	}
	if repositoryRuleMatches(NormalizeRule(Rule{Pattern: Tokens("."), Decision: DecisionPrompt}), []string{"dir/.."}) {
		t.Error("a path with no executable name matched a dot rule")
	}
}
