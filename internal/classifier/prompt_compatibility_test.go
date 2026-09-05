package classifier

import (
	"github.com/victorarias/attn/internal/prompttest"
	"testing"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	prompttest.Equal(t, "classifier", map[string]string{"empty": BuildPrompt(""), "text": BuildPrompt("quote \"λ\"\n{{literal}}")})
}
