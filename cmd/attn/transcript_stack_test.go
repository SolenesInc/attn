package main_test

import (
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestCodexSessionsSharingADirectoryFollowTheirOwnConversationAcrossARestart(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.Codex))
	s.Start()
	app := s.App()
	cwd := s.Path("shop")
	older := s.Spawn(app, fakeagent.Codex, cwd)
	olderRun := s.Launched(older)
	converse(app, olderRun, older, "add a discount field", "Added the discount field.")
	newer := s.Spawn(app, fakeagent.Codex, cwd)
	newerRun := s.Launched(newer)
	converse(app, newerRun, newer, "rename the checkout module", "Renamed the checkout module.")

	s.Stop()
	s.Start()
	app = s.App()
	if got := settledConversation(app, older); !slices.Equal(got, []string{"Added the discount field."}) {
		t.Fatalf("after the restart the older session shows %q, want only its own conversation", got)
	}
	converse(app, newerRun, newer, "and keep the old import path", "Kept the old import path.")
	answered := conversationUntil(app, newer, func(texts []string) bool {
		return len(texts) > 0 && texts[len(texts)-1] == "Kept the old import path."
	})
	if want := []string{"Renamed the checkout module.", "Kept the old import path."}; !slices.Equal(answered, want) {
		t.Fatalf("after answering again the newer session shows %q, want %q", answered, want)
	}
	if got := settledConversation(app, older); !slices.Equal(got, []string{"Added the discount field."}) {
		t.Fatalf("once the newer session answered again the older one shows %q, want only its own conversation", got)
	}
}

func settledConversation(app *testworld.Peer, session string) []string {
	app.T.Helper()
	return conversationUntil(app, session, func([]string) bool { return true })
}

func conversationUntil(app *testworld.Peer, session string, done func(texts []string) bool) []string {
	app.T.Helper()
	for attempt := 0; ; attempt++ {
		requestID := session + "-" + string(rune('a'+attempt))
		window := testworld.Request(app, protocol.SessionMessagesGetMessage{
			Cmd: protocol.CmdSessionMessagesGet, RequestID: requestID, SessionID: session,
		}, protocol.EventSessionMessagesGetResult, func(r protocol.SessionMessagesGetResultMessage) bool { return r.RequestID == requestID })
		if window.Status != protocol.SessionMessageWindowStatusDiscovering {
			if window.Status != protocol.SessionMessageWindowStatusReady {
				app.T.Fatalf("the message window of %s is %s: %s", session, window.Status, protocol.Deref(window.Detail))
			}
			texts := make([]string, 0, len(window.Messages))
			for _, message := range window.Messages {
				texts = append(texts, message.Markdown)
			}
			if done(texts) {
				return texts
			}
		}
		testworld.Await(app, protocol.EventSessionMessagesChanged, func(e protocol.SessionMessagesChangedMessage) bool { return e.SessionID == session })
	}
}
