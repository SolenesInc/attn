package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnOutpostSessionDelegatesThroughItsToolsToItsFolderByARelativeOrAnAbsolutePath(t *testing.T) {
	o := newOutpostTools(t, fakeagent.Claude)
	folder := o.outpost.Path("source")
	_, claude := o.spawn(fakeagent.Claude, folder)
	tools := claudeToolEndpoint(t, claude)

	for _, cwd := range []string{".", folder} {
		var operation protocol.DelegationOperation
		tools.structured(t, "delegate", map[string]any{
			"brief": "Work in the outpost session's folder", "cwd": cwd, "model": "default", "name": "outpost " + filepath.Base(cwd),
		}, &operation)
		delegated := testworld.AwaitSession(o.pair.App, string(operation.SessionID), func(s protocol.Session) bool { return s.Directory != "" })
		if delegated.Directory != folder || protocol.Deref(delegated.Host) != o.pair.Host {
			t.Errorf("delegate with cwd %q started in %q on %q, want the source session's folder %q on %q", cwd, delegated.Directory, protocol.Deref(delegated.Host), folder, o.pair.Host)
		}
	}
}

func TestAnOutpostScriptsDelegateAnswersItsOperationAtTheCallsLimit(t *testing.T) {
	o := newOutpostTools(t, fakeagent.Claude)
	_, claude := o.spawn(fakeagent.Claude, o.outpost.Path("shop"))
	tools := claudeToolEndpoint(t, claude)
	folder := o.outpost.Path("late")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	o.outpost.AskFolderTrustAtNextBoot()
	mark := o.pair.App.EventCount()
	type answer struct {
		reply []byte
		err   error
	}
	answered := make(chan answer, 1)
	go func() {
		_, reply, err := tools.send("attn_do", map[string]any{"script": fmt.Sprintf("return delegate({brief: 'Say hello', cwd: %q, model: 'default', name: 'late'})", folder)})
		answered <- answer{reply, err}
	}()
	late := testworld.AwaitSince(o.pair.App, mark, protocol.EventSessionRegistered, func(m protocol.SessionRegisteredMessage) bool { return m.Session.Label == "late" }).Session
	o.pair.App.AwaitScreen(string(late.ID), "Quick safety check")
	o.outpost.FirePending("agent-script-call")
	var got answer
	select {
	case got = <-answered:
	case <-time.After(fakeagent.HangGuard):
		t.Fatalf("the call did not answer at its limit within %s", fakeagent.HangGuard)
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	for _, want := range []string{"operation_id", "preparing", "folder-trust question", "one call runs at most 50 s", "changed:"} {
		if !strings.Contains(string(got.reply), want) {
			t.Errorf("the answer at the limit lacks %q:\n%s", want, got.reply)
		}
	}
	if strings.Contains(string(got.reply), "it may have taken effect") {
		t.Errorf("the answer leaves the delegation uncertain:\n%s", got.reply)
	}

	o.pair.App.TypeLine(string(late.ID), "\x1b[B")
	if opening := o.outpost.LaunchedCarrying(o.pair.App.Terminal(string(late.ID))).Prompted(); !strings.Contains(opening, `"late"`) {
		t.Errorf("once its folder was trusted the delegate was prompted %q, want its assignment", opening)
	}
}
