package codex

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	ModelName              = "gpt-5.6-luna"
	lowEffort              = "low"
	mediumEffort           = "medium"
	defaultSnapshotMax     = 8 << 20
	defaultConversationMax = 120_000
)

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

var ErrInvalidResponse = errors.New("invalid structured model response")

const AnswerSchema = `{"type":"object","properties":{"answer":{"type":"string"},"evidence":{"type":"array","items":{"type":"object","properties":{"turn_id":{"type":"string"},"quote":{"type":"string"}},"required":["turn_id","quote"],"additionalProperties":false}}},"required":["answer","evidence"],"additionalProperties":false}`

type ConversationTurn struct {
	ID        string
	Author    string
	Text      string
	Timestamp string
}

type CandidateEvidence struct {
	TurnID string `json:"turn_id"`
	Quote  string `json:"quote"`
}

type ModelAnswer struct {
	Answer   string              `json:"answer"`
	Evidence []CandidateEvidence `json:"evidence"`
}

type ModelRequest struct {
	Question                 string
	Conversation             []ConversationTurn
	Effort                   string
	PreviousValidationErrors []string
}

func readSnapshot(path string, max int) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < 1 || info.Size() > int64(max) {
		return nil, errors.New("snapshot unavailable")
	}
	return os.ReadFile(path)
}

type codexRecord struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"payload"`
}

func projectCodexConversation(raw []byte) ([]ConversationTurn, error) {
	var turns []ConversationTurn
	lines := bytes.Split(raw, []byte{'\n'})
	valid := false
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var record codexRecord
		if err := json.Unmarshal(line, &record); err != nil {
			continue
		}
		valid = true
		if record.Type != "event_msg" {
			continue
		}
		author := ""
		switch record.Payload.Type {
		case "user_message":
			author = "user"
		case "agent_message":
			author = "assistant"
		default:
			continue
		}
		text := strings.TrimSpace(record.Payload.Message)
		if text == "" {
			continue
		}
		turns = append(turns, ConversationTurn{ID: fmt.Sprintf("turn-%d", len(turns)+1), Author: author, Text: text, Timestamp: record.Timestamp})
	}
	if !valid {
		return nil, errors.New("not JSONL")
	}
	return turns, nil
}

func conversationChars(turns []ConversationTurn) int {
	n := 0
	for _, turn := range turns {
		n += len(turn.Text)
	}
	return n
}

func hash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func validateCandidate(candidate ModelAnswer, question string, turns []ConversationTurn) ([]string, bool) {
	answer := safeAnswer(candidate.Answer)
	malformed := false
	var errs []string
	if answer == "" {
		errs = append(errs, "answer was empty")
		malformed = true
	}
	if len(candidate.Evidence) == 0 {
		errs = append(errs, "evidence was missing")
		malformed = true
	}
	byID := make(map[string]ConversationTurn, len(turns))
	for _, turn := range turns {
		byID[turn.ID] = turn
	}
	seenUser := false
	for _, evidence := range candidate.Evidence {
		turn, ok := byID[evidence.TurnID]
		if !ok {
			errs = append(errs, "an evidence turn id was not found")
			continue
		}
		if !uniqueQuoteMatch(turn.Text, evidence.Quote) {
			errs = append(errs, "an evidence quote was not uniquely found")
			continue
		}
		if turn.Author == "user" {
			seenUser = true
		}
	}
	if requiresUserEvidence(question, answer) && !seenUser {
		errs = append(errs, "a user-instructions question needs user evidence")
	}
	return errs, malformed
}

func requiresUserEvidence(question, answer string) bool {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "unclear.") {
		return false
	}
	return !isExplicitSpeechReportingQuestion(question)
}

var explicitSpeechReportingQuestion = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`^(?:(?:did|does|do|has|have)\s+(?:the\s+)?(?:agent|assistant|codex)|what\s+(?:did|does|has|have)\s+(?:the\s+)?(?:agent|assistant|codex))\s+(?:say|said|tell|told|write|wrote|state|stated|claim|claimed|report|reported)\b|^(?:is|was|were)\s+(?:the\s+)?(?:agent|assistant|codex)\s+(?:saying|writing|stating|claiming|reporting)\b`)
})

func isExplicitSpeechReportingQuestion(question string) bool {
	q := strings.ToLower(strings.TrimSpace(question))
	return explicitSpeechReportingQuestion().MatchString(q)
}

func uniqueQuoteMatch(text, hint string) bool {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return false
	}
	if strings.Count(text, hint) == 1 {
		return true
	}
	normalizedText, normalizedHint := normalize(text), normalize(hint)
	return normalizedHint != "" && strings.Count(normalizedText, normalizedHint) == 1
}

func normalize(value string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(value) {
		switch r {
		case '“', '”', '„', '‟':
			r = '"'
		case '‘', '’', '‚', '‛':
			r = '\''
		}
		if unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

func resultFromCandidate(candidate ModelAnswer, sessionID protocol.SessionID, path string, fingerprint string, effort string, turns []ConversationTurn) *protocol.SessionInstructionsResult {
	byID := make(map[string]ConversationTurn, len(turns))
	for _, turn := range turns {
		byID[turn.ID] = turn
	}
	evidence := make([]protocol.EvidenceExcerpt, 0, len(candidate.Evidence))
	for _, item := range candidate.Evidence {
		turn := byID[item.TurnID]
		quote := exactQuote(turn.Text, item.Quote)
		evidence = append(evidence, protocol.EvidenceExcerpt{TurnID: turn.ID, Author: turn.Author, Quote: quote, Timestamp: optionalTimestamp(turn.Timestamp)})
	}
	sort.SliceStable(evidence, func(i, j int) bool { return turnPosition(evidence[i].TurnID) < turnPosition(evidence[j].TurnID) })
	return &protocol.SessionInstructionsResult{Answer: safeAnswer(candidate.Answer), Evidence: evidence, SessionID: sessionID, TranscriptPath: path, TranscriptFingerprint: fingerprint, Model: ModelName, ReasoningEffort: effort}
}

func turnPosition(id string) int {
	var position int
	_, _ = fmt.Sscanf(id, "turn-%d", &position)
	return position
}

func exactQuote(text, hint string) string {
	if at := strings.Index(text, hint); at >= 0 {
		return text[at : at+len(hint)]
	}
	return text
}

func optionalTimestamp(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func Prompt(request ModelRequest) string {
	var conversation strings.Builder
	for _, turn := range request.Conversation {
		fmt.Fprintf(&conversation, "[%s %s] %s\n", turn.ID, turn.Author, turn.Text)
	}
	return prompts.RenderText("session-instructions", "question", prompts.Values{
		"question":     request.Question,
		"conversation": conversation.String(),
		"errors":       strings.Join(request.PreviousValidationErrors, "\n- "),
		"retry":        fmt.Sprint(len(request.PreviousValidationErrors) > 0),
	})
}

func ParseModelAnswer(text string) (ModelAnswer, error) {
	var answer ModelAnswer
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &answer); err != nil {
		return ModelAnswer{}, ErrInvalidResponse
	}
	return answer, nil
}

func safeAnswer(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) {
			if r == '\n' || r == '\t' {
				b.WriteByte(' ')
			}
			continue
		}
		b.WriteRune(r)
	}
	answer := strings.TrimSpace(b.String())
	switch strings.ToLower(answer) {
	case "yes":
		return "Yes."
	case "no":
		return "No."
	case "unclear":
		return "Unclear."
	default:
		return answer
	}
}
