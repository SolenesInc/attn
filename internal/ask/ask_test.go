package ask_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/ask"
	"github.com/victorarias/attn/internal/protocol"
)

func TestParseReadsQuestionBlocksBetweenMarkdown(t *testing.T) {
	parts, err := ask.Parse(strings.Join([]string{
		"Deploys empty the cache.",
		"",
		":::question",
		"Which store should keep the cache?",
		"Postgres runs already.",
		"",
		"- [ ] Postgres — already deployed",
		"- [ ] Redis -- one more service",
		"Recommended: postgres",
		":::",
		"",
		"```mermaid",
		":::question",
		"```",
		":::question-multi",
		"Which platforms?",
		"* [x] macOS",
		"- [ ] Linux",
		":::",
		":::question-text",
		"What should the banner say?",
		"Recommended: Welcome back",
		":::",
		":::question",
		"Anything else?",
		":::",
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.MarshalIndent(parts, "", "  ")
	want := `[
  {
    "markdown": "Deploys empty the cache."
  },
  {
    "question": {
      "choices": [
        {
          "description": "already deployed",
          "label": "Postgres"
        },
        {
          "description": "one more service",
          "label": "Redis"
        }
      ],
      "context": "Postgres runs already.",
      "key": "q1",
      "kind": "one",
      "prompt": "Which store should keep the cache?",
      "recommended": "Postgres"
    }
  },
  {
    "markdown": "` + "```mermaid\\n:::question\\n```" + `"
  },
  {
    "question": {
      "choices": [
        {
          "label": "macOS"
        },
        {
          "label": "Linux"
        }
      ],
      "key": "q2",
      "kind": "any",
      "prompt": "Which platforms?"
    }
  },
  {
    "question": {
      "choices": [],
      "key": "q3",
      "kind": "text",
      "prompt": "What should the banner say?",
      "recommended": "Welcome back"
    }
  },
  {
    "question": {
      "choices": [],
      "key": "q4",
      "kind": "text",
      "prompt": "Anything else?"
    }
  }
]`
	if string(got) != want {
		t.Fatalf("parts:\n%s\nwant:\n%s", got, want)
	}
	if subject := ask.Subject("", parts); subject != "Which store should keep the cache?" {
		t.Fatalf("subject = %q", subject)
	}
}

func TestParseRefusesMalformedAsks(t *testing.T) {
	for name, tc := range map[string]struct {
		markdown string
		want     string
	}{
		"empty":               {"  \n", "an ask needs markdown"},
		"unclosed":            {"intro\n:::question\nWhy?\n- [ ] A", "line 2: question block has no closing :::"},
		"no question":         {":::question\n\n- [ ] A\n:::", "line 1: question block has no question"},
		"text with choices":   {":::question-text\nWhy?\n- [ ] A\n:::", "line 1: :::question-text takes no choices"},
		"multi without":       {":::question-multi\nWhich?\n:::", "line 1: :::question-multi needs choices"},
		"unknown recommended": {":::question\nWhich?\n- [ ] A\nRecommended: B\n:::", `line 1: Recommended: "B" names no choice`},
		"duplicate choice":    {":::question\nWhich?\n- [ ] A\n- [ ] a\n:::", `line 4: choice "a" appears twice`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ask.Parse(tc.markdown)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestReplyRendersEachQuestionForTheAgent(t *testing.T) {
	parts, err := ask.Parse(":::question\nWhich store?\n- [ ] Postgres\n- [ ] Redis\nRecommended: Postgres\n:::\n" +
		":::question-multi\nWhich platforms?\n- [ ] macOS\n- [ ] Linux\n:::\n" +
		":::question-text\nBanner text?\n:::\n" +
		":::question\nRelease day?\n- [ ] Monday\n:::")
	if err != nil {
		t.Fatal(err)
	}
	a := protocol.Ask{ID: "ask-1", Subject: "Cache store", Parts: parts}
	text, err := ask.Reply(a, []protocol.AskAnswer{
		{Key: "q1", Picked: []string{"postgres"}},
		{Key: "q2", Picked: []string{"Linux"}, Other: protocol.Ptr("FreeBSD")},
		{Key: "q3", Picked: []string{}, Text: protocol.Ptr("Welcome back\nfriend")},
	}, "Ship it.")
	if err != nil {
		t.Fatal(err)
	}
	want := `🙋 The user answered your ask "Cache store" (ask-1).

## 1. Which store?

Answer: Postgres (your recommendation)

## 2. Which platforms?

Answer: Linux

Their own answer: FreeBSD

## 3. Banner text?

> Welcome back
> friend

## 4. Release day?

Unanswered.

They added:

> Ship it.`
	if text != want {
		t.Fatalf("reply:\n%s\nwant:\n%s", text, want)
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
	text, err := ask.Reply(protocol.Ask{ID: "ask-2", Subject: "Heads up", Parts: []protocol.AskPart{{Markdown: protocol.Ptr("FYI")}}}, nil, "Thanks")
	if err != nil {
		t.Fatal(err)
	}
	if text != "🙋 The user answered your ask \"Heads up\" (ask-2).\n\nThey added:\n\n> Thanks" {
		t.Fatalf("note-only reply: %q", text)
	}
}
