package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type releaseRange struct {
	from, to string
}

func (r releaseRange) revisions() string {
	if r.from == "" {
		return r.to
	}
	return r.from + ".." + r.to
}

func (r releaseRange) marker() string {
	return fmt.Sprintf("<!-- changelog-commits: %s..%s -->", r.from, r.to)
}

type releaseCommit struct {
	sha       string
	subject   string
	body      string
	seeds     []string
	fragments []releaseFragment
}

type releaseFragment struct {
	path    string
	content string
}

type seedRecord struct {
	Seed struct {
		Title  string `json:"title"`
		Body   string `json:"body"`
		Status string `json:"status"`
	} `json:"seed"`
	Notes []struct {
		Kind string `json:"kind"`
		Body string `json:"body"`
	} `json:"notes"`
}

// A whole release's notes for 1,475 commits rendered to 4.4 MB, which claude -p
// refused; a 2.0 MB slice of them was accepted.
const writerPartBytes = 1_000_000

const notesUsage = "usage: release-train notes range | notes split <dir> [--max-bytes N]"

func runNotes(root string, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(notesUsage)
	}
	switch args[0] {
	case "range":
		if len(args) != 1 {
			return errors.New(notesUsage)
		}
		r, err := releaseRangeAt(root, "HEAD")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, r.marker())
		return err
	case "split":
		flags := flag.NewFlagSet("notes split", flag.ContinueOnError)
		maxBytes := flags.Int("max-bytes", writerPartBytes, "largest part, unless one group alone is larger")
		if len(args) < 2 {
			return errors.New(notesUsage)
		}
		if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 {
			return errors.New(notesUsage)
		}
		r, err := releaseRangeAt(root, "HEAD")
		if err != nil {
			return err
		}
		header, groups, err := renderNotes(root, r)
		if err != nil {
			return err
		}
		return writeNoteParts(args[1], header, groups, *maxBytes)
	default:
		return errors.New(notesUsage)
	}
}

func packNoteParts(groups []string, maxBytes int) [][]string {
	parts := [][]string{nil}
	size := 0
	for _, group := range groups {
		if size > 0 && size+len(group) > maxBytes {
			parts = append(parts, nil)
			size = 0
		}
		parts[len(parts)-1] = append(parts[len(parts)-1], group)
		size += len(group)
	}
	return parts
}

func writeNoteParts(dir, header string, groups []string, maxBytes int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	parts := packNoteParts(groups, maxBytes)
	for i, part := range parts {
		content := fmt.Sprintf("Part %d of %d of the %s", i+1, len(parts), header) + strings.Join(part, "")
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("part-%03d.txt", i+1)), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func releaseRangeAt(root, ref string) (releaseRange, error) {
	to, err := resolveCommit(root, ref)
	if err != nil {
		return releaseRange{}, err
	}
	if _, err := gitOutput(root, "cat-file", "-e", to+":"+defaultManifestPath); err != nil {
		return releaseRange{to: to}, nil
	}
	previous, err := readManifestAtRef(root, to, defaultManifestPath)
	if err != nil {
		return releaseRange{}, fmt.Errorf("previous release manifest: %w", err)
	}
	return releaseRange{from: previous.MainSHA, to: to}, nil
}

func releaseCommits(root string, r releaseRange) ([]releaseCommit, error) {
	out, err := gitOutput(root, "log", "--no-merges", "--reverse", "--format=%H", r.revisions())
	if err != nil {
		return nil, err
	}
	var commits []releaseCommit
	for _, sha := range strings.Fields(string(out)) {
		touched, err := gitOutput(root, "diff-tree", "--root", "--no-commit-id", "--name-only", "-r", sha, "--", defaultManifestPath)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(string(touched)) != "" {
			continue
		}
		commit, err := readReleaseCommit(root, sha)
		if err != nil {
			return nil, err
		}
		commits = append(commits, commit)
	}
	return commits, nil
}

func readReleaseCommit(root, sha string) (releaseCommit, error) {
	message, err := gitOutput(root, "log", "-1", "--format=%s%x00%b%x00%(trailers:key=Attn-Seed,valueonly)", sha)
	if err != nil {
		return releaseCommit{}, err
	}
	parts := strings.SplitN(string(message), "\x00", 3)
	if len(parts) != 3 {
		return releaseCommit{}, fmt.Errorf("cannot parse commit %s", sha)
	}
	commit := releaseCommit{sha: sha, subject: parts[0], body: strings.TrimSpace(parts[1]), seeds: strings.Fields(parts[2])}
	added, err := gitOutput(root, "diff-tree", "--root", "--no-commit-id", "--name-only", "--diff-filter=A", "-r", sha, "--", "changelog.d")
	if err != nil {
		return releaseCommit{}, err
	}
	for _, path := range strings.Fields(string(added)) {
		if !strings.HasSuffix(path, ".yaml") {
			continue
		}
		content, err := readRepositoryFile(root, sha, path)
		if err != nil {
			return releaseCommit{}, err
		}
		commit.fragments = append(commit.fragments, releaseFragment{path: path, content: strings.TrimSpace(string(content))})
	}
	return commit, nil
}

func renderNotes(root string, r releaseRange) (string, []string, error) {
	commits, err := releaseCommits(root, r)
	if err != nil {
		return "", nil, err
	}
	var seedOrder []string
	bySeed := map[string][]releaseCommit{}
	var loose []releaseCommit
	for _, commit := range commits {
		if len(commit.seeds) == 0 {
			loose = append(loose, commit)
			continue
		}
		for _, seed := range commit.seeds {
			if _, seen := bySeed[seed]; !seen {
				seedOrder = append(seedOrder, seed)
			}
			bySeed[seed] = append(bySeed[seed], commit)
		}
	}
	header := fmt.Sprintf("%d commits since the last release (%s)\n", len(commits), r.revisions())
	var groups []string
	for _, id := range seedOrder {
		var out strings.Builder
		record, err := showSeed(id)
		if err != nil {
			fmt.Fprintf(&out, "\n=== seed %s: unknown (%v); judge its change from the commits alone\n", id, err)
		} else {
			fmt.Fprintf(&out, "\n=== seed %s: %s [%s]\n", id, record.Seed.Title, record.Seed.Status)
			if body := strings.TrimSpace(record.Seed.Body); body != "" {
				fmt.Fprintf(&out, "%s\n", body)
			}
			for _, note := range record.Notes {
				fmt.Fprintf(&out, "--- %s: %s\n", note.Kind, strings.TrimSpace(note.Body))
			}
		}
		writeReleaseCommits(&out, bySeed[id])
		groups = append(groups, out.String())
	}
	if len(loose) > 0 {
		var out strings.Builder
		out.WriteString("\n=== commits without a seed\n")
		writeReleaseCommits(&out, loose)
		groups = append(groups, out.String())
	}
	return header, groups, nil
}

func writeReleaseCommits(out *strings.Builder, commits []releaseCommit) {
	for _, commit := range commits {
		fmt.Fprintf(out, "--- commit %s %s\n", commit.sha[:12], commit.subject)
		if commit.body != "" {
			fmt.Fprintf(out, "%s\n", commit.body)
		}
		for _, fragment := range commit.fragments {
			fmt.Fprintf(out, "--- release note the author wrote (%s)\n%s\n", fragment.path, fragment.content)
		}
	}
}

func showSeed(id string) (seedRecord, error) {
	command := exec.Command("attn", "seed", "show", id, "--json")
	out, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if reason := strings.TrimSpace(string(exitErr.Stderr)); reason != "" {
				return seedRecord{}, errors.New(firstLine(reason))
			}
		}
		return seedRecord{}, err
	}
	var record seedRecord
	if err := json.Unmarshal(out, &record); err != nil {
		return seedRecord{}, fmt.Errorf("read attn seed show: %w", err)
	}
	return record, nil
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}
