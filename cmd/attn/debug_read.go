package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"
)

// Matches the leading "[2006-01-02 15:04:05]" internal/logging writes on every
// line; a line without one is a continuation (see filterSinceLines).
var daemonLogTimestampPattern = regexp.MustCompile(`^\[(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})\]`)

const daemonLogTimestampLayout = "2006-01-02 15:04:05"

// readLinesFile reads every line without bufio.Scanner's default token size limit,
// which some diagnostic lines (an incident record's ring buffer) exceed.
func readLinesFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no such file: %s", path)
		}
		return nil, err
	}
	defer f.Close()
	return readLines(f)
}

func readLines(r io.Reader) ([]string, error) {
	br := bufio.NewReader(r)
	var lines []string
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimRight(line, "\n")
			line = bytes.TrimRight(line, "\r")
			lines = append(lines, string(line))
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return lines, nil
			}
			return lines, err
		}
	}
}

func tailLines(lines []string, n int) []string {
	if n <= 0 || len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

func grepLines(lines []string, pattern string) ([]string, error) {
	if pattern == "" {
		return lines, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid --grep pattern: %w", err)
	}
	var out []string
	for _, line := range lines {
		if re.MatchString(line) {
			out = append(out, line)
		}
	}
	return out, nil
}

func filterSinceLines(lines []string, cutoff time.Time) []string {
	var out []string
	matching := false
	for _, line := range lines {
		if m := daemonLogTimestampPattern.FindStringSubmatch(line); m != nil {
			ts, err := time.ParseInLocation(daemonLogTimestampLayout, m[1], time.Local)
			if err != nil {
				if matching {
					out = append(out, line)
				}
				continue
			}
			matching = !ts.Before(cutoff)
			if matching {
				out = append(out, line)
			}
			continue
		}
		if matching {
			out = append(out, line)
		}
	}
	return out
}
