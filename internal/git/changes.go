package git

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// WorktreeChanges is what a working tree folder holds against its commit.
type WorktreeChanges struct {
	Head string
	// Dirty are the files under the folder that differ from Head, untracked
	// ones included, as absolute paths.
	Dirty []string
	// Moved are the files under the folder that differ between the commit the
	// caller last saw and Head, as absolute paths.
	Moved []string
}

// WorktreeChangesSince reads folder's commit and its changed files, and the
// files that changed between since and that commit when since is set and
// differs.
func (c *Client) WorktreeChangesSince(ctx context.Context, folder, since string) (WorktreeChanges, error) {
	out, err := c.Output(ctx, OpMetadata, folder, "rev-parse", "--show-prefix", "HEAD")
	if err != nil {
		return WorktreeChanges{}, err
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != 2 {
		return WorktreeChanges{}, fmt.Errorf("git rev-parse --show-prefix HEAD in %s answered %q, want the folder's prefix and the commit", folder, out)
	}
	prefix, head := lines[0], lines[1]
	under := func(path string) string {
		return filepath.Join(folder, filepath.FromSlash(strings.TrimPrefix(path, prefix)))
	}
	changes := WorktreeChanges{Head: head}
	status, err := c.Output(ctx, OpStatus, folder, "--no-optional-locks", "status", "--porcelain=v1", "-z", "--no-renames", "--untracked-files=all", "--", ".")
	if err != nil {
		return WorktreeChanges{}, err
	}
	for _, entry := range splitNUL(status) {
		if len(entry) > 3 {
			changes.Dirty = append(changes.Dirty, under(entry[3:]))
		}
	}
	if since == "" || since == head {
		return changes, nil
	}
	diff, err := c.Output(ctx, OpDiff, folder, "diff", "--name-only", "--no-renames", "-z", since, head, "--", ".")
	if err != nil {
		return WorktreeChanges{}, err
	}
	for _, path := range splitNUL(diff) {
		changes.Moved = append(changes.Moved, under(path))
	}
	return changes, nil
}
