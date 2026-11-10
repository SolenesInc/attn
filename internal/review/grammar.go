package review

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/victorarias/attn/internal/protocol"
)

const (
	FrameBrief       = "brief"
	FrameWalkthrough = "walkthrough"
	FrameMap         = "map"
)

type field struct {
	key, value string
	line       int
}

// frontmatter is the block between the first two --- lines, kept in order so
// review start can rewrite its own keys and leave the rest as written.
type frontmatter struct {
	fields []field
	// The line after the closing ---, 0-based; 0 when there is no frontmatter.
	bodyStart int
}

func (f frontmatter) get(key string) (field, bool) {
	for _, entry := range f.fields {
		if entry.key == key {
			return entry, true
		}
	}
	return field{}, false
}

type document struct {
	meta     frontmatter
	stops    []stop
	flows    []flow
	notShown []notShownEntry
	problems []protocol.ReviewProblem
}

type stop struct {
	line     int
	path     string
	from, to int
	notes    []note
}

type note struct {
	line     int
	from, to int
	look     bool
	text     string
}

type flow struct {
	line     int
	messages []flowMessage
}

type flowMessage struct {
	line int
	text string
	ref  *stopRef
}

type stopRef struct {
	line     int
	path     string
	from, to int
}

type notShownEntry struct {
	line   int
	path   string
	reason string
}

func (d *document) problem(line int, format string, args ...any) {
	d.problems = append(d.problems, protocol.ReviewProblem{Line: line, Message: fmt.Sprintf(format, args...)})
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

func parseFrontmatter(lines []string) frontmatter {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return frontmatter{}
	}
	var meta frontmatter
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			meta.bodyStart = i + 1
			return meta
		}
		key, value, ok := strings.Cut(lines[i], ":")
		if ok {
			meta.fields = append(meta.fields, field{key: strings.TrimSpace(key), value: strings.TrimSpace(value), line: i + 1})
		}
	}
	return frontmatter{}
}

// IsReview reports whether a markdown document declares attn: review.
func IsReview(text string) bool {
	attn, _ := parseFrontmatter(splitLines(text)).get("attn")
	return attn.value == "review"
}

var (
	stopHeading  = pattern(`^(\S+):(\d+)(?:\s*[-–—]\s*(\d+))?$`)
	noteItem     = pattern(`^- L(\d+)(?:\s*[-–—]\s*L?(\d+))?(!?):\s*(.*)$`)
	noteLike     = pattern(`^\s*[-*+]\s*\**\s*[Ll]\s*\d`)
	flowArrow    = pattern(`^\s*[^%\s][^:]*?(?:-{1,2}(?:>>|>|x|\)))[+-]?\s*[^:]+:\s*(.*)$`)
	flowStopNote = pattern(`^\s*%%\s*stop:\s*(.*?)\s*$`)
	fence        = pattern("^\\s*(```+|~~~+)\\s*(\\S*)")
)

func pattern(expr string) func() *regexp.Regexp {
	return sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(expr) })
}

func parse(text string) document {
	lines := splitLines(text)
	doc := document{meta: parseFrontmatter(lines)}
	var current *stop
	var lastNote *note
	inNotShown := false
	var fenceMark string
	var openFlow *flow
	for i := doc.meta.bodyStart; i < len(lines); i++ {
		line, number := lines[i], i+1
		if fenceMark != "" {
			if strings.HasPrefix(strings.TrimSpace(line), fenceMark) {
				fenceMark, openFlow = "", nil
				continue
			}
			if openFlow != nil {
				doc.flowLine(openFlow, line, number)
			}
			continue
		}
		if match := fence().FindStringSubmatch(line); match != nil {
			fenceMark = match[1]
			if match[2] == "mermaid" && sequenceDiagramFollows(lines[i+1:]) {
				doc.flows = append(doc.flows, flow{line: number})
				openFlow = &doc.flows[len(doc.flows)-1]
			}
			lastNote = nil
			continue
		}
		switch {
		case strings.HasPrefix(line, "## "):
			current, lastNote = nil, nil
			inNotShown = strings.EqualFold(strings.TrimSpace(line[3:]), "Not shown")
			continue
		case strings.HasPrefix(line, "# "):
			current, lastNote, inNotShown = nil, nil, false
			continue
		case strings.HasPrefix(line, "### "):
			lastNote, inNotShown = nil, false
			current = doc.stopHeading(strings.TrimSpace(line[4:]), number)
			continue
		}
		if inNotShown {
			if item, ok := strings.CutPrefix(line, "- "); ok {
				path, reason, _ := strings.Cut(item, ": ")
				doc.notShown = append(doc.notShown, notShownEntry{line: number, path: strings.Trim(strings.TrimSpace(path), "`"), reason: strings.TrimSpace(reason)})
			}
			continue
		}
		if match := noteItem().FindStringSubmatch(line); match != nil {
			if current == nil {
				doc.problem(number, "the note %q sits outside a stop. Put it under a `### path:from-to` heading.", line)
				lastNote = nil
				continue
			}
			from, _ := strconv.Atoi(match[1])
			to := from
			if match[2] != "" {
				to, _ = strconv.Atoi(match[2])
			}
			if to < from {
				doc.problem(number, "the note L%d-%d runs backwards. Write L%d-%d.", from, to, to, from)
				from, to = to, from
			}
			current.notes = append(current.notes, note{line: number, from: from, to: to, look: match[3] == "!", text: match[4]})
			lastNote = &current.notes[len(current.notes)-1]
			continue
		}
		if lastNote != nil && strings.HasPrefix(line, "  ") && strings.TrimSpace(line) != "" {
			lastNote.text += " " + strings.TrimSpace(line)
			continue
		}
		lastNote = nil
		if current != nil && noteLike().MatchString(line) {
			doc.problem(number, "%q looks like a note but does not parse. Write `- L12: text`, `- L12-14: text`, or `- L12!: text` for the place to look.", strings.TrimSpace(line))
		}
	}
	return doc
}

func sequenceDiagramFollows(lines []string) bool {
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return strings.HasPrefix(trimmed, "sequenceDiagram")
		}
	}
	return false
}

func (d *document) flowLine(open *flow, line string, number int) {
	if match := flowStopNote().FindStringSubmatch(line); match != nil {
		if len(open.messages) == 0 {
			d.problem(number, "`%%%% stop:` comes before any message. Put it on the line after the message it ties to a stop.")
			return
		}
		last := &open.messages[len(open.messages)-1]
		ref, ok := parseRange(match[1])
		if !ok {
			d.problem(number, "`%%%% stop: %s` does not name a range. Write `%%%% stop: path:from-to`.", match[1])
			return
		}
		ref.line = number
		last.ref = &ref
		return
	}
	if match := flowArrow().FindStringSubmatch(line); match != nil {
		open.messages = append(open.messages, flowMessage{line: number, text: strings.TrimSpace(match[1])})
	}
}

func parseRange(text string) (stopRef, bool) {
	match := stopHeading().FindStringSubmatch(strings.Trim(text, "`"))
	if match == nil {
		return stopRef{}, false
	}
	from, _ := strconv.Atoi(match[2])
	to := from
	if match[3] != "" {
		to, _ = strconv.Atoi(match[3])
	}
	return stopRef{path: match[1], from: from, to: to}, true
}

func (d *document) stopHeading(text string, number int) *stop {
	ref, ok := parseRange(text)
	if !ok {
		if !strings.ContainsAny(text, " \t") && strings.ContainsAny(text, "./") {
			d.problem(number, "the stop %s has no range. Write `### %s:<from>-<to>` with lines at head.", text, strings.Trim(text, "`"))
		}
		return nil
	}
	if path.IsAbs(ref.path) || ref.path != path.Clean(ref.path) || strings.HasPrefix(ref.path, "../") {
		d.problem(number, "the stop path %s must be relative to the repository root, like internal/store/store.go.", ref.path)
		return nil
	}
	if ref.from < 1 {
		d.problem(number, "the stop %s:%d-%d starts before line 1. Lines count from 1.", ref.path, ref.from, ref.to)
		ref.from = 1
	}
	if ref.to < ref.from {
		d.problem(number, "the stop %s:%d-%d runs backwards. Write %s:%d-%d.", ref.path, ref.from, ref.to, ref.path, ref.to, ref.from)
		ref.from, ref.to = ref.to, ref.from
	}
	d.stops = append(d.stops, stop{line: number, path: ref.path, from: ref.from, to: ref.to})
	return &d.stops[len(d.stops)-1]
}
