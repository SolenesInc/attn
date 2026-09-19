package addedcomments

import (
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
	hunkHeader  = regexp.MustCompile(`^@@ -\d+(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
	comment     = regexp.MustCompile(`^(//|/\*)`)
	goDirective = regexp.MustCompile(`^//(go:|line |export |extern |sys(nb)? |\+build|nolint|lint:| Code generated| Output:| Unordered output:)`)
	toolMarker  = regexp.MustCompile(`^(//+|/\*+)\s*(@ts-|eslint-|oxlint-|biome-ignore|prettier-ignore|@vitest-environment|@jest-environment|@jsx|<reference|v8 ignore|c8 ignore|istanbul ignore|#region|#endregion)`)
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
	return comment.MatchString(text) && !goDirective.MatchString(text) && !toolMarker.MatchString(text)
}

func count(group string) int {
	if group == "" {
		return 1
	}
	n, _ := strconv.Atoi(group)
	return n
}

func FindInUnifiedDiff(diff string) []Finding {
	removed := map[string]int{}
	var added []Finding
	file, line, removals, additions := "", 0, 0, 0
	for _, text := range strings.Split(diff, "\n") {
		text = strings.TrimSuffix(text, "\r")
		switch {
		case removals > 0 && strings.HasPrefix(text, "-"):
			removals--
			if Checked(file) && IsComment(text[1:]) {
				removed[strings.TrimSpace(text[1:])]++
			}
		case additions > 0 && strings.HasPrefix(text, "+"):
			additions--
			if Checked(file) && IsComment(text[1:]) {
				added = append(added, Finding{Path: file, Line: line, Text: strings.TrimSpace(text[1:])})
			}
			line++
		case strings.HasPrefix(text, "--- "):
			file = strings.TrimPrefix(strings.TrimRight(strings.TrimPrefix(text, "--- "), "\t"), "a/")
		case strings.HasPrefix(text, "+++ "):
			if name := strings.TrimRight(strings.TrimPrefix(text, "+++ "), "\t"); name != "/dev/null" {
				file = strings.TrimPrefix(name, "b/")
			}
		case strings.HasPrefix(text, "@@"):
			if m := hunkHeader.FindStringSubmatch(text); m != nil {
				removals, additions = count(m[1]), count(m[3])
				line, _ = strconv.Atoi(m[2])
			}
		}
	}
	var out []Finding
	for _, f := range added {
		if removed[f.Text] > 0 {
			removed[f.Text]--
			continue
		}
		out = append(out, f)
	}
	return out
}
