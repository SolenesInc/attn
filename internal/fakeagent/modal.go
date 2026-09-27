package fakeagent

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	methodShowSelector = "show_selector"
	methodAskApproval  = "ask_approval"
	methodDismiss      = "dismiss"
	methodAnswered     = "answered"
)

var selectorLines = []string{
	"Which should I keep?",
	"❯ 1. The old import path",
	"  2. The new import path",
	"Enter to select · ↑/↓ to navigate · Esc to cancel",
}

type modal struct {
	lines     []string
	resting   func()
	typed     []byte
	answering bool
}

type approvalAsker interface {
	approvalTitle() string
	approvalAnswered()
}

type modalResult struct {
	Typed string `json:"typed"`
}

func (r *Run) ShowSelector() {
	r.t.Helper()
	r.call(methodShowSelector, struct{}{}, nil)
}

func (r *Run) AskApproval() {
	r.t.Helper()
	r.call(methodAskApproval, struct{}{}, nil)
}

func (r *Run) Answered() (key string) {
	r.t.Helper()
	var result modalResult
	r.call(methodAnswered, struct{}{}, &result)
	return result.Typed
}

func (r *Run) Dismiss() (typed string) {
	r.t.Helper()
	var result modalResult
	r.call(methodDismiss, struct{}{}, &result)
	return result.Typed
}

func (a *agent) handleModal(method string) (any, error) {
	if method == methodAnswered {
		return modalResult{Typed: <-a.term.answers}, nil
	}
	a.turn.Lock()
	defer a.turn.Unlock()
	switch method {
	case methodShowSelector:
		return struct{}{}, a.term.openModal(&modal{lines: selectorLines, resting: func() {}})
	case methodAskApproval:
		asker, ok := a.conv.(approvalAsker)
		if !ok {
			return nil, fmt.Errorf("%T does not script an approval prompt", a.conv)
		}
		a.term.title(asker.approvalTitle())
		return struct{}{}, a.term.openModal(&modal{
			lines:     []string{"Allow the command to run?", "› 1. Yes, proceed", "  2. No, and tell Codex what to do differently", "Press enter to confirm or esc to cancel"},
			resting:   asker.approvalAnswered,
			answering: true,
		})
	case methodDismiss:
		typed, err := a.term.closeModal()
		return modalResult{Typed: typed}, err
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

func (t *terminal) openModal(m *modal) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.modal != nil {
		return fmt.Errorf("a modal is already open")
	}
	t.modal = m
	t.write("\r\x1b[J" + strings.Join(m.lines, "\r\n"))
	return nil
}

func (t *terminal) closeModal() (string, error) {
	t.mu.Lock()
	m := t.modal
	if m == nil {
		t.mu.Unlock()
		return "", fmt.Errorf("no modal is open")
	}
	t.modal = nil
	t.write("\x1b[" + strconv.Itoa(len(m.lines)-1) + "A\r\x1b[J" + t.composer())
	t.mu.Unlock()
	m.resting()
	return string(m.typed), nil
}

func (t *terminal) capturedByModal(input []byte) bool {
	t.mu.Lock()
	m := t.modal
	if m == nil {
		t.mu.Unlock()
		return false
	}
	m.typed = append(m.typed, input...)
	if !m.answering {
		t.mu.Unlock()
		return true
	}
	t.modal = nil
	t.write("\x1b[" + strconv.Itoa(len(m.lines)-1) + "A\r\x1b[J" + t.composer())
	t.mu.Unlock()
	m.resting()
	t.answers <- string(input)
	return true
}

func (c *codex) approvalTitle() string {
	return "[ . ] Action Required | " + c.restingTitle()
}

func (c *codex) approvalAnswered() {
	c.term.title(codexBusyGlyph + c.restingTitle())
}
