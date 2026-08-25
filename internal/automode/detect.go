package automode

import (
	"strings"

	"github.com/victorarias/attn/internal/git"
)

// DetectFromRepo answers the slots a directory settles on its own, plus the
// remote identities: visibility is a network hop the daemon serves separately.
func DetectFromRepo(dir string) (slots map[string][]string, identities []string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil
	}
	root, err := git.GetRepoRoot(dir)
	if err != nil || root == "" {
		return nil, nil
	}
	identities = git.RemoteHostOwnerRepos(root)
	return map[string][]string{
		"trusted_repo": append([]string{root}, identities...),
	}, identities
}
