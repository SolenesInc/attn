package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

type AgentCall struct {
	Ordinal   OrdinalPath
	Prompt    string
	Schema    json.RawMessage
	Isolation string
	Model     string
	AgentType string
}

type AgentStub interface {
	Run(ctx context.Context, call AgentCall) (json.RawMessage, error)
}

type DefaultStub struct{}

func (DefaultStub) Run(_ context.Context, call AgentCall) (json.RawMessage, error) {
	sum := sha256.Sum256([]byte(call.Prompt))
	h := hex.EncodeToString(sum[:])[:12]
	b, _ := json.Marshal(h)
	return b, nil
}

type StubFunc func(call AgentCall) (json.RawMessage, error)

func (f StubFunc) Run(_ context.Context, call AgentCall) (json.RawMessage, error) {
	return f(call)
}

type ScriptedStub struct {
	resultFor func(ordinal OrdinalPath, prompt string) (json.RawMessage, error)

	mu       sync.Mutex
	gates    map[string]chan struct{}
	released map[string]bool
	openAll  bool
}

func NewScriptedStub(resultFor func(ordinal OrdinalPath, prompt string) (json.RawMessage, error)) *ScriptedStub {
	return &ScriptedStub{
		resultFor: resultFor,
		gates:     map[string]chan struct{}{},
		released:  map[string]bool{},
	}
}

func (s *ScriptedStub) gate(ordinal string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.openAll || s.released[ordinal] {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	if ch, ok := s.gates[ordinal]; ok {
		return ch
	}
	ch := make(chan struct{})
	s.gates[ordinal] = ch
	return ch
}

func (s *ScriptedStub) Run(ctx context.Context, call AgentCall) (json.RawMessage, error) {
	select {
	case <-s.gate(call.Ordinal.String()):
		return s.resultFor(call.Ordinal, call.Prompt)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *ScriptedStub) Release(ordinal string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.released[ordinal] = true
	if ch, ok := s.gates[ordinal]; ok {
		close(ch)
		delete(s.gates, ordinal)
	}
}

func (s *ScriptedStub) ReleaseAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.openAll = true
	for k, ch := range s.gates {
		close(ch)
		delete(s.gates, k)
	}
}
