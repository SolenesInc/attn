package ask_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/protocol"
)

var opened = time.Date(2026, 11, 21, 17, 40, 12, 0, time.UTC)

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
		"a default names its choice in any case and falls due after its duration": {
			":::question\nWhich?\n- [ ] Wait until after lunch\n- [ ] B\nDefault: wait until after lunch after 1h30m\n:::",
			`[{"question":{"choices":[{"label":"Wait until after lunch"},{"label":"B"}],"default":{"choice":"Wait until after lunch","due_at":"2026-11-21T19:10:12Z"},"key":"q1","kind":"one","prompt":"Which?"}}]`,
		},
		"a file question keeps its context": {
			":::question-file\nAttach a screenshot\nOnly if it looks wrong.\n:::",
			`[{"question":{"choices":[],"context":"Only if it looks wrong.","key":"q1","kind":"file","prompt":"Attach a screenshot"}}]`,
		},
		"a question without choices takes a written answer": {
			":::question\nAnything else?\n:::",
			`[{"question":{"choices":[],"key":"q1","kind":"text","prompt":"Anything else?"}}]`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			parts, err := ask.Parse(tc.markdown, opened)
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
		"empty":                 {"  \n", "an ask needs markdown"},
		"unclosed":              {"intro\n:::question\nWhy?\n- [ ] A", "line 2: question block has no closing :::"},
		"unclosed before next":  {":::question\nWhich?\n- [ ] A\n:::question\nAnother?\n- [ ] B\n:::", "line 1: question block has no closing :::"},
		"unknown block":         {"::: question\nWhich?\n- [ ] A\n:::", `line 1: "::: question" opens no question block; use :::question, :::question-multi, :::question-text or :::question-file`},
		"unknown block inside":  {":::question\nWhich?\n- [ ] A\n:::note\n:::", `line 4: ":::note" opens no question block; use :::question, :::question-multi, :::question-text or :::question-file`},
		"stray closer":          {"Intro\n:::", "line 2: ::: closes no question block"},
		"no question":           {":::question\n\n- [ ] A\n:::", "line 1: question block has no question"},
		"text with choices":     {":::question-text\nWhy?\n- [ ] A\n:::", "line 1: :::question-text takes no choices"},
		"file with choices":     {":::question-file\nWhich?\n- [ ] A\n:::", "line 1: :::question-file takes no choices"},
		"file with recommended": {":::question-file\nWhich?\nRecommended: a screenshot\n:::", "line 1: :::question-file takes no Recommended:"},
		"multi without":         {":::question-multi\nWhich?\n:::", "line 1: :::question-multi needs choices"},
		"unknown recommended":   {":::question\nWhich?\n- [ ] A\nRecommended: B\n:::", `line 1: Recommended: names one choice, and "B" is none of them`},
		"two recommended":       {":::question\nWhich?\n- [ ] A\n- [ ] B\nRecommended: A\nRecommended: B\n:::", "line 6: a question takes one Recommended: line"},
		"duplicate choice":      {":::question\nWhich?\n- [ ] A\n- [ ] a\n:::", `line 4: choice "a" appears twice`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ask.Parse(tc.markdown, opened)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestOpeningRefusesAMalformedDefaultAndReadingKeepsItAsContext(t *testing.T) {
	for name, tc := range map[string]struct {
		markdown string
		refusal  string
		read     string
	}{
		"without after": {
			":::question\nWhich?\nPostgres runs already.\nDefault: Postgres\nRedis is faster.\n- [ ] Postgres\n:::",
			`line 4: Default: reads "Default: <choice> after <duration>", such as "Default: Postgres after 2h"`,
			`[{"question":{"choices":[{"label":"Postgres"}],"context":"Postgres runs already.\nDefault: Postgres\nRedis is faster.","key":"q1","kind":"one","prompt":"Which?"}}]`,
		},
		"naming no choice": {
			":::question\nWhich?\n- [ ] A\nDefault: B after 2h\n:::",
			`line 4: Default: names one choice, and "B" is none of them`,
			`[{"question":{"choices":[{"label":"A"}],"context":"Default: B after 2h","key":"q1","kind":"one","prompt":"Which?"}}]`,
		},
		"after no duration": {
			":::question\nWhich?\n- [ ] A\nDefault: A after lunch\n:::",
			`line 4: Default: waits a positive duration such as 30m or 2h, got "lunch"`,
			`[{"question":{"choices":[{"label":"A"}],"context":"Default: A after lunch","key":"q1","kind":"one","prompt":"Which?"}}]`,
		},
		"after no time": {
			":::question\nWhich?\n- [ ] A\nDefault: A after 0s\n:::",
			`line 4: Default: waits a positive duration such as 30m or 2h, got "0s"`,
			`[{"question":{"choices":[{"label":"A"}],"context":"Default: A after 0s","key":"q1","kind":"one","prompt":"Which?"}}]`,
		},
		"on multi": {
			":::question-multi\nWhich?\n- [ ] A\nDefault: A after 2h\n:::",
			"line 4: Default: goes in a :::question with choices, where the user picks one",
			`[{"question":{"choices":[{"label":"A"}],"context":"Default: A after 2h","key":"q1","kind":"any","prompt":"Which?"}}]`,
		},
		"on text": {
			":::question-text\nWhy?\nDefault: A after 2h\n:::",
			"line 3: Default: goes in a :::question with choices, where the user picks one",
			`[{"question":{"choices":[],"context":"Default: A after 2h","key":"q1","kind":"text","prompt":"Why?"}}]`,
		},
		"on file": {
			":::question-file\nA screenshot?\nDefault: A after 2h\n:::",
			"line 3: Default: goes in a :::question with choices, where the user picks one",
			`[{"question":{"choices":[],"context":"Default: A after 2h","key":"q1","kind":"file","prompt":"A screenshot?"}}]`,
		},
		"without choices": {
			":::question\nWhy?\nDefault: A after 2h\n:::",
			"line 3: Default: goes in a :::question with choices, where the user picks one",
			`[{"question":{"choices":[],"context":"Default: A after 2h","key":"q1","kind":"text","prompt":"Why?"}}]`,
		},
		"as the first line": {
			":::question\nDefault: on\n- [ ] A\n:::",
			"line 1: question block has no question",
			`[{"question":{"choices":[{"label":"A"}],"key":"q1","kind":"one","prompt":"Default: on"}}]`,
		},
		"a second in one question": {
			":::question\nWhich?\n- [ ] A\nDefault: A after 1h\nDefault: A after 2h\n:::",
			"line 5: a question takes one Default: line",
			`[{"question":{"choices":[{"label":"A"}],"context":"Default: A after 2h","default":{"choice":"A","due_at":"2026-11-21T18:40:12Z"},"key":"q1","kind":"one","prompt":"Which?"}}]`,
		},
		"a second in another question": {
			":::question\nWhich?\n- [ ] A\nDefault: A after 1h\n:::\n:::question\nAnd?\n- [ ] B\nDefault: B after 1h\n:::",
			"line 6: an ask takes one Default:, and the question on line 1 already has one",
			`[{"question":{"choices":[{"label":"A"}],"default":{"choice":"A","due_at":"2026-11-21T18:40:12Z"},"key":"q1","kind":"one","prompt":"Which?"}},{"question":{"choices":[{"label":"B"}],"context":"Default: B after 1h","key":"q2","kind":"one","prompt":"And?"}}]`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ask.Parse(tc.markdown, opened); err == nil || err.Error() != tc.refusal {
				t.Fatalf("opening: err = %v, want %q", err, tc.refusal)
			}
			parts, err := ask.Read(tc.markdown, opened)
			if err != nil {
				t.Fatalf("reading: %v", err)
			}
			if got, _ := json.Marshal(parts); string(got) != tc.read {
				t.Fatalf("reading gives:\n%s\nwant:\n%s", got, tc.read)
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
			parts, err := ask.Parse(tc.markdown, opened)
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
	parts, err := ask.Parse(":::question\nWhich store?\n- [ ] Postgres\n- [ ] Redis\nRecommended: Postgres\n:::\n"+
		":::question-multi\nWhich platforms?\n- [ ] macOS\n- [ ] Linux, glibc only\n:::\n"+
		":::question-text\nBanner text?\n:::", opened)
	if err != nil {
		t.Fatal(err)
	}
	a := protocol.Ask{ID: "a-1", Subject: "Cache store", Parts: parts}
	for name, tc := range map[string]struct {
		answer protocol.AskAnswer
		index  int
		want   string
	}{
		"the recommended choice, in any case": {protocol.AskAnswer{Key: "q1", Picked: []string{"postgres"}}, 1, "1. Which store?\n\nAnswer: Postgres (the recommended choice)"},
		"another choice":                      {protocol.AskAnswer{Key: "q1", Picked: []string{"Redis"}}, 1, "1. Which store?\n\nAnswer: Redis"},
		"a pick with a condition": {
			protocol.AskAnswer{Key: "q1", Picked: []string{"Postgres"}, Condition: protocol.Ptr(" skip the Windows VM step ")}, 1,
			"1. Which store?\n\nAnswer: Postgres (the recommended choice)\n\nTheir condition: skip the Windows VM step",
		},
		"their own answer": {protocol.AskAnswer{Key: "q1", Picked: []string{}, Other: protocol.Ptr("SQLite")}, 1, "1. Which store?\n\nTheir own answer: SQLite"},
		"several choices and their own": {
			protocol.AskAnswer{Key: "q2", Picked: []string{"Linux, glibc only", "macOS"}, Other: protocol.Ptr("FreeBSD")}, 2,
			"2. Which platforms?\n\nAnswer: \"Linux, glibc only\", \"macOS\"\n\nTheir own answer: FreeBSD",
		},
		"a written answer, quoted": {protocol.AskAnswer{Key: "q3", Picked: []string{}, Text: protocol.Ptr("Welcome back\nfriend")}, 3, "3. Banner text?\n\n> Welcome back\n> friend"},
		"an unanswered question":   {protocol.AskAnswer{Key: "q1", Picked: []string{"Redis"}}, 3, "3. Banner text?\n\nUnanswered."},
	} {
		t.Run(name, func(t *testing.T) {
			text, err := ask.Reply(a, []protocol.AskAnswer{tc.answer}, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			answers, _, _ := strings.Cut(text, "\n\n---\n\n")
			if got := strings.Split(answers, "\n\n## ")[tc.index]; got != tc.want {
				t.Fatalf("entry %d:\n%s\nwant:\n%s", tc.index, got, tc.want)
			}
		})
	}
}

func TestReplyOpensWithTheAskAndEndsWithTheReminder(t *testing.T) {
	for name, tc := range map[string]struct {
		parts   []protocol.AskPart
		answers []protocol.AskAnswer
		note    string
		want    string
	}{
		"answers and a note": {
			[]protocol.AskPart{{Question: &protocol.AskQuestion{Key: "q1", Kind: protocol.AskQuestionKindText, Prompt: "Why?", Choices: []protocol.AskChoice{}}}},
			[]protocol.AskAnswer{{Key: "q1", Picked: []string{}, Text: protocol.Ptr("Because.")}},
			"Ship it.\nToday.",
			"🙋 The user answered the ask \"Heads up\" (a-2).\n\n## 1. Why?\n\n> Because.\n\nThey added:\n\n> Ship it.\n> Today.\n\n---\n\nThey have likely moved on and will not read what you write back here. If the user asks you something in their answer, reply with a new ask that follows this one: the ask tool with `re` set to a-2, or `attn ask --re a-2`. If you still need them, ask again.",
		},
		"answers without a note": {
			[]protocol.AskPart{{Question: &protocol.AskQuestion{Key: "q1", Kind: protocol.AskQuestionKindText, Prompt: "Why?", Choices: []protocol.AskChoice{}}}},
			[]protocol.AskAnswer{{Key: "q1", Picked: []string{}, Text: protocol.Ptr("Because.")}},
			"",
			"🙋 The user answered the ask \"Heads up\" (a-2).\n\n## 1. Why?\n\n> Because.\n\n---\n\nThey have likely moved on and will not read what you write back here. If the user asks you something in their answer, reply with a new ask that follows this one: the ask tool with `re` set to a-2, or `attn ask --re a-2`. If you still need them, ask again.",
		},
		"only a note": {
			[]protocol.AskPart{{Markdown: protocol.Ptr("FYI")}},
			nil,
			"Ship it.\nToday.",
			"🙋 The user answered the ask \"Heads up\" (a-2).\n\nThey added:\n\n> Ship it.\n> Today.\n\n---\n\nThey have likely moved on and will not read what you write back here. If the user asks you something in their answer, reply with a new ask that follows this one: the ask tool with `re` set to a-2, or `attn ask --re a-2`. If you still need them, ask again.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			text, err := ask.Reply(protocol.Ask{ID: "a-2", Subject: "Heads up", Parts: tc.parts}, tc.answers, tc.note, nil)
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
	parts, err := ask.Parse(":::question\nWhich?\n- [ ] A\n- [ ] B\n:::\n:::question-text\nWhy?\n:::\n:::question-file\nA screenshot?\n:::", opened)
	if err != nil {
		t.Fatal(err)
	}
	a := protocol.Ask{ID: "ask-1", Subject: "s", Parts: parts}
	for name, tc := range map[string]struct {
		answers []protocol.AskAnswer
		note    string
		want    string
	}{
		"nothing":                  {nil, " ", "answer at least one question, write a note or attach a file"},
		"unknown key":              {[]protocol.AskAnswer{{Key: "q9", Picked: []string{}}}, "", `the ask has no question "q9"`},
		"unknown choice":           {[]protocol.AskAnswer{{Key: "q1", Picked: []string{"C"}}}, "", `question q1 has no choice "C"`},
		"two for one":              {[]protocol.AskAnswer{{Key: "q1", Picked: []string{"A"}, Other: protocol.Ptr("C")}}, "", "question q1 takes one choice"},
		"pick for text":            {[]protocol.AskAnswer{{Key: "q2", Picked: []string{"A"}}}, "", `question q2 has no choice "A"`},
		"condition without a pick": {[]protocol.AskAnswer{{Key: "q1", Picked: []string{}, Other: protocol.Ptr("C"), Condition: protocol.Ptr("only today")}}, "", "question q1 takes a condition only on a pick"},
		"condition on text":        {[]protocol.AskAnswer{{Key: "q2", Picked: []string{}, Text: protocol.Ptr("x"), Condition: protocol.Ptr("only today")}}, "", "question q2 takes a condition only on a pick"},
		"text for one":             {[]protocol.AskAnswer{{Key: "q1", Picked: []string{}, Text: protocol.Ptr("x")}}, "", "question q1 takes a choice"},
		"files for one":            {[]protocol.AskAnswer{{Key: "q1", Picked: []string{"A"}, Attachments: []string{"f-1"}}}, "", "question q1 takes no files"},
		"files for text":           {[]protocol.AskAnswer{{Key: "q2", Picked: []string{}, Text: protocol.Ptr("x"), Attachments: []string{"f-1"}}}, "", "question q2 takes no files"},
		"text for file":            {[]protocol.AskAnswer{{Key: "q3", Picked: []string{}, Text: protocol.Ptr("x")}}, "", "question q3 takes files"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ask.Reply(a, tc.answers, tc.note, nil)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPlainTakesInlineMarkdownOffALineAndKeepsABlockAsWritten(t *testing.T) {
	cases := []struct{ line, plain string }{
		{"Sign in to *Sigma*", "Sign in to Sigma"},
		{"Open https://auth.openai.com/codex/device and enter the code **Z5RI-IDLV2**, then answer here.", "Open https://auth.openai.com/codex/device and enter the code Z5RI-IDLV2, then answer here."},
		{"Read [the notes](https://example.com/n) and run `attn retry`", "Read the notes and run attn retry"},
		{"See <https://example.com>", "See https://example.com"},
		{"2 \\* 3 is six", "2 * 3 is six"},
		{"Plain words", "Plain words"},
		{"# Not a heading here", "# Not a heading here"},
		{"- a list item", "- a list item"},
		{"", ""},
	}
	for _, c := range cases {
		if got := ask.Plain(c.line); got != c.plain {
			t.Errorf("Plain(%q) = %q, want %q", c.line, got, c.plain)
		}
	}
}
