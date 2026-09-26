package main_test

import (
	"testing"

	"github.com/victorarias/attn/internal/enrollment"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnOutpostFencesEverySeedCommandAndShowsNoGarden(t *testing.T) {
	t.Parallel()
	const home = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	s := testworld.NewStack(t)
	s.Start()
	if planted := s.Attn("seed", "plant", "planted at home"); planted.Code != 0 {
		t.Fatalf("plant at home exited %d: %s", planted.Code, planted.Stderr)
	}
	s.Stop()
	if enrolled := s.Attn("enrollment", "enroll", "--home", home); enrolled.Code != 0 {
		t.Fatalf("enroll exited %d: %s", enrolled.Code, enrolled.Stderr)
	}
	s.Start()

	for _, command := range [][]string{
		{"seed", "plant", "anything"},
		{"seed", "ls"},
		{"seed", "search", "anything"},
		{"seed", "show", "s-7k3f9m"},
		{"seed", "tend", "s-7k3f9m"},
		{"seed", "note", "s-7k3f9m", "-m", "anything"},
		{"seed", "notes", "s-7k3f9m"},
		{"seed", "link", "s-7k3f9m", "blocks", "s-7k3f9n"},
		{"seed", "ready"},
	} {
		fenced := s.Attn(command...)
		if fenced.Code == 0 {
			t.Errorf("attn %v answered on an outpost: %s", command, fenced.Stdout)
			continue
		}
		requireLines(t, "attn "+command[1]+" on an outpost", fenced.Stderr, garden.Surface, home, "attn enrollment leave", enrollment.PlanPath)
	}

	if seeds := s.App().Initial.Seeds; len(seeds) != 0 {
		t.Errorf("an outpost's initial state carries %d seeds, want none", len(seeds))
	}
}
