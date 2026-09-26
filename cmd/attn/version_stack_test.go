package main_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheVersionFlagsPrintWhatBootstrapAndTheHarnessRead(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)

	var build map[string]string
	built := s.Attn("--build-info-json")
	built.JSON(t, &build)
	for _, key := range []string{"version", "buildTime", "sourceFingerprint", "gitCommit"} {
		if build[key] == "" {
			t.Errorf("--build-info-json has no %s: %s", key, built.Stdout)
		}
	}

	for _, args := range [][]string{{"--version"}, {"version"}} {
		got := s.Attn(args...)
		if got.Code != 0 || strings.TrimSpace(got.Stdout) != build["version"] {
			t.Errorf("attn %s exited %d printing %q, want the build's version %q", args[0], got.Code, got.Stdout, build["version"])
		}
	}
	if got := s.Attn("--protocol-version"); got.Code != 0 || strings.TrimSpace(got.Stdout) != protocol.ProtocolVersion {
		t.Errorf("attn --protocol-version exited %d printing %q, want %q", got.Code, got.Stdout, protocol.ProtocolVersion)
	}
}
