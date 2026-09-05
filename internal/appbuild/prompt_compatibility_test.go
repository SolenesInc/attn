package appbuild

import (
	"github.com/victorarias/attn/internal/prompttest"
	"testing"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	prompttest.Equal(t, "app-guidance", map[string]string{"guidance": scaffoldAgentsMD("sample")})
}
