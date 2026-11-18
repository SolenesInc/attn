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

func TestRepositoryIdentityMatchesEveryURLOfOneRepository(t *testing.T) {
	t.Parallel()
	for _, row := range []struct{ a, b string }{
		{"https://github.com/first/shop.git", "git@github.com:first/shop.git"},
		{"https://github.com/First/Shop.git", "ssh://git@github.com/first/shop"},
		{"https://github.com/first/shop.git", "https://github.com/first/shop/"},
		{"/tmp/first/shop", "/tmp/first/shop.git"},
	} {
		if repositoryIdentity(row.a) != repositoryIdentity(row.b) {
			t.Errorf("%q and %q name one repository, but their identities differ", row.a, row.b)
		}
	}
	for _, row := range []struct{ a, b string }{
		{"https://github.com/first/shop.git", "https://github.com/second/shop.git"},
		{"https://github.com/first/shop.git", "https://ghe.corp/first/shop.git"},
		{"/tmp/first/shop", "/tmp/second/shop"},
	} {
		if repositoryIdentity(row.a) == repositoryIdentity(row.b) {
			t.Errorf("%q and %q name different repositories, but share an identity", row.a, row.b)
		}
	}
}
