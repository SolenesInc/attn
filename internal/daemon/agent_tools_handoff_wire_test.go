package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

func TestTheHandoffToolFilesTheMembersLetterAndEndsItsSession(t *testing.T) {
	w := newCrewWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	day := startCrew(t, w.Client(), "trellis", "")
	w.Launched(string(day.SessionID))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(string(day.SessionID)))}
	shapes := newToolShapes(t, agent, listOfferedTools(t, agent))
	before := len(crewLetters(t, w, "trellis"))

	var handed protocol.CrewHandoffResult
	structuredAs(t, shapes.call("handoff", map[string]any{"letter": "Filed through the tool."}), &handed)
	if protocol.Deref(handed.Outcome) != protocol.CrewDayCloseClosed || handed.Name != "Trellis" {
		t.Errorf("handoff answered %+v, want Trellis's day closed", handed)
	}
	if got := len(crewLetters(t, w, "trellis")); got != before+1 {
		t.Errorf("trellis has %d letters after the handoff, want %d", got, before+1)
	}
	shapes.finish("handoff")
}
