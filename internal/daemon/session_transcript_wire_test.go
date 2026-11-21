package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

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

	if _, err := cli.SessionTranscript("no-such-session", ""); client.ErrorCode(err) != "session_not_found" {
		t.Errorf("an unknown session's transcript answered %v, want session_not_found", err)
	}
}

func TestAnAppServerSessionsTranscriptIsReadableWithSecretsRedacted(t *testing.T) {
	eachAppServer(t, func(t *testing.T, h fakeagent.Harness) {
		w := newWorld(t, h)
		app, cli := w.App(), w.Client()
		session, view, server := appServerSession(t, w, app, h)
		promptUntilWorking(app, session, view, server, "deploy with token=do-not-leak")
		server.ToolCall(view.ConversationID, fakeagent.ServerToolCall{Name: "bash", Input: map[string]any{"command": "./deploy.sh"}, Output: "deployed"})
		server.Reply(view.ConversationID, "Deployed.")
		testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStateIdle })

		read, err := cli.SessionTranscript(session, "")
		if err != nil {
			t.Fatalf("read the transcript: %v", err)
		}
		var texts []string
		for _, event := range read.Events {
			texts = append(texts, event.Kind+": "+protocol.Deref(event.ToolName)+protocol.Deref(event.Text))
		}
		want := []string{"user: deploy with token=[REDACTED]", `tool_call: bash{"command":"./deploy.sh"}`, "tool_result: bashdeployed", "assistant: Deployed."}
		if !slices.Equal(texts, want) || !read.AtEnd || read.NextCursor == "" {
			t.Fatalf("transcript = %q (at end %v, cursor %q), want %q with a cursor", texts, read.AtEnd, read.NextCursor, want)
		}

		resumed, err := cli.SessionTranscript(session, read.NextCursor)
		if err != nil {
			t.Fatalf("read from the cursor: %v", err)
		}
		if len(resumed.Events) != 0 || !resumed.AtEnd {
			t.Errorf("reading from the end cursor returned %+v, want nothing new", resumed)
		}
	})
}
