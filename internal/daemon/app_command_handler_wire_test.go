package daemon_test

import (
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const reviewerBundle = `export default {
  commands: {
    approve: (payload, ctx) => ({ approved: payload.id, app: ctx.app, version: ctx.version }),
    refresh: () => {},
    reject: () => { throw new Error("reject needs a reason") },
    dump: () => ({ note: "x".repeat(262144) }),
    hang: () => new Promise(() => {}),
  },
}
`

func applyReviewer(t *testing.T, w *world) *protocol.AppApplyResult {
	t.Helper()
	return applyAppVersion(t, w.Client(), "reviewer", appManifestDeclaration(t, appbuild.Manifest{Name: "reviewer", Commands: []appbuild.Command{
		{Name: "approve"}, {Name: "refresh"}, {Name: "reject"}, {Name: "dump"}, {Name: "hang"},
	}}), reviewerBundle)
}

func TestAppCommandRunsItsHandlerAndAnswersTheCallerWithWhatItReturned(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	w := newWorld(t)
	version := applyReviewer(t, w)
	app := w.App()

	approved := requestAppCommand(app, "reviewer", "approve", `{"id":"tk-1"}`)
	if !approved.Success {
		t.Fatalf("approve failed: %s", protocol.Deref(approved.Error))
	}
	if got, want := protocol.Deref(approved.Payload), `{"approved":"tk-1","app":"reviewer","version":`+strconv.Itoa(version.VersionID)+`}`; got != want {
		t.Errorf("approve answered %s, want %s", got, want)
	}
	refreshed := requestAppCommand(app, "reviewer", "refresh", "")
	if !refreshed.Success || refreshed.Payload != nil {
		t.Errorf("a handler that took nothing and returned nothing = %+v, want success with no payload", refreshed)
	}

	recent := appStatus(t, w.Client(), "reviewer").Recent
	if len(recent) != 2 {
		t.Fatalf("recent invocations = %+v, want the two commands", recent)
	}
	for _, invocation := range recent {
		if invocation.Kind != "command" || invocation.Status != "ok" || protocol.Deref(invocation.EventName) != "app.command" {
			t.Errorf("invocation = %+v, want an ok command named as one", invocation)
		}
	}
}

func TestAppCommandRefusesTheCallerWhenTheHandlerThrowsOrAnswersTooMuch(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	w := newWorld(t)
	applyReviewer(t, w)
	app := w.App()

	thrown := requestAppCommand(app, "reviewer", "reject", "")
	requireAppCommandRefused(t, "a handler that threw", thrown, "reject needs a reason")
	oversized := requestAppCommand(app, "reviewer", "dump", "")
	requireAppCommandRefused(t, "an answer over the limit", oversized, "dump", "reviewer", "262144", "document")
	if oversized.Payload != nil {
		t.Error("the oversized answer reached the caller anyway")
	}

	recent := appStatus(t, w.Client(), "reviewer").Recent
	if len(recent) != 2 || recent[0].Status != "error" || recent[1].Status != "error" {
		t.Fatalf("recent invocations = %+v, want two recorded failures", recent)
	}
}

func TestAppCommandAbandonsAHandlerThatNeverReturnsWithinItsBudget(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	t.Setenv("ATTN_APP_DISPATCH_TIMEOUT", "1s")
	w := newWorld(t)
	applyReviewer(t, w)
	if warm := requestAppCommand(w.App(), "reviewer", "refresh", ""); !warm.Success {
		t.Fatalf("refresh = %+v", warm)
	}

	abandoned := requestAppCommand(w.App(), "reviewer", "hang", "")
	requireAppCommandRefused(t, "a handler that never returned", abandoned, "hang", "reviewer", "did not return within 1s")
	if status := appStatus(t, w.Client(), "reviewer"); status.Stall != nil || !status.App.Consumer.Enabled {
		t.Errorf("after an abandoned command: stall %+v, consumer %+v; want the app enabled and off the auto-disable clock", status.Stall, status.App.Consumer)
	}
	if again := requestAppCommand(w.App(), "reviewer", "approve", `{"id":"tk-2"}`); !again.Success {
		t.Errorf("the next command after an abandoned one = %+v, want it answered", again)
	}
}

func TestAppCommandQueuedBehindAFrozenHandlerIsRefusedInsideItsOwnBudget(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	t.Setenv("ATTN_APP_DISPATCH_TIMEOUT", "1s")
	w := newWorld(t)
	applyRunningApp(t, w.Client(), withCollections(appbuild.Manifest{Name: "reviewer", Commands: []appbuild.Command{{Name: "spin"}, {Name: "approve"}}}, "marks"), `
export default { commands: {
  spin: async (_, ctx) => { await ctx.collections.marks.put("spinning", {}); for (;;) {} },
  approve: () => "approved",
} }
`)
	marks := watchAppDocs(t, w, "reviewer", "marks")
	app := w.App()
	if warm := requestAppCommand(app, "reviewer", "approve", ""); !warm.Success {
		t.Fatalf("approve = %+v", warm)
	}
	spinning := uuid.NewString()
	app.Send(protocol.AppCommandMessage{Cmd: protocol.CmdAppCommand, RequestID: spinning, App: "reviewer", Command: "spin"})
	marks.await("spinning")

	queued := requestAppCommand(app, "reviewer", "approve", "")
	requireAppCommandRefused(t, "a command queued behind the frozen one", queued, "approve", "reviewer", "1s", "never got a turn", "attn app logs reviewer")
	testworld.Await(app, protocol.EventAppCommandResult, func(r protocol.AppCommandResultMessage) bool { return r.RequestID == spinning })
}
