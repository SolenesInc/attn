package review

import (
	"fmt"
	"path"
	"regexp"
	"slices"
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
	line         int
	start        int
	side         string
	undrawable   int
	participants []flowParticipant
	messages     []flowMessage
	note         caption
	groups       []flowGroup
	open         []flowGroup
}

type caption struct {
	line int
	text string
}

// flowGroup is a block, from its opening line to its end, or one else, and or
// option section inside it. It holds the messages written while it is open.
type flowGroup struct {
	line        int
	kind, label string
	first, last int
	block       int
}

type flowParticipant struct {
	id, label string
	actor     bool
}

type flowMessage struct {
	line     int
	from, to string
	arrow    string
	text     string
	ref      *stopRef
	note     caption
	section  int
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
	stopHeading     = pattern(`^(\S+):(\d+)(?:\s*[-–—]\s*(\d+))?$`)
	noteItem        = pattern(`^- L(\d+)(?:\s*[-–—]\s*L?(\d+))?(!?):\s*(.*)$`)
	noteLike        = pattern(`^\s*[-*+]\s*\**\s*[Ll]\s*\d`)
	flowMessageLine = pattern(`^\s*([^\s%:;+-][^:;]*?)\s*(-{1,2}(?:>>|>|x|\)))([+-]?)\s*([^:;]+?)\s*:(.*)$`)
	participantLine = pattern(`^\s*(participant|actor)\s+(.+?)(?:\s+as\s+(.+?))?\s*$`)
	flowStopNote    = pattern(`^\s*%%\s*stop:\s*(.*?)\s*$`)
	flowNoteLine    = pattern(`(?i)^\s*note\s+(?:left\s+of|right\s+of|over)\s+([^:]+?)\s*:(.*)$`)
	flowBlockLine   = pattern(`^\s*(rect|opt|loop|alt|critical|par|break)(?:\s+(.*?))?\s*$`)
	flowSectionLine = pattern(`^\s*(else|option|and)(?:\s+(.*?))?\s*$`)
	flowEndLine     = pattern(`^\s*end\s*$`)
	fence           = pattern("^\\s*(```+|~~~+)\\s*(\\S*)\\s*(\\S*)")
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
				if openFlow != nil {
					doc.closeFlow(openFlow, number)
				}
				fenceMark, openFlow = "", nil
				continue
			}
			if openFlow != nil && number > openFlow.start {
				doc.flowLine(openFlow, line, number)
			}
			continue
		}
		if match := fence().FindStringSubmatch(line); match != nil {
			fenceMark = match[1]
			if start, ok := sequenceDiagramAt(lines[i+1:], match[1]); match[2] == "mermaid" && ok {
				side := ""
				if match[3] == "before" || match[3] == "after" {
					side = match[3]
				}
				doc.flows = append(doc.flows, flow{line: number, start: number + 1 + start, side: side})
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
	if openFlow != nil {
		doc.closeFlow(openFlow, len(lines)+1)
	}
	return doc
}

// sequenceDiagramAt finds the sequenceDiagram line that opens a mermaid
// fence, past what mermaid reads before it: blank lines, %% comments and
// directives, and a --- frontmatter block.
func sequenceDiagramAt(lines []string, fenceMark string) (int, bool) {
	inFrontmatter := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, fenceMark):
			return 0, false
		case trimmed == "---":
			inFrontmatter = !inFrontmatter
		case inFrontmatter, trimmed == "", strings.HasPrefix(trimmed, "%%"):
		default:
			return i, strings.HasPrefix(trimmed, "sequenceDiagram")
		}
	}
	return 0, false
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
		if last.ref != nil {
			d.problem(number, "the message on line %d is already tied to a stop by `%%%% stop:` on line %d, and a message ties to one stop. Tie these lines to a message of their own, or widen that stop.", last.line, last.ref.line)
			return
		}
		last.ref = &ref
		return
	}
	if match := flowMessageLine().FindStringSubmatch(line); match != nil {
		from, to := open.participant(match[1], "", false), open.participant(match[4], "", false)
		open.messages = append(open.messages, flowMessage{line: number, from: from, to: to, arrow: match[2], text: strings.TrimSpace(match[5]), section: open.section()})
		for i := range open.open {
			if open.open[i].first == 0 {
				open.open[i].first = number
			}
			open.open[i].last = number
		}
		return
	}
	if match := participantLine().FindStringSubmatch(line); match != nil {
		open.participant(match[2], match[3], match[1] == "actor")
		return
	}
	if match := flowNoteLine().FindStringSubmatch(line); match != nil {
		for _, id := range strings.Split(match[1], ",") {
			open.participant(id, "", false)
		}
		d.flowNote(open, caption{line: number, text: strings.TrimSpace(match[2])})
		return
	}
	if match := flowBlockLine().FindStringSubmatch(line); match != nil {
		open.open = append(open.open, flowGroup{line: number, kind: match[1], label: match[2]})
		return
	}
	if match := flowSectionLine().FindStringSubmatch(line); match != nil {
		d.flowSection(open, flowGroup{line: number, kind: match[1], label: match[2]})
		return
	}
	if flowEndLine().MatchString(line) {
		if len(open.open) == 0 {
			d.flowProblem(open, number, "`end` closes no block. Remove it, or open the block it ends with rect, opt, loop, alt, critical, par or break.")
			return
		}
		d.closeBlock(open)
		return
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || trimmed == "sequenceDiagram" || strings.HasPrefix(trimmed, "%%") || open.undrawable != 0 {
		return
	}
	open.undrawable = number
	d.problem(number, "attn draws participants, actors, messages, notes and the blocks rect, opt, loop, alt, critical, par and break; `%s` is outside that, so this diagram shows as a mermaid figure and the Map frame cannot walk it. Remove the line, or say it in the prose.", trimmed)
}

// flowProblem reports a problem in how the diagram tells its story. Once a
// line attn cannot draw has made the diagram a mermaid figure, those problems
// no longer apply.
func (d *document) flowProblem(open *flow, number int, format string, args ...any) {
	if open.undrawable == 0 {
		d.problem(number, format, args...)
	}
}

// section is the line of the innermost open block section, 0 outside every
// block.
func (f *flow) section() int {
	if len(f.open) == 0 {
		return 0
	}
	return f.open[len(f.open)-1].line
}

func (d *document) flowNote(open *flow, note caption) {
	if len(open.messages) == 0 && open.section() == 0 {
		if open.note.line != 0 {
			d.flowProblem(open, note.line, "the diagram already has its caption on line %d, and a diagram has one. Merge the two notes.", open.note.line)
			return
		}
		open.note = note
		return
	}
	if len(open.messages) == 0 || open.messages[len(open.messages)-1].section != open.section() {
		d.flowProblem(open, note.line, "a Note captions the message right before it in the same block, and this one has none. Move it under a message in its block, or put the text in the block's label.")
		return
	}
	last := &open.messages[len(open.messages)-1]
	if last.note.line != 0 {
		d.flowProblem(open, note.line, "the message on line %d already has its caption on line %d, and a message has one. Merge the two notes.", last.line, last.note.line)
		return
	}
	last.note = note
}

var sectionOf = map[string]string{"else": "alt", "option": "critical", "and": "par"}

func isSection(group flowGroup) bool {
	return sectionOf[group.kind] != ""
}

// flowSection opens an else, option or and section. The block stays open
// under it, so the block's group spans all its sections.
func (d *document) flowSection(open *flow, section flowGroup) {
	blockKind := sectionOf[section.kind]
	at := len(open.open) - 1
	if at >= 0 && isSection(open.open[at]) {
		at--
	}
	if at < 0 || open.open[at].kind != blockKind {
		d.flowProblem(open, section.line, "`%s` divides a block opened by `%s`, and this line is inside none. Open the block with `%s`, or remove the line.", section.kind, blockKind, blockKind)
		return
	}
	if at < len(open.open)-1 {
		d.closeGroup(open)
	}
	section.block = open.open[at].line
	open.open = append(open.open, section)
}

// closeBlock ends the innermost block and the section open inside it.
func (d *document) closeBlock(open *flow) {
	if isSection(open.open[len(open.open)-1]) {
		d.closeGroup(open)
	}
	d.closeGroup(open)
}

func (d *document) closeGroup(open *flow) {
	group := open.open[len(open.open)-1]
	open.open = open.open[:len(open.open)-1]
	if group.first == 0 {
		d.flowProblem(open, group.line, "the `%s` on line %d holds no message, so there is nothing to draw it around. Put a message in it, or remove it.", group.kind, group.line)
		return
	}
	open.groups = append(open.groups, group)
}

// closeFlow ends a diagram at its closing fence, or at the end of the
// document when the fence never closes.
func (d *document) closeFlow(open *flow, number int) {
	for len(open.open) > 0 {
		block := open.open[len(open.open)-1]
		if isSection(block) {
			block = open.open[len(open.open)-2]
		}
		d.flowProblem(open, block.line, "the `%s` block on line %d has no `end`. Add `end` after its last message, before line %d.", block.kind, block.line, number)
		d.closeBlock(open)
	}
	slices.SortStableFunc(open.groups, func(a, b flowGroup) int { return a.line - b.line })
}

// participant returns the id a participant line or message names, declaring
// it on first use as mermaid does.
func (f *flow) participant(id, label string, actor bool) string {
	id = strings.TrimSpace(id)
	label = strings.TrimSpace(label)
	for i := range f.participants {
		if f.participants[i].id == id {
			if label != "" {
				f.participants[i].label = label
			}
			f.participants[i].actor = f.participants[i].actor || actor
			return id
		}
	}
	if label == "" {
		label = id
	}
	f.participants = append(f.participants, flowParticipant{id: id, label: label, actor: actor})
	return id
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
	written := strings.Trim(text, "`")
	if ref.to < ref.from {
		ref.from, ref.to = ref.to, ref.from
		if ref.from >= 1 {
			d.problem(number, "the stop %s runs backwards. Write %s:%d-%d.", written, ref.path, ref.from, ref.to)
		}
	}
	if ref.from < 1 {
		ref.from, ref.to = 1, max(ref.to, 1)
		d.problem(number, "the stop %s starts before line 1. Lines count from 1: write %s:%d-%d.", written, ref.path, ref.from, ref.to)
	}
	d.stops = append(d.stops, stop{line: number, path: ref.path, from: ref.from, to: ref.to})
	return &d.stops[len(d.stops)-1]
}
