package main_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/testworld"
)

func TestNoProcessALaunchStartsHoldsTheAgentCredentialInItsArguments(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the login shell comes from SHELL only on Linux, and the test records it there")
	}
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude, fakeagent.Codex))
	loginShell := recordingLoginShell(t, s)
	s.Start()
	app := s.App()
	requireStdout(t, s.Attn("settings", "set", "agent_tools_enabled", "true"), "true")
	for _, h := range []fakeagent.Harness{fakeagent.Claude, fakeagent.Codex} {
		launch := string(h)
		session := s.Spawn(app, h, s.Path(string(h)))
		agent := s.Launched(session)
		credential := stackCredential(t, s, "t."+app.Terminal(session))
		if h == fakeagent.Claude && (!slices.Contains(agent.Env, "ATTN_AGENT_CREDENTIAL="+credential) || len(credentialsSignedBy(t, s, strings.Join(agent.Env, " "))) == 0) {
			t.Errorf("%s: the agent's environment lacks a credential the scan finds", launch)
		}
		arguments := append([]string{strings.Join(agent.Argv, " ")}, runningProcessArguments(t)...)
		for _, args := range append(arguments, loginShell.launches(t)...) {
			if held := credentialsSignedBy(t, s, args); len(held) > 0 {
				t.Errorf("%s: a process started with the credentials %q in its arguments", launch, held)
			}
		}
	}
}

type loginShellRecord string

func recordingLoginShell(t *testing.T, s *testworld.Stack) loginShellRecord {
	t.Helper()
	dir := t.TempDir()
	record := loginShellRecord(filepath.Join(dir, "launches"))
	shell := filepath.Join(dir, "recording-shell")
	script := "#!/bin/sh\nprintf '%s\\001' \"$@\" >> '" + string(record) + "'\nprintf '\\002' >> '" + string(record) + "'\nexec /bin/sh \"$@\"\n"
	if err := os.WriteFile(shell, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Vars = append(s.Vars, "SHELL="+shell)
	return record
}

func (r loginShellRecord) launches(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(string(r))
	if err != nil {
		t.Fatalf("the recording login shell never ran: %v", err)
	}
	var launches []string
	for launch := range strings.SplitSeq(strings.TrimSuffix(string(data), "\x02"), "\x02") {
		launches = append(launches, strings.ReplaceAll(strings.TrimSuffix(launch, "\x01"), "\x01", " "))
	}
	return launches
}

func runningProcessArguments(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("ps", "-ww", "-A", "-o", "args=").Output()
	if err != nil {
		t.Fatalf("list running processes: %v", err)
	}
	return strings.Split(string(bytes.TrimSpace(out)), "\n")
}

var credentialShape = regexp.MustCompile(`[ts]\.[^\s.]+\.[0-9a-f]{64}`)

func credentialsSignedBy(t *testing.T, s *testworld.Stack, text string) []string {
	t.Helper()
	var held []string
	for _, candidate := range credentialShape.FindAllString(text, -1) {
		subject := candidate[:strings.LastIndex(candidate, ".")]
		if candidate == stackCredential(t, s, subject) {
			held = append(held, subject)
		}
	}
	return held
}

func stackCredential(t *testing.T, s *testworld.Stack, subject string) string {
	t.Helper()
	key, err := os.ReadFile(filepath.Join(s.Dir, "agent-key"))
	if err != nil {
		t.Fatalf("the test daemon keeps no agent key: %v", err)
	}
	secret, err := hex.DecodeString(strings.TrimSpace(string(key)))
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(subject))
	return subject + "." + hex.EncodeToString(mac.Sum(nil))
}
