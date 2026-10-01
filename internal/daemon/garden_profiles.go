package daemon

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) resolveGardenProfile(sessionID, requested, selected string) (profiles.Profile, error) {
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		return d.callerProfile(sessionID)
	}
	if selected != "" {
		return d.store.LiveProfile(selected)
	}
	live, err := d.store.ListProfiles(false)
	if err != nil {
		return profiles.Profile{}, err
	}
	if requested = strings.TrimSpace(requested); requested != "" {
		for _, profile := range live {
			if profile.ID == requested || strings.EqualFold(profile.Name, requested) {
				return profile, nil
			}
		}
	}
	if requested == "" && len(live) == 1 {
		return live[0], nil
	}
	choices := make([]string, 0, len(live))
	for _, profile := range live {
		choices = append(choices, fmt.Sprintf("%q (%s)", profile.Name, profile.ID))
	}
	return profiles.Profile{}, fmt.Errorf("choose --profile <name|id>; available profiles: %s", strings.Join(choices, ", "))
}

func (d *Daemon) requireSeedInProfile(id, profileID string, archive bool) error {
	seed, _, err := d.readSeed(id)
	if err != nil {
		return err
	}
	if seed.ProfileID == profileID {
		return nil
	}
	owner, err := d.store.GetProfile(seed.ProfileID)
	if err != nil {
		return err
	}
	if archive && owner.Deleted() && garden.Closed(seed.Status) {
		return nil
	}
	caller, err := d.store.GetProfile(profileID)
	if err != nil {
		return err
	}
	return fmt.Errorf("seed %s belongs to profile %q (%s); caller belongs to profile %q (%s)", id, owner.Name, owner.ID, caller.Name, caller.ID)
}

func (d *Daemon) scopeGardenRequest(cmd string, msg any, selected string) error {
	if !strings.HasPrefix(cmd, "seed_") {
		return nil
	}
	if err := d.requireHome(garden.Surface); err != nil {
		return err
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	var scope struct {
		SourceSessionID string                            `json:"source_session_id"`
		ProfileID       string                            `json:"profile_id"`
		SeedID          string                            `json:"seed_id"`
		ToSeedID        string                            `json:"to_seed_id"`
		PartOf          string                            `json:"part_of"`
		DiscoveredFrom  string                            `json:"discovered_from"`
		Plot            string                            `json:"plot"`
		Member          string                            `json:"member"`
		ReviewID        string                            `json:"review_id"`
		Review          *protocol.SeedReviewActionContext `json:"review"`
	}
	if err := json.Unmarshal(raw, &scope); err != nil {
		return err
	}
	profile, err := d.resolveGardenProfile(scope.SourceSessionID, scope.ProfileID, selected)
	if err != nil {
		return err
	}
	if selected != "" && profile.ID != selected {
		owner, err := d.store.GetProfile(selected)
		if err != nil {
			return err
		}
		return fmt.Errorf("source session belongs to profile %q; app belongs to profile %q", profile.Name, owner.Name)
	}
	archive := cmd == protocol.CmdSeedShow || cmd == protocol.CmdSeedNotes || cmd == protocol.CmdSeedDocumentGet || cmd == protocol.CmdSeedArtifactTarget
	for _, id := range []string{scope.SeedID, scope.ToSeedID, scope.PartOf, scope.DiscoveredFrom, scope.Plot} {
		if id != "" {
			if err := d.requireSeedInProfile(id, profile.ID, archive); err != nil {
				return err
			}
		}
	}
	if scope.Member != "" {
		member := d.resolveTenderMember(scope.Member, scope.SourceSessionID)
		memberProfile, err := d.store.CrewProfile(member)
		if err != nil {
			return err
		}
		existingClaim := false
		if cmd == protocol.CmdSeedTransition && scope.SeedID != "" {
			seed, _, err := d.readSeed(scope.SeedID)
			if err != nil {
				return err
			}
			existingClaim = seed.TenderMember == member
		}
		if memberProfile != "" && memberProfile != profile.ID && !existingClaim {
			owner, err := d.store.GetProfile(memberProfile)
			if err != nil {
				return err
			}
			return fmt.Errorf("crew member %s belongs to profile %q; caller belongs to profile %q", scope.Member, owner.Name, profile.Name)
		}
	}
	reviewID := scope.ReviewID
	if scope.Review != nil {
		reviewID = scope.Review.ReviewID
	}
	if reviewID != "" {
		run, _, found, err := d.readGardenReviewRun(reviewID)
		if err != nil {
			return err
		}
		if found && run.ProfileID != profile.ID {
			owner, err := d.store.GetProfile(run.ProfileID)
			if err != nil {
				return err
			}
			return fmt.Errorf("garden review %s belongs to profile %q; caller belongs to profile %q", reviewID, owner.Name, profile.Name)
		}
	}
	resolved, err := json.Marshal(struct {
		ProfileID string `json:"profile_id"`
	}{profile.ID})
	if err != nil {
		return err
	}
	return json.Unmarshal(resolved, msg)
}

func gardenProfileFilters(profileID string) []docstore.Filter {
	if profileID == "" {
		return nil
	}
	return []docstore.Filter{{Field: "profile_id", Op: docstore.OpEq, Value: profileID}}
}

func (d *Daemon) seedBirthProfile(seed garden.Seed) (string, error) {
	if seed.ProfileID != "" {
		return seed.ProfileID, nil
	}
	sessionID := seed.PlanterSession
	if sessionID == "" {
		sessionID = seed.TenderSession
	}
	profile, err := d.resolveGardenProfile(sessionID, "", "")
	if err != nil {
		return "", err
	}
	return profile.ID, nil
}

func (d *Daemon) sendGardenProfile(client *wsClient) {
	d.sendToClient(client, d.gardenProfileSnapshot(client.selectedProfile()))
}

func (d *Daemon) gardenProfileSnapshot(profileID string) *protocol.GardenSeedsUpdatedMessage {
	if profileID == "" {
		return &protocol.GardenSeedsUpdatedMessage{Event: protocol.EventGardenSeedsUpdated, Seeds: []protocol.Seed{}}
	}
	return &protocol.GardenSeedsUpdatedMessage{
		Event: protocol.EventGardenSeedsUpdated, ProfileID: profileID,
		Seeds: d.seedsForBroadcast(profileID), Total: d.countSeedsForBroadcast(profileID),
	}
}

func firstProfile(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func (d *Daemon) sendGardenScopeError(client *wsClient, cmd string, msg any, err error) {
	raw, _ := json.Marshal(msg)
	var request struct {
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(raw, &request)
	event := cmd + "_result"
	if strings.HasPrefix(cmd, "seed_review_") && cmd != protocol.CmdSeedReviewDraft {
		event = protocol.EventSeedReviewResult
	}
	d.sendToClient(client, map[string]any{"event": event, "profile_id": client.selectedProfile(), "request_id": request.RequestID, "success": false, "error": err.Error(), "operation": strings.TrimPrefix(cmd, "seed_review_")})
}
