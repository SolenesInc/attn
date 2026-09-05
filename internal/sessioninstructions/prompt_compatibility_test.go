package sessioninstructions

import (
	"github.com/victorarias/attn/internal/prompttest"
	"strconv"
	"testing"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	out := map[string]string{}
	for mask := 0; mask < 4; mask++ {
		r := ModelRequest{Question: "What happened {{literal}}?"}
		if mask&1 != 0 {
			r.Conversation = []ConversationTurn{{ID: "t1", Author: "user", Text: "hello λ\n{{literal}}"}, {ID: "t2", Author: "assistant", Text: "done"}}
		}
		if mask&2 != 0 {
			r.PreviousValidationErrors = []string{"unknown turn", "wrong quote"}
		}
		out[strconv.Itoa(mask)] = Prompt(r)
	}
	prompttest.Equal(t, "session-instructions", out)
}
