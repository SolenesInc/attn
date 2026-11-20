package daemon_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type agentToolsCaller struct {
	w       *world
	bearer  string
	headers map[string]string
}

func terminalCredential(t *testing.T, w *world, terminal string) string {
	t.Helper()
	return agentCredential(t, w, "t."+terminal)
}

func agentCredential(t *testing.T, w *world, subject string) string {
	t.Helper()
	key, err := os.ReadFile(filepath.Join(w.Dir, "agent-key"))
	if err != nil {
		t.Fatalf("the daemon keeps no agent key: %v", err)
	}
	secret, err := hex.DecodeString(strings.TrimSpace(string(key)))
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(subject))
	return subject + "." + hex.EncodeToString(mac.Sum(nil))
}

func (c agentToolsCaller) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if c.bearer != "" {
		r.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	for name, value := range c.headers {
		r.Header.Set(name, value)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func (c agentToolsCaller) connect(t *testing.T, query string) (*mcp.ClientSession, error) {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "wire-test", Version: "1"}, nil)
	endpoint := "http://" + c.w.WSAddr + "/mcp" + query
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{Transport: c}}, nil)
	if err == nil {
		t.Cleanup(func() { _ = session.Close() })
	}
	return session, err
}

func (c agentToolsCaller) call(t *testing.T, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()
	session, err := c.connect(t, "")
	if err != nil {
		return nil, err
	}
	return session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
}

func (c agentToolsCaller) mustCall(t *testing.T, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := c.call(t, tool, args)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return result
}

func resultText(r *mcp.CallToolResult) string {
	var texts []string
	for _, content := range r.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			texts = append(texts, text.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func newestNoteAuthor(t *testing.T, w *world, seed string) (string, string) {
	t.Helper()
	notes, err := w.Client().SeedNotes("", seed, client.SeedNotesQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(notes.Notes) == 0 {
		t.Fatalf("seed %s has no notes", seed)
	}
	note := notes.Notes[0]
	return strings.TrimPrefix(string(note.Author.Ref), "session:"), note.Body
}

func plantForTools(t *testing.T, w *world) string {
	t.Helper()
	planted, err := w.Client().SeedPlant("", "Fix the flaky checkout test", "Retry the payment call.", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return planted.ID
}

func TestAgentToolsRefuseACallWithoutThisDaemonsCredential(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	terminal := app.Terminal(session)
	genuine := terminalCredential(t, w, terminal)

	for name, bearer := range map[string]string{
		"no bearer":                     "",
		"a forged signature":            "t." + terminal + "." + strings.Repeat("0", 64),
		"another daemon's credential":   "t." + terminal + "." + hex.EncodeToString(hmac.New(sha256.New, []byte("another key")).Sum(nil)),
		"a credential for a made-up id": "t.terminal-nobody-made." + strings.Repeat("ab", 32),
	} {
		_, err := agentToolsCaller{w: w, bearer: bearer}.connect(t, "")
		if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
			t.Errorf("%s: connecting gave %v, want 401 Unauthorized", name, err)
		}
	}
	if _, err := (agentToolsCaller{w: w, bearer: genuine}).connect(t, ""); err != nil {
		t.Fatalf("the terminal's own credential was refused: %v", err)
	}
}

func TestAToolCallActsAsTheSessionTheCredentialsTerminalShows(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	seed := plantForTools(t, w)
	first := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	other := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	claude := w.Launched(first)
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(first))}

	agent.mustCall(t, "seed_note", map[string]any{"seed": seed, "text": "Retries pass."})
	if author, body := newestNoteAuthor(t, w, seed); author != first || body != "Retries pass." {
		t.Errorf("the seed's newest note is %q by %s, want it by %s", body, author, first)
	}

	for _, args := range []map[string]any{
		{"seed": seed, "text": "as the other", "session": other},
		{"seed": seed, "text": "as the other", "session_id": other},
		{"seed": seed, "text": "as the other", "caller_id": app.Terminal(other)},
	} {
		refused := agent.mustCall(t, "seed_note", args)
		if !refused.IsError || !strings.HasPrefix(resultText(refused), "usage: ") {
			t.Errorf("a call naming another session in %v was answered %q, want a usage refusal", args, resultText(refused))
		}
	}
	if author, _ := newestNoteAuthor(t, w, seed); author != first {
		t.Errorf("after the refused calls the newest note is by %s, want still %s", author, first)
	}

	next := clearClaude(app, claude, first)
	agent.mustCall(t, "seed_note", map[string]any{"seed": seed, "text": "After the clear."})
	if author, _ := newestNoteAuthor(t, w, seed); author != string(next.ID) {
		t.Errorf("after /clear the same credential's note is by %s, want %s, the session the terminal shows now", author, next.ID)
	}
}

func TestATerminalsCredentialEndsWithItsTerminalAndOutlivesADaemonRestart(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	seed := plantForTools(t, w)
	kept := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	ended := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	keptTerminal := app.Terminal(kept)
	keptClaude := w.Launched(kept)
	app.TypeLine(kept, "fix the flaky checkout test")
	keptClaude.Prompted()
	keptClaude.Reply("Fixed. <!-- attn:state=idle -->")
	keptCaller := agentToolsCaller{w: w, bearer: terminalCredential(t, w, keptTerminal)}
	endedCaller := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(ended))}
	keptCaller.mustCall(t, "seed_show", map[string]any{"seed": seed})

	app.Send(protocol.KillSessionMessage{Cmd: protocol.CmdKillSession, ID: protocol.SessionID(ended)})
	testworld.Await(app, protocol.EventSessionExited, func(e protocol.SessionExitedMessage) bool { return string(e.SessionID) == ended })
	if _, err := endedCaller.connect(t, ""); err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Errorf("the ended terminal's credential connected with %v, want 401 Unauthorized", err)
	}

	w.restart()
	app = w.App()
	revive(w, app, fakeagent.Claude, kept, w.Path("shop"))
	if got := app.Terminal(kept); got != keptTerminal {
		t.Fatalf("after a restart %s runs in terminal %s, want %s", kept, got, keptTerminal)
	}
	keptCaller.mustCall(t, "seed_note", map[string]any{"seed": seed, "text": "After the restart."})
	if author, _ := newestNoteAuthor(t, w, seed); author != kept {
		t.Errorf("after a restart the credential's note is by %s, want %s", author, kept)
	}
}

func TestEveryAgentToolCallIsRefusedWithTheSettingOff(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	seed := plantForTools(t, w)
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"seed_show", map[string]any{"seed": seed}},
		{"seed_note", map[string]any{"seed": seed, "text": "with the setting off"}},
		{"seed_ready", map[string]any{}},
	} {
		refused := agent.mustCall(t, call.tool, call.args)
		if text := resultText(refused); !refused.IsError || !strings.HasPrefix(text, "setting_off: ") || !strings.Contains(text, "agent_tools_enabled") {
			t.Errorf("%s with the setting off answered %q, want the setting's refusal", call.tool, text)
		}
	}
	if notes, err := w.Client().SeedNotes("", seed, client.SeedNotesQuery{Kind: "note"}); err != nil || len(notes.Notes) != 0 {
		t.Errorf("with the setting off the seed has notes %+v, %v; want only the plant", notes, err)
	}
}

func TestASharedServersCredentialActsAsTheSessionHoldingTheConversationItsHeaderNames(t *testing.T) {
	w := newWorld(t, fakeagent.Sigma)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	seed := plantForTools(t, w)
	session, view, _ := appServerSession(t, w, app, fakeagent.Sigma)
	credential := agentCredential(t, w, "s.sigma")

	agent := agentToolsCaller{w: w, bearer: credential, headers: map[string]string{"Sigma-Root-Conversation": view.ConversationID}}
	agent.mustCall(t, "seed_note", map[string]any{"seed": seed, "text": "From the Sigma conversation."})
	if author, _ := newestNoteAuthor(t, w, seed); author != session {
		t.Errorf("the server's note is by %s, want %s, the session holding conversation %s", author, session, view.ConversationID)
	}

	for name, headers := range map[string]map[string]string{
		"a conversation no session holds": {"Sigma-Root-Conversation": "conversation-nobody-holds"},
		"no conversation header":          {},
	} {
		_, err := agentToolsCaller{w: w, bearer: credential, headers: headers}.connect(t, "")
		if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
			t.Errorf("%s: connecting gave %v, want 401 Unauthorized", name, err)
		}
	}
}
