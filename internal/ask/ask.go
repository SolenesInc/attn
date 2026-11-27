package ask

import (
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/document"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/shortid"
)

const (
	MaxMarkdownBytes = 64 << 10
	MaxQuestions     = 12
	maxSubjectRunes  = 120
)

func Parse(markdown string, openedAt time.Time) ([]protocol.AskPart, error) {
	return parse(markdown, openedAt, true)
}

func Read(markdown string, openedAt time.Time) ([]protocol.AskPart, error) {
	return parse(markdown, openedAt, false)
}

func parse(markdown string, openedAt time.Time, strict bool) ([]protocol.AskPart, error) {
	if len(markdown) > MaxMarkdownBytes {
		return nil, fmt.Errorf("an ask holds at most %d KiB of markdown", MaxMarkdownBytes>>10)
	}
	if strings.TrimSpace(markdown) == "" {
		return nil, fmt.Errorf("an ask needs markdown")
	}
	blocks, unreadable := document.Blocks(markdown)
	var parts []protocol.AskPart
	questions := 0
	defaultLine := 0
	for _, block := range blocks {
		if !block.IsPart() {
			parts = append(parts, protocol.AskPart{Markdown: protocol.Ptr(block.Text())})
			continue
		}
		questions++
		if questions > MaxQuestions {
			return nil, &document.LineError{Line: block.Line, Message: fmt.Sprintf("an ask holds at most %d questions", MaxQuestions)}
		}
		question, after, err := document.Question(block, document.QuestionRules{Strict: strict, EarlierDefaultLine: defaultLine})
		if err != nil {
			return nil, err
		}
		if question.Default != nil {
			defaultLine = block.Line
			question.Default.DueAt = openedAt.Add(after).UTC().Format(time.RFC3339)
		}
		question.Key = fmt.Sprintf("q%d", questions)
		parts = append(parts, protocol.AskPart{Question: &question})
	}
	if unreadable != nil {
		return nil, unreadable
	}
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
		var fence document.Fence
		for _, line := range strings.Split(*part.Markdown, "\n") {
			inCode := fence.Open()
			fence.Track(line)
			if inCode || fence.Open() {
				continue
			}
			if text := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#>*- ")); text != "" {
				return truncate(text, maxSubjectRunes)
			}
		}
	}
	return "Question"
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

func NewID() (string, error) { return shortid.New("a-") }
