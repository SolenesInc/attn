package main

import (
	"strings"
	"testing"
)

func TestWriteInstanceEnvClearsRoutingOverridesBeforeSelectingInstance(t *testing.T) {
	var output strings.Builder
	writeInstanceEnv(&output, "dev", false)

	got := output.String()
	for _, name := range instanceRoutingOverrides {
		if !strings.Contains(got, "unset "+name+"\n") {
			t.Fatalf("instance env output missing %s cleanup: %q", name, got)
		}
	}
	if !strings.HasSuffix(got, "export ATTN_INSTANCE=dev\n") {
		t.Fatalf("instance env output does not select dev last: %q", got)
	}
}

func TestWriteInstanceEnvClearsTheDataDir(t *testing.T) {
	var output strings.Builder
	writeInstanceEnv(&output, "dev", false)

	if !strings.Contains(output.String(), "unset ATTN_DATA_DIR\n") {
		t.Fatalf("instance env output must clear ATTN_DATA_DIR: %q", output.String())
	}
}

func TestWriteInstanceEnvFishClearsRoutingOverridesWhenReturningToDefault(t *testing.T) {
	var output strings.Builder
	writeInstanceEnv(&output, "", true)

	got := output.String()
	for _, name := range instanceRoutingOverrides {
		if !strings.Contains(got, "set -e "+name+"\n") {
			t.Fatalf("fish instance env output missing %s cleanup: %q", name, got)
		}
	}
	if !strings.HasSuffix(got, "set -e ATTN_INSTANCE\n") {
		t.Fatalf("fish instance env output does not clear instance last: %q", got)
	}
}
