package daemon_test

import (
	"fmt"
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

func noteThroughCodexConfig(t *testing.T, server *fakeagent.CodexServer, conversation, seed, text string) {
	t.Helper()
	if result := server.CallTool(conversation, "attn", "attn_do", noteScript(seed, text)); result.IsError {
		t.Fatalf("seed_note from conversation %s was refused: %s", conversation, result.Text)
	}
}

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
		noteThroughCodexConfig(t, server, conversation, seed, text)
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

func TestACodexLaunchCarriesOneAttnServerAndLeavesTheCLITeachingToIt(t *testing.T) {
	wantServer := func(t *testing.T, given map[string]string, auth string) {
		t.Helper()
		var keys []string
		for key := range given {
			if strings.HasPrefix(key, "mcp_servers.") {
				keys = append(keys, key)
			}
		}
		slices.Sort(keys)
		want := []string{"mcp_servers.attn." + auth, "mcp_servers.attn.default_tools_approval_mode", "mcp_servers.attn.url"}
		slices.Sort(want)
		if !slices.Equal(keys, want) || given["mcp_servers.attn.default_tools_approval_mode"] != "approve" || !strings.HasSuffix(given["mcp_servers.attn.url"], "/mcp") {
			t.Errorf("Codex was configured with %v, want one attn server %v that approves its tools", given, want)
		}
	}
	t.Run("shared", func(t *testing.T) {
		w := newWorld(t, fakeagent.Codex)
		app := w.App()
		_, before, _ := sharedCodexWaiting(t, w, app)
		for key := range w.CodexServer().MemoryConfig(before.ConversationID) {
			if strings.HasPrefix(key, "mcp_servers.") {
				t.Errorf("with agent_tools_enabled off, the conversation was configured with %s", key)
			}
		}
		setSetting(t, app, "agent_tools_enabled", "true")
		seed := plantForTools(t, w)
		session, codex, _ := sharedCodexWaiting(t, w, app)
		given := map[string]string{}
		for key, value := range w.CodexServer().MemoryConfig(codex.ConversationID) {
			given[key] = fmt.Sprint(value)
		}
		wantServer(t, given, "http_headers.Authorization")
		if prompt := launchGuidance(t, w, codex); strings.Contains(prompt, seedCLITeaching) {
			t.Errorf("a shared Codex launched with attn's server still reads the garden's CLI teaching:\n%s", prompt)
		}
		noteThroughCodexConfig(t, w.CodexServer(), codex.ConversationID, seed, "Called through the server Codex was given.")
		if author, _ := newestNoteAuthor(t, w, seed); author != session {
			t.Errorf("the note is by %s, want the session %s", author, session)
		}
	})
	t.Run("plain", func(t *testing.T) {
		inBubbleWithAgents(t, func(t *testing.T, w *world) {
			app := w.App()
			setSetting(t, app, "direct_agent_launch_enabled", "true")
			launch := func() (string, ptybackend.SpawnOptions) {
				session := w.Spawn(app, fakeagent.Codex, w.Path("shop"))
				return session, w.terms.Terminal(app.Terminal(session)).Options
			}
			overrides := func(options ptybackend.SpawnOptions) map[string]string {
				given := map[string]string{}
				for _, arg := range options.AgentArgv {
					if key, value, ok := strings.Cut(arg, "="); ok {
						given[key] = strings.Trim(value, "\"")
					}
				}
				return given
			}
			_, off := launch()
			for key := range overrides(off) {
				if strings.HasPrefix(key, "mcp_servers.") {
					t.Errorf("with agent_tools_enabled off, Codex launched with %s", key)
				}
			}
			setSetting(t, app, "agent_tools_enabled", "true")
			session, on := launch()
			given := overrides(on)
			wantServer(t, given, "bearer_token_env_var")
			if prompt := given["developer_instructions"]; prompt == "" || strings.Contains(prompt, seedCLITeaching) {
				t.Errorf("a plain Codex launched with attn's server reads the prompt %q, want one without the garden's CLI teaching", prompt)
			}
			credential := ""
			for _, env := range on.AgentEnv {
				if value, ok := strings.CutPrefix(env, given["mcp_servers.attn.bearer_token_env_var"]+"="); ok {
					credential = value
				}
			}
			if credential == "" {
				t.Fatalf("Codex launched without the credential its server entry names: %q", on.AgentEnv)
			}
			if want := terminalCredential(t, w, app.Terminal(session)); credential != want {
				t.Errorf("Codex launched with the credential %q, want its terminal's %q", credential, want)
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

	mark := len(app.Log())
	typeInto(app, second, "/resume "+conversation+"\r")
	taxCodex.Prompted()
	if got := awaitTerminalSessionChange(app, mark, second, tax); got != cart {
		t.Fatalf("after /resume of the conversation of %s, terminal %s shows session %s", cart, second, got)
	}
	mark = len(app.Log())
	moveOn(t, app, codex, first, "/new", "add a discount field")
	discount := awaitTerminalSessionChange(app, mark, first, cart)

	noteThroughCodexConfig(t, w.CodexServer(), conversation, seed, "Locked the tax lookup.")
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
	noteThroughCodexConfig(t, w.CodexServer(), conversation, seed, "Woken by feedback.")
	if author, _ := newestNoteAuthor(t, w, seed); author != checkout {
		t.Errorf("seed_note from the woken conversation of %s is by %s", checkout, author)
	}
}
