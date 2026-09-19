package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

func Clone(cloneURL, targetPath string) error {
	return defaultClient.Clone(context.Background(), cloneURL, targetPath)
}

func (c *Client) Clone(ctx context.Context, cloneURL, targetPath string) error {
	return c.cloneWithHTTPAuthorization(ctx, cloneURL, targetPath, "")
}

func (c *Client) cloneWithHTTPAuthorization(ctx context.Context, cloneURL, targetPath, authorization string) error {
	targetPath = ExpandPath(targetPath)
	var err error
	authorization, err = authorizationForGitURL(cloneURL, authorization)
	if err != nil {
		return err
	}

	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("target path already exists: %s", targetPath)
	}

	parentDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	if out, err := c.combinedWithHTTPAuthorization(ctx, OpClone, "", cloneURL, authorization, "clone", cloneURL, targetPath); err != nil {
		return fmt.Errorf("git clone failed: %s", string(out))
	}

	return nil
}

func EnsureRepo(cloneURL, targetPath string) (bool, error) {
	return defaultClient.EnsureRepo(context.Background(), cloneURL, targetPath)
}

func (c *Client) EnsureRepo(ctx context.Context, cloneURL, targetPath string) (bool, error) {
	targetPath = ExpandPath(targetPath)

	if c.isGitRepo(ctx, targetPath) {
		return false, nil
	}

	if err := c.Clone(ctx, cloneURL, targetPath); err != nil {
		return false, err
	}

	return true, nil
}
