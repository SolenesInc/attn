package viewlang

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Stamp is what the first line of an exported view file says it came from:
// version Version of the kept view View.
type Stamp struct {
	View    string
	Version int
}

func (s Stamp) String() string { return fmt.Sprintf("%s@%d", s.View, s.Version) }

var stampPattern = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^([a-z][a-z0-9-]*)@([1-9][0-9]*)$`) })

func parseStamp(words []string) (Stamp, bool) {
	if len(words) != 2 {
		return Stamp{}, false
	}
	match := stampPattern().FindStringSubmatch(words[1])
	if match == nil {
		return Stamp{}, false
	}
	version, err := strconv.Atoi(match[2])
	return Stamp{View: match[1], Version: version}, err == nil
}

// StampOf is the stamp on the first line of markdown, when it has one.
func StampOf(markdown string) (Stamp, bool) {
	first, _, _ := strings.Cut(markdown, "\n")
	match := versionPattern().FindStringSubmatch(strings.TrimSpace(first))
	if match == nil {
		return Stamp{}, false
	}
	return parseStamp(strings.Fields(match[1]))
}

// Saved is markdown as attn keeps it: its first line names the language it is
// written in, and nothing else.
func Saved(markdown string, language int) string {
	return withFirstLine(markdown, fmt.Sprintf("<!-- attn-view %d -->", language))
}

// Stamped is markdown as an export gives it: its first line names the
// language and the version of the view it came from.
func Stamped(markdown string, language int, stamp Stamp) string {
	return withFirstLine(markdown, fmt.Sprintf("<!-- attn-view %d %s -->", language, stamp))
}

func withFirstLine(markdown, line string) string {
	first, rest, _ := strings.Cut(markdown, "\n")
	if versionPattern().MatchString(strings.TrimSpace(strings.TrimSuffix(first, "\r"))) {
		return line + "\n" + rest
	}
	return line + "\n" + markdown
}
