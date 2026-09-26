package fakeagent

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type Run struct {
	Harness        Harness
	SessionID      string
	ConversationID string
	Resumed        bool
	Argv           []string
	Env            []string
	AutoMode       json.RawMessage
	Yolo           bool
	t              testing.TB
	fake           *fake
}

func (r *Run) Prompted() string {
	r.t.Helper()
	var prompted promptedResult
	r.call(methodPrompted, struct{}{}, &prompted)
	r.ConversationID = prompted.ConversationID
	return prompted.Text
}

func (r *Run) Reply(text string) {
	r.t.Helper()
	r.call(methodReply, textParams{Text: text}, nil)
}

func (r *Run) ReplyAfterStop(text string) {
	r.t.Helper()
	r.call(methodReplyLate, textParams{Text: text}, nil)
}

func (r *Run) Stream(text string) {
	r.t.Helper()
	r.call(methodStream, textParams{Text: text}, nil)
}

func (r *Run) Subagent(text string) {
	r.t.Helper()
	r.call(methodSubagent, textParams{Text: text}, nil)
}

func (r *Run) DeleteSubagentTranscripts() {
	r.t.Helper()
	r.call(methodDropSubs, textParams{}, nil)
}

func (r *Run) Exit(code int) {
	r.t.Helper()
	if err := r.fake.peer.notify(methodExit, exitParams{Code: code}); err != nil {
		r.t.Fatalf("%s for session %s: exit %d: %v", r.Harness, r.SessionID, code, err)
	}
	select {
	case <-r.fake.peer.done:
	case <-time.After(HangGuard):
		r.t.Fatalf("%s for session %s (pid %d) still running %s after exit %d", r.Harness, r.SessionID, r.fake.Pid, HangGuard, code)
	}
	if got := r.fake.exited(); got != code {
		r.t.Fatalf("%s for session %s exited %d, want %d", r.Harness, r.SessionID, got, code)
	}
}

func (r *Run) call(method string, params, result any) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), HangGuard)
	defer cancel()
	if err := r.fake.peer.call(ctx, method, params, result); err != nil {
		r.t.Fatalf("%s %s for session %s: %v", r.Harness, method, r.SessionID, err)
	}
}
