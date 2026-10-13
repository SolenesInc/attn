package daemon_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/codemode"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
)

const triageProgram = `package main

import (
	"attn"
	"fmt"
	"log"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"
)

func main() {
	titles := []string{"Fix login redirect", "Add request metrics", "Document code mode"}
	planted := make([]attn.Seed, len(titles))
	var g errgroup.Group
	for i, title := range titles {
		g.Go(func() error {
			s, err := attn.Plant(title, attn.PlantOptions{Body: "Found while triaging."})
			if err != nil {
				return fmt.Errorf("plant %q: %w", title, err)
			}
			planted[i] = s
			return attn.AddNote(s.ID, "triaged by code mode")
		})
	}
	if err := g.Wait(); err != nil {
		log.Fatal(err)
	}

	mine, err := attn.Tend(planted[0].ID)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := attn.Park(planted[1].ID, "waiting on metrics design"); err != nil {
		log.Fatal(err)
	}

	open, err := attn.Seeds()
	if err != nil {
		log.Fatal(err)
	}
	var lines []string
	for _, s := range open {
		lines = append(lines, s.State+" "+s.Title)
	}
	slices.Sort(lines)
	fmt.Println(strings.Join(lines, "\n"))
	fmt.Println("tender is me:", mine.Tender == attn.Me())

	_, notes, err := attn.Show(planted[1].ID)
	if err != nil {
		log.Fatal(err)
	}
	for _, n := range notes {
		fmt.Println("note:", n.Body)
	}
	if _, _, err := attn.Show("no-such-seed"); err != nil {
		fmt.Println("missing seed fails")
	}
}
`

func TestCodeModeProgramDrivesTheGardenAsItsSession(t *testing.T) {
	w := newWorld(t, fakeagent.Claude)
	app, cli := w.App(), w.Client()
	agent := spawnPanes(w, app, w.Path("agent"))[0]
	caller := codemode.Caller{Client: cli, Session: protocol.SessionID(agent.session)}

	var stdout, stderr strings.Builder
	stats, err := codemode.Run(context.Background(), caller, "triage.go", triageProgram, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr.String())
	}
	want := `growing Fix login redirect
parked Add request metrics
planted Document code mode
tender is me: true
note: triaged by code mode
note: waiting on metrics design
missing seed fails
`
	if stdout.String() != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", stdout.String(), want)
	}
	t.Logf("fuel %d, memory %d", stats.Fuel, stats.Memory)

	listed, err := cli.SeedList("", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	var states []string
	for _, s := range listed.Seeds {
		states = append(states, s.Status+":"+s.Title)
		if notes := lifeNoteBodies(t, cli, s.ID); !slices.Contains(notes, "triaged by code mode") {
			t.Errorf("%s log = %q", s.Title, notes)
		}
	}
	slices.Sort(states)
	if got := strings.Join(states, ","); got != "dormant:Add request metrics,growing:Fix login redirect,planted:Document code mode" {
		t.Errorf("garden after the program = %s", got)
	}
}
