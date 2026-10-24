package main_test

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestTheVersionFlagsPrintWhatBootstrapAndTheHarnessRead(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)

	var build struct {
		Version           string `json:"version"`
		Channel           string `json:"channel"`
		BuildTime         string `json:"buildTime"`
		SourceFingerprint string `json:"sourceFingerprint"`
		GitCommit         string `json:"gitCommit"`
		Dirty             bool   `json:"dirty"`
	}
	built := s.Attn("--build-info-json")
	built.JSON(t, &build)
	for key, value := range map[string]string{"version": build.Version, "channel": build.Channel, "buildTime": build.BuildTime, "sourceFingerprint": build.SourceFingerprint} {
		if value == "" {
			t.Errorf("--build-info-json has no %s: %s", key, built.Stdout)
		}
	}
	head, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimSpace(string(head)); build.GitCommit != want {
		t.Errorf("--build-info-json reports commit %q, want the checkout's %q", build.GitCommit, want)
	}
	if got := s.Attn("version", "--json"); got.Stdout != built.Stdout {
		t.Errorf("attn version --json printed %q, want what --build-info-json prints %q", got.Stdout, built.Stdout)
	}

	dirty := ""
	if build.Dirty {
		dirty = ", dirty"
	}
	line := fmt.Sprintf("%s (%s, %s%s)", build.Version, build.Channel, build.GitCommit[:8], dirty)
	if got := s.Attn("version"); got.Code != 0 || strings.TrimSpace(got.Stdout) != line {
		t.Errorf("attn version exited %d printing %q, want %q", got.Code, got.Stdout, line)
	}
	if got := s.Attn("--version"); got.Code != 0 || strings.TrimSpace(got.Stdout) != build.Version {
		t.Errorf("attn --version exited %d printing %q, want the bare version %q that release preflight compares", got.Code, got.Stdout, build.Version)
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
