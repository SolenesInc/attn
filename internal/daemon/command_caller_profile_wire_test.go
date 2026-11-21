package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAnotherProfilesSessionDoesNotExistForACommandsCaller(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "planter", "neighbour")
		side := createProfile(w.App(), "Side")
		if err := w.InjectSession("stranger", "stranger", w.Path("stranger"), protocol.SessionAgentClaude, side.ID); err != nil {
			t.Fatalf("register stranger: %v", err)
		}
		planted, err := cli.SeedPlant("planter", "Planter's work", "", "", "")
		if err != nil {
			t.Fatalf("plant: %v", err)
		}
		note := func(caller *client.Client, text string) error {
			var result protocol.SeedNoteResult
			return caller.RunCommand("seed note", "planter", map[string]any{"seed": planted.ID, "text": text}, &result)
		}

		stranger := cli.WithCaller(protocol.TerminalID(w.Terminal("stranger")))
		if err := note(stranger, "from another profile"); err == nil || !strings.Contains(err.Error(), "unknown session planter") {
			t.Errorf("an agent in another profile notes as planter = %v, want unknown session planter", err)
		}
		neighbour := cli.WithCaller(protocol.TerminalID(w.Terminal("neighbour")))
		if err := note(neighbour, "from the same profile"); err != nil {
			t.Errorf("an agent in the same profile notes as planter: %v", err)
		}
		if err := note(cli, "from outside attn"); err != nil {
			t.Errorf("a caller with no terminal notes as planter: %v", err)
		}

		notes, err := cli.SeedNotes("planter", planted.ID, client.SeedNotesQuery{})
		if err != nil {
			t.Fatalf("notes: %v", err)
		}
		var bodies []string
		for _, entry := range notes.Notes {
			bodies = append(bodies, entry.Body)
		}
		got := strings.Join(bodies, "|")
		if strings.Contains(got, "from another profile") || !strings.Contains(got, "from the same profile") || !strings.Contains(got, "from outside attn") {
			t.Errorf("planter's seed log = %q, want the same-profile and outside notes and not the other profile's", got)
		}
	})
}
