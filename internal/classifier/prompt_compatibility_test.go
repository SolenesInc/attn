package classifier

import (
	"testing"

	"github.com/victorarias/attn/internal/prompttest"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	prompttest.Equal(t, "classifier", map[string]string{"empty": BuildPrompt(""), "text": BuildPrompt("quote \"λ\"\n{{literal}}")})
}
