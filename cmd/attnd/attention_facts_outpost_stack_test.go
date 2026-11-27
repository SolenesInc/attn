package main_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

type explainedFacts struct {
	State    string `json:"state"`
	OpenTool *struct {
		Reason  string `json:"reason"`
		OpenFor string `json:"open_for"`
	} `json:"open_tool"`
	Background []struct {
		ID          string `json:"id"`
		Kind        string `json:"kind"`
		Description string `json:"description"`
		Command     string `json:"command"`
		WakesAgent  string `json:"wakes_agent"`
	} `json:"background"`
	LastMessage string `json:"last_message"`
}

func TestTheHomeExplainsAnOutpostAgentWithWhatItsMonitorHolds(t *testing.T) {
	home, outpost := testworld.NewStack(t), testworld.NewStack(t, testworld.WithAgents(fakeagent.Claude))
	pair := testworld.PrepareOutpostPair(t, home, outpost)
	home.Start()
	outpost.Start()
	pair.Connect()
	awaitNotebookState(t, pair, protocol.OutpostNotebookStateInSync)
	app := pair.App
	session := home.Spawn(app, fakeagent.Claude, outpost.Path("shop"), func(msg *protocol.SpawnSessionMessage) { msg.Host = &pair.Host })
	run := outpost.LaunchedCarrying(app.Terminal(session))

	app.TypeLine(session, "deploy to staging")
	run.Prompted()
	run.LaunchBackgroundShell("bdeploy01", "Deploy staging", "./deploy.sh staging")
	run.Reply("The deploy runs in the background; I will report when it ends.")

	app.TypeLine(session, "and run the migration")
	run.Prompted()
	run.AskApproval()
	testworld.AwaitSession(app, session, func(s protocol.Session) bool { return s.State == protocol.SessionStatePendingApproval })

	var explained protocol.StateExplainResult
	home.Attn("state", "explain", session, "--json").JSON(t, &explained)
	encoded, err := json.Marshal(explained.Facts)
	if err != nil {
		t.Fatal(err)
	}
	var facts explainedFacts
	if err := json.Unmarshal(encoded, &facts); err != nil {
		t.Fatalf("facts %s: %v", encoded, err)
	}
	if facts.State != string(protocol.SessionStatePendingApproval) || facts.OpenTool == nil {
		t.Errorf("the home explains %s, want the open approval as the open tool", encoded)
	}
	if len(facts.Background) != 1 || facts.Background[0].ID != "bdeploy01" || facts.Background[0].Kind != "shell" ||
		facts.Background[0].Description != "Deploy staging" || facts.Background[0].Command != "./deploy.sh staging" || facts.Background[0].WakesAgent != "exit" {
		t.Errorf("the home explains background %s, want the deploy shell the outpost's stop listed", encoded)
	}
	if !strings.Contains(facts.LastMessage, "The deploy runs in the background") {
		t.Errorf("the home explains last message %q, want the agent's reply read from the outpost", facts.LastMessage)
	}

	text := home.Attn("state", "explain", session).Stdout
	if !strings.Contains(text, "facts the attention judge reads:") || !strings.Contains(text, `"command": "./deploy.sh staging"`) {
		t.Errorf("state explain does not print the facts:\n%s", text)
	}
}
