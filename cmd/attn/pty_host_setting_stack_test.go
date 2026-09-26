package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEnablingASharedPTYHostThatCannotStartIsRefusedWithoutBlockingTheDaemon(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	release := filepath.Join(s.Dir, "release-pty-host")
	if err := syscall.Mkfifo(release, 0o600); err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(s.Dir, "unstartable-pty-host")
	script := fmt.Sprintf("#!/bin/sh\nread _ < %q\necho 'host unavailable' >&2\nexit 1\n", release)
	if err := os.WriteFile(host, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	s.Vars = append(s.Vars, "ATTN_PTY_BACKEND=migrating", "ATTN_PTY_HOST_BINARY="+host)
	s.Start()
	app := s.App()

	requestID := uuid.NewString()
	app.Send(protocol.SetSettingMessage{
		Cmd: protocol.CmdSetSetting, Key: "pty_shared_host_enabled", Value: "true", RequestID: protocol.Ptr(requestID),
	})
	probing := openPTYHostRelease(t, release)

	snapshot := s.App().Initial.Settings
	if enabled, active := snapshot["pty_shared_host_enabled"], snapshot["pty_shared_host_active"]; enabled != "false" || active != "false" {
		t.Errorf("settings while the host is probed read enabled=%s active=%s, want the old false/false", enabled, active)
	}
	stateID := uuid.NewString()
	if state := testworld.Request(app, protocol.AutoModeGetMessage{Cmd: protocol.CmdAutoModeGet, RequestID: stateID}, protocol.EventAutoModeStateResult,
		func(r protocol.AutoModeStateResultMessage) bool { return r.RequestID == stateID }); !state.Success {
		t.Errorf("automode_get while the host is probed failed: %s", protocol.Deref(state.Error))
	}

	if _, err := probing.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	probing.Close()
	answer := testworld.Await(app, protocol.EventSettingsUpdated,
		func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
	if protocol.Deref(answer.Success) || !strings.Contains(protocol.Deref(answer.Error), "cannot enable shared PTY host") {
		t.Errorf("enabling an unstartable host answered success=%t error=%q, want a refusal naming the host", protocol.Deref(answer.Success), protocol.Deref(answer.Error))
	}
	if enabled, active := answer.Settings["pty_shared_host_enabled"], answer.Settings["pty_shared_host_active"]; enabled != "false" || active != "false" {
		t.Errorf("after the refusal the shared host reads enabled=%s active=%s, want false/false", enabled, active)
	}
}

func openPTYHostRelease(t *testing.T, fifo string) *os.File {
	t.Helper()
	opened := make(chan *os.File, 1)
	go func() {
		f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			t.Error(err)
		}
		opened <- f
	}()
	select {
	case f := <-opened:
		if f == nil {
			t.FailNow()
		}
		return f
	case <-time.After(fakeagent.HangGuard):
		t.Fatalf("the daemon never started the shared PTY host within %s", fakeagent.HangGuard)
		return nil
	}
}
