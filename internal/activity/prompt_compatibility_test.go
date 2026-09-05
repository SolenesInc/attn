package activity

import (
	"github.com/victorarias/attn/internal/prompttest"
	"strconv"
	"testing"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	out := map[string]string{}
	for mask := 0; mask < 8; mask++ {
		in := Input{State: " working "}
		if mask&1 != 0 {
			in.StateReason = " reason {{literal}} "
		}
		if mask&2 != 0 {
			in.Previous = " Previous λ "
		}
		if mask&4 != 0 {
			in.Window = " Event\n{{literal}} "
		}
		p := Baseline().Render(in)
		out[strconv.Itoa(mask)+"/system"] = p.System
		out[strconv.Itoa(mask)+"/user"] = p.User
	}
	prompttest.Equal(t, "activity", out)
}
