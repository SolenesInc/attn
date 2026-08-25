package git

import (
	"fmt"
	"os"
	"path/filepath"
)

func Clone(cloneURL, targetPath string) error {
	return cloneWithHTTPAuthorization(cloneURL, targetPath, "")
}

func cloneWithHTTPAuthorization(cloneURL, targetPath, authorization string) error {
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

	if out, err := runGitCombinedWithHTTPAuthorization(OpClone, "", cloneURL, authorization, "clone", cloneURL, targetPath); err != nil {
		return fmt.Errorf("git clone failed: %s", string(out))
	}

	return nil
}

func EnsureRepo(cloneURL, targetPath string) (bool, error) {
	targetPath = ExpandPath(targetPath)

	if isGitRepo(targetPath) {
		return false, nil
	}

	if err := Clone(cloneURL, targetPath); err != nil {
		return false, err
	}

	return true, nil
}
