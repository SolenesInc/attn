package crew

import (
	"github.com/victorarias/attn/internal/prompttest"
	"strconv"
	"strings"
	"testing"
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
