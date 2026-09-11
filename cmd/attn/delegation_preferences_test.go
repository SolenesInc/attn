package main

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestDelegateRoleRequestOptions(t *testing.T) {
	parsed, err := parseDelegateArgs([]string{"--brief", "Build this", "--cwd", "/repo", "--role", "builder", "--choice", "hard", "--model", "custom", "--effort", "high"})
	if err != nil {
		t.Fatal(err)
	}
	request := parsed.request
	if protocol.Deref(request.Role) != "builder" || protocol.Deref(request.Choice) != "hard" || protocol.Deref(request.Model) != "custom" || protocol.Deref(request.Effort) != "high" {
		t.Fatalf("%+v", request)
	}

	parsed, err = parseDelegateArgs([]string{"--brief", "Task", "--cwd", "/notes", "--fallback", "--effort", "default"})
	if err != nil || !protocol.Deref(parsed.request.Fallback) || parsed.request.Effort == nil || *parsed.request.Effort != "" {
		t.Fatalf("%+v %v", parsed, err)
	}
}
