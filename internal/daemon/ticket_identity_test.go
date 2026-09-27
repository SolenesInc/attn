package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/store"
)

func TestTicketIdentityRoundTripsForChiefSession(t *testing.T) {
	d, _ := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "chief", "Chief")
	if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, "chief"); err != nil {
		t.Fatal(err)
	}

	observers := d.ticketObserversForSession("chief")
	if len(observers) != 2 {
		t.Fatalf("chief observers = %+v, want its session identity plus the durable role", observers)
	}
	for _, obs := range observers {
		if got := d.ticketSessionForIdentity(obs.ID); got != "chief" {
			t.Fatalf("inverse of %q = %q, want chief", obs.ID, got)
		}
		if obs.AuthorID != "chief" || obs.DeliveryID != "chief" {
			t.Fatalf("observer %+v must author and deliver as the chief session", obs)
		}
	}
	roleIdentity := store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)
	if observers[1].ID != roleIdentity {
		t.Fatalf("second observer = %q, want %q", observers[1].ID, roleIdentity)
	}
	if got := d.ticketAttentionKey("chief"); got != roleIdentity {
		t.Fatalf("attention key = %q, want %q", got, roleIdentity)
	}
}

func TestTicketIdentityFollowsRoleTransfer(t *testing.T) {
	d, _ := newChiefOfStaffTestDaemon(t)
	addChiefOfStaffTestSession(d, "chief-a", "Chief A")
	addChiefOfStaffTestSession(d, "chief-b", "Chief B")
	roleIdentity := store.TicketRoleIdentity(store.TicketRoleChiefOfStaff)

	if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, "chief-a"); err != nil {
		t.Fatal(err)
	}
	if got := d.ticketSessionForIdentity(roleIdentity); got != "chief-a" {
		t.Fatalf("role delivers to %q, want chief-a", got)
	}

	if err := d.store.SetInstanceRole(instanceRoleChiefOfStaff, "chief-b"); err != nil {
		t.Fatal(err)
	}
	if got := d.ticketSessionForIdentity(roleIdentity); got != "chief-b" {
		t.Fatalf("after transfer the role delivers to %q, want chief-b", got)
	}
	if len(d.ticketObserversForSession("chief-a")) != 1 {
		t.Fatalf("the former chief still observes through the role identity")
	}
	if got := d.ticketAttentionKey("chief-a"); got != "chief-a" {
		t.Fatalf("former chief attention key = %q, want its own session", got)
	}
}
