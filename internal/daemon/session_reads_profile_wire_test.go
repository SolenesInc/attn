package daemon_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

type sessionReads interface {
	run(t *testing.T, name string, args map[string]any, out any) (refusal string)
}

type cliReads struct{ cli *client.Client }

func (r cliReads) run(t *testing.T, name string, args map[string]any, out any) string {
	t.Helper()
	if err := r.cli.RunCommand(name, "", args, out); err != nil {
		if code := client.ErrorCode(err); code != "" {
			return code
		}
		t.Fatalf("%s: %v", name, err)
	}
	return ""
}

type toolReads struct{ agent agentToolsCaller }

func (r toolReads) run(t *testing.T, name string, args map[string]any, out any) string {
	t.Helper()
	result := r.agent.mustCall(t, strings.ReplaceAll(name, " ", "_"), args)
	if result.IsError {
		code, _, _ := strings.Cut(resultText(result), ":")
		return code
	}
	data, err := json.Marshal(result.StructuredContent)
	if err == nil {
		err = json.Unmarshal(data, out)
	}
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return ""
}

func TestSessionReadsAndActivityClearKeepToTheCallersProfile(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	home := app.SelectedProfile()
	mine := w.Spawn(app, fakeagent.Claude, w.Path("mine"))
	private := createProfile(app, "Private")
	otherApp := w.AppOn(private.ID)
	theirs := w.Spawn(otherApp, fakeagent.Claude, w.Path("theirs"))

	callers := map[string]sessionReads{
		"the CLI in the session's terminal": cliReads{w.Client().WithCaller(protocol.TerminalID(app.Terminal(mine)))},
		"the session's agent tools":         toolReads{agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(mine))}},
	}
	for surface, reads := range callers {
		var listed protocol.ListResult
		if refusal := reads.run(t, "list", map[string]any{}, &listed); refusal != "" {
			t.Fatalf("list through %s refused: %s", surface, refusal)
		}
		if len(listed.Sessions) != 1 || string(listed.Sessions[0].ID) != mine || len(listed.Profiles) != 1 || listed.Profiles[0].ID != home {
			t.Errorf("list through %s shows sessions %v and profiles %v; want only %s and its profile %s", surface, sessionIDs(listed.Sessions), profileIDs(listed.Profiles), mine, home)
		}

		var activity protocol.ActivityStatusResult
		if refusal := reads.run(t, "activity", map[string]any{}, &activity); refusal != "" {
			t.Fatalf("activity through %s refused: %s", surface, refusal)
		}
		if len(activity.Sessions) != 1 || string(activity.Sessions[0].ID) != mine {
			t.Errorf("activity through %s shows %+v; want only %s", surface, activity.Sessions, mine)
		}

		var explained protocol.StateExplainResult
		if refusal := reads.run(t, "state explain", map[string]any{"id": theirs}, &explained); refusal != "session_not_found" {
			t.Errorf("state explain of another profile's session through %s answered %q, want session_not_found", surface, refusal)
		}
		if refusal := reads.run(t, "state explain", map[string]any{"id": mine}, &explained); refusal != "" {
			t.Errorf("state explain of the caller's own session through %s refused: %s", surface, refusal)
		}

		var cleared string
		if refusal := reads.run(t, "activity clear", map[string]any{"id": theirs}, &cleared); refusal != "session_not_found" {
			t.Errorf("activity clear of another profile's session through %s answered %q, want session_not_found", surface, refusal)
		}
		if refusal := reads.run(t, "activity clear", map[string]any{"id": mine}, &cleared); refusal != "" {
			t.Errorf("activity clear of the caller's own session through %s refused: %s", surface, refusal)
		}
	}

	outside := cliReads{w.Client()}
	var listed protocol.ListResult
	outside.run(t, "list", map[string]any{}, &listed)
	if len(listed.Sessions) != 2 || len(listed.Profiles) != 2 {
		t.Errorf("list from a shell outside attn shows sessions %v and profiles %v; want both profiles' sessions", sessionIDs(listed.Sessions), profileIDs(listed.Profiles))
	}
	var explained protocol.StateExplainResult
	if refusal := outside.run(t, "state explain", map[string]any{"id": theirs}, &explained); refusal != "" {
		t.Errorf("state explain from a shell outside attn refused: %s", refusal)
	}
	var cleared string
	if refusal := outside.run(t, "activity clear", map[string]any{"id": theirs}, &cleared); refusal != "" {
		t.Errorf("activity clear from a shell outside attn refused: %s", refusal)
	}
}

func sessionIDs(sessions []protocol.Session) []protocol.SessionID {
	ids := make([]protocol.SessionID, 0, len(sessions))
	for _, s := range sessions {
		ids = append(ids, s.ID)
	}
	return ids
}

func profileIDs(profiles []protocol.Profile) []string {
	ids := make([]string, 0, len(profiles))
	for _, p := range profiles {
		ids = append(ids, p.ID)
	}
	return ids
}
