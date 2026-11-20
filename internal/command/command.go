package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

type Caller struct {
	Session  protocol.SessionID
	Profile  string
	Terminal protocol.TerminalID
}

const CodeUsage = "usage"

type Refusal struct {
	Code      string
	Message   string
	Fix       string
	Arguments bool
}

func (r *Refusal) Error() string { return r.Message }

func Usage(format string, args ...any) *Refusal {
	return &Refusal{Code: CodeUsage, Message: fmt.Sprintf(format, args...), Arguments: true}
}

func Invalid(code, format string, args ...any) *Refusal {
	return &Refusal{Code: code, Message: fmt.Sprintf(format, args...), Arguments: true}
}

type Handler func(ctx context.Context, c Caller, args json.RawMessage) (any, error)

func Typed[A any](run func(ctx context.Context, c Caller, args A) (any, error)) Handler {
	return func(ctx context.Context, c Caller, raw json.RawMessage) (any, error) {
		var args A
		if len(bytes.TrimSpace(raw)) > 0 && string(raw) != "null" {
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&args); err != nil {
				return nil, Usage("arguments: %v", err)
			}
		}
		return run(ctx, c, args)
	}
}

type spec struct {
	readOnly bool
}

type Layer struct {
	handlers map[string]Handler
}

func New(handlers map[string]Handler) (*Layer, error) {
	var problems []string
	for name := range handlers {
		if _, ok := specs[name]; !ok {
			problems = append(problems, fmt.Sprintf("handler for %q, which is not a catalog command", name))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, errors.New("command layer: " + strings.Join(problems, "; "))
	}
	return &Layer{handlers: handlers}, nil
}

func (l *Layer) Run(ctx context.Context, c Caller, name string, args json.RawMessage) (any, *Refusal) {
	handler, ok := l.handlers[name]
	if !ok {
		return nil, Usage("%q is not a command", name)
	}
	if c.Terminal != "" && (!specs[name].readOnly || c.Profile == "") {
		return nil, Invalid("no_session", "terminal %s is not showing a session; pass --session <id> to identify the caller; --profile <name|id> selects a garden and does not identify a session", c.Terminal)
	}
	result, err := handler(ctx, c, args)
	if err != nil {
		return nil, AsRefusal(err)
	}
	return result, nil
}

func AsRefusal(err error) *Refusal {
	var refusal *Refusal
	if errors.As(err, &refusal) {
		return refusal
	}
	code := protocol.RefusalUnclassified
	if class := protocol.RefusalOf(err); class != nil {
		code = *class
	}
	return &Refusal{Code: string(code), Message: err.Error()}
}
