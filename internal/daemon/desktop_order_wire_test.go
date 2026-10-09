package daemon_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func createOrderedDesktop(t *testing.T, app *testworld.Peer, profileID string, slot int) protocol.Desktop {
	t.Helper()
	id := uuid.NewString()
	result := desktopRequest(app, protocol.DesktopCreateMessage{Cmd: protocol.CmdDesktopCreate, RequestID: id, ProfileID: profileID, ShortcutSlot: &slot}, id)
	if !result.Success {
		t.Fatalf("create desktop: %s", protocol.Deref(result.Error))
	}
	return result.Desktops[0]
}

func desktopOrder(desktops []protocol.Desktop) []string {
	slices.SortFunc(desktops, func(a, b protocol.Desktop) int {
		if a.OrderKey < b.OrderKey {
			return -1
		}
		if a.OrderKey > b.OrderKey {
			return 1
		}
		return 0
	})
	ids := make([]string, len(desktops))
	for i, desktop := range desktops {
		ids[i] = desktop.ID
	}
	return ids
}

func TestDesktopOrderIsAtomicAndNewNumbersFollowTheirNearestLowerNumber(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	profileID := app.Initial.Profiles[0].ID
	first := app.Initial.Desktops[0]
	six := createOrderedDesktop(t, app, profileID, 6)
	named := createOrderedDesktop(t, app, profileID, 0)
	three := createOrderedDesktop(t, app, profileID, 3)
	previous := []string{six.ID, named.ID, first.ID, three.ID}
	set := func(ids []string) protocol.ProfileActionResultMessage {
		id := uuid.NewString()
		return desktopRequest(app, protocol.DesktopSetOrderMessage{Cmd: protocol.CmdDesktopSetOrder, RequestID: id, ProfileID: profileID, DesktopIds: ids}, id)
	}
	if result := set(previous); !result.Success {
		t.Fatalf("set order: %s", protocol.Deref(result.Error))
	}
	for _, ids := range [][]string{previous[:3], append(slices.Clone(previous), "unknown"), {six.ID, named.ID, first.ID, "unknown"}, {six.ID, named.ID, first.ID, first.ID}} {
		if result := set(ids); result.Success {
			t.Fatalf("accepted invalid order %v", ids)
		}
		if got := desktopOrder(w.App().Initial.Desktops); !slices.Equal(got, previous) {
			t.Fatalf("invalid order changed desktops: %v", got)
		}
	}
	seven := createOrderedDesktop(t, app, profileID, 7)
	want := []string{six.ID, seven.ID, named.ID, first.ID, three.ID}
	if got := desktopOrder(w.App().Initial.Desktops); !slices.Equal(got, want) {
		t.Fatalf("new 7 order = %v, want %v", got, want)
	}
	extra := createOrderedDesktop(t, app, profileID, 0)
	want = append(want, extra.ID)
	if got := desktopOrder(w.App().Initial.Desktops); !slices.Equal(got, want) {
		t.Fatalf("unnumbered order = %v, want %v", got, want)
	}
	restored := slices.Clone(want)
	slices.Reverse(restored)
	if result := set(restored); !result.Success || !slices.Equal(desktopOrder(result.Desktops), restored) {
		t.Fatalf("restore order: %+v", result)
	}
	w.restart()
	if got := desktopOrder(w.App().Initial.Desktops); !slices.Equal(got, restored) {
		t.Fatalf("restarted order = %v, want %v", got, restored)
	}
}
