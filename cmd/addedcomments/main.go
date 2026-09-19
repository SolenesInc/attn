package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/devdiff"
	"github.com/victorarias/attn/internal/lint/addedcomments"
)

func run(base string) ([]addedcomments.Finding, error) {
	change, err := devdiff.Since(base)
	if err != nil {
		return nil, err
	}
	diff, err := change.UnifiedDiff()
	if err != nil {
		return nil, err
	}
	findings := addedcomments.FindInUnifiedDiff(diff)
	untracked, err := change.Untracked()
	if err != nil {
		return nil, err
	}
	for _, file := range untracked {
		if !addedcomments.Checked(file) {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		findings = append(findings, addedcomments.FindInAddedLines(file, 1, strings.Split(string(src), "\n"))...)
	}
	return findings, nil
}

func main() {
	base := flag.String("base", "origin/next", "ref the change will merge into")
	flag.Parse()
	findings, err := run(*base)
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
