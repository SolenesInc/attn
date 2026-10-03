package daemon_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/github"
)

func TestAReviewCloneOverrideMustBeTheRepositoryItselfOverAnEncryptedOrigin(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	cli := w.Client()
	for _, tc := range []struct {
		name, origin, refusal string
	}{
		{"cased", "git@github.test:Acme/Shop.git", ""},
		{"other", "git@github.test:acme/shop-fork.git", "origin mismatch"},
		{"plain", "http://github.test/acme/shop.git", "plaintext HTTP"},
	} {
		clone := newRepo(t, tc.name)
		runGit(t, clone, "remote", "add", "origin", tc.origin)
		_, err := cli.AutomationApply(automationReviewSpec("review-"+tc.name, "manual", automationReviewOverride(clone)))
		if tc.refusal == "" && err != nil {
			t.Errorf("a clone whose origin is %s was refused: %v", tc.origin, err)
		}
		if tc.refusal != "" && (err == nil || !strings.Contains(err.Error(), tc.refusal)) {
			t.Errorf("a clone whose origin is %s answered %v, want a refusal naming %q", tc.origin, err, tc.refusal)
		}
	}
}

func TestAReviewFetchHandsGitTheGitHubTokenOnlyThroughItsEnvironment(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	shims := t.TempDir()
	calls := filepath.Join(shims, "calls")
	shim := "#!/bin/sh\n" +
		"{ printf 'argv:'; printf ' %s' \"$@\"; printf '\\n'; env | grep '^GIT_CONFIG_' | sed 's/^/env:/'; } >> '" + calls + "'\n" +
		"if [ \"$1\" = fetch ]; then echo 'fatal: github.test is unreachable' >&2; exit 128; fi\n" +
		"exec '" + realGit + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shims, "git"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shims+string(os.PathListSeparator)+os.Getenv("PATH"))
	newAutomationGitHub(t)
	w := newWorld(t, fakeagent.Claude)
	cli := w.Client()
	clone := newRepo(t, "shop")
	runGit(t, clone, "remote", "add", "origin", "https://github.test/acme/shop.git")
	applyAutomation(t, cli, automationReviewSpec("review", "manual", automationReviewOverride(clone)))

	_, _ = cli.AutomationRun(1, "unfetched", automationReviewInput(42, strings.Repeat("a", 40)))
	log, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	header := github.GitHTTPSAuthorizationHeader("test-token")
	var fetchedWithToken bool
	for _, call := range strings.Split(string(log), "argv:")[1:] {
		argv, env, _ := strings.Cut(call, "\n")
		if strings.Contains(argv, header) || strings.Contains(argv, "test-token") {
			t.Errorf("git ran with the GitHub token on its command line: %s", argv)
		}
		if strings.Contains(argv, "fetch") && strings.Contains(argv, "refs/pull/42/head") && strings.Contains(env, "="+header+"\n") {
			fetchedWithToken = true
		}
	}
	if !fetchedWithToken {
		t.Errorf("git never fetched the pull request with the token in its environment:\n%s", log)
	}
}
