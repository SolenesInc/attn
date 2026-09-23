package git

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func RepositoryCacheKey(identity string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.ToLower(strings.TrimSpace(identity))))
}

func (c *Client) ValidateLocalClone(ctx context.Context, path, expectedIdentity string) (string, error) {
	path = CanonicalizePath(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("local clone: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("local clone is not a directory: %s", path)
	}
	root, err := c.GetRepoRoot(ctx, path)
	if err != nil || !sameDirectory(root, path) {
		return "", fmt.Errorf("local clone is not a repository root: %s", path)
	}
	mainRepo := c.ResolveMainRepoPath(ctx, root)
	host, ownerRepo, _ := c.OriginHostOwnerRepo(ctx, mainRepo)
	identity := strings.ToLower(host + "/" + ownerRepo)
	if identity != strings.ToLower(expectedIdentity) {
		return "", fmt.Errorf("local clone origin mismatch: got %s want %s", identity, strings.ToLower(expectedIdentity))
	}
	remoteURL, err := c.Output(ctx, OpMetadata, mainRepo, "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("read local clone origin: %w", err)
	}
	if _, err := authorizationForGitURL(strings.TrimSpace(string(remoteURL)), "validation"); err != nil {
		return "", err
	}
	return mainRepo, nil
}

func authorizationForGitURL(rawURL, authorization string) (string, error) {
	if authorization == "" {
		return "", nil
	}
	if !strings.Contains(rawURL, "://") {
		return "", nil
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("parse git remote URL: %w", err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return authorization, nil
	case "http":
		return "", errors.New("refusing authenticated Git access over plaintext HTTP")
	default:
		return "", nil
	}
}

func (c *Client) EnsureManagedClone(ctx context.Context, cloneURL, target, expectedIdentity, authorization string) (string, bool, error) {
	if _, err := os.Stat(target); err == nil {
		mainRepo, err := c.ValidateLocalClone(ctx, target, expectedIdentity)
		return mainRepo, false, err
	} else if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("inspect managed clone: %w", err)
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", false, fmt.Errorf("create managed clone parent: %w", err)
	}
	stagingRoot, err := os.MkdirTemp(parent, ".clone-*")
	if err != nil {
		return "", false, fmt.Errorf("create managed clone staging directory: %w", err)
	}
	defer os.RemoveAll(stagingRoot)
	staging := filepath.Join(stagingRoot, "repo")
	if err := c.cloneWithHTTPAuthorization(ctx, cloneURL, staging, authorization); err != nil {
		return "", false, err
	}
	mainRepo, err := c.publishManagedClone(ctx, staging, target, expectedIdentity)
	return mainRepo, err == nil, err
}

func (c *Client) publishManagedClone(ctx context.Context, staging, target, expectedIdentity string) (string, error) {
	if _, err := c.ValidateLocalClone(ctx, staging, expectedIdentity); err != nil {
		return "", err
	}
	if err := os.Rename(staging, target); err != nil {
		return "", fmt.Errorf("publish managed clone: %w", err)
	}
	mainRepo, err := c.ValidateLocalClone(ctx, target, expectedIdentity)
	if err != nil {
		return "", fmt.Errorf("validate published managed clone: %w", err)
	}
	return mainRepo, nil
}
