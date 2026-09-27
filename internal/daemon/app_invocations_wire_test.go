package daemon_test

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAnAppsStatusListsItsInvocationsNewestFirst(t *testing.T) {
	testworld.UseRealAppRuntime(t)
	w := newWorld(t)
	cli := w.Client()
	applyRunningApp(t, cli, subscriber("greeter", "ticket.*"), `
export default { subscriptions: { "ticket.*": async (ev, ctx) => {
  await ctx.collections.marks.put(ev.subject, { seq: ev.seq })
} } }
`)
	invocations := watchAppInvocations(t, w, "greeter")
	for _, title := range []string{"Price the order", "Ship the order", "Bill the order"} {
		if _, err := cli.CreateTicket("planner", title, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	var handled []int
	for range 3 {
		handled = append(handled, protocol.Deref(awaitAppInvocation(t, invocations).EventSeq))
	}

	recent := appStatus(t, cli, "greeter").Recent
	if len(recent) != 3 {
		t.Fatalf("greeter's status lists %d invocations, want the 3 it handled", len(recent))
	}
	for i, invocation := range recent {
		if want := handled[len(handled)-1-i]; protocol.Deref(invocation.EventSeq) != want {
			t.Errorf("greeter's status lists seq %d at %d, want %v newest first", protocol.Deref(invocation.EventSeq), i, handled)
		}
	}
}
