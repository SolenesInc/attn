package hooks

import (
	"github.com/victorarias/attn/internal/prompttest"
	"strconv"
	"testing"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	out := map[string]string{}
	for mask := 0; mask < 32; mask++ {
		launch := Launch{InjectWorkflow: mask&4 != 0, Garden: mask&8 != 0}
		if mask&1 != 0 {
			launch.NotebookRoot = " /tmp/book \"λ\" {{literal}} "
		}
		launch.SelfReportPullRequests = mask&2 != 0
		if mask&16 != 0 {
			launch.Crew = " Crew {{literal}}.\nSecond line. "
		}
		out[strconv.Itoa(mask)] = launch.Instructions()
	}
	prompttest.Equal(t, "launch", out)
}
