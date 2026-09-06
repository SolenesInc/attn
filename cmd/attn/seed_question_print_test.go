package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func TestSeedQuestionDistinguishesListAndShowText(t *testing.T) {
	seed := protocol.Seed{
		ID: "s-7k3f9m", Title: "Choose the contract", StepSlug: "choose-contract",
		Status: garden.StatusGrowing, CreatedAt: "2026-09-07T10:00:00Z",
		TenderSession: "sess-a",
		Question: &protocol.SeedQuestion{
			ID: "q-aaaaaa", Text: "Which contract wins?", AskedAt: "2026-09-07T10:01:00Z",
			AskedBySession: "sess-a", Status: garden.QuestionOpen,
		},
	}
	if got := seedStatusLabel(seed); got != "growing · waiting on you" {
		t.Fatalf("list status = %q", got)
	}
	var out bytes.Buffer
	fprintSeed(&out, seed)
	text := out.String()
	for _, want := range []string{"waiting on you", "Which contract wins?", "sess-a"} {
		if !strings.Contains(text, want) {
			t.Fatalf("show output misses %q:\n%s", want, text)
		}
	}
}
