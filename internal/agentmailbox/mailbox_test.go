package agentmailbox

import "testing"

func TestItemStateFollowsTheReceiptLifecycle(t *testing.T) {
	for _, test := range []struct {
		name string
		item Item
		want State
	}{
		{name: "queued", item: Item{}, want: StateQueued},
		{name: "notified", item: Item{NotifiedAt: "n"}, want: StateNotified},
		{name: "read", item: Item{NotifiedAt: "n", ReadAt: "r"}, want: StateRead},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.item.State(); got != test.want {
				t.Fatalf("State() = %q, want %q", got, test.want)
			}
		})
	}
}
