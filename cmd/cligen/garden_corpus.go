package main

import (
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/victorarias/attn/internal/garden"
)

type stepSlugCase struct {
	Title string `json:"title"`
	Slug  string `json:"slug"`
}

type checkIDCase struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

func stepSlugCases(random *rand.Rand) []any {
	words := []string{"the", "a", "an", "and", "of", "to", "Draw", "GRID", "scroll", "λ", "Über", "x1", "2024", "don't", "co-op", "a_b", "--", "...", "日本語", "🙂", "\t", "\n", "very-long-word-" + strings.Repeat("x", 40)}
	titles := []string{"", " ", "the", "the a an", "Do the thing", "do the THING", "Draw the grid", strings.Repeat("word ", 40)}
	for len(titles) < 500 {
		parts := make([]string, random.IntN(12))
		for i := range parts {
			parts[i] = pick(random, words)
		}
		titles = append(titles, strings.Join(parts, pick(random, []string{" ", "  ", "-", "/", ", "})))
	}
	cases := make([]any, len(titles))
	for i, title := range titles {
		cases[i] = stepSlugCase{Title: title, Slug: garden.StepSlug(title)}
	}
	return cases
}

func checkIDCases(random *rand.Rand) []any {
	ids := []string{"", "C1", "c1", "C0", "C01", "AB12", "A1", "ABCDE1", "C", "1", "C1 ", " C1", "C-1", "Cé1", "C123456789"}
	for len(ids) < 200 {
		ids = append(ids, fmt.Sprintf("%s%d", pick(random, []string{"C", "D", "R", "AB", "c", "", "ABCDEFGHIJ", "É"}), random.IntN(100000)))
	}
	cases := make([]any, len(ids))
	for i, id := range ids {
		problem := ""
		if err := garden.ValidateCheckID(id); err != nil {
			problem = err.Error()
		}
		cases[i] = checkIDCase{ID: id, Error: problem}
	}
	return cases
}
