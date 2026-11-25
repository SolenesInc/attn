package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func startSampleHarnessSession(t *testing.T) (s *testworld.Stack, app *testworld.Peer, session string, seed protocol.Seed) {
	t.Helper()
	harnessID := "author_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	plugin := authorsPlugin(t, harnessID, packSDK(t))
	s = testworld.NewStack(t)
	s.Start()
	app = s.App()
	requireStdout(t, s.Attn("settings", "set", "agent_tools_enabled", "true"), "true")
	if installed := s.Attn("plugin", "install", "--path", plugin); installed.Code != 0 {
		t.Fatalf("attn plugin install: %s%s", installed.Stdout, installed.Stderr)
	}
	agent := fakeagent.Harness(harnessID)
	awaitAgentAvailable(app, agent)
	seed = plant(t, s, "Round the checkout total")
	session = s.Spawn(app, agent, s.Path("shop"))
	app.AwaitScreen(session, "sample harness started conversation")
	return s, app, session, seed
}

func TestTheSampleHarnessCallsAttnsToolsAsItsSession(t *testing.T) {
	t.Parallel()
	s, app, session, seed := startSampleHarnessSession(t)

	app.TypeLine(session, `/tool attn_do {"script":"seed_note({seed:'`+seed.ID+`',text:'Rounded half up.'})"}`)
	app.AwaitScreen(session, "tool attn_do:")
	shown := showSeed(t, s, "", seed.ID)
	if len(shown.Notes) == 0 || shown.Notes[0].Body != "Rounded half up." || stackAuthorSession(shown.Notes[0]) != protocol.SessionID(session) {
		t.Fatalf("the sample harness's tool call did not note as its session %s: %+v", session, shown.Notes)
	}
}

func TestTheSampleHarnessTakesTheNextToolCallAfterATypoInTheArguments(t *testing.T) {
	t.Parallel()
	s, app, session, seed := startSampleHarnessSession(t)

	app.TypeLine(session, `/tool attn_do {"script":`)
	app.AwaitScreen(session, "tool: ")
	app.TypeLine(session, `/tool attn_do {"script":"seed_note({seed:'`+seed.ID+`',text:'Typed again.'})"}`)
	app.AwaitScreen(session, "tool attn_do:")
	shown := showSeed(t, s, "", seed.ID)
	if len(shown.Notes) == 0 || shown.Notes[0].Body != "Typed again." {
		t.Fatalf("the session did not take the tool call typed after the typo: %+v", shown.Notes)
	}
}

func installedSampleServer(t *testing.T, id, tarball string) string {
	t.Helper()
	dir := t.TempDir()
	for _, file := range []string{"attn-plugin.toml", "package.json", "src/index.ts"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "examples", "plugins", "sample-server", file))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		switch file {
		case "package.json":
			text = strings.Replace(text, `"0.8.0"`, `"file:`+tarball+`"`, 1)
		case "src/index.ts":
			text = strings.Replace(text, `const harnessId = "sample-server"`, `const harnessId = "`+id+`"`, 1)
		}
		path := filepath.Join(dir, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestEachConversationOfTheSampleServerCallsAttnsToolsAsItsOwnSession(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t, testworld.WithAgents(fakeagent.SampleServer))
	s.Vars = append(s.Vars, "ATTN_SAMPLE_SERVER_EXECUTABLE="+filepath.Join(s.Dir, "bin", "sample-server"), "SAMPLE_SERVER_HOME="+filepath.Join(s.Dir, "sample-server-home"))
	s.Start()
	app := s.App()
	requireStdout(t, s.Attn("settings", "set", "agent_tools_enabled", "true"), "true")
	id := "tools_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if installed := s.Attn("plugin", "install", "--path", installedSampleServer(t, id, packSDK(t))); installed.Code != 0 {
		t.Fatalf("attn plugin install: %s%s", installed.Stdout, installed.Stderr)
	}
	awaitAgentAvailable(app, fakeagent.Harness(id))
	seed := plant(t, s, "Price the checkout")
	sessions := []string{s.Spawn(app, fakeagent.Harness(id), s.Path("shop")), s.Spawn(app, fakeagent.Harness(id), s.Path("shop"))}
	server := s.AppServer(fakeagent.SampleServer)
	for _, session := range sessions {
		conversation := s.Launched(session).ConversationID
		if used := server.Started(conversation).MCPServers; len(used) != 1 || used[0].Name != "attn" || used[0].Description == "" {
			t.Fatalf("conversation %s started with MCP servers %+v, want attn's by name and description", conversation, used)
		}
		called := server.CallTool(conversation, "attn_do", map[string]any{"script": "seed_note({seed: '" + seed.ID + "', text: 'From " + conversation + "'})"})
		if called.Refused != "" || called.IsError {
			t.Fatalf("conversation %s calling seed_note: refused %q, %s", conversation, called.Refused, called.Text())
		}
		shown := showSeed(t, s, "", seed.ID)
		if len(shown.Notes) == 0 || shown.Notes[0].Body != "From "+conversation || stackAuthorSession(shown.Notes[0]) != protocol.SessionID(session) {
			t.Fatalf("conversation %s did not note as its session %s: %+v", conversation, session, shown.Notes)
		}
		t.Logf("conversation %s noted as session %s", conversation, session)
	}
}
