package main_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnOutpostSessionDownloadsAFileTheUserAttachedOnTheHomeOntoTheOutpost(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	for _, stack := range []*testworld.Stack{home, outpost} {
		stack.Vars = append(stack.Vars, "SHELL=/bin/bash")
	}
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	pair.Start()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	id := home.Spawn(pair.App, fakeagent.Claude, outpost.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	outpost.LaunchedCarrying(pair.App.Terminal(id))

	data, err := os.ReadFile("../../docs/banner.png")
	if err != nil {
		t.Fatal(err)
	}
	app := home.TrustedApp()
	capture, attachment := uuid.NewString(), uuid.NewString()
	uploadQuickCaptureChunks(t, app, capture, attachment, data)
	quickCaptureCall(t, app, protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, CaptureID: capture, Mailbox: protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession, SessionID: protocol.Ptr(protocol.SessionID(id))}, Content: "the banner", AttachmentIds: []string{attachment}})

	if err := os.MkdirAll(outpost.Path("shell"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := outpost.Run(testworld.Invocation{Args: []string{"agent", "attachment", attachment, "--out", "banner.png"}, Terminal: pair.App.Terminal(id), Dir: outpost.Path("shell")})
	if got.Code != 0 {
		t.Fatalf("download on the outpost exited %d: %s", got.Code, got.Stderr)
	}
	saved, err := os.ReadFile(outpost.Path("shell") + "/banner.png")
	if err != nil || !bytes.Equal(saved, data) {
		t.Fatalf("the outpost's copy differs from the attached file: %d bytes, %v", len(saved), err)
	}

	side := testworld.Request(app, protocol.ProfileCreateMessage{Cmd: protocol.CmdProfileCreate, RequestID: "side", Name: "Side"}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "side" })
	if !side.Success {
		t.Fatalf("create Side: %+v", side)
	}
	if err := home.InjectSession("stranger", "stranger", home.Path("stranger"), "claude", side.Profile.ID); err != nil {
		t.Fatalf("register stranger: %v", err)
	}
	if selected := testworld.Request(app, protocol.ProfileSelectMessage{Cmd: protocol.CmdProfileSelect, ProfileID: side.Profile.ID, RequestID: "select-side"}, protocol.EventProfileActionResult, func(r protocol.ProfileActionResultMessage) bool { return r.RequestID == "select-side" }); selected.Error != nil {
		t.Fatalf("select Side: %s", *selected.Error)
	}
	foreign, foreignAttachment := uuid.NewString(), uuid.NewString()
	uploadQuickCaptureChunks(t, app, foreign, foreignAttachment, data)
	quickCaptureCall(t, app, protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, CaptureID: foreign, Mailbox: protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession, SessionID: protocol.Ptr(protocol.SessionID("stranger"))}, Content: "the other profile's banner", AttachmentIds: []string{foreignAttachment}})

	for _, args := range [][]string{{"--out", "mine.png"}, {"--out", "named.png", "--session", "stranger"}} {
		got := outpost.Run(testworld.Invocation{Args: append([]string{"agent", "attachment", foreignAttachment}, args...), Terminal: pair.App.Terminal(id), Dir: outpost.Path("shell")})
		if _, err := os.Stat(outpost.Path("shell") + "/" + args[1]); got.Code == 0 || err == nil {
			t.Errorf("the outpost session fetched another profile's attachment with %q: exit %d, %s", args, got.Code, got.Stderr)
		}
	}
}
