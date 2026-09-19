package addedcomments

import (
	"bufio"
	"path"
	"regexp"
	"strconv"
	"strings"
)

type Finding struct {
	Path string
	Line int
	Text string
}

var extensions = map[string]bool{
	".go": true, ".rs": true,
	".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
}

var (
	hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)
	comment    = regexp.MustCompile(`^(//|/\*|\*/|\*$|\* )`)
	directive  = regexp.MustCompile(`^//+\s*(go:|nolint|lint:|export |line |sys|extern |\+build|Code generated|@ts-|eslint-|oxlint-|biome-ignore|prettier-ignore|@vitest-environment|@jest-environment|@jsx|<reference|v8 ignore|c8 ignore|istanbul ignore|#region|#endregion)`)
)

func Checked(file string) bool {
	if !extensions[path.Ext(file)] {
		return false
	}
	for _, part := range strings.Split(file, "/") {
		if part == "testdata" || part == "node_modules" || part == "vendor" || part == "sdkdist" {
			return false
		}
	}
	return !strings.Contains(path.Base(file), "generated")
}

func IsComment(line string) bool {
	text := strings.TrimSpace(line)
	return comment.MatchString(text) && !directive.MatchString(text)
}

func FindInUnifiedDiff(diff string) []Finding {
	removed := map[string]bool{}
	var added []Finding
	file, line := "", 0
	scanner := bufio.NewScanner(strings.NewReader(diff))
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<26)
	for scanner.Scan() {
		text := scanner.Text()
		switch {
		case strings.HasPrefix(text, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(text, "+++ "), "b/")
		case strings.HasPrefix(text, "--- "):
		case strings.HasPrefix(text, "@@"):
			if m := hunkHeader.FindStringSubmatch(text); m != nil {
				line, _ = strconv.Atoi(m[1])
			}
		case strings.HasPrefix(text, "-"):
			if IsComment(text[1:]) {
				removed[strings.TrimSpace(text[1:])] = true
			}
		case strings.HasPrefix(text, "+"):
			if Checked(file) && IsComment(text[1:]) {
				added = append(added, Finding{Path: file, Line: line, Text: strings.TrimSpace(text[1:])})
			}
			line++
		}
	}
	var out []Finding
	for _, f := range added {
		if !removed[f.Text] {
			out = append(out, f)
		}
	}
	return out
}
