package git

import "testing"

func TestHostOwnerRepoFromRemote(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		remote   string
		wantHost string
		wantSlug string
	}{
		{"scp-like with .git", "git@github.com:owner/name.git", "github.com", "owner/name"},
		{"scp-like without .git", "git@github.com:owner/name", "github.com", "owner/name"},
		{"ssh URL", "ssh://git@github.com/owner/name.git", "github.com", "owner/name"},
		{"ssh URL enterprise host", "ssh://git@ghe.corp/owner/name", "ghe.corp", "owner/name"},
		{"https URL", "https://github.com/owner/name.git", "github.com", "owner/name"},
		{"https URL without .git", "https://github.com/owner/name", "github.com", "owner/name"},
		{"https URL with trailing slash", "https://github.com/owner/name/", "github.com", "owner/name"},
		{"ssh URL with port", "ssh://git@github.com:2222/owner/name.git", "github.com", "owner/name"},
		{"empty", "", "", ""},
		{"bare name", "just-a-name", "", ""},
		{"host without repo", "https://github.com/", "", ""},
		{"relative path", "relative/path/only", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, slug := hostOwnerRepoFromRemote(tc.remote)
			if host != tc.wantHost || slug != tc.wantSlug {
				t.Errorf("hostOwnerRepoFromRemote(%q) = (%q, %q), want (%q, %q)",
					tc.remote, host, slug, tc.wantHost, tc.wantSlug)
			}
		})
	}
}
