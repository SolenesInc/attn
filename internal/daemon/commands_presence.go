package daemon

import (
	"context"
	"slices"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) presenceCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"presence": command.Typed(func(_ context.Context, c command.Caller, _ command.PresenceArgs) (any, error) {
			return d.presence(c)
		}),
	}
}

func (d *Daemon) presence(c command.Caller) (protocol.PresenceResult, error) {
	result := protocol.PresenceResult{SessionID: c.Session, SeedIds: []string{}}
	if c.Session == "" {
		return result, nil
	}
	if session := d.store.Get(c.Session); session != nil {
		if stake, ok := d.gardenDispatchCrown(c.Session); ok && stake != "" {
			if seed, _, err := d.readSeed(stake); err == nil && seed.ProfileID == session.ProfileID {
				result.SeedIds = append(result.SeedIds, stake)
			}
		}
	}
	msg := seedListRequest{Cmd: cmdSeedList}
	msg.SourceSessionID, msg.ProfileID = gardenScope(c)
	if err := d.scoped(&msg, msg.Cmd); err != nil {
		return protocol.PresenceResult{}, err
	}
	listed, err := d.seedList(&msg)
	if err != nil {
		return protocol.PresenceResult{}, err
	}
	for _, seed := range listed.Seeds {
		if seed.Tender != nil && protocol.Deref(seed.Tender.SessionID) == c.Session && seed.Status == garden.StatusGrowing && !slices.Contains(result.SeedIds, seed.ID) {
			result.SeedIds = append(result.SeedIds, seed.ID)
		}
	}
	return result, nil
}
