package ask

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

const MaxAnswerBytes = 16 << 10

func Reply(ask protocol.Ask, answers []protocol.AskAnswer, note string) (string, error) {
	questions := Questions(ask.Parts)
	byKey, err := answersByKey(questions, answers)
	if err != nil {
		return "", err
	}
	note = strings.TrimSpace(note)
	if len(byKey) == 0 && note == "" {
		return "", fmt.Errorf("answer at least one question or write a note")
	}
	entries := make([]string, 0, len(questions))
	for i, question := range questions {
		entries = append(entries, renderEntry(i+1, question, byKey[question.Key]))
	}
	text := prompts.RenderText("ask", "answer", prompts.Values{
		"subject": strconv.Quote(ask.Subject),
		"ask_id":  ask.ID,
		"entries": strings.Join(entries, "\n\n"),
		"note":    quote(note),
	})
	if len(text) > MaxAnswerBytes {
		return "", fmt.Errorf("an answer holds at most %d KiB", MaxAnswerBytes>>10)
	}
	return text, nil
}

func answersByKey(questions []protocol.AskQuestion, answers []protocol.AskAnswer) (map[string]protocol.AskAnswer, error) {
	byKey := make(map[string]protocol.AskAnswer, len(answers))
	for _, answer := range answers {
		question, ok := questionByKey(questions, answer.Key)
		if !ok {
			return nil, fmt.Errorf("the ask has no question %q", answer.Key)
		}
		if _, dup := byKey[answer.Key]; dup {
			return nil, fmt.Errorf("question %s is answered twice", answer.Key)
		}
		answer, err := normalizeAnswer(question, answer)
		if err != nil {
			return nil, err
		}
		if answered(answer) {
			byKey[answer.Key] = answer
		}
	}
	return byKey, nil
}

func normalizeAnswer(question protocol.AskQuestion, answer protocol.AskAnswer) (protocol.AskAnswer, error) {
	picked := make([]string, 0, len(answer.Picked))
	for _, label := range answer.Picked {
		canonical, ok := choiceLabel(question.Choices, label)
		if !ok {
			return answer, fmt.Errorf("question %s has no choice %q", question.Key, label)
		}
		picked = append(picked, canonical)
	}
	answer.Picked = picked
	other := strings.TrimSpace(protocol.Deref(answer.Other))
	text := strings.TrimSpace(protocol.Deref(answer.Text))
	switch question.Kind {
	case protocol.AskQuestionKindText:
		if len(picked) > 0 || other != "" {
			return answer, fmt.Errorf("question %s takes a written answer", question.Key)
		}
	case protocol.AskQuestionKindOne:
		if text != "" {
			return answer, fmt.Errorf("question %s takes a choice", question.Key)
		}
		if len(picked)+boolCount(other != "") > 1 {
			return answer, fmt.Errorf("question %s takes one choice", question.Key)
		}
	default:
		if text != "" {
			return answer, fmt.Errorf("question %s takes choices", question.Key)
		}
	}
	answer.Other = optional(other)
	answer.Text = optional(text)
	return answer, nil
}

func renderEntry(index int, question protocol.AskQuestion, answer protocol.AskAnswer) string {
	values := prompts.Values{
		"index":       strconv.Itoa(index),
		"prompt":      question.Prompt,
		"picked":      strings.Join(answer.Picked, ", "),
		"recommended": strconv.FormatBool(recommendedPick(question, answer)),
		"other":       protocol.Deref(answer.Other),
		"text":        quote(protocol.Deref(answer.Text)),
		"unanswered":  strconv.FormatBool(!answered(answer)),
	}
	return prompts.RenderText("ask", "entry", values)
}

func recommendedPick(question protocol.AskQuestion, answer protocol.AskAnswer) bool {
	recommended := protocol.Deref(question.Recommended)
	return recommended != "" && len(answer.Picked) == 1 && answer.Other == nil && answer.Picked[0] == recommended
}

func questionByKey(questions []protocol.AskQuestion, key string) (protocol.AskQuestion, bool) {
	for _, question := range questions {
		if question.Key == key {
			return question, true
		}
	}
	return protocol.AskQuestion{}, false
}

func answered(answer protocol.AskAnswer) bool {
	return len(answer.Picked) > 0 || protocol.Deref(answer.Other) != "" || protocol.Deref(answer.Text) != ""
}

func quote(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight("> "+line, " ")
	}
	return strings.Join(lines, "\n")
}

func optional(text string) *string {
	if text == "" {
		return nil
	}
	return protocol.Ptr(text)
}

func boolCount(b bool) int {
	if b {
		return 1
	}
	return 0
}
