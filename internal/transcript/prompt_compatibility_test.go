package transcript

import (
	"fmt"
	"testing"

	"github.com/victorarias/attn/internal/prompttest"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	out := map[string]string{}
	for _, text := range []string{"", " \n\t", "λ {{literal}}\nnext"} {
		for mask := 0; mask < 16; mask++ {
			slice := ConversationSlice{}
			if mask&1 != 0 {
				slice.Brief = text
			}
			if mask&2 != 0 {
				slice.Rescoping = []string{text}
			}
			if mask&4 != 0 {
				slice.Summary = text
			}
			if mask&8 != 0 {
				slice.AgentTurns = []string{text}
			}
			out[fmt.Sprintf("slice/%d/%s", mask, text)] = slice.Render()
		}
		for _, limit := range []int{0, 1, 8, 100} {
			out[fmt.Sprintf("cap/%d/%s", limit, text)] = fmt.Sprintf("%q", capText(text, limit))
		}
	}
	prompttest.Equal(t, "evidence", out)
}
