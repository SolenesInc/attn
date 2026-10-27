package crew

import (
	"strconv"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/prompttest"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	out := map[string]string{"unbound": (Priming{}).Block()}
	for mask := 0; mask < 32; mask++ {
		p := Priming{Name: "Keeper", HomeDir: "/tmp/home", HandoffName: "letter.md"}
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
	for mask := 0; mask < 4; mask++ {
		p := Priming{Name: "keeper", HomeDir: "/tmp/home", Charter: "Charter.", MemoryEnabled: true}
		if mask&1 != 0 {
			p.HandoffName = "2026-10-26T20-00Z-keeper.md"
			p.Handoff = "The user chose option B, replacing the earlier choice of option A."
		}
		if mask&2 != 0 {
			p.ImportLetters = true
			p.MemoryView = "No memory entries yet."
		} else {
			p.MemoryView = "#0 2026-10-25T20:00:00Z The user chose option A for the current plan."
		}
		out["memory/"+strconv.Itoa(mask)] = p.Block()
	}
	for _, text := range []string{"short", strings.Repeat("λ", handoffInlineLimit/2+2)} {
		got := inlineHandoff(text, "/tmp/letter {{literal}}.md")
		out["inline/"+strconv.Itoa(len(text))] = strings.TrimPrefix(got, text[:min(len(text), handoffInlineLimit)])
	}
	prompttest.Equal(t, "crew", out)
}
