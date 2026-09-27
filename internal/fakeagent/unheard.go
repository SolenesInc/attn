package fakeagent

import (
	"encoding/json"
	"errors"
)

const methodReplyUnheard = "reply_unheard"

type hookFailure struct{ error }

func (f hookFailure) Unwrap() error { return f.error }

func (r *Run) ReplyUnheard(text string) {
	r.t.Helper()
	r.call(methodReplyUnheard, textParams{Text: text}, nil)
}

func (a *agent) replyUnheard(params json.RawMessage) (any, error) {
	var p textParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	a.turn.Lock()
	defer a.turn.Unlock()
	if err := a.conv.reply(p.Text, false); err != nil && !errors.As(err, new(hookFailure)) {
		return nil, err
	}
	return struct{}{}, nil
}
