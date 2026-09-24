package automode

import (
	"context"
	"strings"

	"github.com/victorarias/attn/internal/git"
)

type repositoryGit interface {
	GetRepoRoot(context.Context, string) (string, error)
	RemoteHostOwnerRepos(context.Context, string) []string
}

func DetectFromRepo(dir string) (slots map[string][]string, identities []string) {
	return DetectFromRepoWithGit(context.Background(), git.NewClient(), dir)
}

func DetectFromRepoWithGit(ctx context.Context, client repositoryGit, dir string) (slots map[string][]string, identities []string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	root, err := client.GetRepoRoot(ctx, dir)
	if err != nil || root == "" {
		return nil, nil
	}
	identities = client.RemoteHostOwnerRepos(ctx, root)
	return map[string][]string{
		"trusted_repo": append([]string{root}, identities...),
	}, identities
}
