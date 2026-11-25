package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/testworld"
)

func sharedCodexCaller(t *testing.T, w *world, app *testworld.Peer) agentToolsCaller {
	t.Helper()
	return agentToolsCaller{w: w, bearer: agentCredential(t, w, "s.codex:"+app.SelectedProfile())}
}

func (c agentToolsCaller) noteFrom(t *testing.T, conversation, seed, text string) {
	t.Helper()
	result, err := c.callNaming(t, "seed_note", map[string]any{"seed": seed, "text": text}, mcp.Meta{"threadId": conversation})
	if err != nil || result.IsError {
		t.Fatalf("seed_note from conversation %s was refused: %v %s", conversation, err, resultText(result))
	}
}

func TestSharedCodexConversationsCallAttnToolsAsTheSessionThatShowsThem(t *testing.T) {
	w := newWorld(t, fakeagent.Codex)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	seed := plantForTools(t, w)
	checkout, codex, terminal := sharedCodexWaiting(t, w, app)
	server := w.CodexServer()
	agent := sharedCodexCaller(t, w, app)
	tax := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
	taxCodex := w.Launched(tax)
	if len(w.CodexServers()) != 1 {
		t.Fatalf("the two Codex sessions run on %d app-servers, want one shared", len(w.CodexServers()))
	}
	note := func(conversation, text, want string) {
		t.Helper()
		agent.noteFrom(t, conversation, seed, text)
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
}

func TestACodexLaunchCarriesNoAttnServerAndReadsThePromptItReadWithTheSettingOff(t *testing.T) {
	t.Run("shared", func(t *testing.T) {
		w := newWorld(t, fakeagent.Codex)
		app := w.App()
		off := launchGuidance(t, w, w.Launched(w.Spawn(app, fakeagent.Codex, w.Path("shop"))))
		setSetting(t, app, "agent_tools_enabled", "true")
		_, codex, terminal := sharedCodexWaiting(t, w, app)
		moved := *codex
		typeInto(app, terminal, "/side\r")
		codex.Prompted()
		for _, conversation := range []string{moved.ConversationID, codex.ConversationID} {
			for key := range w.CodexServer().MemoryConfig(conversation) {
				if strings.HasPrefix(key, "mcp_servers.") {
					t.Errorf("conversation %s was configured with %s", conversation, key)
				}
			}
		}
		if on := launchGuidance(t, w, &moved); on != off {
			t.Errorf("with agent_tools_enabled on, Codex launched with the prompt\n%s\nwant the one it has with the setting off\n%s", on, off)
		}
	})
	t.Run("plain", func(t *testing.T) {
		inBubbleWithAgents(t, func(t *testing.T, w *world) {
			app := w.App()
			setSetting(t, app, "direct_agent_launch_enabled", "true")
			launch := func() ptybackend.SpawnOptions {
				session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
				return w.terms.Terminal(app.Terminal(session)).Options
			}
			prompt := func(options ptybackend.SpawnOptions) string {
				at := slices.IndexFunc(options.AgentArgv, func(arg string) bool { return strings.HasPrefix(arg, "developer_instructions=") })
				if at < 0 {
					t.Fatalf("Codex launched without instructions: %q", options.AgentArgv)
				}
				return options.AgentArgv[at]
			}
			off := launch()
			setSetting(t, app, "agent_tools_enabled", "true")
			on := launch()
			for _, given := range slices.Concat(on.AgentArgv, on.AgentEnv) {
				if strings.Contains(given, "mcp_servers.attn") || strings.Contains(given, "ATTN_AGENT_CREDENTIAL") || strings.Contains(given, "ATTN_AGENT_TOOLS_URL") {
					t.Errorf("Codex launched with %q", given)
				}
			}
			if prompt(on) != prompt(off) {
				t.Errorf("with agent_tools_enabled on, Codex launched with the prompt\n%s\nwant the one it has with the setting off\n%s", prompt(on), prompt(off))
			}
		})
	})
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
	agent := sharedCodexCaller(t, w, app)

	mark := len(app.Log())
	typeInto(app, second, "/resume "+conversation+"\r")
	taxCodex.Prompted()
	if got := awaitTerminalSessionChange(app, mark, second, tax); got != cart {
		t.Fatalf("after /resume of the conversation of %s, terminal %s shows session %s", cart, second, got)
	}
	mark = len(app.Log())
	moveOn(t, app, codex, first, "/new", "add a discount field")
	discount := awaitTerminalSessionChange(app, mark, first, cart)

	agent.noteFrom(t, conversation, seed, "Locked the tax lookup.")
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
	agent := sharedCodexCaller(t, w, app)

	for name, meta := range map[string]mcp.Meta{
		"no conversation":         nil,
		"an unknown conversation": {"threadId": "conversation-nobody-holds"},
	} {
		refused, err := agent.callNaming(t, "seed_note", map[string]any{"seed": seed, "text": "From nowhere."}, meta)
		if err != nil {
			t.Fatalf("%s: the profile's shared Codex credential was refused: %v", name, err)
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
	w.CodexServer().Prompted(conversation)
	sharedCodexCaller(t, w, app).noteFrom(t, conversation, seed, "Woken by feedback.")
	if author, _ := newestNoteAuthor(t, w, seed); author != checkout {
		t.Errorf("seed_note from the woken conversation of %s is by %s", checkout, author)
	}
}
