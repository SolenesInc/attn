package crew

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/prompts"
)

const HandoffsDirName = "handoffs"

// Match the filing tripwire so every letter attn accepts is inlined whole. A
// hand-edited file can still exceed it, so that path names the required read.
const handoffInlineLimit = MaxHandoffBytes

type Priming struct {
	Member        string
	HomeDir       string
	CharterPath   string
	CWD           string
	AwarenessDirs []string

	Charter       string
	Handoff       string
	HandoffName   string
	OlderHandoffs []string
}

// SortHandoffNames orders letters freshest first: the file names are UTC
// timestamps, so lexicographic order is chronological.
func SortHandoffNames(names []string) {
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
}

func (p Priming) Block() string {
	if strings.TrimSpace(p.Member) == "" {
		return ""
	}
	handoffsDir := filepath.Join(p.HomeDir, HandoffsDirName)
	return prompts.RenderText("crew", "priming", prompts.Values{
		"display_name":     DisplayName(p.Member),
		"home_dir":         p.HomeDir,
		"has_charter":      fmt.Sprint(strings.TrimSpace(p.Charter) != ""),
		"charter_file":     CharterFileName,
		"charter_path":     filepath.Join(p.HomeDir, CharterFileName),
		"cwd":              strings.TrimSpace(p.CWD),
		"awareness_dirs":   quotedList(trimmed(p.AwarenessDirs)),
		"handoff_name":     p.HandoffName,
		"handoffs_dirname": HandoffsDirName,
		"handoffs_dir":     handoffsDir,
		"older_handoffs":   strings.Join(backticked(trimmed(p.OlderHandoffs)), ", "),
		"handoff":          inlineHandoff(strings.TrimSpace(p.Handoff), filepath.Join(handoffsDir, p.HandoffName)),
	})
}

func inlineHandoff(text, path string) string {
	if len(text) <= handoffInlineLimit {
		return text
	}
	end := handoffInlineLimit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return prompts.RenderText("crew", "truncated-handoff", prompts.Values{"text": text[:end], "limit": fmt.Sprint(handoffInlineLimit), "total": fmt.Sprint(len(text)), "path": path})
}

func trimmed(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func backticked(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, "`"+value+"`")
	}
	return out
}

func quotedList(values []string) string {
	if len(values) == 0 {
		return ""
	}
	quoted := backticked(values)
	if len(quoted) == 1 {
		return quoted[0]
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
}
