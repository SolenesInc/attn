package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

type GrepHit struct {
	Path string
	Line int
	Text string
}

// ListFiles names the files of the working tree: tracked, and untracked ones
// git does not ignore.
func (c *Client) ListFiles(ctx context.Context, root string) ([]string, error) {
	out, err := c.outputWithEnv(ctx, OpMetadata, root, nil, nil, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// GrepResult holds the first hits of a search, at most perFile in each file.
// Crowded names the files that had more, and More says the search stopped at
// its limit before reading every match.
type GrepResult struct {
	Hits    []GrepHit
	Crowded []string
	More    bool
}

// Grep finds query as fixed text, ignoring case, in the working tree's text
// files. It stops git once limit hits are kept.
func (c *Client) Grep(ctx context.Context, root, query string, perFile, limit int) (GrepResult, error) {
	var result GrepResult
	inFile := map[string]int{}
	read := func(stdout io.Reader) bool {
		lines := bufio.NewReader(stdout)
		for {
			record, err := lines.ReadBytes('\n')
			if hit, ok := grepHit(record); ok {
				inFile[hit.Path]++
				switch {
				case inFile[hit.Path] <= perFile && len(result.Hits) == limit:
					result.More = true
					return true
				case inFile[hit.Path] <= perFile:
					result.Hits = append(result.Hits, hit)
				case inFile[hit.Path] == perFile+1:
					result.Crowded = append(result.Crowded, hit.Path)
				}
			}
			if err != nil {
				return false
			}
		}
	}
	args := []string{"grep", "-z", "-n", "-I", "-F", "-i", "--untracked", "--no-color", "-m", strconv.Itoa(perFile + 1), "-e", query, "--"}
	_, err := launchGit(ctx, OpMetadata, defaultTimeout(OpMetadata), gitProcess{dir: root, env: mergedCommandEnv(nil), read: read}, args...)
	err = withStderr(args, err)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return GrepResult{}, nil
	}
	if err != nil {
		return GrepResult{}, err
	}
	return result, nil
}

func grepHit(record []byte) (GrepHit, bool) {
	parts := bytes.SplitN(bytes.TrimSuffix(record, []byte("\n")), []byte{0}, 3)
	if len(parts) != 3 {
		return GrepHit{}, false
	}
	line, err := strconv.Atoi(string(parts[1]))
	if err != nil {
		return GrepHit{}, false
	}
	return GrepHit{Path: string(parts[0]), Line: line, Text: strings.TrimRight(string(parts[2]), "\r")}, true
}
