package present

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/git"
)

type presentGit interface {
	Output(context.Context, git.Operation, string, ...string) ([]byte, error)
}

func Pin(m *Manifest) (baseSHA, headSHA string, err error) {
	return PinWithGit(context.Background(), git.NewClient(), m)
}

func PinWithGit(ctx context.Context, client presentGit, m *Manifest) (baseSHA, headSHA string, err error) {
	if _, statErr := os.Stat(m.Frame.Repo); statErr != nil {
		return "", "", fmt.Errorf("present: frame.repo %q does not exist: %w", m.Frame.Repo, statErr)
	}

	baseSHA, err = resolveSHA(ctx, client, m.Frame.Repo, m.Frame.Base)
	if err != nil {
		return "", "", fmt.Errorf("present: resolve frame.base %q: %w", m.Frame.Base, err)
	}

	headSHA, err = resolveSHA(ctx, client, m.Frame.Repo, m.Frame.Head)
	if err != nil {
		return "", "", fmt.Errorf("present: resolve frame.head %q: %w", m.Frame.Head, err)
	}

	return baseSHA, headSHA, nil
}

func resolveSHA(ctx context.Context, client presentGit, repoDir, ref string) (string, error) {
	out, err := client.Output(ctx, git.OpMetadata, repoDir, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
