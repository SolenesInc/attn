package git

import "testing"

func TestGitArgsInLogsAndTimeoutsHideURLCredentials(t *testing.T) {
	for _, tc := range []struct{ arg, want string }{
		{"https://user:super-secret-token@example.com/acme/repo.git?token=also-secret#frag", "https://REDACTED@example.com/acme/repo.git?REDACTED#REDACTED"},
		{"https://x-access-token:ghp_abc@github.com/acme/repo.git", "https://REDACTED@github.com/acme/repo.git"},
		{"http://user@example.com/repo.git", "http://REDACTED@example.com/repo.git"},
		{"ssh://git:pw@example.com/acme/repo.git", "ssh://REDACTED@example.com/acme/repo.git"},
		{"https://github.com/acme/repo.git", "https://github.com/acme/repo.git"},
		{"git@github.com:acme/repo.git", "git@github.com:acme/repo.git"},
		{"file:///tmp/repo?x=1", "file:///tmp/repo?x=1"},
		{"refs/pull/42/head", "refs/pull/42/head"},
		{"--depth", "--depth"},
	} {
		if got := redactGitArgs([]string{tc.arg})[0]; got != tc.want {
			t.Errorf("redactGitArgs(%q) = %q, want %q", tc.arg, got, tc.want)
		}
	}
}
