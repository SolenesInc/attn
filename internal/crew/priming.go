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

// MaxHeldSeeds is a tripwire: on 2026-09-06 the busiest holder in the production
// garden (477 seeds, 157 open) held 5, so ten is twice past the largest real case.
const MaxHeldSeeds = 10

// MaxHeldHandoffBytes is the per-note priming budget: of the 31 seeds carrying a
// handoff note on 2026-09-06 the freshest measured p50 795 and max 2,650 bytes.
const MaxHeldHandoffBytes = 1200

type HeldSeed struct {
	ID      string
	Slug    string
	Title   string
	Handoff string
}

type PlotReady struct {
	ID    string
	Slug  string
	Title string
	Ready int
}

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

	GardenRead bool
	Held       []HeldSeed
	HeldTotal  int
	Plots      []PlotReady
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
		"garden_holdings":  p.GardenSection(),
	})
}

// GardenSection renders the garden block alone so the daemon can log its size.
func (p Priming) GardenSection() string {
	if strings.TrimSpace(p.Member) == "" {
		return ""
	}
	total := ""
	if p.HeldTotal > len(p.Held) {
		total = fmt.Sprint(p.HeldTotal)
	}
	return prompts.RenderText("crew", "garden", prompts.Values{
		"garden_read": fmt.Sprint(p.GardenRead),
		"held_seeds":  heldSeedEntries(p.Held),
		"held_total":  total,
		"held_limit":  fmt.Sprint(len(p.Held)),
		"plot_ready":  plotReadyLines(p.Plots),
	})
}

func heldSeedEntries(held []HeldSeed) string {
	entries := make([]string, 0, len(held))
	for _, seed := range held {
		note := "No handoff note yet."
		if text := strings.TrimSpace(seed.Handoff); text != "" {
			note = "Freshest handoff: " + inlineHeldHandoff(text, seed.ID)
		}
		entries = append(entries, fmt.Sprintf("`%s` %s — %s\n%s", seed.ID, seed.Slug, seed.Title, note))
	}
	return strings.Join(entries, "\n\n")
}

func plotReadyLines(plots []PlotReady) string {
	lines := make([]string, 0, len(plots))
	for _, plot := range plots {
		lines = append(lines, fmt.Sprintf("- `%s` %s — %d ready", plot.ID, plot.Slug, plot.Ready))
	}
	return strings.Join(lines, "\n")
}

func inlineHeldHandoff(text, seedID string) string {
	if len(text) <= MaxHeldHandoffBytes {
		return text
	}
	return prompts.RenderText("crew", "trimmed-note", prompts.Values{
		"text":  cutAtRune(text, MaxHeldHandoffBytes),
		"limit": fmt.Sprint(MaxHeldHandoffBytes),
		"total": fmt.Sprint(len(text)),
		"seed":  seedID,
	})
}

func inlineHandoff(text, path string) string {
	if len(text) <= handoffInlineLimit {
		return text
	}
	return prompts.RenderText("crew", "truncated-handoff", prompts.Values{"text": cutAtRune(text, handoffInlineLimit), "limit": fmt.Sprint(handoffInlineLimit), "total": fmt.Sprint(len(text)), "path": path})
}

func cutAtRune(text string, limit int) string {
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
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
