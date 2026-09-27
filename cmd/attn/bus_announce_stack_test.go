package main_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/pausepoint"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestASeedPlantedWhileAnotherUpdateOvertakesItsAnnouncementStillReachesTheApp(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	announce := s.PauseAt(pausepoint.BusAnnounce)
	s.Start()
	app := s.App()

	planting := s.Launch(testworld.Invocation{Args: []string{"seed", "plant", "Water the ferns", "--json"}})
	announce.Await()
	setSetting(t, app, "auto_approve_enabled", "true")
	announce.Release()
	var seed protocol.Seed
	planting.Wait().JSON(t, &seed)
	setSetting(t, app, "auto_approve_enabled", "false")

	for _, e := range app.Received() {
		if e.Event == protocol.EventGardenSeedsUpdated && slices.ContainsFunc(e.Seeds, func(s protocol.Seed) bool { return s.ID == seed.ID }) {
			return
		}
	}
	t.Errorf("the app never learned of seed %s, planted while another update's announcement overtook it", seed.ID)
}

func setSetting(t *testing.T, app *testworld.Peer, key, value string) {
	t.Helper()
	requestID := uuid.NewString()
	set := testworld.Request(app, protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: key, Value: value, RequestID: protocol.Ptr(requestID)},
		protocol.EventSettingsUpdated, func(m protocol.SettingsUpdatedMessage) bool { return protocol.Deref(m.RequestID) == requestID })
	if !protocol.Deref(set.Success) {
		t.Fatalf("set %s=%s: %s", key, value, protocol.Deref(set.Error))
	}
}
