package daemon

import (
	"strings"
	"testing"
)

func TestCrewDocuments_OutpostsAreRefused(t *testing.T) {
	const home = "d-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	outpost := newEnrolledDaemon(t, home)
	t.Cleanup(outpost.stopEventBus)
	writeCrewHomes(t, outpost.dataRoot)
	outpost.ensureCrewCollections()
	outpost.importCrewHomes()
	_, err := outpost.crewCharterGet("alder")
	if err == nil || !strings.Contains(err.Error(), home) {
		t.Fatalf("outpost read error = %v", err)
	}
}
