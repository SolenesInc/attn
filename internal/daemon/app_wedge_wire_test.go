package daemon_test

import (
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAFrozenEventLoopIsChargedToTheHandlerOnItNotToTheOneThatWaitedLongest(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	t.Setenv("ATTN_APP_DISPATCH_TIMEOUT", "1s")
	w := newWorld(t)
	cli := w.Client()
	applyRunningApp(t, cli, subscriber("bystander", "ticket.created"), `
export default { subscriptions: { "ticket.created": async (ev, ctx) => {
  if (await ctx.collections.marks.get("entered")) return
  await ctx.collections.marks.put("entered", {})
  await new Promise(() => {})
} } }
`)
	applyRunningApp(t, cli, subscriber("hog", "document.changed"), `
export default { subscriptions: { "document.changed": async (ev, ctx) => {
  if (ev.payload.namespace !== "app/approval-gate" || (await ctx.collections.marks.get("spun"))) return
  await ctx.collections.marks.put("spun", {})
  for (;;) {}
} } }
`)
	defineRequests(t, cli, gateNS)
	bystanderMarks, hogMarks := watchAppDocs(t, w, "bystander", "marks"), watchAppDocs(t, w, "hog", "marks")
	bystander, hog := watchAppInvocations(t, w, "bystander"), watchAppInvocations(t, w, "hog")

	fileTicket(t, cli, "Price the order")
	bystanderMarks.await("entered")
	put(t, cli, gateNS, "go", `{}`)
	hogMarks.await("spun")

	victim := awaitAppInvocation(t, bystander)
	if victim.Status != "runtime_error" || !strings.Contains(protocol.Deref(victim.Error), "hog") {
		t.Errorf("the handler waiting when the loop froze = %+v, want a runtime_error naming hog", victim)
	}
	culprit := awaitInvocationWith(t, hog, "error")
	if culprit.Status != "error" || !strings.Contains(protocol.Deref(culprit.Error), "did not return within 1s") {
		t.Errorf("hog's dispatch = %+v, want an error for not returning", culprit)
	}

	awaitInvocationWith(t, bystander, "ok")
	awaitInvocationWith(t, hog, "ok")
	if runtime := appRuntimeStatus(t, cli).Runtime; runtime == nil || runtime.Generation < 2 || runtime.LastExit == nil {
		t.Errorf("after the wedge the runtime = %+v, want the frozen generation ended and a replacement serving", runtime)
	}
}

func TestAHandlerThatNeverSettlesOnATurningLoopIsChargedToItsOwnApp(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	t.Setenv("ATTN_APP_DISPATCH_TIMEOUT", "300ms")
	w := newWorld(t)
	cli := w.Client()
	applyRunningApp(t, cli, subscriber("dawdler", "ticket.created"), `
export default { subscriptions: { "ticket.created": () => new Promise(() => {}) } }
`)
	invocations := watchAppInvocations(t, w, "dawdler")
	fileTicket(t, cli, "Price the order")

	hung := awaitAppInvocation(t, invocations)
	if hung.Status != "error" || !strings.Contains(protocol.Deref(hung.Error), "did not return within 300ms") {
		t.Errorf("the hung dispatch = %+v, want an error for not returning within the budget", hung)
	}
	awaitAppInvocation(t, invocations)
	if stall := appStatus(t, cli, "dawdler").Stall; stall == nil || !strings.Contains(stall.LastError, "did not return") {
		t.Errorf("dawdler's stall = %+v, want its own hung handler on the clock", stall)
	}
}
