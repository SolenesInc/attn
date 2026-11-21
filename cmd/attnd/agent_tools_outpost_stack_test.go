package main_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type bearerTransport string

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

func TestAnOutpostSessionsToolCallRunsOnTheHomeAsThatSession(t *testing.T) {
	home, outpost := testworld.NewStack(t, testworld.WithoutAgentKit()), testworld.NewStack(t)
	for _, stack := range []*testworld.Stack{home, outpost} {
		stack.Vars = append(stack.Vars, "SHELL=/bin/bash")
	}
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	requireStdout(t, home.Attn("settings", "set", "agent_tools_enabled", "true"), "true")
	remote := outpost.App()
	pair.Connect()
	testworld.Await(remote, protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return m.Settings["agent_tools_enabled"] == "true" })
	seed := plant(t, home, "Work done on the outpost")
	id := home.Spawn(pair.App, fakeagent.Harness(protocol.SessionAgentShell), outpost.Path("shell"), func(m *protocol.SpawnSessionMessage) { m.Host = &pair.Host })
	terminal := pair.App.Terminal(id)

	key, err := os.ReadFile(filepath.Join(outpost.Dir, "agent-key"))
	if err != nil {
		t.Fatalf("the outpost keeps no agent key of its own: %v", err)
	}
	secret, err := hex.DecodeString(strings.TrimSpace(string(key)))
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("t." + terminal))
	credential := "t." + terminal + "." + hex.EncodeToString(mac.Sum(nil))

	client := mcp.NewClient(&mcp.Implementation{Name: "stack-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: "http://" + outpost.WSAddr + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport(credential)}}, nil)
	if err != nil {
		t.Fatalf("the outpost's /mcp refused its own session's credential: %v", err)
	}
	defer session.Close()
	noted, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "seed_note", Arguments: map[string]any{"seed": seed.ID, "text": "Noted through the outpost's tools"}})
	if err != nil || noted.IsError {
		t.Fatalf("seed_note through the outpost: %+v, %v", noted, err)
	}
	shown := showSeed(t, home, "", seed.ID)
	if len(shown.Notes) == 0 || shown.Notes[0].Body != "Noted through the outpost's tools" || stackAuthorSession(shown.Notes[0]) != protocol.SessionID(id) {
		t.Fatalf("the home's newest note is not the outpost session's: %+v", shown.Notes)
	}

	if err := os.WriteFile(filepath.Join(outpost.Path("shell"), "retry.log"), []byte("3 passes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	attached, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "seed_attach", Arguments: map[string]any{"seed": seed.ID, "path": "retry.log", "mode": "copy"}})
	if err != nil {
		t.Fatal(err)
	}
	if attached.IsError {
		t.Fatalf("seed_attach of retry.log, a file in the outpost session's folder, refused: %s", toolText(attached))
	}
	shown = showSeed(t, home, "", seed.ID)
	if !slices.ContainsFunc(shown.Artifacts, func(a protocol.SeedArtifact) bool { return a.Filename == "retry.log" }) {
		t.Errorf("the home's seed has artifacts %+v, want retry.log copied from the outpost", shown.Artifacts)
	}

	home.Stop()
	refused, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "seed_show", Arguments: map[string]any{"seed": seed.ID}})
	if err != nil {
		t.Fatal(err)
	}
	name, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	text := toolText(refused)
	if !refused.IsError || !strings.Contains(text, name) {
		t.Errorf("seed_show with the home down answered %q, want a refusal naming the home %s", text, name)
	}
	t.Logf("seed_show with the home down: %s", text)
}

func toolText(r *mcp.CallToolResult) string {
	var text string
	for _, content := range r.Content {
		if c, ok := content.(*mcp.TextContent); ok {
			text += c.Text
		}
	}
	return text
}
