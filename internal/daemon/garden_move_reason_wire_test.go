package daemon_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

var whereILeftIt = "waiting on the payment team. " + strings.Repeat("The retry wraps the charge call and the idempotency key comes from the cart. ", 6)

var movesWithAReason = []struct{ verb, reason, entry string }{
	{"tend", "picking up the retry", "tended: picking up the retry"},
	{"park", whereILeftIt, "parked: " + strings.TrimSpace(whereILeftIt)},
	{"replant", "the payment team is back", "replanted: the payment team is back"},
}

func TestTendParkAndReplantThroughTheCLISaveTheirReasonOnTheMove(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	w.Launched(session)
	seed := plantForTools(t, w)

	for _, move := range movesWithAReason {
		t.Run(move.verb, func(t *testing.T) {
			var moved protocol.SeedTransitionResult
			if err := w.Client().RunCommand("seed "+move.verb, protocol.SessionID(session), map[string]any{"seed": seed, "reason": move.reason}, &moved); err != nil {
				t.Fatalf("seed %s -m through run_command: %v", move.verb, err)
			}
			requireNewestMoveEntry(t, w.Client(), seed, move.entry)
		})
	}
}

func TestTendParkAndReplantAsToolsSaveTheirReasonOnTheMove(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app := w.App()
	setSetting(t, app, "agent_tools_enabled", "true")
	session := w.Spawn(app, fakeagent.Claude, w.Path("shop"))
	agent := agentToolsCaller{w: w, bearer: terminalCredential(t, w, app.Terminal(session))}
	seed := plantForTools(t, w)

	for _, move := range movesWithAReason {
		t.Run(move.verb, func(t *testing.T) {
			if result := agent.mustCall(t, "seed_"+move.verb, map[string]any{"seed": seed, "reason": move.reason}); result.IsError {
				t.Fatalf("seed_%s with a reason was refused: %s", move.verb, resultText(result))
			}
			requireNewestMoveEntry(t, w.Client(), seed, move.entry)
		})
	}
}

func requireNewestMoveEntry(t *testing.T, cli *client.Client, seed, want string) {
	t.Helper()
	notes, err := cli.SeedNotes("", seed, client.SeedNotesQuery{})
	if err != nil {
		t.Fatal(err)
	}
	newest := slices.IndexFunc(notes.Notes, func(note protocol.SeedNote) bool { return note.Kind == "move" })
	if newest < 0 || notes.Notes[newest].Body != want {
		bodies := make([]string, 0, len(notes.Notes))
		for _, note := range notes.Notes {
			bodies = append(bodies, note.Kind+": "+note.Body)
		}
		t.Fatalf("%s's log = %q, want its newest move entry to be %q", seed, bodies, want)
	}
}
