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

func TestTheCLIWarnsWhenItsReleaseDiffersFromTheRunningDaemons(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.RunFrom(testworld.AttnBinaryAt(t, "0.10.2"))
	s.Start()

	for _, tc := range []struct {
		cli    string
		binary string
		warn   bool
	}{
		{cli: "0.5.0", binary: testworld.AttnBinaryAt(t, "0.5.0"), warn: true},
		{cli: "0.10.2", binary: testworld.AttnBinaryAt(t, "0.10.2")},
		{cli: "dev", binary: testworld.AttnBinary(t)},
	} {
		got := s.Run(testworld.Invocation{Args: []string{"list"}, Binary: tc.binary})
		warned := strings.Contains(got.Stderr, "this CLI is "+tc.cli+" but the running attn app is 0.10.2") && strings.Contains(got.Stderr, "`which -a attn`")
		if got.Code != 0 || warned != tc.warn || (!tc.warn && strings.Contains(got.Stderr, "warning")) {
			t.Errorf("attn %s list against a 0.10.2 daemon exited %d with stderr %q, want a warning: %t", tc.cli, got.Code, got.Stderr, tc.warn)
		}
	}
}
