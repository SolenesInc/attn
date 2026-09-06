package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestBroadcastSessionResolvesItsRecordedDispatcher(t *testing.T) {
	tests := []struct {
		name         string
		recordMember bool
		afterRecord  func(*testing.T, *Daemon)
		wantSession  string
		wantMember   string
	}{
		{
			name:         "live dispatcher",
			recordMember: true,
			wantSession:  "dispatcher-old",
			wantMember:   "alder",
		},
		{
			name:         "ended dispatcher with an awake member",
			recordMember: true,
			afterRecord: func(t *testing.T, d *Daemon) {
				d.releaseExitedCrewBinding("dispatcher-old")
				if d.store.Get("dispatcher-old") == nil {
					t.Fatal("crew exit removed the historical dispatcher session")
				}
				addSession(t, d, "dispatcher-new")
				if _, err := d.claimCrewBinding("alder", "dispatcher-new"); err != nil {
					t.Fatalf("rebind alder: %v", err)
				}
			},
			wantSession: "dispatcher-new",
			wantMember:  "alder",
		},
		{
			name:         "ended dispatcher with a sleeping member",
			recordMember: true,
			afterRecord: func(t *testing.T, d *Daemon) {
				d.releaseExitedCrewBinding("dispatcher-old")
				if d.store.Get("dispatcher-old") == nil {
					t.Fatal("crew exit removed the historical dispatcher session")
				}
			},
			wantMember: "alder",
		},
		{
			name: "ended dispatcher with no member",
			afterRecord: func(_ *testing.T, d *Daemon) {
				d.store.Remove("dispatcher-old")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d := newCrewDaemon(t)
			d.ensureGardenCollections()
			addSession(t, d, "dispatcher-old")
			addSession(t, d, "delegate")
			if test.recordMember {
				if _, err := d.claimCrewBinding("alder", "dispatcher-old"); err != nil {
					t.Fatalf("bind alder: %v", err)
				}
			}
			if err := d.recordGardenDispatch("delegate", "s-source", "dispatcher-old", "", "", false); err != nil {
				t.Fatalf("record dispatch: %v", err)
			}
			dispatch, ok := d.gardenDispatch("delegate")
			if !ok || dispatch.DispatcherMember != test.wantMember {
				t.Fatalf("recorded dispatch = %+v, want member %q", dispatch, test.wantMember)
			}
			if test.afterRecord != nil {
				test.afterRecord(t, d)
			}

			got := d.sessionForBroadcast(d.store.Get("delegate"))
			if got == nil {
				t.Fatal("delegate disappeared from the broadcast")
			}
			if sessionID := protocol.Deref(got.DispatcherSessionID); sessionID != test.wantSession {
				t.Fatalf("dispatcher_session_id = %q, want %q", sessionID, test.wantSession)
			}
			if member := protocol.Deref(got.DispatcherMember); member != test.wantMember {
				t.Fatalf("dispatcher_member = %q, want %q", member, test.wantMember)
			}
		})
	}
}
