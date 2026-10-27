package crew

import (
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/prompttest"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	out := map[string]string{"unbound": (Priming{}).Block()}
	for mask := 0; mask < 32; mask++ {
		p := Priming{Member: "keeper", HomeDir: "/tmp/home", HandoffName: "letter.md"}
		if mask&1 != 0 {
			p.Charter = "Charter."
		}
		if mask&2 != 0 {
			p.CWD = " /tmp/work {{literal}} "
		}
		if mask&4 != 0 {
			p.AwarenessDirs = []string{" /tmp/one ", "", "/tmp/two"}
		}
		if mask&8 != 0 {
			p.Handoff = " Handoff {{literal}}.\nSecond line. "
		}
		if mask&16 != 0 {
			p.OlderHandoffs = []string{"old.md", "", "older.md"}
		}
		out[strconv.Itoa(mask)] = p.Block()
	}
	for _, text := range []string{"short", strings.Repeat("λ", handoffInlineLimit/2+2)} {
		got := inlineHandoff(text, "/tmp/letter {{literal}}.md")
		out["inline/"+strconv.Itoa(len(text))] = strings.TrimPrefix(got, text[:min(len(text), handoffInlineLimit)])
	}
	prompttest.Equal(t, "crew", out)
}

func TestCrewV2PromptCompatibility(t *testing.T) {
	firstDay := Priming{Member: "keeper", HomeDir: "/tmp/home", V2Enabled: true, GardenRead: true}
	existing := firstDay
	existing.Charter = "Charter."
	existing.CWD = "/tmp/work {{literal}}"
	existing.AwarenessDirs = []string{"/tmp/one", "/tmp/two"}
	existing.HandoffName = "letter.md"
	existing.Handoff = "Follow s-example to /tmp/work."
	existing.OlderHandoffs = []string{"old.md"}
	existing.Held = []HeldSeed{{ID: "s-example", Slug: "example", Title: "Example work", Handoff: "Read the work."}}
	out := map[string]string{
		"first-day": firstDay.Block(),
		"existing":  existing.Block(),
	}
	for _, event := range []string{"wake", "successor"} {
		out[event] = prompts.RenderText("crew", event, prompts.Values{"crew_v2_enabled": "true"})
	}
	prompttest.Equal(t, "crew-v2", out)
}
