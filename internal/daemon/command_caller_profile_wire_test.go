package daemon_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/client"
	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/protocol"
)

func TestAnotherProfilesSessionDoesNotExistForACommandsCaller(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "planter", "neighbour")
		side := createProfile(w.App(), "Side")
		if err := w.InjectSession("stranger", "stranger", w.Path("stranger"), "claude", side.ID); err != nil {
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
		if err := note(stranger, "from another profile"); err == nil || !strings.Contains(err.Error(), `the caller "planter" is not a session`) {
			t.Errorf("an agent in another profile notes as planter = %v, want the caller planter refused as no session", err)
		}
		neighbour := cli.WithCaller(protocol.TerminalID(w.Terminal("neighbour")))
		if err := note(neighbour, "from the same profile"); err != nil {
			t.Errorf("an agent in the same profile notes as planter: %v", err)
		}
		if err := note(cli, "from outside attn"); err != nil {
			t.Errorf("a caller with no terminal notes as planter: %v", err)
		}

		sideThread := cli.WithCaller(protocol.TerminalID(harness.ServerCaller{Variable: "CODEX_THREAD_ID", Scope: side.ID, Conversation: "side-thread"}.Terminal()))
		var listed protocol.PullRequestListResult
		for _, named := range []string{"planter", "plant"} {
			if err := sideThread.RunCommand("pr ls", protocol.SessionID(named), command.PRLsArgs{}, &listed); err == nil || !strings.Contains(err.Error(), "unknown session "+named) {
				t.Errorf("a shared Codex conversation in another profile lists the pull requests of --session %s = %v, want a refusal saying %s is not a session here", named, err, named)
			}
		}
		for _, named := range []string{"stranger", "strang"} {
			if err := sideThread.RunCommand("pr ls", protocol.SessionID(named), command.PRLsArgs{}, &listed); err != nil || listed.SessionID != "stranger" {
				t.Errorf("a shared Codex conversation lists --session %s of its own profile = %+v, %v, want stranger's", named, listed, err)
			}
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

func TestAFramedSessionIsAKnownSessionOfTheAskingProfile(t *testing.T) {
	inBubble(t, func(t *testing.T, w *world) {
		cli := w.Client()
		registerSessions(t, w, cli, "planter", "neighbour", "closer")
		side := createProfile(w.App(), "Side")
		if err := w.InjectSession("stranger", "stranger", w.Path("stranger"), "claude", side.ID); err != nil {
			t.Fatalf("register stranger: %v", err)
		}
		if err := cli.Unregister("closer"); err != nil {
			t.Fatalf("close closer: %v", err)
		}
		planter := cli.WithCaller(protocol.TerminalID(w.Terminal("planter")))
		run := func(session protocol.SessionID, name string, args map[string]any) (string, error) {
			var result any
			err := planter.RunCommand(name, session, args, &result)
			return fmt.Sprint(result), err
		}

		for name, args := range map[string]map[string]any{"agent list": {}, "session list": {"all": true}} {
			listed, err := run("closer", name, args)
			if err != nil || strings.Contains(listed, "stranger") || !strings.Contains(listed, "neighbour") {
				t.Errorf("%s as a closed session of the profile = %v, %v, want neighbour and not stranger", name, listed, err)
			}
		}
		if _, err := run("closer", "agent peek", map[string]any{"target": "stranger"}); client.ErrorCode(err) != "session_not_found" {
			t.Errorf("peek of another profile's session as a closed session of the profile = %v, want session_not_found", err)
		}

		if _, err := run("no-such-session", "agent list", map[string]any{}); err == nil || !strings.Contains(err.Error(), `the caller "no-such-session" is not a session`) {
			t.Errorf("agent list as a session attn does not know = %v, want the caller refused as no session", err)
		}

		calls := []struct {
			name string
			args map[string]any
			role string
		}{
			{"agent list", map[string]any{}, "caller"},
			{"agent peek", map[string]any{"target": "neighbour"}, "caller"},
			{"agent inbox", map[string]any{}, "caller"},
			{"agent msg", map[string]any{"target": "neighbour", "text": "as a stranger"}, "sender"},
			{"agent close", map[string]any{"target": "neighbour", "reason": "as a stranger"}, "caller"},
			{"session list", map[string]any{"all": true}, "caller"},
		}
		for _, call := range calls {
			want := fmt.Sprintf(`the %s "str" is not a session`, call.role)
			if got, err := run("str", call.name, call.args); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s as a prefix of another profile's session = %v, %v, want %s", call.name, got, err, want)
			}
		}

		for _, selector := range []string{"Side", side.ID} {
			outside := cli.WithGardenProfile(selector, "")
			var peeked any
			if err := outside.RunCommand("agent peek", "", map[string]any{"target": "stranger"}, &peeked); err != nil {
				t.Errorf("peek of a Side session from outside attn with --profile %s: %v", selector, err)
			}
			var listed any
			if err := outside.RunCommand("agent list", "", map[string]any{}, &listed); err != nil || !strings.Contains(fmt.Sprint(listed), "stranger") || strings.Contains(fmt.Sprint(listed), "neighbour") {
				t.Errorf("agent list from outside attn with --profile %s = %v, %v, want stranger and not neighbour", selector, listed, err)
			}
		}
		var listed any
		if err := cli.WithGardenProfile("Nowhere", "").RunCommand("agent list", "", map[string]any{}, &listed); err == nil || !strings.Contains(err.Error(), "choose --profile") {
			t.Errorf("agent list from outside attn with an unknown --profile = %v, %v, want it refused", listed, err)
		}
	})
}
