package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

type twoProfileTools struct {
	w           *world
	side        protocol.Profile
	here, there string
	mine        agentToolsCaller
	theirs      agentToolsCaller
}

func newTwoProfileTools(t *testing.T, settings ...string) twoProfileTools {
	t.Helper()
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	for _, key := range append([]string{"agent_tools_enabled"}, settings...) {
		setSetting(t, app, key, "true")
	}
	side := createProfile(app, "Side")
	here := w.Spawn(app, fakeagent.Claude, w.Path("here"))
	w.Launched(here)
	sideApp := w.AppOn(side.ID)
	there := w.Spawn(sideApp, fakeagent.Claude, w.Path("there"))
	w.Launched(there)
	return twoProfileTools{
		w: w, side: side, here: here, there: there,
		mine:   agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(here))},
		theirs: agentToolsCaller{w: w, bearer: terminalCredential(t, w, sideApp.Terminal(there))},
	}
}

func TestTheVentToolsRecordAndWithdrawTheCallersVent(t *testing.T) {
	s := newSessionTools(t, "analytics.enabled")
	var vent protocol.Vent
	structuredAs(t, s.shapes.call("vent", map[string]any{"about": "attn", "text": "attn seed show printed no bookmark."}), &vent)
	if vent.SessionID != protocol.SessionID(s.session) || vent.Kind != protocol.VentKindAttn {
		t.Errorf("vent recorded %+v, want an attn vent of session %s", vent, s.session)
	}
	var withdrawn protocol.VentWithdrawResult
	structuredAs(t, s.shapes.call("vent_withdraw", map[string]any{"vent": vent.ID}), &withdrawn)
	if withdrawn.ID != vent.ID {
		t.Errorf("vent_withdraw answered %+v, want %s", withdrawn, vent.ID)
	}
	s.shapes.finish("vent")
}

func TestAnAgentCannotWithdrawAVentOfAnotherProfile(t *testing.T) {
	p := newTwoProfileTools(t, "analytics.enabled")
	var vented protocol.Vent
	structuredAs(t, p.theirs.mustCall(t, "vent", map[string]any{"about": "attn", "text": "the side profile's vent"}), &vented)
	if err := p.w.Client().WithdrawVent(protocol.SessionID(p.here), vented.ID); err == nil {
		t.Errorf("vent withdraw of the side profile's vent through run_command succeeded")
	}
	if refused := p.mine.mustCall(t, "vent_withdraw", map[string]any{"vent": vented.ID}); !refused.IsError {
		t.Errorf("vent_withdraw of the side profile's vent through /mcp answered %s, want a refusal", resultText(refused))
	}
}

func TestASecondVentInTheSameTurnNamesTheWithdrawToolToAToolCaller(t *testing.T) {
	s := newSessionTools(t, "analytics.enabled")
	var first protocol.Vent
	structuredAs(t, s.shapes.call("vent", map[string]any{"about": "attn", "text": "attn seed show printed no bookmark."}), &first)
	refused := s.agent.mustCall(t, "vent", map[string]any{"about": "attn", "text": "attn seed note printed nothing."})
	want := "Withdraw it (`vent_withdraw` with {\"vent\": \"" + first.ID + "\"}) and send one vent that covers both"
	if text := resultText(refused); !refused.IsError || !strings.Contains(text, want) {
		t.Errorf("a second vent in the turn answered %q, want a refusal containing %q", text, want)
	}
}
