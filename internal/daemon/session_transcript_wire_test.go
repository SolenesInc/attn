package daemon_test

import (
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAClosedPiTranscriptIncludesRedactedMessagesThinkingToolsAndErrors(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	driver := connectDriver(t, w, "attn-pi", "pi", map[string]bool{"state_reporting": true})
	awaitDriverAvailable(app, "pi")
	session, run := spawnDriven(w, app, driver, w.Path("shop"))
	path := w.Path("pi-session.jsonl")
	const records = `{"type":"session","version":3,"id":"pi-day","timestamp":"2026-10-01T12:00:00Z"}
{"type":"message","id":"user","timestamp":"2026-10-01T12:00:00Z","message":{"role":"user","content":"deploy with token=do-not-leak"}}
{"type":"message","id":"call","parentId":"user","timestamp":"2026-10-01T12:00:01Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Check the deployment."},{"type":"toolCall","id":"deploy","name":"bash","arguments":{"command":"deploy","token":"do-not-leak"}}]}}
{"type":"message","id":"result","parentId":"call","timestamp":"2026-10-01T12:00:02Z","message":{"role":"toolResult","toolCallId":"deploy","toolName":"bash","isError":true,"content":[{"type":"text","text":"deploy failed"}]}}
{"type":"message","id":"error","parentId":"result","timestamp":"2026-10-01T12:00:03Z","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"Provider unavailable."}}
{"type":"message","id":"answer","parentId":"error","timestamp":"2026-10-01T12:00:04Z","message":{"role":"assistant","content":[{"type":"text","text":"Retry after the provider recovers. <!-- attn:state=idle -->"}]}}
`
	if err := os.WriteFile(path, []byte(records), 0600); err != nil {
		t.Fatal(err)
	}
	if err := reportTranscript(driver, run.SessionID, run.RunID, path); err != nil {
		t.Fatal(err)
	}
	closeSession(t, w.Client(), session, "day finished")
	read, err := w.Client().SessionTranscript(session, "")
	if err != nil {
		t.Fatal(err)
	}
	var kinds, texts []string
	for _, event := range read.Events {
		kinds = append(kinds, event.Kind)
		texts = append(texts, protocol.Deref(event.Text))
	}
	if strings.Join(kinds, ",") != "user,thinking,tool_call,tool_result,error,assistant" {
		t.Fatalf("Pi transcript events: %v", kinds)
	}
	if texts[0] != "deploy with token=[REDACTED]" || texts[1] != "Check the deployment." ||
		texts[3] != "deploy failed" || texts[4] != "Provider unavailable." || texts[5] != "Retry after the provider recovers." ||
		strings.Contains(strings.Join(texts, "\n"), "do-not-leak") {
		t.Fatalf("Pi transcript text: %q", texts)
	}
	if protocol.Deref(read.Events[2].ToolName) != "bash" || protocol.Deref(read.Events[2].ToolCallID) != "deploy" ||
		protocol.Deref(read.Events[3].ToolCallID) != "deploy" || !protocol.Deref(read.Events[3].IsError) || read.Events[3].Timestamp == nil {
		t.Fatalf("Pi transcript tool events: %+v", read.Events[2:4])
	}
	resumed, err := w.Client().SessionTranscript(session, read.NextCursor)
	if err != nil || !resumed.AtEnd || len(resumed.Events) != 0 {
		t.Fatalf("Pi transcript cursor: %+v, %v", resumed, err)
	}
}

func TestASessionsTranscriptIsReadableWithSecretsRedacted(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	cli := w.Client()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	claude := w.Launched(session)
	app.TypeLine(session, "deploy with token=do-not-leak")
	claude.Prompted()
	claude.Reply("Deployed. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

	read, err := cli.SessionTranscript(session, "")
	if err != nil {
		t.Fatalf("read the transcript: %v", err)
	}
	var texts []string
	for _, event := range read.Events {
		texts = append(texts, event.Kind+": "+protocol.Deref(event.Text))
	}
	if len(texts) != 2 || !strings.HasPrefix(texts[0], "user: deploy with token=[REDACTED]") || !strings.HasPrefix(texts[1], "assistant: Deployed.") ||
		!read.AtEnd || read.NextCursor == "" {
		t.Fatalf("transcript = %q (at end %v, cursor %q), want the redacted prompt and the reply with a cursor", texts, read.AtEnd, read.NextCursor)
	}
	if strings.Contains(strings.Join(texts, "\n"), "do-not-leak") {
		t.Errorf("the transcript leaked the secret: %q", texts)
	}

	resumed, err := cli.SessionTranscript(session, read.NextCursor)
	if err != nil {
		t.Fatalf("read from the cursor: %v", err)
	}
	if len(resumed.Events) != 0 || !resumed.AtEnd {
		t.Errorf("reading from the end cursor returned %+v, want nothing new", resumed)
	}

	if _, err := cli.SessionTranscript("no-such-session", ""); err == nil || !strings.Contains(err.Error(), "session_not_found") {
		t.Errorf("an unknown session's transcript answered %v, want session_not_found", err)
	}
}
