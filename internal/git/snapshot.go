package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type UntrackedLimits struct {
	Files     int
	Bytes     int64
	FileBytes int64
}

type LeftOut struct {
	Path   string
	Reason string
}

type Blob struct {
	Found   bool
	Content []byte
}

type FileChange struct {
	Status  byte
	Path    string
	OldPath string
	Added   int
	Removed int
	Binary  bool
}

var snapshotIdentity = map[string]string{
	"GIT_AUTHOR_NAME":     "attn review",
	"GIT_AUTHOR_EMAIL":    "review@attn.invalid",
	"GIT_COMMITTER_NAME":  "attn review",
	"GIT_COMMITTER_EMAIL": "review@attn.invalid",
}

func (c *Client) outputWithEnv(ctx context.Context, op Operation, dir string, env map[string]string, stdin io.Reader, args ...string) ([]byte, error) {
	out, err := launchGit(ctx, op, defaultTimeout(op), gitProcess{dir: dir, env: mergedCommandEnv(env), stdin: stdin}, args...)
	return out, withStderr(args, err)
}

func withStderr(args []string, err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(bytes.TrimSpace(exitErr.Stderr)) > 0 {
		return fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(string(exitErr.Stderr)), err)
	}
	return err
}

func (c *Client) ResolveCommit(ctx context.Context, root, rev string) (string, error) {
	out, err := c.outputWithEnv(ctx, OpMetadata, root, nil, nil, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("%s is not a commit in %s", rev, root)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// SnapshotWorktree writes the working tree, tracked and untracked files, as a
// commit whose parent is HEAD, through a private copy of the index so the
// user's index never changes, and points ref at it. Untracked directories and
// files past limits stay out of the commit and come back as LeftOut.
func (c *Client) SnapshotWorktree(ctx context.Context, root, ref, message string, limits UntrackedLimits) (string, []LeftOut, error) {
	env, done, err := c.privateIndex(ctx, root)
	if err != nil {
		return "", nil, err
	}
	defer done()
	leftOut, err := c.addWorkingTree(ctx, root, env, []string{"add", "-A"}, limits)
	if err != nil {
		return "", nil, err
	}
	tree, err := c.outputWithEnv(ctx, OpDiff, root, env, nil, "write-tree")
	if err != nil {
		return "", nil, err
	}
	commitArgs := []string{"commit-tree", "--no-gpg-sign", strings.TrimSpace(string(tree)), "-m", message}
	if parent, err := c.ResolveCommit(ctx, root, "HEAD"); err == nil {
		commitArgs = append(commitArgs, "-p", parent)
	}
	commit, err := c.outputWithEnv(ctx, OpMetadata, root, snapshotIdentity, nil, commitArgs...)
	if err != nil {
		return "", nil, err
	}
	sha := strings.TrimSpace(string(commit))
	if _, err := c.outputWithEnv(ctx, OpMetadata, root, nil, nil, "update-ref", ref, sha); err != nil {
		return "", nil, err
	}
	return sha, leftOut, nil
}

// privateIndex copies the repository's index so a command can stage into it
// without the user's index changing. done removes the copy.
func (c *Client) privateIndex(ctx context.Context, root string) (map[string]string, func(), error) {
	indexPath, err := c.outputWithEnv(ctx, OpMetadata, root, nil, nil, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return nil, nil, err
	}
	dir, err := os.MkdirTemp("", "attn-review-index-")
	if err != nil {
		return nil, nil, err
	}
	done := func() { os.RemoveAll(dir) }
	private := filepath.Join(dir, "index")
	if err := copyIndex(strings.TrimSpace(string(indexPath)), private); err != nil {
		done()
		return nil, nil, err
	}
	return map[string]string{"GIT_INDEX_FILE": private}, done, nil
}

// copyIndex keeps the index's modification time on the copy: git trusts an
// entry's stat data only when the file is older than the index, so a fresh
// time would hide a same-size edit made in the second the index was written.
func copyIndex(from, to string) error {
	info, err := os.Stat(from)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	content, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.WriteFile(to, content, 0o600); err != nil {
		return err
	}
	return os.Chtimes(to, info.ModTime(), info.ModTime())
}

func (c *Client) addWorkingTree(ctx context.Context, root string, env map[string]string, add []string, limits UntrackedLimits) ([]LeftOut, error) {
	leftOut, err := c.oversizedUntracked(ctx, root, limits)
	if err != nil {
		return nil, err
	}
	args := append(append([]string{}, add...), "--", ".")
	for _, left := range leftOut {
		args = append(args, ":(exclude,literal)"+left.Path)
	}
	_, err = c.outputWithEnv(ctx, OpDiff, root, env, nil, args...)
	return leftOut, err
}

func (c *Client) oversizedUntracked(ctx context.Context, root string, limits UntrackedLimits) ([]LeftOut, error) {
	entries, err := c.outputWithEnv(ctx, OpStatus, root, nil, nil, "ls-files", "-o", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return nil, err
	}
	tops := splitNUL(entries)
	if len(tops) == 0 {
		return nil, nil
	}
	type sum struct {
		files   int
		bytes   int64
		biggest int64
	}
	sums := make(map[string]*sum, len(tops))
	for _, top := range tops {
		sums[top] = &sum{}
	}
	files, err := c.outputWithEnv(ctx, OpStatus, root, nil, nil, "ls-files", "-o", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	for _, file := range splitNUL(files) {
		owner := file
		for sums[owner] == nil {
			parent := strings.TrimSuffix(owner, "/")
			cut := strings.LastIndexByte(parent, '/')
			if cut < 0 {
				owner = ""
				break
			}
			owner = parent[:cut+1]
		}
		if owner == "" {
			continue
		}
		var size int64
		if info, err := os.Lstat(filepath.Join(root, file)); err == nil {
			size = info.Size()
		}
		s := sums[owner]
		s.files++
		s.bytes += size
		s.biggest = max(s.biggest, size)
	}
	var leftOut []LeftOut
	for _, top := range tops {
		s, isDir := sums[top], strings.HasSuffix(top, "/")
		var reason string
		switch {
		case s.files > limits.Files:
			reason = fmt.Sprintf("%d untracked files, over the limit of %d", s.files, limits.Files)
		case s.bytes > limits.Bytes:
			reason = fmt.Sprintf("%s untracked, over the limit of %s", megabytes(s.bytes), megabytes(limits.Bytes))
		case !isDir && s.biggest > limits.FileBytes:
			reason = fmt.Sprintf("%s, over the limit of %s for one untracked file", megabytes(s.biggest), megabytes(limits.FileBytes))
		default:
			continue
		}
		leftOut = append(leftOut, LeftOut{Path: top, Reason: reason})
	}
	return leftOut, nil
}

func megabytes(n int64) string {
	return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
}

func splitNUL(out []byte) []string {
	var parts []string
	for _, part := range bytes.Split(out, []byte{0}) {
		if len(part) > 0 {
			parts = append(parts, string(part))
		}
	}
	return parts
}

// ReadBlobs answers each "<rev>:<path>" spec in one process. A spec naming
// nothing, or something other than a file, comes back not found.
func (c *Client) ReadBlobs(ctx context.Context, root string, specs []string) ([]Blob, error) {
	blobs := make([]Blob, len(specs))
	if len(specs) == 0 {
		return blobs, nil
	}
	var stdin bytes.Buffer
	for _, spec := range specs {
		stdin.WriteString(spec)
		stdin.WriteByte('\n')
	}
	out, err := c.outputWithEnv(ctx, OpDiff, root, nil, &stdin, "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	reader := bufio.NewReader(bytes.NewReader(out))
	for i := range specs {
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("git cat-file: answer for %s: %w", specs[i], err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			continue
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("git cat-file: header %q", header)
		}
		content := make([]byte, size+1)
		if _, err := io.ReadFull(reader, content); err != nil {
			return nil, fmt.Errorf("git cat-file: content of %s: %w", specs[i], err)
		}
		if fields[1] == "blob" {
			blobs[i] = Blob{Found: true, Content: content[:size]}
		}
	}
	return blobs, nil
}

// TreeChanges lists what changed from one commit to another, with renames.
func (c *Client) TreeChanges(ctx context.Context, root, from, to string) ([]FileChange, error) {
	out, err := c.outputWithEnv(ctx, OpDiff, root, nil, nil, "diff-tree", "-r", "-z", "-M", "--no-ext-diff", "--raw", "--numstat", from, to)
	if err != nil {
		return nil, err
	}
	return parseChanges(out), nil
}

// WorktreeChanges lists what changed from a commit to the working tree, with
// renames, untracked files included as added and those past limits left out.
func (c *Client) WorktreeChanges(ctx context.Context, root, from string, limits UntrackedLimits) ([]FileChange, error) {
	env, done, err := c.privateIndex(ctx, root)
	if err != nil {
		return nil, err
	}
	defer done()
	if _, err := c.addWorkingTree(ctx, root, env, []string{"add", "--intent-to-add"}, limits); err != nil {
		return nil, err
	}
	out, err := c.outputWithEnv(ctx, OpDiff, root, env, nil, "diff", "-z", "-M", "--no-ext-diff", "--raw", "--numstat", from, "--")
	if err != nil {
		return nil, err
	}
	return parseChanges(out), nil
}

// parseChanges reads `--raw --numstat -z`: every raw record, then every
// numstat record, each naming its path again.
func parseChanges(out []byte) []FileChange {
	parts := strings.Split(string(out), "\x00")
	var changes []FileChange
	at := map[string]int{}
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		if strings.HasPrefix(part, ":") {
			fields := strings.Fields(part)
			if i+1 >= len(parts) || len(fields) == 0 {
				break
			}
			change := FileChange{Status: fields[len(fields)-1][0], Path: parts[i+1], OldPath: parts[i+1]}
			i++
			if (change.Status == 'R' || change.Status == 'C') && i+1 < len(parts) {
				change.Path = parts[i+1]
				i++
			}
			at[change.Path] = len(changes)
			changes = append(changes, change)
			continue
		}
		fields := strings.SplitN(part, "\t", 3)
		if len(fields) != 3 {
			continue
		}
		path := fields[2]
		if path == "" && i+2 < len(parts) {
			path = parts[i+2]
			i += 2
		}
		index, ok := at[path]
		if !ok {
			continue
		}
		if fields[0] == "-" {
			changes[index].Binary = true
		} else {
			changes[index].Added, _ = strconv.Atoi(fields[0])
			changes[index].Removed, _ = strconv.Atoi(fields[1])
		}
	}
	return changes
}
