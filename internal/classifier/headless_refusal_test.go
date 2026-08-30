package classifier

import (
	"errors"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/headless"
)

// Both shell a vendor CLI directly instead of going through the agent driver's
// headless seam, so each needs its own refusal.
func TestHeadlessSwitchOffRefusesTheRawClassifierExecs(t *testing.T) {
	t.Setenv(headless.EnvVar, "off")

	t.Run("copilot", func(t *testing.T) {
		state, err := ClassifyWithCopilot("did that work?", time.Second)
		if !errors.Is(err, headless.ErrRefused) {
			t.Fatalf("ClassifyWithCopilot() error = %v, want ErrRefused", err)
		}
		if state != "unknown" {
			t.Fatalf("state = %q, want unknown", state)
		}
	})

	t.Run("codex", func(t *testing.T) {
		state, err := ClassifyWithCodexExecutableInDir("did that work?", "", "", time.Second)
		if !errors.Is(err, headless.ErrRefused) {
			t.Fatalf("ClassifyWithCodexExecutableInDir() error = %v, want ErrRefused", err)
		}
		if state != "unknown" {
			t.Fatalf("state = %q, want unknown", state)
		}
	})
}
