package ask

import (
	"crypto/rand"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

const (
	MaxMarkdownBytes = 64 << 10
	MaxQuestions     = 12
	MaxChoices       = 20
	maxSubjectRunes  = 120
)

const recommendedPrefix = "Recommended:"

var openers = map[string]protocol.AskQuestionKind{
	":::question":       protocol.AskQuestionKindOne,
	":::question-multi": protocol.AskQuestionKindAny,
	":::question-text":  protocol.AskQuestionKindText,
}

type LineError struct {
	Line    int
	Message string
}

func (e *LineError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Message) }

func Parse(markdown string) ([]protocol.AskPart, error) {
	if len(markdown) > MaxMarkdownBytes {
		return nil, fmt.Errorf("an ask holds at most %d KiB of markdown", MaxMarkdownBytes>>10)
	}
	if strings.TrimSpace(markdown) == "" {
		return nil, fmt.Errorf("an ask needs markdown")
	}
	var parts []protocol.AskPart
	var prose []string
	flushProse := func() {
		if text := strings.Trim(strings.Join(prose, "\n"), "\n"); strings.TrimSpace(text) != "" {
			parts = append(parts, protocol.AskPart{Markdown: protocol.Ptr(text)})
		}
		prose = nil
	}
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	var fence codeFence
	questions := 0
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		kind, opens := openers[trimmed]
		if !fence.open() && !opens && isDirective(trimmed) {
			return nil, strayDirective(trimmed, i+1)
		}
		if fence.open() || !opens {
			fence.track(line)
			prose = append(prose, line)
			continue
		}
		end, err := blockEnd(lines, i)
		if err != nil {
			return nil, err
		}
		questions++
		if questions > MaxQuestions {
			return nil, &LineError{Line: i + 1, Message: fmt.Sprintf("an ask holds at most %d questions", MaxQuestions)}
		}
		question, err := parseQuestion(kind, lines[i+1:end], i+2)
		if err != nil {
			return nil, err
		}
		question.Key = fmt.Sprintf("q%d", questions)
		flushProse()
		parts = append(parts, protocol.AskPart{Question: &question})
		i = end
	}
	flushProse()
	return parts, nil
}

func Questions(parts []protocol.AskPart) []protocol.AskQuestion {
	var out []protocol.AskQuestion
	for _, part := range parts {
		if part.Question != nil {
			out = append(out, *part.Question)
		}
	}
	return out
}

func Subject(explicit string, parts []protocol.AskPart) string {
	if subject := firstLine(explicit); subject != "" {
		return subject
	}
	if questions := Questions(parts); len(questions) > 0 {
		return truncate(questions[0].Prompt, maxSubjectRunes)
	}
	for _, part := range parts {
		if part.Markdown == nil {
			continue
		}
		var fence codeFence
		for _, line := range strings.Split(*part.Markdown, "\n") {
			inCode := fence.open()
			fence.track(line)
			if inCode || fence.open() {
				continue
			}
			if text := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#>*- ")); text != "" {
				return truncate(text, maxSubjectRunes)
			}
		}
	}
	return "Question"
}

func blockEnd(lines []string, open int) (int, error) {
	var fence codeFence
	for j := open + 1; j < len(lines); j++ {
		if !fence.open() {
			trimmed := strings.TrimSpace(lines[j])
			if trimmed == ":::" {
				return j, nil
			}
			if _, opens := openers[trimmed]; opens {
				break
			}
		}
		fence.track(lines[j])
	}
	return 0, &LineError{Line: open + 1, Message: "question block has no closing :::"}
}

func isDirective(trimmed string) bool {
	return strings.HasPrefix(trimmed, ":::")
}

func strayDirective(directive string, line int) *LineError {
	if directive == ":::" {
		return &LineError{Line: line, Message: "::: closes no question block"}
	}
	return &LineError{Line: line, Message: fmt.Sprintf("%q opens no question block; use :::question, :::question-multi or :::question-text", directive)}
}

func parseQuestion(kind protocol.AskQuestionKind, body []string, firstLineNumber int) (protocol.AskQuestion, error) {
	question := protocol.AskQuestion{Kind: kind, Choices: []protocol.AskChoice{}}
	var context []string
	recommended := ""
	var fence codeFence
	for offset, line := range body {
		trimmed := strings.TrimSpace(line)
		if fence.open() {
			fence.track(line)
			context = append(context, line)
			continue
		}
		if isDirective(trimmed) {
			return question, strayDirective(trimmed, firstLineNumber+offset)
		}
		choice, isChoice := parseChoice(trimmed)
		recommendation, isRecommendation := strings.CutPrefix(trimmed, recommendedPrefix)
		if question.Prompt == "" {
			if isChoice || isRecommendation {
				break
			}
			if trimmed != "" {
				question.Prompt = trimmed
			}
			continue
		}
		if isChoice {
			if len(question.Choices) == MaxChoices {
				return question, &LineError{Line: firstLineNumber + offset, Message: fmt.Sprintf("a question offers at most %d choices", MaxChoices)}
			}
			for _, existing := range question.Choices {
				if strings.EqualFold(existing.Label, choice.Label) {
					return question, &LineError{Line: firstLineNumber + offset, Message: fmt.Sprintf("choice %q appears twice", choice.Label)}
				}
			}
			question.Choices = append(question.Choices, choice)
			continue
		}
		if isRecommendation {
			if recommended != "" {
				return question, &LineError{Line: firstLineNumber + offset, Message: "a question takes one Recommended: line"}
			}
			recommended = strings.TrimSpace(recommendation)
			continue
		}
		fence.track(line)
		context = append(context, line)
	}
	if question.Prompt == "" {
		return question, &LineError{Line: firstLineNumber - 1, Message: "question block has no question"}
	}
	if question.Kind == protocol.AskQuestionKindOne && len(question.Choices) == 0 {
		question.Kind = protocol.AskQuestionKindText
	}
	if question.Kind == protocol.AskQuestionKindText && len(question.Choices) > 0 {
		return question, &LineError{Line: firstLineNumber - 1, Message: ":::question-text takes no choices"}
	}
	if question.Kind == protocol.AskQuestionKindAny && len(question.Choices) == 0 {
		return question, &LineError{Line: firstLineNumber - 1, Message: ":::question-multi needs choices"}
	}
	if recommended != "" {
		if question.Kind != protocol.AskQuestionKindText {
			label, ok := choiceLabel(question.Choices, recommended)
			if !ok {
				return question, &LineError{Line: firstLineNumber - 1, Message: fmt.Sprintf("Recommended: names one choice, and %q is none of them", recommended)}
			}
			recommended = label
		}
		question.Recommended = protocol.Ptr(recommended)
	}
	if text := strings.Trim(strings.Join(context, "\n"), "\n"); strings.TrimSpace(text) != "" {
		question.Context = protocol.Ptr(text)
	}
	return question, nil
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

func choiceLabel(choices []protocol.AskChoice, label string) (string, bool) {
	for _, choice := range choices {
		if strings.EqualFold(choice.Label, strings.TrimSpace(label)) {
			return choice.Label, true
		}
	}
	return "", false
}

type codeFence struct {
	marker string
}

func (f *codeFence) open() bool { return f.marker != "" }

func (f *codeFence) track(line string) {
	trimmed := strings.TrimSpace(line)
	if f.marker != "" {
		if strings.HasPrefix(trimmed, f.marker) && strings.Trim(trimmed, f.marker[:1]) == "" {
			f.marker = ""
		}
		return
	}
	for _, char := range []string{"`", "~"} {
		run := len(trimmed) - len(strings.TrimLeft(trimmed, char))
		if run >= 3 {
			f.marker = strings.Repeat(char, run)
			return
		}
	}
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return truncate(strings.TrimSpace(line), maxSubjectRunes)
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}

const (
	idPrefix   = "a-"
	idBodyLen  = 6
	idAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"
)

func NewID() (string, error) {
	buf := make([]byte, idBodyLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mint ask id: %w", err)
	}
	out := []byte(idPrefix)
	for _, b := range buf {
		out = append(out, idAlphabet[int(b)%len(idAlphabet)])
	}
	return string(out), nil
}
