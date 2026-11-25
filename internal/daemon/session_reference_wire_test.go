package daemon_test

import (
	"maps"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASessionIsNamedByItsShortIDOrAUniquePrefixOfItsIDLiveOrClosed(t *testing.T) {
	const (
		live          = "3f9a2c4e-8b1d-4c6e-9a7f-2d5e8c1b4a6f"
		closed        = "71c0d9ab-0f42-4e0b-8d0e-5a9f1c3b7e22"
		closedTwin    = "71c0d9ab-9d77-4b53-a1c4-0e6b2f8d4c19"
		closedSingle  = "c5e81f07-4a36-4f8f-b2d9-7701aa5c9e3d"
		liveTwin      = "a4b6d210-1c5e-4d27-8e3f-6b90c2d7f148"
		liveTwin2     = "a4b6d210-7e29-4a61-9f0c-3d58e1b4a7c6"
		shadowed      = "e07b3c95-2d64-4f18-a5c7-94f1b8e60d2a"
		shadowing     = "e07b3c95-8a13-4b7e-b6d0-17c5f2e94a88"
		stranger      = "9d2e4f61-5b08-4c3a-87e9-f1a60d7c2b54"
		strangerClose = "58f1a7c3-6e90-4d2b-9c14-0b7e3f5a8d61"
		sevenHex      = "c5e81f0b-2d1a-4c7e-9f30-84b6e7a1d5c2"
		sevenHexLive  = "3f9a2c40-6b7d-4e19-a3c8-1f5d02e9b764"
	)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		app := w.App()
		registerSessions(t, w, cli, live, closed, closedTwin, closedSingle, liveTwin, liveTwin2, shadowed, shadowing, sevenHex, sevenHexLive)
		side := createProfile(w.App(), "Side")
		for _, id := range []string{stranger, strangerClose} {
			if err := w.InjectSession(id, id, w.Path(id), "claude", side.ID); err != nil {
				t.Fatalf("register %s: %v", id, err)
			}
		}
		for _, id := range []string{closed, closedTwin, closedSingle, shadowed, sevenHex, strangerClose} {
			if err := cli.Unregister(protocol.SessionID(id)); err != nil {
				t.Fatalf("close %s: %v", id, err)
			}
		}
		agent := cli.WithCaller(protocol.TerminalID(w.Terminal(live)))
		run := func(name string, args map[string]any) (string, error) {
			var shown protocol.SessionShowResult
			err := agent.RunCommand(name, "", args, &shown)
			return string(shown.Entry.ID), err
		}
		for _, named := range []struct{ ref, want string }{
			{"3f9a2c4e", live},
			{"3f9a2c4e-8b1d", live},
			{live, live},
			{"ag-7yd2rk", live},
			{"ag-rqm1y1", closedSingle},
			{"ag-w1xks5", shadowing},
			{"c5e81f07", closedSingle},
			{closedSingle, closedSingle},
			{"71c0d9ab-0", closed},
			{"e07b3c95", shadowing},
			{"e07b3c95-2", shadowed},
		} {
			if got, err := run("session show", map[string]any{"id": named.ref}); err != nil || got != named.want {
				t.Errorf("session show %s = %s, %v, want %s", named.ref, got, err, named.want)
			}
		}

		refuses := func(code string, calls map[string]map[string]any) {
			t.Helper()
			for name, args := range calls {
				var answer any
				if err := agent.RunCommand(name, "", args, &answer); client.ErrorCode(err) != code {
					t.Errorf("%s %v = %v, want %s", name, args, err, code)
				}
			}
		}
		for _, ref := range []string{"71c0d9ab", "a4b6d210", "ag-e70dka", "ag-mjvd44"} {
			_, err := run("session show", map[string]any{"id": ref})
			if client.ErrorCode(err) != "ambiguous_session" || !strings.Contains(err.Error(), "give more of the full id") {
				t.Errorf("session show %s = %v, want ambiguous_session asking for more of the full id", ref, err)
			}
		}
		refuses("ambiguous_session", map[string]map[string]any{
			"session transcript": {"id": "71c0d9ab"},
			"session reopen":     {"id": "71c0d9ab"},
			"session list":       {"before": "71c0d9ab", "all": true},
			"state explain":      {"id": "a4b6d210"},
			"activity clear":     {"id": "a4b6d210"},
			"session rename":     {"name": "taken", "target": "a4b6d210"},
			"session priority":   {"priority": "on", "target": "a4b6d210"},
		})
		for _, foreign := range []string{"9d2e4f61", "58f1a7c3", "ag-kmq4yr", "ag-b3rtfg", "ag-zzzzzz", "ag-7yd2"} {
			refuses("session_not_found", map[string]map[string]any{
				"session show":       {"id": foreign},
				"session transcript": {"id": foreign},
				"session reopen":     {"id": foreign},
			})
		}

		resolved := testworld.Request(app, protocol.ResolveSessionsMessage{
			Cmd: protocol.CmdResolveSessions, RequestID: protocol.Ptr("resolve"),
			Refs: []string{"ag-rqm1y1", closed, "c5e81f07", "ag-7yd2rk", live, "ag-e70dka", "71c0d9ab", strangerClose, "ag-b3rtfg", "ag-zzzzzz", "00000000-0000-4000-8000-000000000000"},
		}, protocol.EventResolveSessionsResult, func(r protocol.ResolveSessionsResultMessage) bool { return r.RequestID == "resolve" })
		found := map[string]string{}
		for _, r := range resolved.Found {
			found[r.Ref] = string(r.Closed.ID)
		}
		if want := map[string]string{"ag-rqm1y1": closedSingle, closed: closed, "c5e81f07": closedSingle}; !resolved.Success || !maps.Equal(found, want) {
			t.Errorf("resolve_sessions = %v (success %v), want only the closed sessions of the app's profile that one reference names, %v", found, resolved.Success, want)
		}

		var renamed string
		if err := agent.RunCommand("session rename", "", map[string]any{"name": "Short named", "target": "3f9a2c4e"}, &renamed); err != nil {
			t.Errorf("rename by prefix: %v", err)
		}
		if shown, err := cli.SessionShow("3f9a2c4e"); err != nil || shown.Entry.Label != "Short named" || string(shown.Entry.ID) != live {
			t.Errorf("after the rename, show by prefix = %+v, %v, want %s renamed", shown, err, live)
		}
		if err := agent.RunCommand("session rename", "", map[string]any{"name": "Too late", "target": "c5e81f07"}, &renamed); err == nil || !strings.Contains(err.Error(), "unknown live session c5e81f07") {
			t.Errorf("rename of a closed session by prefix = %v, want it refused as no live session", err)
		}
		var explained protocol.StateExplainResult
		if err := agent.RunCommand("state explain", "", map[string]any{"id": "3f9a2c4e"}, &explained); err != nil || string(explained.SessionID) != live {
			t.Errorf("state explain by prefix = %+v, %v, want %s", explained, err, live)
		}
		var snoozed protocol.SessionSnoozeResult
		if err := agent.RunCommand("session snooze", "", map[string]any{"ids": []string{"3f9a2c4e"}, "until": "1h"}, &snoozed); err != nil || len(snoozed.Outcomes) != 1 || string(snoozed.Outcomes[0].SessionID) != live || snoozed.Outcomes[0].Error != nil {
			t.Errorf("snooze by prefix = %+v, %v, want %s snoozed", snoozed, err, live)
		}
	})
}

func TestALiveSessionIsAddressedByItsNameAndACallerIsNot(t *testing.T) {
	const (
		caller   = "3f9a2c4e-8b1d-4c6e-9a7f-2d5e8c1b4a6f"
		review   = "71c0d9ab-0f42-4e0b-8d0e-5a9f1c3b7e22"
		twin     = "a4b6d210-1c5e-4d27-8e3f-6b90c2d7f148"
		twinTwo  = "b4b6d210-7e29-4a61-9f0c-3d58e1b4a7c6"
		gone     = "c5e81f07-4a36-4f8f-b2d9-7701aa5c9e3d"
		shadowed = "e07b3c95-2d64-4f18-a5c7-94f1b8e60d2a"
		prefix   = "9d2e4f61-5b08-4c3a-87e9-f1a60d7c2b54"
		stranger = "58f1a7c3-6e90-4d2b-9c14-0b7e3f5a8d61"
	)
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		for _, s := range []struct{ id, name string }{
			{caller, "caller"}, {review, "api-review"}, {twin, "Twin"}, {twinTwo, "twin"},
			{gone, "gone"}, {prefix, "prefix"}, {shadowed, "9d2e"},
		} {
			if err := w.InjectSession(s.id, s.name, w.Path(s.id), "claude"); err != nil {
				t.Fatalf("register %s: %v", s.name, err)
			}
		}
		side := createProfile(w.App(), "Side")
		if err := w.InjectSession(stranger, "API-Review", w.Path(stranger), "claude", side.ID); err != nil {
			t.Fatal(err)
		}
		if err := cli.Unregister(protocol.SessionID(gone)); err != nil {
			t.Fatal(err)
		}
		agent := cli.WithCaller(protocol.TerminalID(w.Terminal(caller)))

		for _, named := range []struct{ ref, want string }{{"api-review", review}, {" API-Review ", review}, {"9d2e", prefix}, {review, review}} {
			var shown protocol.SessionShowResult
			if err := agent.RunCommand("session show", "", map[string]any{"id": named.ref}, &shown); err != nil || string(shown.Entry.ID) != named.want {
				t.Errorf("session show %q = %s, %v, want %s", named.ref, shown.Entry.ID, err, named.want)
			}
		}
		var explained protocol.StateExplainResult
		if err := agent.RunCommand("state explain", "", map[string]any{"id": "api-review"}, &explained); err != nil || string(explained.SessionID) != review {
			t.Errorf("state explain by name = %+v, %v", explained, err)
		}
		peeked, err := agent.AgentPeek("api-review")
		if err != nil || string(peeked.SessionID) != review {
			t.Errorf("peek by name = %+v, %v", peeked, err)
		}
		if sent, err := agent.AgentMsg("api-review", "", "api-v2 is ready"); err != nil || string(sent.TargetSessionID) != review {
			t.Errorf("message by name = %+v, %v", sent, err)
		}
		var done any
		for name, args := range map[string]map[string]any{
			"session priority": {"priority": "on", "target": "api-review"},
			"activity clear":   {"id": "api-review"},
			"session rename":   {"name": "api-review", "target": "api-review"},
		} {
			if err := agent.RunCommand(name, "", args, &done); err != nil {
				t.Errorf("%s by name: %v", name, err)
			}
		}
		if _, err := agent.AgentClose("api-review", "", "done"); err == nil || !strings.Contains(err.Error(), "outside "+caller+"'s chain") {
			t.Errorf("close of an agent outside the caller's chain by name = %v, want it refused as that rule", err)
		}
		reloaded, err := agent.AgentReload([]string{"api-review"}, "", false)
		if err != nil || len(reloaded.Outcomes) != 1 || string(protocol.Deref(reloaded.Outcomes[0].SessionID)) != review {
			t.Errorf("reload by name = %+v, %v", reloaded, err)
		}

		for name, args := range map[string]map[string]any{
			"session show":  {"id": "twin"},
			"state explain": {"id": "twin"},
			"agent peek":    {"target": "twin"},
			"agent msg":     {"target": "twin", "text": "hello"},
			"agent close":   {"target": "twin", "reason": "done"},
		} {
			err := agent.RunCommand(name, "", args, &done)
			if client.ErrorCode(err) != "ambiguous_session" || !strings.Contains(err.Error(), "ag-mjvd44 \"Twin\"") || !strings.Contains(err.Error(), "\"twin\" (claude") {
				t.Errorf("%s twin = %v, want ambiguous_session naming both sessions", name, err)
			}
		}
		for name, args := range map[string]map[string]any{
			"session show":   {"id": "gone"},
			"session reopen": {"id": "api-review"},
			"agent peek":     {"target": "api"},
		} {
			if err := agent.RunCommand(name, "", args, &done); client.ErrorCode(err) != "session_not_found" {
				t.Errorf("%s %v = %v, want session_not_found", name, args, err)
			}
		}
		if _, err := agent.CrewCreate("Keel", "claude"); err != nil {
			t.Fatalf("create a member: %v", err)
		}
		if err := w.InjectSession("6b3f0d85-91e7-4c2a-b4d8-5e07a9c1f362", "keel", w.Path("keel"), "claude"); err != nil {
			t.Fatalf("register a session named like the member: %v", err)
		}
		if err := agent.RunCommand("terminal read", "", map[string]any{"terminal": "keel"}, &done); err == nil || !strings.Contains(err.Error(), "terminal \"keel\" not found") {
			t.Errorf("terminal read of a member between sessions = %v, want no terminal found", err)
		}
		if _, err := cli.AgentMsg("caller", "api-review", "who am I"); client.ErrorCode(err) != "session_not_found" {
			t.Errorf("a name as the sender = %v, want session_not_found", err)
		}
	})
}
