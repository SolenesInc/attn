package daemon

import "testing"

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
			a, b := normalizedOrigin(c.a), normalizedOrigin(c.b)
			if a == "" || b == "" {
				t.Fatalf("origins %q and %q normalize to %q and %q, want both named", c.a, c.b, a, b)
			}
			if (a == b) != c.same {
				t.Errorf("origins %q and %q match = %v, want %v", c.a, c.b, a == b, c.same)
			}
		})
	}
}
