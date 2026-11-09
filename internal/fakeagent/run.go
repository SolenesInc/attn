package fakeagent

import (
	"context"
	"testing"
	"time"
)

type Run struct {
	PID            int
	Harness        Harness
	SessionID      string
	ConversationID string
	Resumed        bool
	ResumePicker   bool
	Argv           []string
	Env            []string
	Yolo           bool
	ThroughMod     bool
	t              testing.TB
	fake           *fake
	kit            *Kit
}

func (r *Run) Prompted() string {
	r.t.Helper()
	var prompted promptedResult
	r.call(methodPrompted, struct{}{}, &prompted)
	r.ConversationID = prompted.ConversationID
	r.ThroughMod = prompted.ThroughMod
	return prompted.Text
}

func (r *Run) DeliveredPrompt() string {
	r.t.Helper()
	if r.Harness == Codex {
		return r.kit.CodexServer().Prompted(r.ConversationID)
	}
	return r.Prompted()
}

func (r *Run) Input(text string) {
	r.t.Helper()
	r.call(methodInput, textParams{Text: text}, nil)
}

func (r *Run) Refused() string {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), HangGuard)
	defer cancel()
	var prompted promptedResult
	err := r.fake.peer.call(ctx, methodPrompted, struct{}{}, &prompted)
	if err == nil {
		r.t.Fatalf("%s took %q for session %s, want it refused", r.Harness, prompted.Text, r.SessionID)
	}
	return err.Error()
}

func (r *Run) Reply(text string) {
	r.t.Helper()
	r.call(methodReply, textParams{Text: text}, nil)
}

func (r *Run) ReplyAfterStop(text string) {
	r.t.Helper()
	r.call(methodReplyLate, textParams{Text: text}, nil)
}

type backgroundReplyParams struct {
	Text string `json:"text"`
	Task string `json:"task"`
}

func (r *Run) ReplyLeavingBackgroundWork(text, task string) {
	r.t.Helper()
	r.call(methodReplyBg, backgroundReplyParams{Text: text, Task: task}, nil)
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

func (r *Run) Halt() {
	r.t.Helper()
	r.call(methodHalt, struct{}{}, nil)
}

func (r *Run) ModNext() ModEvent {
	r.t.Helper()
	var event ModEvent
	r.call(methodModNext, struct{}{}, &event)
	return event
}

func (r *Run) ModToolBoundary() []string {
	r.t.Helper()
	var appended []string
	r.call(methodModTool, struct{}{}, &appended)
	return appended
}

func (r *Run) ModEsc() []string {
	r.t.Helper()
	var returned []string
	r.call(methodModEsc, struct{}{}, &returned)
	return returned
}

func (r *Run) ModReload() {
	r.t.Helper()
	r.call(methodModReload, struct{}{}, nil)
}

func (r *Run) ModStopPolling() {
	r.t.Helper()
	r.call(methodModStop, struct{}{}, nil)
}

// ModPause stops the mod's loop before its next give-back and poll until resume. ModGiveBack steps the give-back alone.
func (r *Run) ModPause() (resume func()) {
	r.t.Helper()
	r.call(methodModPause, struct{}{}, nil)
	return func() {
		r.t.Helper()
		r.call(methodModResume, struct{}{}, nil)
	}
}

func (r *Run) ModGiveBack() {
	r.t.Helper()
	r.call(methodModGiveBack, struct{}{}, nil)
}

func (r *Run) ModHeldRingsRecordAddress(address string) {
	r.t.Helper()
	r.call(methodModReaddressHeld, address, nil)
}

func (r *Run) ModSkipNextCompact(reason string) {
	r.t.Helper()
	r.call(methodModCompactOutcome, ModCompactReport{Outcome: ModCompactSkipped, Reason: reason}, nil)
}

func (r *Run) ModFailNextCompact(reason string) {
	r.t.Helper()
	r.call(methodModCompactOutcome, ModCompactReport{Outcome: ModCompactFailed, Reason: reason}, nil)
}

func (r *Run) ModBusyNextCompact(reason string) {
	r.t.Helper()
	r.call(methodModCompactOutcome, ModCompactReport{Outcome: ModCompactBusy, Reason: reason}, nil)
}

func (r *Run) ModHoldNextCompact() (arrived, release func()) {
	r.t.Helper()
	r.call(methodModCompactHold, struct{}{}, nil)
	arrived = func() {
		r.t.Helper()
		r.call(methodModCompactArrived, struct{}{}, nil)
	}
	release = func() {
		r.t.Helper()
		r.call(methodModCompactRelease, struct{}{}, nil)
	}
	return arrived, release
}

func (r *Run) ModCompactions() int {
	r.t.Helper()
	var ran int
	r.call(methodModCompactions, struct{}{}, &ran)
	return ran
}

func (r *Run) ModNextCompactReport() ModCompactReport {
	r.t.Helper()
	var report ModCompactReport
	r.call(methodModCompactReport, struct{}{}, &report)
	return report
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

func (r *Run) AwaitGone() {
	r.t.Helper()
	select {
	case <-r.fake.peer.done:
	case <-time.After(HangGuard):
		r.t.Fatalf("%s for session %s (pid %d) still running after %s", r.Harness, r.SessionID, r.fake.Pid, HangGuard)
	}
}

func (r *Run) CrashAppServer() {
	r.t.Helper()
	r.call(methodCrashSrv, struct{}{}, nil)
}

func (r *Run) HoldRejoin() {
	r.t.Helper()
	r.call(methodHoldRejoin, struct{}{}, nil)
}

func (r *Run) ReleaseRejoin() {
	r.t.Helper()
	r.call(methodFreeRejoin, struct{}{}, nil)
}

func (r *Run) StopReadingTerminal() {
	r.t.Helper()
	r.call(methodStopRead, struct{}{}, nil)
}

func (r *Run) ProgramStatus(fields string) {
	r.t.Helper()
	r.call(methodStatus, textParams{Text: fields}, nil)
}

func (r *Run) ClaudeSuggestion(text string) {
	r.t.Helper()
	r.call(methodSuggestion, textParams{Text: text}, nil)
}

func (r *Run) call(method string, params, result any) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), HangGuard)
	defer cancel()
	if err := r.fake.peer.call(ctx, method, params, result); err != nil {
		r.t.Fatalf("%s %s for session %s: %v", r.Harness, method, r.SessionID, err)
	}
}
