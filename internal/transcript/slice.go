package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type ConversationSlice struct {
	Brief      string
	Rescoping  []string
	Summary    string
	AgentTurns []string
	HumanCount int
	AgentCount int
}

func (s ConversationSlice) Empty() bool {
	return s.Brief == "" && len(s.Rescoping) == 0 && s.Summary == "" && len(s.AgentTurns) == 0
}

func (s ConversationSlice) Render() string {
	var sections []string
	if s.Brief != "" {
		sections = append(sections, "## TICKET BRIEF (first human turn)\n"+s.Brief)
	}
	if len(s.Rescoping) > 0 {
		sections = append(sections, "## LATER HUMAN TURNS (re-scoping)\n"+strings.Join(s.Rescoping, "\n---\n"))
	}
	if s.Summary != "" {
		sections = append(sections, "## COMPACTION SUMMARY (most recent)\n"+s.Summary)
	}
	if len(s.AgentTurns) > 0 {
		sections = append(sections, "## AGENT'S LAST STATUS TURNS\n"+strings.Join(s.AgentTurns, "\n---\n"))
	}
	return strings.Join(sections, "\n\n")
}

type SliceOptions struct {
	MaxRescopingTurns int
	MaxAgentTurns     int
	TurnCharCap       int
	SummaryCharCap    int
}

func DefaultSliceOptions() SliceOptions {
	return SliceOptions{
		MaxRescopingTurns: 4,
		MaxAgentTurns:     6,
		TurnCharCap:       3000,
		SummaryCharCap:    12000,
	}
}

func resolveSliceOptions(opts SliceOptions) SliceOptions {
	def := DefaultSliceOptions()
	if opts.MaxRescopingTurns <= 0 {
		opts.MaxRescopingTurns = def.MaxRescopingTurns
	}
	if opts.MaxAgentTurns <= 0 {
		opts.MaxAgentTurns = def.MaxAgentTurns
	}
	if opts.TurnCharCap <= 0 {
		opts.TurnCharCap = def.TurnCharCap
	}
	if opts.SummaryCharCap <= 0 {
		opts.SummaryCharCap = def.SummaryCharCap
	}
	return opts
}

type sliceLineOrigin struct {
	Kind string `json:"kind"`
}

type sliceLine struct {
	Type             string           `json:"type"`
	IsCompactSummary bool             `json:"isCompactSummary"`
	Origin           *sliceLineOrigin `json:"origin"`
	Message          struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`

	// Codex event_msg and response_item envelopes carry their records here.
	Payload json.RawMessage `json:"payload"`

	Data struct {
		Content string `json:"content"`
	} `json:"data"`
}

type sliceCodexResponseMessage struct {
	Type     string          `json:"type"`
	Role     string          `json:"role"`
	Content  json.RawMessage `json:"content"`
	Metadata *struct {
		ContentItemKinds []string `json:"content_item_kinds"`
	} `json:"internal_chat_message_metadata_passthrough"`
}

type humanTurns struct {
	maxTail int

	haveFirst bool
	first     string
	lastText  string
	tail      []string

	count int
}

func (h *humanTurns) add(text string) {
	if text == h.lastText {
		return
	}
	h.lastText = text
	h.count++

	if !h.haveFirst {
		h.first = text
		h.haveFirst = true
		return
	}

	h.tail = append(h.tail, text)
	if len(h.tail) > h.maxTail {
		h.tail = h.tail[len(h.tail)-h.maxTail:]
	}
}

// `permissive` also takes user-role lines with no provenance and is used whenever `strict`
// saw nothing — the read for transcripts predating the `origin` field.
type sliceBuilder struct {
	opts SliceOptions

	strict     humanTurns
	permissive humanTurns

	lastSummary string

	tailAgent         []string
	responseTailAgent []string

	agentCount         int
	responseAgentCount int

	responseStrict     humanTurns
	responsePermissive humanTurns
}

func newSliceBuilder(opts SliceOptions) *sliceBuilder {
	return &sliceBuilder{
		opts:               opts,
		strict:             humanTurns{maxTail: opts.MaxRescopingTurns},
		permissive:         humanTurns{maxTail: opts.MaxRescopingTurns},
		responseStrict:     humanTurns{maxTail: opts.MaxRescopingTurns},
		responsePermissive: humanTurns{maxTail: opts.MaxRescopingTurns},
	}
}

func (b *sliceBuilder) addHuman(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	b.strict.add(text)
	b.permissive.add(text)
}

func (b *sliceBuilder) addUnstampedHuman(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	b.permissive.add(text)
}

func (b *sliceBuilder) humans() *humanTurns {
	if b.strict.count > 0 {
		return &b.strict
	}
	return &b.permissive
}

func (b *sliceBuilder) addAgent(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	b.agentCount++

	b.tailAgent = append(b.tailAgent, text)
	if len(b.tailAgent) > b.opts.MaxAgentTurns {
		b.tailAgent = b.tailAgent[len(b.tailAgent)-b.opts.MaxAgentTurns:]
	}
}

func (b *sliceBuilder) addResponseHuman(text string, stamped bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if stamped {
		b.responseStrict.add(text)
	}
	b.responsePermissive.add(text)
}

func (b *sliceBuilder) addResponseAgent(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	b.responseAgentCount++
	b.responseTailAgent = append(b.responseTailAgent, text)
	if len(b.responseTailAgent) > b.opts.MaxAgentTurns {
		b.responseTailAgent = b.responseTailAgent[len(b.responseTailAgent)-b.opts.MaxAgentTurns:]
	}
}

func responseItemIsHuman(metadata *struct {
	ContentItemKinds []string `json:"content_item_kinds"`
}) bool {
	for _, kind := range metadata.ContentItemKinds {
		if kind == "user.text" {
			return true
		}
	}
	return false
}

func (b *sliceBuilder) setSummary(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	b.lastSummary = text
}

func (b *sliceBuilder) processLine(line []byte) {
	var e sliceLine
	if err := json.Unmarshal(line, &e); err != nil {
		return
	}

	switch e.Type {
	case "user":
		if e.IsCompactSummary {
			b.setSummary(extractTextContent(e.Message.Content))
			return
		}
		switch {
		case e.Origin == nil:
			b.addUnstampedHuman(extractTextContent(e.Message.Content))
		case e.Origin.Kind == "human":
			b.addHuman(extractTextContent(e.Message.Content))
		}
		return
	case "assistant":
		b.addAgent(extractTextContent(e.Message.Content))
		return
	case "event_msg":
		var payload codexEventMessage
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			return
		}
		switch payload.Type {
		case "user_message":
			b.addHuman(payload.Message)
		case "agent_message":
			b.addAgent(payload.Message)
		}
		return
	case "response_item":
		var payload sliceCodexResponseMessage
		if err := json.Unmarshal(e.Payload, &payload); err != nil || payload.Type != "message" {
			return
		}
		content := extractTextContent(payload.Content)
		switch payload.Role {
		case "user":
			if payload.Metadata == nil {
				b.addResponseHuman(content, false)
			} else if responseItemIsHuman(payload.Metadata) {
				b.addResponseHuman(content, true)
			}
		case "assistant":
			b.addResponseAgent(content)
		}
		return
	case "user.message":
		b.addHuman(e.Data.Content)
		return
	case "assistant.message":
		b.addAgent(e.Data.Content)
		return
	}
}

func capText(s string, n int) string {
	s = strings.TrimSpace(s)
	if n > 0 && len(s) > n {
		return s[:n] + fmt.Sprintf("\n...[truncated, %d chars total]", len(s))
	}
	return s
}

func capTexts(items []string, n int) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = capText(s, n)
	}
	return out
}

func (b *sliceBuilder) toSlice(opts SliceOptions) ConversationSlice {
	h := b.humans()
	if h.count == 0 {
		if b.responseStrict.count > 0 {
			h = &b.responseStrict
		} else {
			h = &b.responsePermissive
		}
	}
	tailAgent := b.tailAgent
	agentCount := b.agentCount
	if agentCount == 0 {
		tailAgent = b.responseTailAgent
		agentCount = b.responseAgentCount
	}
	return ConversationSlice{
		Brief:      capText(h.first, opts.TurnCharCap),
		Rescoping:  capTexts(h.tail, opts.TurnCharCap),
		Summary:    capText(b.lastSummary, opts.SummaryCharCap),
		AgentTurns: capTexts(tailAgent, opts.TurnCharCap),
		HumanCount: h.count,
		AgentCount: agentCount,
	}
}

func ExtractConversationSlice(path string, opts SliceOptions) (ConversationSlice, error) {
	opts = resolveSliceOptions(opts)

	file, err := os.Open(path)
	if err != nil {
		return ConversationSlice{}, err
	}
	defer file.Close()

	b := newSliceBuilder(opts)

	if err := readJSONLLines(file, b.processLine); err != nil {
		return ConversationSlice{}, err
	}

	return b.toSlice(opts), nil
}
