package git

import (
	"context"
	"errors"
	"testing"
)

func TestOriginHostOwnerRepo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "remote", "add", "origin", "ssh://git@github.com:2222/owner/name.git")

	host, slug, err := NewClient().OriginHostOwnerRepo(context.Background(), dir)
	if err != nil || host != "github.com" || slug != "owner/name" {
		t.Errorf("OriginHostOwnerRepo() = (%q, %q, %v), want (%q, %q)",
			host, slug, err, "github.com", "owner/name")
	}
}

func TestOriginHostOwnerRepoContextReturnsCancellationCause(t *testing.T) {
	cause := errors.New("foreground preempted sweep")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)

	_, _, err := NewClient().OriginHostOwnerRepo(ctx, t.TempDir())
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want cancellation cause", err)
	}
}

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

func TestOriginHostOwnerRepo_NotGitRepo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if host, slug, _ := NewClient().OriginHostOwnerRepo(context.Background(), dir); host != "" || slug != "" {
		t.Errorf("OriginHostOwnerRepo(non-repo) = (%q, %q), want empty", host, slug)
	}
}

func TestOriginHostOwnerRepo_NoOrigin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runGit(t, dir, "init")
	if host, slug, _ := NewClient().OriginHostOwnerRepo(context.Background(), dir); host != "" || slug != "" {
		t.Errorf("OriginHostOwnerRepo(no origin) = (%q, %q), want empty", host, slug)
	}
}

func TestHostOwnerRepoFromRemoteUnparseable(t *testing.T) {
	t.Parallel()
	cases := []string{"", "just-a-name", "https://github.com/", "relative/path/only"}
	for _, remote := range cases {
		if host, slug := hostOwnerRepoFromRemote(remote); host != "" || slug != "" {
			t.Errorf("hostOwnerRepoFromRemote(%q) = (%q, %q), want empty", remote, host, slug)
		}
	}
}
