package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/victorarias/attn/internal/lint/addedcomments"
)

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

func run(base string) ([]addedcomments.Finding, error) {
	mergeBase, err := git("merge-base", base, "HEAD")
	if err != nil {
		return nil, err
	}
	diff, err := git("diff", "-U0", "--no-color", "--no-ext-diff", "--no-renames", "--diff-filter=AM", strings.TrimSpace(mergeBase), "--")
	if err != nil {
		return nil, err
	}
	findings := addedcomments.FindInUnifiedDiff(diff)
	untracked, err := git("ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	for _, file := range strings.Fields(untracked) {
		if !addedcomments.Checked(file) {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		for i, line := range strings.Split(string(src), "\n") {
			if addedcomments.IsComment(line) {
				findings = append(findings, addedcomments.Finding{Path: file, Line: i + 1, Text: strings.TrimSpace(line)})
			}
		}
	}
	return findings, nil
}

func main() {
	base := flag.String("base", "origin/next", "ref the change will merge into")
	flag.Parse()
	root, err := git("rev-parse", "--show-toplevel")
	if err == nil {
		err = os.Chdir(strings.TrimSpace(root))
	}
	var findings []addedcomments.Finding
	if err == nil {
		findings, err = run(*base)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "addedcomments:", err)
		os.Exit(2)
	}
	for _, f := range findings {
		fmt.Printf("%s:%d: added comment: %s\n", f.Path, f.Line, f.Text)
	}
	if len(findings) > 0 {
		fmt.Printf("\n%d added comment lines since %s. This repository takes no new prose comments:\nsay it with names and structure, or delete it. Tool directives are exempt.\n", len(findings), *base)
		os.Exit(1)
	}
}
