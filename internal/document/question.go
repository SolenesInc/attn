package document

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
)

const MaxChoices = 20

const (
	recommendedPrefix = "Recommended:"
	defaultPrefix     = "Default:"
	defaultAfter      = " after "
)

var questionKinds = map[string]protocol.AskQuestionKind{
	":::question":       protocol.AskQuestionKindOne,
	":::question-multi": protocol.AskQuestionKindAny,
	":::question-text":  protocol.AskQuestionKindText,
	":::question-file":  protocol.AskQuestionKindFile,
}

type QuestionRules struct {
	Strict             bool
	EarlierDefaultLine int
}

func Question(part Block, rules QuestionRules) (protocol.AskQuestion, time.Duration, error) {
	body, firstLineNumber, strict := part.Lines, part.Line+1, rules.Strict
	question := protocol.AskQuestion{Kind: questionKinds[part.Opener], Choices: []protocol.AskChoice{}}
	var context []string
	recommended := ""
	fallback, fallbackLine, fallbackAt := "", 0, 0
	var fence Fence
	for offset, line := range body {
		trimmed := strings.TrimSpace(line)
		if fence.Open() {
			fence.Track(line)
			context = append(context, line)
			continue
		}
		if isDirective(trimmed) {
			return question, 0, strayDirective(trimmed, firstLineNumber+offset)
		}
		choice, isChoice := parseChoice(trimmed)
		recommendation, isRecommendation := strings.CutPrefix(trimmed, recommendedPrefix)
		defaulted, isDefault := strings.CutPrefix(trimmed, defaultPrefix)
		if question.Prompt == "" {
			if isChoice || isRecommendation || (strict && isDefault) {
				break
			}
			if trimmed != "" {
				question.Prompt = trimmed
			}
			continue
		}
		if isChoice {
			if len(question.Choices) == MaxChoices {
				return question, 0, &LineError{Line: firstLineNumber + offset, Message: fmt.Sprintf("a question offers at most %d choices", MaxChoices)}
			}
			for _, existing := range question.Choices {
				if strings.EqualFold(existing.Label, choice.Label) {
					return question, 0, &LineError{Line: firstLineNumber + offset, Message: fmt.Sprintf("choice %q appears twice", choice.Label)}
				}
			}
			question.Choices = append(question.Choices, choice)
			continue
		}
		if isRecommendation {
			if recommended != "" {
				return question, 0, &LineError{Line: firstLineNumber + offset, Message: "a question takes one Recommended: line"}
			}
			recommended = strings.TrimSpace(recommendation)
			continue
		}
		if isDefault && fallbackLine != 0 && strict {
			return question, 0, &LineError{Line: firstLineNumber + offset, Message: "a question takes one Default: line"}
		}
		if isDefault && fallbackLine == 0 {
			fallback, fallbackLine, fallbackAt = strings.TrimSpace(defaulted), firstLineNumber+offset, len(context)
			continue
		}
		fence.Track(line)
		context = append(context, line)
	}
	if question.Prompt == "" {
		return question, 0, &LineError{Line: firstLineNumber - 1, Message: "question block has no question"}
	}
	if question.Kind == protocol.AskQuestionKindOne && len(question.Choices) == 0 {
		question.Kind = protocol.AskQuestionKindText
	}
	if question.Kind == protocol.AskQuestionKindText && len(question.Choices) > 0 {
		return question, 0, &LineError{Line: firstLineNumber - 1, Message: ":::question-text takes no choices"}
	}
	if strict && question.Kind == protocol.AskQuestionKindFile && len(question.Choices) > 0 {
		return question, 0, &LineError{Line: firstLineNumber - 1, Message: ":::question-file takes no choices"}
	}
	if strict && question.Kind == protocol.AskQuestionKindFile && recommended != "" {
		return question, 0, &LineError{Line: firstLineNumber - 1, Message: ":::question-file takes no Recommended:"}
	}
	if question.Kind == protocol.AskQuestionKindAny && len(question.Choices) == 0 {
		return question, 0, &LineError{Line: firstLineNumber - 1, Message: ":::question-multi needs choices"}
	}
	if recommended != "" {
		if question.Kind != protocol.AskQuestionKindText {
			label, ok := ChoiceLabel(question.Choices, recommended)
			if !ok {
				return question, 0, &LineError{Line: firstLineNumber - 1, Message: fmt.Sprintf("Recommended: names one choice, and %q is none of them", recommended)}
			}
			recommended = label
		}
		question.Recommended = protocol.Ptr(recommended)
	}
	var after time.Duration
	if fallbackLine != 0 {
		fallen, waits, err := parseDefault(question, fallback, fallbackLine)
		if err == nil && rules.EarlierDefaultLine != 0 {
			err = &LineError{Line: firstLineNumber - 1, Message: fmt.Sprintf("an ask takes one Default:, and the question on line %d already has one", rules.EarlierDefaultLine)}
		}
		switch {
		case err == nil:
			question.Default, after = fallen, waits
		case strict:
			return question, 0, err
		default:
			context = slices.Insert(context, fallbackAt, body[fallbackLine-firstLineNumber])
		}
	}
	if text := strings.Trim(strings.Join(context, "\n"), "\n"); strings.TrimSpace(text) != "" {
		question.Context = protocol.Ptr(text)
	}
	return question, after, nil
}

func parseDefault(question protocol.AskQuestion, line string, lineNumber int) (*protocol.AskDefault, time.Duration, error) {
	refuse := func(format string, args ...any) (*protocol.AskDefault, time.Duration, error) {
		return nil, 0, &LineError{Line: lineNumber, Message: fmt.Sprintf(format, args...)}
	}
	if question.Kind != protocol.AskQuestionKindOne {
		return refuse("Default: goes in a :::question with choices, where the user picks one")
	}
	cut := strings.LastIndex(line, defaultAfter)
	if cut < 0 {
		return refuse("Default: reads \"Default: <choice> after <duration>\", such as \"Default: %s after 2h\"", question.Choices[0].Label)
	}
	named, duration := strings.TrimSpace(line[:cut]), strings.TrimSpace(line[cut+len(defaultAfter):])
	label, ok := ChoiceLabel(question.Choices, named)
	if !ok {
		return refuse("Default: names one choice, and %q is none of them", named)
	}
	after, err := time.ParseDuration(duration)
	if err != nil || after <= 0 {
		return refuse("Default: waits a positive duration such as 30m or 2h, got %q", duration)
	}
	return &protocol.AskDefault{Choice: label}, after, nil
}

func parseChoice(line string) (protocol.AskChoice, bool) {
	rest, ok := cutCheckbox(line)
	if !ok {
		return protocol.AskChoice{}, false
	}
	label, description := rest, ""
	for _, separator := range []string{" — ", " – ", " -- "} {
		if before, after, found := strings.Cut(rest, separator); found {
			label, description = before, after
			break
		}
	}
	label = strings.TrimSpace(label)
	if label == "" {
		return protocol.AskChoice{}, false
	}
	choice := protocol.AskChoice{Label: label}
	if description = strings.TrimSpace(description); description != "" {
		choice.Description = protocol.Ptr(description)
	}
	return choice, true
}

func cutCheckbox(line string) (string, bool) {
	for _, bullet := range []string{"- ", "* "} {
		rest, ok := strings.CutPrefix(line, bullet)
		if !ok {
			continue
		}
		rest = strings.TrimLeft(rest, " ")
		for _, box := range []string{"[ ]", "[x]", "[X]"} {
			if label, ok := strings.CutPrefix(rest, box); ok {
				return label, true
			}
		}
	}
	return "", false
}

func ChoiceLabel(choices []protocol.AskChoice, label string) (string, bool) {
	for _, choice := range choices {
		if strings.EqualFold(choice.Label, strings.TrimSpace(label)) {
			return choice.Label, true
		}
	}
	return "", false
}
