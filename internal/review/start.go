package review

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

// UntrackedLimits keep a forgotten build or package store out of a snapshot.
// Measured across the 341 repositories and worktrees on the user's Mac on
// 2026-11-10: a healthy change carries at most 70 untracked files, 5.7 MB in
// all, 486 KB for the biggest. The worktrees past them held a package store
// (20,922 files, 298 MB) and an investigation's logs (81 files, 435 MB).
var UntrackedLimits = git.UntrackedLimits{Files: 1000, Bytes: 64 << 20, FileBytes: 16 << 20}

// refPrefix names the refs that keep a document's snapshots alive. The head
// ref is written by every start; the sent ref by a sent review.
func refPrefix(document string) string {
	sum := sha256.Sum256([]byte(document))
	return "refs/attn/review/" + hex.EncodeToString(sum[:8])
}

func HeadRef(document string) string { return refPrefix(document) + "/head" }

func SentRef(document string) string { return refPrefix(document) + "/sent" }

// Start takes a fresh head for a review document and writes frame,
// repository, base and head into its frontmatter, leaving the text below as
// written. Base is the one asked for, else the head of the last sent review,
// else the document's own base.
func Start(ctx context.Context, client *git.Client, document, dir, base string) (protocol.ReviewStartResult, error) {
	result := protocol.ReviewStartResult{Document: document, LeftOut: []protocol.ReviewLeftOut{}}
	if !filepath.IsAbs(document) {
		return result, fmt.Errorf("document path must be absolute: %s", document)
	}
	root, err := client.GetRepoRoot(ctx, dir)
	if err != nil {
		return result, fmt.Errorf("%s is not inside a git repository: %w", dir, err)
	}
	result.Repository = root
	text, err := os.ReadFile(document)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		result.Created = true
	case err != nil:
		return result, err
	}
	meta := parseFrontmatter(splitLines(string(text)))
	if len(text) > 0 && meta.bodyStart == 0 && strings.HasPrefix(string(text), "---") {
		return result, fmt.Errorf("%s opens a frontmatter block with --- and never closes it. Close it with a --- line", document)
	}
	resolvedBase, err := startBase(ctx, client, root, document, base, meta)
	if err != nil {
		return result, err
	}
	head, leftOut, err := client.SnapshotWorktree(ctx, root, HeadRef(document), "attn review head of "+document, UntrackedLimits)
	if err != nil {
		return result, fmt.Errorf("snapshot %s: %w", root, err)
	}
	for _, left := range leftOut {
		result.LeftOut = append(result.LeftOut, protocol.ReviewLeftOut{Path: left.Path, Reason: left.Reason})
	}
	frame := FrameBrief
	if existing, ok := meta.get("frame"); ok && existing.value != "" {
		frame = existing.value
	}
	values := []field{{key: "attn", value: "review"}, {key: "frame", value: frame}, {key: "repository", value: root}, {key: "base", value: resolvedBase}, {key: "head", value: head}}
	if err := writeAtomically(document, rewriteFrontmatter(string(text), meta, values)); err != nil {
		return result, err
	}
	result.Head = head
	if resolvedBase != "" {
		result.Base = protocol.Ptr(resolvedBase)
	}
	return result, nil
}

// MarkSent records that the user sent a review of head, so the next start
// takes it as its base and the user sees what changed since.
func MarkSent(ctx context.Context, client *git.Client, document, text, head string) error {
	repository, ok := parse(text).meta.get("repository")
	if !ok || !filepath.IsAbs(repository.value) {
		return fmt.Errorf("%s names no repository in its frontmatter. Run `attn review start %s` from the repository under review", document, document)
	}
	commit, err := client.ResolveCommit(ctx, repository.value, head)
	if err != nil {
		return fmt.Errorf("the reviewed head %s: %w", head, err)
	}
	return client.UpdateRef(ctx, repository.value, SentRef(document), commit)
}

func startBase(ctx context.Context, client *git.Client, root, document, asked string, meta frontmatter) (string, error) {
	if asked != "" {
		commit, err := client.ResolveCommit(ctx, root, asked)
		if err != nil {
			return "", fmt.Errorf("--base %s: %w", asked, err)
		}
		return commit, nil
	}
	if sent, err := client.ResolveCommit(ctx, root, SentRef(document)); err == nil {
		return sent, nil
	}
	existing, _ := meta.get("base")
	if repository, _ := meta.get("repository"); existing.value == "" || repository.value != root {
		return "", nil
	}
	commit, err := client.ResolveCommit(ctx, root, existing.value)
	if err != nil {
		return "", fmt.Errorf("the document's base %s: %w. Pass one with --base", existing.value, err)
	}
	return commit, nil
}

// rewriteFrontmatter sets the given keys, in place where the document already
// has them and after its own keys where it does not; an empty value removes
// the key.
func rewriteFrontmatter(text string, meta frontmatter, values []field) string {
	lines := splitLines(text)
	var kept []string
	written := map[string]bool{}
	byKey := map[string]string{}
	for _, value := range values {
		byKey[value.key] = value.value
	}
	if meta.bodyStart > 0 {
		for i := 1; i < meta.bodyStart-1; i++ {
			key, _, ok := strings.Cut(lines[i], ":")
			key = strings.TrimSpace(key)
			value, ours := byKey[key]
			switch {
			case !ok || !ours:
				kept = append(kept, lines[i])
			case !written[key] && value != "":
				kept = append(kept, key+": "+value)
				written[key] = true
			default:
				written[key] = true
			}
		}
	}
	for _, value := range values {
		if !written[value.key] && value.value != "" {
			kept = append(kept, value.key+": "+value.value)
		}
	}
	var out strings.Builder
	out.WriteString("---\n")
	for _, line := range kept {
		out.WriteString(line + "\n")
	}
	out.WriteString("---\n")
	body := lines[meta.bodyStart:]
	out.WriteString(strings.Join(body, "\n"))
	if len(body) > 0 && strings.HasSuffix(text, "\n") {
		out.WriteString("\n")
	}
	return out.String()
}

func writeAtomically(path, content string) error {
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.WriteString(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
