package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type captureSendOutput struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Mailbox struct {
		Kind      string `json:"kind"`
		SessionID string `json:"session_id"`
	} `json:"mailbox"`
	CreatedAt   string `json:"created_at"`
	Attachments []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		MediaType string `json:"media_type"`
		Bytes     int    `json:"bytes"`
	} `json:"attachments"`
}

func sendCaptureFromOutside(t *testing.T, s *testworld.Stack, session string, message string, files ...string) captureSendOutput {
	t.Helper()
	args := []string{"capture", "send", "--session", session, "-m", message, "--json"}
	for _, f := range files {
		args = append(args, "--file", f)
	}
	sent := s.Attn(args...)
	if sent.Code != 0 {
		t.Fatalf("capture send: %+v", sent)
	}
	var keys map[string]json.RawMessage
	sent.JSON(t, &keys)
	got := slices.Sorted(func(yield func(string) bool) {
		for k := range keys {
			if !yield(k) {
				return
			}
		}
	})
	if want := []string{"attachments", "content", "created_at", "id", "mailbox"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("capture send --json keys = %v; the app reads %v", got, want)
	}
	var out captureSendOutput
	sent.JSON(t, &out)
	if out.ID == "" || out.CreatedAt == "" || out.Content != message || out.Mailbox.Kind != "session" || out.Mailbox.SessionID != session || len(out.Attachments) != len(files) {
		t.Fatalf("capture send --json = %s", sent.Stdout)
	}
	for i, a := range out.Attachments {
		if a.ID == "" || a.Name != filepath.Base(files[i]) || a.MediaType == "" || a.Bytes == 0 {
			t.Fatalf("attachment %d = %+v", i, a)
		}
	}
	return out
}

func awaitCapturePrompt(t *testing.T, agent *fakeagent.Run, capture captureSendOutput) {
	t.Helper()
	prompt := agent.Prompted()
	if !strings.Contains(prompt, "sent through Quick Capture") || !strings.Contains(prompt, capture.Content) {
		t.Fatalf("delivered prompt = %q", prompt)
	}
	for _, a := range capture.Attachments {
		if !strings.Contains(prompt, "attn agent attachment "+capture.ID+" "+a.ID+" --out <path>") {
			t.Fatalf("prompt lacks the pull command for %s: %q", a.Name, prompt)
		}
	}
}

func pullCaptureFile(t *testing.T, s *testworld.Stack, terminal string, capture captureSendOutput, index int, want []byte) {
	t.Helper()
	out := filepath.Join(t.TempDir(), capture.Attachments[index].Name)
	pulled := s.Run(testworld.Invocation{Terminal: terminal, Args: []string{"agent", "attachment", capture.ID, capture.Attachments[index].ID, "--out", out}})
	if pulled.Code != 0 {
		t.Fatalf("agent attachment: %+v", pulled)
	}
	got, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("pulled %s differs from the sent file (%d bytes, want %d): %v", out, len(got), len(want), err)
	}
}

func TestCaptureSendDeliversFilesToLocalAndOutpostSessions(t *testing.T) {
	home := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	outpost := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)

	image, err := os.ReadFile("../../docs/banner.png")
	if err != nil {
		t.Fatal(err)
	}
	notes := []byte("Steps: open settings, press Save twice.\n")
	dir := t.TempDir()
	shot, steps := filepath.Join(dir, "shot.png"), filepath.Join(dir, "steps.txt")
	if err := os.WriteFile(shot, image, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(steps, notes, 0600); err != nil {
		t.Fatal(err)
	}

	remote := home.Spawn(pair.App, fakeagent.Claude, outpost.Path("remote"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	remoteTerminal := pair.App.Terminal(remote)
	remoteAgent := outpost.LaunchedCarrying(remoteTerminal)
	toRemote := sendCaptureFromOutside(t, home, remote, "Arrow marks the broken button", shot)
	awaitCapturePrompt(t, remoteAgent, toRemote)
	pullCaptureFile(t, outpost, remoteTerminal, toRemote, 0, image)

	local := home.Spawn(pair.App, fakeagent.Claude, home.Path("local"))
	localAgent := home.Launched(local)
	toLocal := sendCaptureFromOutside(t, home, local, "Both files show the bug", shot, steps)
	awaitCapturePrompt(t, localAgent, toLocal)
	pullCaptureFile(t, home, home.Terminal(local), toLocal, 0, image)
	pullCaptureFile(t, home, home.Terminal(local), toLocal, 1, notes)

	history, err := home.TrustedApp().QuickCapture(protocol.QuickCaptureListMessage{Cmd: protocol.CmdQuickCaptureList, Limit: 10})
	if err != nil || len(history.List.Items) != 0 || len(history.List.DraftAssets) != 0 {
		t.Fatalf("Quick Capture panel history shows session deliveries: %+v %v", history, err)
	}

	missing := home.Attn("capture", "send", "--session", "no-such-session", "--file", shot, "-m", "lost")
	if missing.Code == 0 || !strings.Contains(missing.Stderr, "no-such-session") {
		t.Fatalf("send to a missing session: %+v", missing)
	}
}
