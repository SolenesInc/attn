package review

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

// MaxFileRowsBytes keeps a file's rows inside one socket frame.
const MaxFileRowsBytes = protocol.MaxSocketFrameBytes - 1<<20

// Search limits: a name or a word common across the repository matches
// thousands of lines, and the list is for picking one, so it stops early and
// says so. A specific name matched fewer than 10 lines across attn.
const (
	MaxSearchFiles      = 200
	MaxSearchHits       = 500
	MaxSearchHitsInFile = 20
)

type repository struct {
	root, base, head string
}

func openRepository(ctx context.Context, client *git.Client, text string) (repository, error) {
	meta := parse(text).meta
	root, ok := meta.get("repository")
	if !ok || !filepath.IsAbs(root.value) {
		return repository{}, fmt.Errorf("the review names no absolute repository in its frontmatter. Run `attn review start <document>` from the repository under review")
	}
	repo := repository{root: root.value}
	if base, ok := meta.get("base"); ok && base.value != "" {
		resolved, err := client.ResolveCommit(ctx, repo.root, base.value)
		if err != nil {
			return repository{}, fmt.Errorf("base: %s: %w", base.value, err)
		}
		repo.base = resolved
		head, ok := meta.get("head")
		if !ok {
			return repository{}, fmt.Errorf("the review has no head in its frontmatter. Run `attn review start <document>` to take the snapshot")
		}
		repo.head, err = client.ResolveCommit(ctx, repo.root, head.value)
		if err != nil {
			return repository{}, fmt.Errorf("head: %s: %w. Run `attn review start <document>` again to take a fresh snapshot", head.value, err)
		}
	}
	return repo, nil
}

func (repo repository) local(path string) (string, error) {
	if !filepath.IsLocal(filepath.FromSlash(path)) {
		return "", fmt.Errorf("%s is not a path inside the review's repository %s; the file view shows only that repository's files", path, repo.root)
	}
	return filepath.Join(repo.root, filepath.FromSlash(path)), nil
}

// File is the whole file at path, relative to the review's repository, as it
// is now, with the lines removed since base drawn in.
func File(ctx context.Context, client *git.Client, text, path string) (protocol.ReviewFileResultMessage, error) {
	repo, err := openRepository(ctx, client, text)
	if err != nil {
		return protocol.ReviewFileResultMessage{}, err
	}
	full, err := repo.local(path)
	if err != nil {
		return protocol.ReviewFileResultMessage{}, err
	}
	now, hash, gone, err := readNow(full)
	if err != nil {
		return protocol.ReviewFileResultMessage{}, fmt.Errorf("%s: %w", path, err)
	}
	var base []string
	if repo.base != "" {
		if base, err = repo.baseLines(ctx, client, path); err != nil {
			return protocol.ReviewFileResultMessage{}, fmt.Errorf("%s: %w", path, err)
		}
	}
	answer := protocol.ReviewFileResultMessage{Event: protocol.EventReviewFileResult, Success: true, Path: protocol.Ptr(path), Hash: protocol.Ptr(hash)}
	switch {
	case gone && base == nil:
		return protocol.ReviewFileResultMessage{}, fmt.Errorf("%s exists neither now nor at base", path)
	case repo.base == "":
		for line, text := range now {
			answer.Rows = append(answer.Rows, protocol.ReviewRow{Kind: " ", Now: protocol.Ptr(line + 1), Text: text})
		}
	default:
		if base == nil {
			base = []string{}
		}
		var complete bool
		answer.Rows, complete = rows(base, now, 1, len(now))
		if !complete {
			answer.Problem = protocol.Ptr(fmt.Sprintf("%s changed too much since base to diff line by line within %d search steps, so part of it shows as one replaced block.", path, diffBudget))
		}
	}
	if encoded, err := json.Marshal(answer.Rows); err == nil && len(encoded) > MaxFileRowsBytes {
		return protocol.ReviewFileResultMessage{}, fmt.Errorf("%s with its change from base is %d bytes as rows; the file view carries at most %d bytes in one frame", path, len(encoded), MaxFileRowsBytes)
	}
	return answer, nil
}

// baseLines are the file's lines at base, following a rename between base
// and the review's head. A file that is new since base has none.
func (repo repository) baseLines(ctx context.Context, client *git.Client, path string) ([]string, error) {
	renames, err := basePaths(ctx, client, repo.root, repo.base, repo.head)
	if err != nil {
		return nil, fmt.Errorf("the change from base to head could not be read: %w", err)
	}
	basePath := path
	if renamed, ok := renames[path]; ok {
		basePath = renamed
	}
	blobs, err := client.ReadBlobs(ctx, repo.root, []string{repo.base + ":" + basePath})
	if err != nil || !blobs[0].Found {
		return nil, nil
	}
	lines := splitLines(string(blobs[0].Content))
	if lines == nil {
		lines = []string{}
	}
	return lines, nil
}

// Search finds files of the review's repository whose path holds query, and
// lines that hold it as text, ignoring case.
func Search(ctx context.Context, client *git.Client, text, query string) (protocol.ReviewSearchResultMessage, error) {
	repo, err := openRepository(ctx, client, text)
	if err != nil {
		return protocol.ReviewSearchResultMessage{}, err
	}
	query = strings.TrimSpace(query)
	answer := protocol.ReviewSearchResultMessage{Event: protocol.EventReviewSearchResult, Success: true, Files: []string{}, Hits: []protocol.ReviewSearchHit{}}
	if query == "" {
		return answer, nil
	}
	files, err := client.ListFiles(ctx, repo.root)
	if err != nil {
		return protocol.ReviewSearchResultMessage{}, err
	}
	answer.Files = filesNamed(files, query)
	found, err := client.Grep(ctx, repo.root, query, MaxSearchHitsInFile, MaxSearchHits)
	if err != nil {
		return protocol.ReviewSearchResultMessage{}, err
	}
	var limits []string
	if len(answer.Files) > MaxSearchFiles {
		limits = append(limits, fmt.Sprintf("%d files match by name; the first %d are shown", len(answer.Files), MaxSearchFiles))
		answer.Files = answer.Files[:MaxSearchFiles]
	}
	if found.More {
		limits = append(limits, fmt.Sprintf("over %d lines match; the first %d are shown", MaxSearchHits, MaxSearchHits))
	}
	if len(found.Crowded) > 0 {
		limits = append(limits, fmt.Sprintf("%s match on more than %d lines; the first %d of each are shown", strings.Join(found.Crowded, ", "), MaxSearchHitsInFile, MaxSearchHitsInFile))
	}
	for _, hit := range found.Hits {
		answer.Hits = append(answer.Hits, protocol.ReviewSearchHit{Path: hit.Path, Line: hit.Line, Text: hit.Text})
	}
	if len(limits) > 0 {
		answer.Problem = protocol.Ptr(strings.Join(limits, "; ") + ". Search for something more specific.")
	}
	return answer, nil
}

// filesNamed puts paths whose file name holds query before paths that hold it
// only in a directory, each group in path order.
func filesNamed(files []string, query string) []string {
	query = strings.ToLower(query)
	var byName, byDirectory []string
	for _, path := range files {
		lower := strings.ToLower(path)
		switch {
		case strings.Contains(lower[strings.LastIndex(lower, "/")+1:], query):
			byName = append(byName, path)
		case strings.Contains(lower, query):
			byDirectory = append(byDirectory, path)
		}
	}
	sort.Strings(byName)
	sort.Strings(byDirectory)
	return append(append([]string{}, byName...), byDirectory...)
}
