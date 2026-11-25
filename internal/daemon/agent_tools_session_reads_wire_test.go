package daemon_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestEveryAgentSessionAndStateCommandReturnsToAScriptWhatItsCLIPrints(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	shapes := newToolShapes(t, agent, listOfferedTools(t, agent))

	shapes.call("state_explain", map[string]any{"id": session})
	shapes.call("list", map[string]any{})
	shapes.call("activity", map[string]any{})
	shapes.call("activity_clear", map[string]any{"id": session})
	shapes.call("session_list", map[string]any{})
	shapes.call("session_list", map[string]any{"all": true, "reopen": true})
	shapes.call("session_list", map[string]any{"tree": true})
	shapes.call("session_show", map[string]any{"id": session})
	shapes.call("session_rename", map[string]any{"name": "tool rename"})
	shapes.call("session_priority", map[string]any{"priority": "on"})
	shapes.call("session_snooze", map[string]any{"ids": []string{session}, "until": "1h"})
	shapes.call("session_unsnooze", map[string]any{"ids": []string{session}})
	shapes.call("session_reopen", map[string]any{"id": session})
	shapes.finish("state_", "list", "activity", "session_list", "session_show", "session_rename", "session_priority", "session_snooze", "session_unsnooze", "session_reopen")

	unknown := agent.mustCall(t, "state_explain", map[string]any{"id": "no-such-session"})
	if text := resultText(unknown); !unknown.IsError || text != `session_not_found: no session "no-such-session"; pass a name, short id, full id or unique prefix`+"\nFix: Pass the name, short id, full id or unique prefix of a live session." {
		t.Errorf("state_explain of a session that does not exist answered %q, want session_not_found with the catalog's fix", text)
	}
}

func TestTheSessionTranscriptAndInstructionsCommandsReturnToAScriptWhatTheirCLIPrints(t *testing.T) {
	w := newTitlingWorld(t, fakeagent.Codex)
	w.AnswerHeadlessTasks(func(task *fakeagent.HeadlessTask) {
		switch {
		case strings.Contains(task.Prompt, "You generate short titles"):
			task.Answer("Merge PR 571")
		case strings.Contains(task.Prompt, "Classify how this assistant message ends"):
			task.Answer(`{"verdict":"DONE"}`)
		default:
			task.Answer(instructionsAnswer("Yes: the user said so.", instructionsEvidence{"turn-1", "Please merge PR #571."}))
		}
	})
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	codex := w.Launched(session)
	app.TypeLine(session, "Please merge PR #571.")
	codex.Prompted()
	working := testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateWorking })
	codex.Reply("Merged PR #571.")
	testworld.AwaitStateAfter(app, working, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	shapes := newToolShapes(t, agent, listOfferedTools(t, agent))
	shapes.call("session_transcript", map[string]any{"id": session})
	shapes.call("session_instructions", map[string]any{"id": session, "question": "Was merging PR #571 authorized?"})
	shapes.finish("session_transcript", "session_instructions")
}

func TestAnotherProfilesSessionDoesNotExistForTheSessionCommands(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "agent_tools_enabled", "true")
	registerSessions(t, w, cli, "planter", "neighbour")
	side := createProfile(app, "Side")
	if err := w.InjectSession("stranger", "stranger", w.Path("stranger"), "claude", side.ID); err != nil {
		t.Fatalf("register stranger: %v", err)
	}
	planter := cli.WithCaller(protocol.TerminalID(w.Terminal("planter")))
	run := func(caller *client.Client, name string, args map[string]any) (any, error) {
		var result any
		err := caller.RunCommand(name, "", args, &result)
		return result, err
	}
	foreign := []struct {
		name string
		args map[string]any
	}{
		{"session show", map[string]any{"id": "stranger"}},
		{"session transcript", map[string]any{"id": "stranger"}},
		{"session instructions", map[string]any{"id": "stranger", "question": "what?"}},
		{"session reopen", map[string]any{"id": "stranger"}},
		{"session rename", map[string]any{"name": "taken over", "target": "stranger"}},
		{"session priority", map[string]any{"priority": "on", "target": "stranger"}},
	}
	for _, call := range foreign {
		if _, err := run(planter, call.name, call.args); err == nil || (client.ErrorCode(err) != "session_not_found" && !strings.Contains(err.Error(), "unknown live session stranger")) {
			t.Errorf("%s of another profile's session from planter = %v, want it unknown", call.name, err)
		}
	}
	snoozed, err := run(planter, "session snooze", map[string]any{"ids": []string{"stranger"}, "until": "1h"})
	if err != nil || !strings.Contains(fmt.Sprint(snoozed), "unknown live session stranger") {
		t.Errorf("snooze of another profile's session from planter = %v, %v, want it unknown", snoozed, err)
	}
	for _, args := range []map[string]any{{}, {"all": true}, {"profile": side.ID}, {"tree": true}} {
		listed, err := run(planter, "session list", args)
		if err != nil || strings.Contains(fmt.Sprint(listed), "stranger") || !strings.Contains(fmt.Sprint(listed), "neighbour") && args["profile"] == nil {
			t.Errorf("session list %v from planter = %v, %v, want neighbour and not stranger", args, listed, err)
		}
	}
	for _, all := range []bool{false, true} {
		if _, err := run(planter, "session list", map[string]any{"before": "stranger", "all": all}); client.ErrorCode(err) != "unknown_session" {
			t.Errorf("session list before another profile's session (all %v) = %v, want unknown_session", all, err)
		}
	}
	if _, err := run(cli, "session list", map[string]any{"before": "stranger", "all": true}); err != nil {
		t.Errorf("session list before any profile's session from outside attn: %v", err)
	}
	if _, err := run(cli, "session show", map[string]any{"id": "stranger"}); err != nil {
		t.Errorf("session show of any profile's session from outside attn: %v", err)
	}
	if listed, err := run(cli, "session list", map[string]any{"all": true}); err != nil || !strings.Contains(fmt.Sprint(listed), "stranger") {
		t.Errorf("session list from outside attn = %v, %v, want every profile", listed, err)
	}

	mine := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(mine))}
	if shown := agent.mustCall(t, "session_show", map[string]any{"id": "stranger"}); !shown.IsError || !strings.HasPrefix(resultText(shown), "session_not_found: ") {
		t.Errorf("session_show of another profile's session over /mcp = %q, want session_not_found", resultText(shown))
	}
	if listed := agent.mustCall(t, "session_list", map[string]any{"all": true}); listed.IsError || strings.Contains(fmt.Sprint(listed.StructuredContent), "stranger") {
		t.Errorf("session_list over /mcp = %v, want no session of another profile", listed.StructuredContent)
	}
}

func TestEveryAgentCommandReturnsToAScriptWhatItsCLIPrints(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	mine := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(mine)
	other := w.Spawn(app, fakeagent.Claude, w.Path("blog"))
	w.Launched(other)
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(mine))}
	shapes := newToolShapes(t, agent, listOfferedTools(t, agent))

	shapes.call("agent_list", map[string]any{})
	shapes.call("agent_peek", map[string]any{"target": other})
	sent := shapes.call("agent_msg", map[string]any{"target": other, "text": "the build is green"})
	messageID := fmt.Sprint(sent.StructuredContent.(map[string]any)["message_id"])
	shapes.call("agent_msg_status", map[string]any{"message": messageID})
	shapes.call("agent_inbox", map[string]any{})
	capture, attachment := uuid.NewString(), uuid.NewString()
	trusted := w.TrustedApp()
	uploadQuickCapture(t, trusted, capture, attachment, quickCapturePNG(t), true)
	if _, err := trusted.QuickCapture(protocol.QuickCaptureSendMessage{Cmd: protocol.CmdQuickCaptureSend, CaptureID: capture, Mailbox: protocol.QuickCaptureMailbox{Kind: protocol.QuickCaptureMailboxKindSession, SessionID: protocol.Ptr(protocol.SessionID(mine))}, Content: "see the screenshot", AttachmentIds: []string{attachment}}); err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(w.Path("shop"), "screenshot.png")
	shapes.call("agent_attachment", map[string]any{"attachment": attachment, "out": saved})
	if data, err := os.ReadFile(saved); err != nil || !bytes.Equal(data, quickCapturePNG(t)) {
		t.Errorf("agent_attachment wrote %d bytes, %v; want the uploaded screenshot", len(data), err)
	}
	shapes.call("agent_reload", map[string]any{"targets": []string{mine}})
	shapes.call("agent_close", map[string]any{"self": true, "reason": "Done with the shop"})
	shapes.finish("agent_")
}

func TestAnotherProfilesSessionDoesNotExistForTheAgentCommands(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	setSetting(t, app, "agent_tools_enabled", "true")
	registerSessions(t, w, cli, "planter", "neighbour")
	side := createProfile(app, "Side")
	if err := w.InjectSession("stranger", "stranger", w.Path("stranger"), "claude", side.ID); err != nil {
		t.Fatalf("register stranger: %v", err)
	}
	planter := cli.WithCaller(protocol.TerminalID(w.Terminal("planter")))
	run := func(name string, args map[string]any) (any, error) {
		var result any
		err := planter.RunCommand(name, "", args, &result)
		return result, err
	}
	if _, err := run("agent peek", map[string]any{"target": "stranger"}); client.ErrorCode(err) != "session_not_found" {
		t.Errorf("peek of another profile's session = %v, want session_not_found", err)
	}
	if _, err := run("agent msg", map[string]any{"target": "stranger", "text": "hello"}); client.ErrorCode(err) != "session_or_crew_member_not_found" {
		t.Errorf("msg to another profile's session = %v, want session_or_crew_member_not_found", err)
	}
	if _, err := run("agent close", map[string]any{"target": "stranger", "reason": "not mine"}); err == nil {
		t.Errorf("close of another profile's session succeeded")
	}
	if reloaded, err := run("agent reload", map[string]any{"targets": []string{"stranger"}}); err != nil || strings.Contains(fmt.Sprint(reloaded), "success:true") {
		t.Errorf("reload of another profile's session = %v, %v, want a failed outcome", reloaded, err)
	}
	if listed, err := run("agent list", map[string]any{}); err != nil || strings.Contains(fmt.Sprint(listed), "stranger") || !strings.Contains(fmt.Sprint(listed), "neighbour") {
		t.Errorf("agent list from planter = %v, %v, want neighbour and not stranger", listed, err)
	}
	var everyone any
	if err := cli.RunCommand("agent list", "", map[string]any{}, &everyone); err != nil || !strings.Contains(fmt.Sprint(everyone), "stranger") {
		t.Errorf("agent list from outside attn = %v, %v, want every profile", everyone, err)
	}

	mine := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(mine))}
	if peek := agent.mustCall(t, "agent_peek", map[string]any{"target": "stranger"}); !peek.IsError || !strings.HasPrefix(resultText(peek), "session_not_found: ") {
		t.Errorf("agent_peek of another profile's session over /mcp = %q, want session_not_found", resultText(peek))
	}
	if listed := agent.mustCall(t, "agent_list", map[string]any{}); listed.IsError || strings.Contains(fmt.Sprint(listed.StructuredContent), "stranger") {
		t.Errorf("agent_list over /mcp = %v, want no session of another profile", listed.StructuredContent)
	}
}
