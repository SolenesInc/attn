package daemon_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type agentToolsCaller struct {
	w        *world
	bearer   string
	headers  map[string]string
	endpoint string
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
	if c.endpoint != "" {
		endpoint = c.endpoint + query
	}
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
	session, view, server := appServerSession(t, w, app, fakeagent.Sigma)
	server.AwaitMCPRegistration("attn", true)
	credential := strings.TrimPrefix(server.MCPHeader("attn", "Authorization"), "Bearer ")

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

func TestAgentToolsAnswerEachPostAloneWithOneJSONBodyAndRefuseAStream(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	seed := plantForTools(t, w)
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	bearer := terminalCredential(t, w, app.Terminal(session))
	send := func(method, body string) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+w.WSAddr+"/mcp", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp, string(data)
	}

	resp, body := send(http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"seed_note","arguments":{"seed":"`+seed+`","text":"Without a handshake."}}}`)
	var reply struct {
		ID     int `json:"id"`
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &reply); err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/json" || reply.ID != 1 || reply.Result.IsError {
		t.Fatalf("a tools/call with no initialize before it answered %d %q with %q (%v), want one JSON reply to id 1", resp.StatusCode, resp.Header.Get("Content-Type"), body, err)
	}
	if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
		t.Errorf("the server named session %q, want no session", id)
	}
	if author, text := newestNoteAuthor(t, w, seed); author != session || text != "Without a handshake." {
		t.Errorf("the seed's newest note is %q by %s, want the call's note by %s", text, author, session)
	}

	if resp, _ := send(http.MethodPost, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); resp.StatusCode != http.StatusAccepted {
		t.Errorf("a notification answered %d, want 202 Accepted", resp.StatusCode)
	}
	if resp, body := send(http.MethodPost, `{"jsonrpc":"2.0","id":"probe","method":"server/discover","params":{}}`); resp.StatusCode != http.StatusOK || !strings.Contains(body, `"code":-32601`) {
		t.Errorf("server/discover answered %d %q, want -32601, which makes Claude Code fall back to initialize", resp.StatusCode, body)
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		if resp, _ := send(method, ""); resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != http.MethodPost {
			t.Errorf("%s answered %d with Allow %q, want 405 and Allow POST", method, resp.StatusCode, resp.Header.Get("Allow"))
		}
	}
}

func TestALaunchedClaudeCallsTheToolsAsItsSessionWithACredentialOnlyItsEnvironmentHolds(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run("direct="+strconv.FormatBool(direct), func(t *testing.T) {
			w := newWorld(t, fakeagent.Claude)
			app := w.App()
			setSetting(t, app, "direct_agent_launch_enabled", strconv.FormatBool(direct))
			seed := plantForTools(t, w)

			without := w.Launched(w.Spawn(app, fakeagent.Claude, w.Path("off")))
			if config, ok := flagValue(without.Argv, "--mcp-config"); ok {
				t.Errorf("with agent_tools_enabled off claude launched with --mcp-config %s, want none", config)
			}

			setSetting(t, app, "agent_tools_enabled", "true")
			session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
			claude := w.Launched(session)
			agent := launchedToolsCaller(t, w, claude)
			for _, arg := range claude.Argv {
				if strings.Contains(arg, agent.bearer) {
					t.Fatalf("claude's argv holds the credential in %q", arg)
				}
			}
			settings, _ := flagValue(claude.Argv, "--settings")
			var parsed struct {
				Permissions struct{ Allow []string } `json:"permissions"`
			}
			if err := json.Unmarshal([]byte(settings), &parsed); err != nil || !slices.Contains(parsed.Permissions.Allow, "mcp__attn__*") {
				t.Errorf("claude launched with --settings allowing %v (%v), want mcp__attn__*", parsed.Permissions.Allow, err)
			}

			agent.mustCall(t, "seed_note", map[string]any{"seed": seed, "text": "From the launched claude."})
			if author, body := newestNoteAuthor(t, w, seed); author != session || body != "From the launched claude." {
				t.Errorf("the seed's newest note is %q by %s, want it by %s", body, author, session)
			}
		})
	}
}

func launchedToolsCaller(t *testing.T, w *world, claude *fakeagent.Run) agentToolsCaller {
	t.Helper()
	raw, ok := flagValue(claude.Argv, "--mcp-config")
	if !ok {
		t.Fatalf("claude launched as %q, want --mcp-config", claude.Argv)
	}
	var config struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("--mcp-config %s: %v", raw, err)
	}
	server, ok := config.MCPServers["attn"]
	if !ok || server.Type != "http" {
		t.Fatalf("--mcp-config %s, want an http server named attn", raw)
	}
	authorization := os.Expand(server.Headers["Authorization"], func(name string) string {
		for _, entry := range claude.Env {
			if key, value, _ := strings.Cut(entry, "="); key == name {
				return value
			}
		}
		t.Fatalf("the Authorization header names $%s, which claude's environment lacks", name)
		return ""
	})
	bearer, ok := strings.CutPrefix(authorization, "Bearer ")
	if !ok || bearer == "" {
		t.Fatalf("the Authorization header expands to %q, want a bearer credential", authorization)
	}
	return agentToolsCaller{w: w, bearer: bearer, endpoint: server.URL}
}

var coreTools = []string{"seed_show", "seed_note", "seed_check_report", "ask", "agent_msg", "delegate"}

func TestTheIntentMapNamesEveryReferenceTheCLIMapNames(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(w.Spawn(app, fakeagent.Claude, w.Path("shop"))))}
	session, err := agent.connect(t, "")
	if err != nil {
		t.Fatal(err)
	}
	bundled := strings.Fields(resultText(agent.mustCall(t, "skill", map[string]any{"list": true})))

	lastCells := func(table string) []string {
		var names []string
		for _, line := range strings.Split(table, "\n") {
			cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
			if !strings.HasPrefix(line, "| ") || len(cells) != 3 || strings.TrimSpace(cells[2]) == "Reference" {
				continue
			}
			for _, name := range strings.Split(cells[2], ",") {
				if name = strings.ReplaceAll(strings.TrimSpace(name), "`", ""); name != "" {
					names = append(names, name)
				}
			}
		}
		return names
	}
	rowReferences := func(list string) []string {
		var names []string
		for _, line := range strings.Split(list, "\n") {
			row, isRow := strings.CutPrefix(line, "- ")
			open := strings.LastIndex(row, " (")
			if !isRow || open < 0 || !strings.HasSuffix(row, ")") {
				continue
			}
			for _, name := range strings.Split(strings.TrimSuffix(row[open+2:], ")"), ",") {
				names = append(names, strings.TrimSpace(name))
			}
		}
		return names
	}
	cliMap := lastCells(resultText(agent.mustCall(t, "skill", map[string]any{})))
	intentMap := rowReferences(session.InitializeResult().Instructions)
	if len(cliMap) == 0 {
		t.Fatal("the CLI map names no reference")
	}
	for _, name := range cliMap {
		if !slices.Contains(intentMap, name) {
			t.Errorf("the CLI map names the reference %q and the intent map does not: %v", name, intentMap)
		}
	}
	for _, name := range intentMap {
		if topic, guide := strings.CutPrefix(name, "guide "); guide {
			if page := agent.mustCall(t, "guide", map[string]any{"topic": topic}); page.IsError {
				t.Errorf("the intent map names guide topic %q, which the guide refuses: %s", topic, resultText(page))
			}
		} else if !slices.Contains(bundled, name) {
			t.Errorf("the intent map names reference %q, which attn does not bundle: %v", name, bundled)
		}
	}
}

func TestEachToolServerTeachesInTheTextItsHarnessShows(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(w.Spawn(app, fakeagent.Claude, w.Path("shop"))))}
	served := map[string]*mcp.InitializeResult{}
	for _, query := range []string{"", "?tools=core", "?tools=rest"} {
		session, err := agent.connect(t, query)
		if err != nil {
			t.Fatalf("%q: %v", query, err)
		}
		served[query] = session.InitializeResult()
		listed, err := session.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range listed.Tools {
			if loaded := tool.Meta["anthropic/alwaysLoad"] == true; loaded != slices.Contains(coreTools, tool.Name) {
				t.Errorf("%q lists %s with alwaysLoad %t; exactly the core tools %v are always loaded", query, tool.Name, loaded, coreTools)
			}
		}
	}

	intentMap := served[""].Instructions
	for _, tool := range coreTools {
		if !strings.Contains(intentMap, tool) {
			t.Errorf("the server's instructions do not name the core tool %q:\n%s", tool, intentMap)
		}
	}
	if len([]rune(intentMap)) > 4096 {
		t.Errorf("the intent map is %d characters; Claude Code keeps 4,096", len([]rune(intentMap)))
	}
	coreMap := served["?tools=core"].Instructions
	for _, paragraph := range strings.Split(intentMap, "\n\n") {
		if !strings.Contains(coreMap, paragraph) {
			t.Errorf("Codex's always-loaded attn-core server leaves out %q of the intent map", paragraph)
		}
	}
	for _, paragraph := range strings.Split(coreMap, "\n\n") {
		if strings.Contains(intentMap, paragraph) {
			continue
		}
		for _, name := range append([]string{"attn-core"}, coreTools...) {
			if !strings.Contains(paragraph, name) {
				t.Errorf("Codex's always-loaded attn-core server says which tools it holds as %q, without %s", paragraph, name)
			}
		}
	}
	if coreMap == intentMap {
		t.Error("Codex's always-loaded attn-core server does not say which tools it holds")
	}
	if rest := served["?tools=rest"].Instructions; rest == "" || strings.Contains(rest, "\n") || len([]rune(rest)) > 250 {
		t.Errorf("Codex's deferred attn server teaches %q; Codex shows a deferred server's first line cut at 250 characters, so it must be the one-line description", rest)
	}
}
