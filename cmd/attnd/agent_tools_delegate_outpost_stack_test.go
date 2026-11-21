package main_test

import (
	"path/filepath"
	"testing"

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
