package daemon

import (
	"testing"

	"github.com/victorarias/attn/internal/garden"
)

func newGardenDelegationDaemon(t *testing.T) (*Daemon, *fakeSpawnBackend, string) {
	t.Helper()
	d := newEnrolledDaemon(t, "")
	t.Cleanup(d.stopEventBus)
	d.ensureGardenCollections()
	backend := &fakeSpawnBackend{}
	_, sourceSessionID, _ := setupDelegationSource(t, d, backend)
	return d, backend, sourceSessionID
}

func tendAs(t *testing.T, d *Daemon, seedID, sessionID string) {
	t.Helper()
	if _, _, err := d.applySeedTransition(seedID, garden.VerbTend, garden.Ask{Actor: garden.Tender{Session: sessionID}}); err != nil {
		t.Fatalf("tend %s as %s: %v", seedID, sessionID, err)
	}
}
