package main_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestARingAModHeldThroughTheUpgradeToChiefMembersComesBackToTheChiefOnce(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	s.Start()
	app := s.TrustedApp()
	if set := s.Attn("crew", "set", "chief", "--agent", "claude"); set.Code != 0 {
		t.Fatalf("crew set chief: %+v", set)
	}
	var woken protocol.CrewWakeResult
	s.Attn("crew", "wake", "chief", "--json").JSON(t, &woken)
	chief := string(woken.SessionID)
	run := s.Launched(chief)
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)
	run.Prompted()
	running := testworld.AwaitSession(app, chief, func(protocol.Session) bool { return true })
	run.Reply("Ready. <!-- attn:state=idle -->")

	run.Input("write the essay")
	if got := run.Prompted(); got != "write the essay" {
		t.Fatalf("the Chief took %q, want the user's prompt", got)
	}
	testworld.AwaitSession(app, chief, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })

	const words = "check the rounding"
	capture := uuid.NewString()
	member := "chief"
	if _, err := app.QuickCapture(protocol.QuickCaptureSendMessage{
		Cmd: protocol.CmdQuickCaptureSend, CaptureID: capture, Content: words, AttachmentIds: []string{},
		Mailbox: protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindCrewMember, MemberID: &member},
	}); err != nil {
		t.Fatal(err)
	}
	for {
		event := run.ModNext()
		if event.Kind == fakeagent.ModEventHeld && strings.Contains(event.Text, words) {
			break
		}
	}
	testworld.Await(app, protocol.EventQuickCaptureRead, func(m protocol.QuickCaptureReadMessage) bool { return m.CaptureID == capture })

	run.ModHeldRingsRecordAddress("chief:" + running.ProfileID)
	s.Stop()
	s.Start()
	app = s.TrustedApp()
	s.AwaitTimer(daemon.ClaudeModPollHoldTimer)

	if returned := run.ModEsc(); len(returned) != 1 {
		t.Fatalf("Esc returned %v, want the held Quick Capture", returned)
	}
	testworld.Await(app, protocol.EventQuickCaptureUnread, func(m protocol.QuickCaptureUnreadMessage) bool { return m.CaptureID == capture })

	run.Input("carry on")
	if got := run.Prompted(); got != "carry on" {
		t.Fatalf("the Chief took %q, want the user's prompt", got)
	}
	run.Reply("Done. <!-- attn:state=waiting_input -->")
	if got := run.Prompted(); !strings.Contains(got, words) || !run.ThroughMod {
		t.Fatalf("the Chief took %q (through the mod: %t), want the returned Quick Capture", got, run.ThroughMod)
	}
	run.Reply("Thanks. <!-- attn:state=waiting_input -->")
	testworld.Await(app, protocol.EventQuickCaptureRead, func(m protocol.QuickCaptureReadMessage) bool { return m.CaptureID == capture })
	probe := "probe-" + uuid.NewString()[:8]
	app.TypeLine(chief, probe)
	if got := run.Prompted(); got != probe {
		t.Fatalf("the Chief took %q before %q, want the Quick Capture only once", got, probe)
	}
}
