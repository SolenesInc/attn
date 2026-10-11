package ask_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/protocol"
)

func TestParseReadsQuestionBlocksBetweenMarkdown(t *testing.T) {
	for name, tc := range map[string]struct {
		markdown string
		want     string
	}{
		"prose around a block": {
			"Before.\n\n:::question\nWhich?\n- [ ] A\n:::\n\nAfter.",
			`[{"markdown":"Before."},{"question":{"choices":[{"label":"A"}],"key":"q1","kind":"one","prompt":"Which?"}},{"markdown":"After."}]`,
		},
		"keys count questions across markdown": {
			":::question\r\nOne?\r\n:::\r\nBetween.\r\n:::question\r\nTwo?\r\n:::",
			`[{"question":{"choices":[],"key":"q1","kind":"text","prompt":"One?"}},{"markdown":"Between."},{"question":{"choices":[],"key":"q2","kind":"text","prompt":"Two?"}}]`,
		},
		"an opener in a code fence stays markdown": {
			"```mermaid\n:::question\n```",
			"[{\"markdown\":\"```mermaid\\n:::question\\n```\"}]",
		},
		"a code fence inside a block keeps its :::": {
			":::question\nWhich?\n```\n:::\n```\n- [ ] A\n:::",
			"[{\"question\":{\"choices\":[{\"label\":\"A\"}],\"context\":\"```\\n:::\\n```\",\"key\":\"q1\",\"kind\":\"one\",\"prompt\":\"Which?\"}}]",
		},
		"any bullet and box": {
			":::question-multi\nWhich?\n* [x] A\n- [X] B\n-  [ ] C\n:::",
			`[{"question":{"choices":[{"label":"A"},{"label":"B"},{"label":"C"}],"key":"q1","kind":"any","prompt":"Which?"}}]`,
		},
		"dash separators give a description": {
			":::question\nWhich?\n- [ ] A — why a\n- [ ] B – why b\n- [ ] C -- why c\n:::",
			`[{"question":{"choices":[{"description":"why a","label":"A"},{"description":"why b","label":"B"},{"description":"why c","label":"C"}],"key":"q1","kind":"one","prompt":"Which?"}}]`,
		},
		"lines between the prompt and the choices are context": {
			":::question\nWhich?\nPostgres runs already.\n\n- [ ] A\n:::",
			`[{"question":{"choices":[{"label":"A"}],"context":"Postgres runs already.","key":"q1","kind":"one","prompt":"Which?"}}]`,
		},
		"a recommendation names its choice in any case": {
			":::question\nWhich?\n- [ ] Postgres\nRecommended: postgres\n:::",
			`[{"question":{"choices":[{"label":"Postgres"}],"key":"q1","kind":"one","prompt":"Which?","recommended":"Postgres"}}]`,
		},
		"a written question suggests its recommendation": {
			":::question-text\nBanner?\nRecommended: Welcome back\n:::",
			`[{"question":{"choices":[],"key":"q1","kind":"text","prompt":"Banner?","recommended":"Welcome back"}}]`,
		},
		"a question without choices takes a written answer": {
			":::question\nAnything else?\n:::",
			`[{"question":{"choices":[],"key":"q1","kind":"text","prompt":"Anything else?"}}]`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			parts, err := ask.Parse(tc.markdown)
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := json.Marshal(parts); string(got) != tc.want {
				t.Fatalf("parts:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

func TestParseRefusesMalformedAsks(t *testing.T) {
	for name, tc := range map[string]struct {
		markdown string
		want     string
	}{
		"empty":                {"  \n", "an ask needs markdown"},
		"unclosed":             {"intro\n:::question\nWhy?\n- [ ] A", "line 2: question block has no closing :::"},
		"unclosed before next": {":::question\nWhich?\n- [ ] A\n:::question\nAnother?\n- [ ] B\n:::", "line 1: question block has no closing :::"},
		"unknown block":        {"::: question\nWhich?\n- [ ] A\n:::", `line 1: "::: question" opens no question block; use :::question, :::question-multi or :::question-text`},
		"unknown block inside": {":::question\nWhich?\n- [ ] A\n:::note\n:::", `line 4: ":::note" opens no question block; use :::question, :::question-multi or :::question-text`},
		"stray closer":         {"Intro\n:::", "line 2: ::: closes no question block"},
		"no question":          {":::question\n\n- [ ] A\n:::", "line 1: question block has no question"},
		"text with choices":    {":::question-text\nWhy?\n- [ ] A\n:::", "line 1: :::question-text takes no choices"},
		"multi without":        {":::question-multi\nWhich?\n:::", "line 1: :::question-multi needs choices"},
		"unknown recommended":  {":::question\nWhich?\n- [ ] A\nRecommended: B\n:::", `line 1: Recommended: names one choice, and "B" is none of them`},
		"two recommended":      {":::question\nWhich?\n- [ ] A\n- [ ] B\nRecommended: A\nRecommended: B\n:::", "line 6: a question takes one Recommended: line"},
		"duplicate choice":     {":::question\nWhich?\n- [ ] A\n- [ ] a\n:::", `line 4: choice "a" appears twice`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ask.Parse(tc.markdown)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSubjectFallsBackToTheFirstQuestionThenTheProse(t *testing.T) {
	long := strings.Repeat("a", 130)
	for name, tc := range map[string]struct {
		explicit string
		markdown string
		want     string
	}{
		"the given subject's first line": {"Cache store\nmore", ":::question\nWhich?\n:::", "Cache store"},
		"the first question":             {"", "Intro.\n\n:::question\nWhich store?\n:::", "Which store?"},
		"a long question, cut":           {"", ":::question\n" + long + "\n:::", strings.Repeat("a", 119) + "…"},
		"a heading":                      {"", "# Deploy plan\nMore.", "Deploy plan"},
		"prose after a code fence":       {"", "```go\nx := 1\n```\nThe plan.", "The plan."},
		"nothing but code":               {"", "```\ncode\n```", "Question"},
	} {
		t.Run(name, func(t *testing.T) {
			parts, err := ask.Parse(tc.markdown)
			if err != nil {
				t.Fatal(err)
			}
			if got := ask.Subject(tc.explicit, parts); got != tc.want {
				t.Fatalf("subject = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReplyRendersEachAnswerForTheAgent(t *testing.T) {
	parts, err := ask.Parse(":::question\nWhich store?\n- [ ] Postgres\n- [ ] Redis\nRecommended: Postgres\n:::\n" +
		":::question-multi\nWhich platforms?\n- [ ] macOS\n- [ ] Linux, glibc only\n:::\n" +
		":::question-text\nBanner text?\n:::")
	if err != nil {
		t.Fatal(err)
	}
	a := protocol.Ask{ID: "a-1", Subject: "Cache store", Parts: parts}
	for name, tc := range map[string]struct {
		answer protocol.AskAnswer
		index  int
		want   string
	}{
		"the recommended choice, in any case": {protocol.AskAnswer{Key: "q1", Picked: []string{"postgres"}}, 1, "1. Which store?\n\nAnswer: Postgres (your recommendation)"},
		"another choice":                      {protocol.AskAnswer{Key: "q1", Picked: []string{"Redis"}}, 1, "1. Which store?\n\nAnswer: Redis"},
		"their own answer":                    {protocol.AskAnswer{Key: "q1", Picked: []string{}, Other: protocol.Ptr("SQLite")}, 1, "1. Which store?\n\nTheir own answer: SQLite"},
		"several choices and their own": {
			protocol.AskAnswer{Key: "q2", Picked: []string{"Linux, glibc only", "macOS"}, Other: protocol.Ptr("FreeBSD")}, 2,
			"2. Which platforms?\n\nAnswer: \"Linux, glibc only\", \"macOS\"\n\nTheir own answer: FreeBSD",
		},
		"a written answer, quoted": {protocol.AskAnswer{Key: "q3", Picked: []string{}, Text: protocol.Ptr("Welcome back\nfriend")}, 3, "3. Banner text?\n\n> Welcome back\n> friend"},
		"an unanswered question":   {protocol.AskAnswer{Key: "q1", Picked: []string{"Redis"}}, 3, "3. Banner text?\n\nUnanswered."},
	} {
		t.Run(name, func(t *testing.T) {
			text, err := ask.Reply(a, []protocol.AskAnswer{tc.answer}, "")
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Split(text, "\n\n## ")[tc.index]; got != tc.want {
				t.Fatalf("entry %d:\n%s\nwant:\n%s", tc.index, got, tc.want)
			}
		})
	}
}

func TestReplyOpensWithTheAskAndEndsWithTheNote(t *testing.T) {
	for name, tc := range map[string]struct {
		parts   []protocol.AskPart
		answers []protocol.AskAnswer
		want    string
	}{
		"answers and a note": {
			[]protocol.AskPart{{Question: &protocol.AskQuestion{Key: "q1", Kind: protocol.AskQuestionKindText, Prompt: "Why?", Choices: []protocol.AskChoice{}}}},
			[]protocol.AskAnswer{{Key: "q1", Picked: []string{}, Text: protocol.Ptr("Because.")}},
			"🙋 The user answered your ask \"Heads up\" (a-2).\n\n## 1. Why?\n\n> Because.\n\nThey added:\n\n> Ship it.\n> Today.",
		},
		"only a note": {
			[]protocol.AskPart{{Markdown: protocol.Ptr("FYI")}},
			nil,
			"🙋 The user answered your ask \"Heads up\" (a-2).\n\nThey added:\n\n> Ship it.\n> Today.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			text, err := ask.Reply(protocol.Ask{ID: "a-2", Subject: "Heads up", Parts: tc.parts}, tc.answers, "Ship it.\nToday.")
			if err != nil {
				t.Fatal(err)
			}
			if text != tc.want {
				t.Fatalf("reply:\n%s\nwant:\n%s", text, tc.want)
			}
		})
	}
}

func TestReplyRefusesAnswersTheAskCannotHold(t *testing.T) {
	parts, err := ask.Parse(":::question\nWhich?\n- [ ] A\n- [ ] B\n:::\n:::question-text\nWhy?\n:::")
	if err != nil {
		t.Fatal(err)
	}
	a := protocol.Ask{ID: "ask-1", Subject: "s", Parts: parts}
	for name, tc := range map[string]struct {
		answers []protocol.AskAnswer
		note    string
		want    string
	}{
		"nothing":        {nil, " ", "answer at least one question or write a note"},
		"unknown key":    {[]protocol.AskAnswer{{Key: "q9", Picked: []string{}}}, "", `the ask has no question "q9"`},
		"unknown choice": {[]protocol.AskAnswer{{Key: "q1", Picked: []string{"C"}}}, "", `question q1 has no choice "C"`},
		"two for one":    {[]protocol.AskAnswer{{Key: "q1", Picked: []string{"A"}, Other: protocol.Ptr("C")}}, "", "question q1 takes one choice"},
		"pick for text":  {[]protocol.AskAnswer{{Key: "q2", Picked: []string{"A"}}}, "", `question q2 has no choice "A"`},
		"text for one":   {[]protocol.AskAnswer{{Key: "q1", Picked: []string{}, Text: protocol.Ptr("x")}}, "", "question q1 takes a choice"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ask.Reply(a, tc.answers, tc.note)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
