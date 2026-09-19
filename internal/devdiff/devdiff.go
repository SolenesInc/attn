package devdiff

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Change struct {
	Base      string
	MergeBase string
}

func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exit.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

func Since(base string) (Change, error) {
	root, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return Change{}, err
	}
	if err := os.Chdir(strings.TrimSpace(root)); err != nil {
		return Change{}, err
	}
	mergeBase, err := git("merge-base", base, "HEAD")
	if err != nil {
		return Change{}, fmt.Errorf("%w\nfetch %s, or pass another ref with DIFF_BASE=<ref>", err, base)
	}
	return Change{Base: base, MergeBase: strings.TrimSpace(mergeBase)}, nil
}

func (c Change) UnifiedDiff() (string, error) {
	return git("diff", "-U0", "--no-color", "--no-ext-diff", "--no-renames", "--diff-filter=AM", c.MergeBase, "--")
}

func (c Change) Untracked() ([]string, error) {
	out, err := git("ls-files", "-z", "--others", "--exclude-standard")
	return splitNUL(out), err
}

func (c Change) Paths() ([]string, error) {
	out, err := git("diff", "-z", "--name-only", "--no-renames", c.MergeBase, "--")
	if err != nil {
		return nil, err
	}
	untracked, err := c.Untracked()
	return append(splitNUL(out), untracked...), err
}

func splitNUL(out string) []string {
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}
