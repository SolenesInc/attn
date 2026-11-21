package daemon_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestSharedCodexConversationsCallAttnToolsAsTheSessionThatShowsThem(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	seed := plantForTools(t, w)
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	server := w.CodexServer()
	tax := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	taxCodex := w.Launched(tax)
	if len(w.CodexServers()) != 1 {
		t.Fatalf("the two Codex sessions run on %d app-servers, want one shared", len(w.CodexServers()))
	}
	note := func(conversation, text, want string) {
		t.Helper()
		result := server.CallTool(conversation, "attn-core", "seed_note", map[string]any{"seed": seed, "text": text})
		if result.IsError {
			t.Fatalf("seed_note from conversation %s was refused: %s", conversation, result.Text)
		}
		if author, body := newestNoteAuthor(t, w, seed); author != want || body != text {
			t.Errorf("seed_note from conversation %s wrote %q by %s, want it by %s", conversation, body, author, want)
		}
	}

	note(codex.ConversationID, "The checkout test races the tax lookup.", checkout)
	note(taxCodex.ConversationID, "The tax table is fine.", tax)
	note(server.SpawnSubagent(codex.ConversationID), "A subagent locked the lookup.", checkout)

	first := codex.ConversationID
	mark := len(app.Log())
	moveOn(t, app, codex, terminal, "/clear", "add a discount field")
	discount := awaitTerminalSessionChange(app, mark, terminal, checkout)
	note(codex.ConversationID, "The discount field is in.", discount)

	mark = len(app.Log())
	typeInto(app, terminal, "/agents "+first+"\r")
	codex.Prompted()
	if got := awaitTerminalSessionChange(app, mark, terminal, discount); got != checkout {
		t.Fatalf("after /agents to %s, terminal %s shows session %s, want %s", first, terminal, got, checkout)
	}
	note(first, "Back on the checkout test.", checkout)

	typeInto(app, terminal, "/side\r")
	codex.Prompted()
	if codex.ConversationID == first {
		t.Fatalf("/side left terminal %s on conversation %s", terminal, first)
	}
	note(codex.ConversationID, "Asked on the side.", checkout)
}

func TestSharedCodexGetsNoAttnToolsWhileTheSettingIsOff(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	_, codex, _ := sharedCodexWaiting(t, w, app)
	for key := range w.CodexServer().MemoryConfig(codex.ConversationID) {
		if strings.HasPrefix(key, "mcp_servers.") {
			t.Errorf("with agent_tools_enabled off, the conversation was configured with %s", key)
		}
	}
}

func TestASharedCodexConversationResumedInASecondTerminalCallsToolsAsItsOwnSessionAfterTheFirstMovesOn(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	seed := plantForTools(t, w)
	cart, codex, first := sharedCodexWaiting(t, w, app)
	conversation := codex.ConversationID
	tax := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	taxCodex := w.Launched(tax)
	second := app.Terminal(tax)
	server := w.CodexServer()

	mark := len(app.Log())
	typeInto(app, second, "/resume "+conversation+"\r")
	taxCodex.Prompted()
	if got := awaitTerminalSessionChange(app, mark, second, tax); got != cart {
		t.Fatalf("after /resume of the conversation of %s, terminal %s shows session %s", cart, second, got)
	}
	mark = len(app.Log())
	moveOn(t, app, codex, first, "/new", "add a discount field")
	discount := awaitTerminalSessionChange(app, mark, first, cart)

	result := server.CallTool(conversation, "attn-core", "seed_note", map[string]any{"seed": seed, "text": "Locked the tax lookup."})
	if result.IsError {
		t.Fatalf("seed_note from conversation %s was refused: %s", conversation, result.Text)
	}
	if author, _ := newestNoteAuthor(t, w, seed); author != cart {
		t.Errorf("seed_note from the conversation of %s, which terminal %s shows, is by %s (the first terminal now shows %s)", cart, second, author, discount)
	}
}

func TestASharedCodexCallThatNamesNoConversationOfItsProfileIsRefusedNamingIt(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	seed := plantForTools(t, w)
	sharedCodexWaiting(t, w, app)
	agent := agentToolsCaller{w: w, bearer: agentCredential(t, w, "s.codex:"+app.SelectedProfile())}

	for name, meta := range map[string]mcp.Meta{
		"no conversation":         nil,
		"an unknown conversation": {"threadId": "conversation-nobody-holds"},
	} {
		session, err := agent.connect(t, "?tools=core")
		if err != nil {
			t.Fatalf("%s: the profile's shared Codex credential was refused: %v", name, err)
		}
		refused, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "seed_note", Arguments: map[string]any{"seed": seed, "text": "From nowhere."}, Meta: meta})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := resultText(refused)
		if !refused.IsError || (meta != nil && !strings.Contains(text, "conversation-nobody-holds")) || (meta == nil && !strings.Contains(text, "threadId")) {
			t.Errorf("%s: seed_note was answered %q, want a refusal that names it", name, text)
		}
	}
	notes, err := w.Client().SeedNotes("", seed, client.SeedNotesQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(notes.Notes) != 0 {
		t.Errorf("a refused call wrote a note: %+v", notes.Notes[0])
	}
}

func TestAHiddenSharedCodexConversationThatAttnWakesKeepsItsAttnTools(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	seed := plantForTools(t, w)
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	conversation := codex.ConversationID
	moveOn(t, app, codex, terminal, "/new", "add a discount field")
	codex.Reply("Added. <!-- attn:state=idle -->")
	testworld.AwaitSession(app, checkout, func(s protocol.Session) bool { return protocol.Deref(s.Hidden) })

	if got := submitSessionAnnotationFeedback(app, checkout, sessionAnnotationFeedback); !got.success {
		t.Fatalf("feedback to the hidden session = %+v", got)
	}
	server := w.CodexServer()
	server.Prompted(conversation)
	result := server.CallTool(conversation, "attn-core", "seed_note", map[string]any{"seed": seed, "text": "Woken by feedback."})
	if result.IsError {
		t.Fatalf("seed_note from the woken conversation %s was refused: %s", conversation, result.Text)
	}
	if author, _ := newestNoteAuthor(t, w, seed); author != checkout {
		t.Errorf("seed_note from the woken conversation of %s is by %s", checkout, author)
	}
}
