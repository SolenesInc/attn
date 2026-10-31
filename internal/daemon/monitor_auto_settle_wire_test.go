package daemon_test

import (
	"testing"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

func TestAutoSettleDropsItsCountdownAcrossARestartAndTheNextUserInputCanArmIt(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		setSetting(t, app, "auto_settle_enabled", "true")
		agent := w.bubbleClaude(t, app, "shop")
		app.TypeLine(agent.id, "pick a colour")
		agent.reply("Which colour? <!-- attn:state=waiting_input -->")
		app.TypeLine(agent.id, "blue")
		w.advance(autoSettleDefaultArm)
		if s := listedState(t, w, agent.id); s.AutoSettleFiresAt == nil || !protocol.Deref(s.TurnOwed) {
			t.Fatalf("before restart: %+v", s)
		}
		w.restart()
		app, agent.cli = w.App(), w.Client()
		w.advance(autoSettleDefaultCountdown + autoSettleDefaultArm)
		if s := listedState(t, w, agent.id); s.AutoSettleFiresAt != nil || !protocol.Deref(s.TurnOwed) {
			t.Fatalf("restart must drop the runtime countdown and keep the owed turn: %+v", s)
		}
		app.TypeLine(agent.id, "keep going")
		w.advance(autoSettleDefaultArm)
		if s := listedState(t, w, agent.id); s.AutoSettleFiresAt == nil {
			t.Fatalf("new user input after restart armed nothing: %+v", s)
		}
		w.advance(autoSettleDefaultCountdown)
		if s := listedState(t, w, agent.id); protocol.Deref(s.TurnOwed) {
			t.Fatalf("new countdown did not settle the turn: %+v", s)
		}
	})
}

func TestEnablingAutoSettleWhileTheSessionWorksArmsItsCreditedRun(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		setSetting(t, app, "auto_settle_enabled", "false")
		agent := w.bubbleClaude(t, app, "shop")
		app.TypeLine(agent.id, "pick a colour")
		agent.reply("Which colour? <!-- attn:state=waiting_input -->")
		app.TypeLine(agent.id, "blue")
		w.advance(autoSettleDefaultArm + autoSettleDefaultCountdown)
		if s := listedState(t, w, agent.id); s.AutoSettleFiresAt != nil || !protocol.Deref(s.TurnOwed) {
			t.Fatalf("disabled auto-settle changed the turn: %+v", s)
		}
		enabledAt := time.Now()
		setSetting(t, app, "auto_settle_enabled", "true")
		w.advance(autoSettleDefaultArm)
		s := listedState(t, w, agent.id)
		if s.AutoSettleFiresAt == nil || !autoSettleFiresAt(t, s).Equal(enabledAt.Add(autoSettleDefaultArm+autoSettleDefaultCountdown)) {
			t.Fatalf("enabling while working gave countdown %q", protocol.Deref(s.AutoSettleFiresAt))
		}
		w.advance(autoSettleDefaultCountdown)
		if s := listedState(t, w, agent.id); protocol.Deref(s.TurnOwed) {
			t.Fatalf("enabled countdown did not settle the turn: %+v", s)
		}
	})
}

func TestANewCountdownLengthReachesASessionAlreadyArmed(t *testing.T) {
	inBubbleWithAgents(t, func(t *testing.T, w *world) {
		app := w.App()
		setSetting(t, app, "auto_settle_enabled", "true")
		agent := w.bubbleClaude(t, app, "shop")
		app.TypeLine(agent.id, "pick a colour")
		agent.reply("Which colour? <!-- attn:state=waiting_input -->")
		armedAt := time.Now()
		app.TypeLine(agent.id, "blue")
		countdown := 40 * time.Second
		setSetting(t, app, "auto_settle_countdown_seconds", "40")
		w.advance(autoSettleDefaultArm)
		s := listedState(t, w, agent.id)
		if want := armedAt.Add(autoSettleDefaultArm + countdown); !autoSettleFiresAt(t, s).Equal(want) {
			t.Fatalf("the countdown armed before the change fires at %q, want %s", protocol.Deref(s.AutoSettleFiresAt), want.UTC().Format(time.RFC3339Nano))
		}
		w.advance(autoSettleDefaultCountdown)
		if s := listedState(t, w, agent.id); !protocol.Deref(s.TurnOwed) {
			t.Fatalf("the old countdown length settled the turn: %+v", s)
		}
		w.advance(countdown - autoSettleDefaultCountdown)
		if s := listedState(t, w, agent.id); protocol.Deref(s.TurnOwed) {
			t.Fatalf("the new countdown did not settle the turn: %+v", s)
		}
	})
}
