package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type coverBlock struct {
	startLine, startCol, endLine, endCol int
	count                                int
}

type coverage struct {
	Test   string
	blocks map[string][]coverBlock
}

func runScenario(root string, spec flowSpec) (*coverage, error) {
	dir, err := os.MkdirTemp("", "flowviz-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	profile := filepath.Join(dir, "cover.out")
	cmd := exec.Command("go", "test", spec.TestPkg, "-run", "^"+spec.TestName+"$", "-count=1",
		"-coverpkg=./internal/...", "-coverprofile="+profile)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("%w\n%s", err, out)
	}
	cov, err := parseProfile(profile)
	if err != nil {
		return nil, err
	}
	cov.Test = spec.TestPkg + " " + spec.TestName
	return cov, nil
}

func parseProfile(path string) (*coverage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cov := &coverage{blocks: map[string][]coverBlock{}}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode:") {
			continue
		}
		file, block, ok := parseBlock(line)
		if ok {
			cov.blocks[file] = append(cov.blocks[file], block)
		}
	}
	return cov, scanner.Err()
}

func parseBlock(line string) (string, coverBlock, bool) {
	colon := strings.LastIndex(line, ":")
	if colon < 0 {
		return "", coverBlock{}, false
	}
	var b coverBlock
	var stmts int
	_, err := fmt.Sscanf(line[colon+1:], "%d.%d,%d.%d %d %d", &b.startLine, &b.startCol, &b.endLine, &b.endCol, &stmts, &b.count)
	return line[:colon], b, err == nil
}

type ran int

const (
	ranUnknown ran = iota
	ranYes
	ranNo
)

func (c *coverage) at(importFile string, line, col int) ran {
	if c == nil {
		return ranUnknown
	}
	blocks, ok := c.blocks[importFile]
	if !ok {
		return ranUnknown
	}
	result := ranUnknown
	for _, b := range blocks {
		if b.contains(line, col) {
			if b.count > 0 {
				return ranYes
			}
			result = ranNo
		}
	}
	return result
}

func (b coverBlock) contains(line, col int) bool {
	if line < b.startLine || line > b.endLine {
		return false
	}
	if line == b.startLine && col < b.startCol {
		return false
	}
	if line == b.endLine && col > b.endCol {
		return false
	}
	return true
}

func (r ran) String() string {
	switch r {
	case ranYes:
		return "yes"
	case ranNo:
		return "no"
	}
	return ""
}
