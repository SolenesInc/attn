package hooks

import (
	"strconv"
	"testing"

	"github.com/victorarias/attn/internal/prompttest"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	out := map[string]string{}
	for mask := 0; mask < 16; mask += 2 {
		launch := Launch{Garden: mask&4 != 0}
		launch.SelfReportPullRequests = mask&2 != 0
		if mask&8 != 0 {
			launch.Crew = " Crew {{literal}}.\nSecond line. "
		}
		out[strconv.Itoa(mask)] = launch.Instructions()
	}
	prompttest.Equal(t, "launch", out)
}
