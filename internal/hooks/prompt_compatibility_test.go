package hooks

import (
	"strconv"
	"testing"

	"github.com/victorarias/attn/internal/prompttest"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	out := map[string]string{}
	for mask := 0; mask < 16; mask++ {
		launch := Launch{Garden: mask&4 != 0}
		if mask&1 != 0 {
			launch.NotebookRoot = " /tmp/book \"λ\" {{literal}} "
		}
		launch.SelfReportPullRequests = mask&2 != 0
		if mask&8 != 0 {
			launch.Crew = " Crew {{literal}}.\nSecond line. "
		}
		out[strconv.Itoa(mask)] = launch.Instructions()
	}
	prompttest.Equal(t, "launch", out)
}
