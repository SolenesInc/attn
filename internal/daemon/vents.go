package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/analytics"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/shortid"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/transcript"
)

const (
	ventsPruneKind     = "vents_prune"
	ventsPruneInterval = time.Hour
	ventsPruneTimeout  = time.Minute
	ventsRetention     = 30 * 24 * time.Hour
)

type ventFact struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
}

func (d *Daemon) launchVentIntent(sessionID protocol.SessionID) (bool, []string) {
	if intent, ok := d.store.LaunchIntent(sessionID); ok {
		return intent.Vent, intent.Experimental
	}
	return d.analyticsEnabled(), d.experimentalSettingsOn()
}

func (d *Daemon) launchedWithVent(sessionID protocol.SessionID) bool {
	vent, _ := d.launchVentIntent(sessionID)
	return vent
}

func (d *Daemon) experimentalSettingsOn() []string {
	on := []string{}
	for _, spec := range settingSpecs {
		if spec.experimental && parseBooleanSetting(d.store.GetSetting(spec.key)) {
			on = append(on, spec.key)
		}
	}
	return on
}

func (d *Daemon) handleRecordVent(conn net.Conn, msg *protocol.RecordVentMessage) {
	vent, err := d.recordVent(msg)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	wire := vent.wire()
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, RecordVentResult: &wire})
	d.publishFact(FactVentRecorded, vent.ID, vent.fact())
}

func (d *Daemon) recordVent(msg *protocol.RecordVentMessage) (storedVent, error) {
	sessionID := protocol.TrimID(msg.SessionID)
	session := d.store.Get(sessionID)
	if sessionID == "" || session == nil {
		return storedVent{}, errSessionNotLive(sessionID)
	}
	if !d.analyticsEnabled() {
		return storedVent{}, errors.New("usage analytics is off on this machine, so attn records no vents. Keep working")
	}
	if !d.launchedWithVent(sessionID) {
		return storedVent{}, errors.New("this session launched while usage analytics was off, so it cannot vent. Keep working")
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return storedVent{}, errors.New("the vent is empty: say what happened, what you expected and what you did instead")
	}
	if len(text) > analytics.VentTextLimit {
		return storedVent{}, fmt.Errorf("the limit is %d bytes of text, and this vent has %d. Keep it to what happened, what you expected and what you did instead", analytics.VentTextLimit, len(text))
	}
	id, err := shortid.New("v-")
	if err != nil {
		return storedVent{}, err
	}
	vent := storedVent{store.Vent{
		ID: id, ProfileID: session.ProfileID, SessionID: sessionID, Text: text,
		Turn:      fmt.Sprintf("%s:%d", d.daemonInstanceID, d.monitorEntry(sessionID).LatestRun()),
		Context:   d.ventContext(session),
		CreatedAt: time.Now(),
	}}
	var taken *store.VentTurnTakenError
	switch err := d.store.AddVent(vent.Vent); {
	case errors.As(err, &taken):
		return storedVent{}, fmt.Errorf("the limit is 1 vent per turn, and this turn already has %s. Withdraw it (attn vent withdraw %s) and send one vent that covers both", taken.VentID, taken.VentID)
	case err != nil:
		return storedVent{}, err
	}
	return vent, nil
}

func (d *Daemon) ventContext(session *protocol.Session) protocol.VentContext {
	intent, _ := d.store.LaunchIntent(session.ID)
	_, experimental := d.launchVentIntent(session.ID)
	build := analytics.CurrentBuild()
	context := protocol.VentContext{
		SessionLabel: session.Label,
		Harness:      session.Agent,
		Build: protocol.AnalyticsBuild{
			Version: build.Version, Commit: build.Commit, Channel: build.Channel, BuildTime: build.BuildTime,
			Fingerprint: build.Fingerprint, Protocol: build.Protocol, Os: build.OS, Arch: build.Arch, Dirty: build.Dirty,
		},
		Experimental: experimental,
	}
	if context.Experimental == nil {
		context.Experimental = []string{}
	}
	if model := d.leadingAgentModel(session); model != "" {
		context.Model = &model
	} else if intent.Model != "" {
		context.Model = protocol.Ptr(intent.Model)
	}
	if seed, ok := d.gardenDispatchCrown(session.ID); ok && seed != "" {
		context.SeedID = &seed
	}
	if path := d.inspectableTranscriptPath(session); path != "" {
		if info, err := os.Stat(path); err == nil {
			context.Transcript = &protocol.VentTranscript{Path: path, Bytes: int(info.Size())}
		}
		if version := transcript.HarnessVersion(path, session.Agent); version != "" {
			context.HarnessVersion = &version
		}
	}
	return context
}

func (d *Daemon) leadingAgentModel(session *protocol.Session) string {
	clone := cloneSession(session)
	d.decorateSessionWithCost(clone)
	if clone.Usage == nil {
		return ""
	}
	leading, most := "", -1
	for _, row := range clone.Usage.Models {
		if row.Purpose == "agent" && row.TotalTokens > most {
			leading, most = row.Model, row.TotalTokens
		}
	}
	return leading
}

func (d *Daemon) handleWithdrawVent(conn net.Conn, msg *protocol.WithdrawVentMessage) {
	sessionID, id := protocol.TrimID(msg.SessionID), strings.TrimSpace(msg.VentID)
	withdrawn, err := d.store.WithdrawVent(sessionID, id, time.Now())
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if !withdrawn {
		d.sendError(conn, fmt.Sprintf("%s is not a vent this session can withdraw", id))
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true})
	d.publishVentFact(FactVentWithdrawn, id)
}

func (d *Daemon) handleSetVentDismissed(client *wsClient, msg *protocol.SetVentDismissedMessage) {
	result := protocol.SetVentDismissedResultMessage{Event: protocol.EventSetVentDismissedResult, RequestID: msg.RequestID, VentID: msg.VentID}
	if err := d.setVentDismissed(msg, client.selectedProfile()); err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
	}
	d.sendToClient(client, result)
}

func (d *Daemon) setVentDismissed(msg *protocol.SetVentDismissedMessage, profileID string) error {
	id := strings.TrimSpace(msg.VentID)
	vent, err := d.store.Vent(id)
	if errors.Is(err, store.ErrVentNotFound) || (err == nil && vent.ProfileID != profileID) {
		return fmt.Errorf("no vent %s in this profile", id)
	}
	if err != nil {
		return err
	}
	changed, err := d.store.SetVentDismissed(id, msg.Dismissed, time.Now())
	if err != nil || !changed {
		return err
	}
	fact := FactVentRestored
	if msg.Dismissed {
		fact = FactVentDismissed
	}
	d.publishFact(fact, id, storedVent{vent}.fact())
	return nil
}

func (d *Daemon) publishVentFact(name, id string) {
	vent, err := d.store.Vent(id)
	if err != nil {
		d.logf("vent %s: %v", id, err)
		return
	}
	d.publishFact(name, id, storedVent{vent}.fact())
}

func (d *Daemon) projectVents() {
	if d.store == nil || d.wsHub == nil {
		return
	}
	d.projectSnapshot(snapshotVents, func() {
		snapshots := map[string][]byte{}
		d.wsHub.ForEachClient(func(client *wsClient) {
			profileID := client.selectedProfile()
			data, ok := snapshots[profileID]
			if !ok {
				var err error
				data, err = json.Marshal(d.ventsSnapshot(profileID))
				if err != nil {
					d.logf("vents snapshot: %v", err)
					return
				}
				snapshots[profileID] = data
			}
			d.sendOutbound(client, outboundMessage{kind: messageKindText, payload: data})
		})
	})
}

func (d *Daemon) ventsSnapshot(profileID string) *protocol.VentsUpdatedMessage {
	message := &protocol.VentsUpdatedMessage{Event: protocol.EventVentsUpdated, ProfileID: profileID, Vents: []protocol.Vent{}}
	if profileID == "" {
		return message
	}
	vents, err := d.store.Vents(profileID)
	if err != nil {
		d.logf("vents snapshot for profile %s: %v", profileID, err)
		return message
	}
	for _, vent := range vents {
		message.Vents = append(message.Vents, storedVent{vent}.wire())
	}
	return message
}

func (d *Daemon) ventsPruneHandler(_ context.Context, _ *jobs.Job) (any, error) {
	pruned, err := d.store.PruneVents(time.Now().Add(-ventsRetention))
	if err != nil {
		return nil, fmt.Errorf("pruning vents: %w", err)
	}
	return map[string]any{"pruned": pruned}, nil
}

type storedVent struct{ store.Vent }

func (v storedVent) fact() ventFact {
	return ventFact{Harness: v.Context.Harness, Model: protocol.Deref(v.Context.Model)}
}

func (v storedVent) wire() protocol.Vent {
	wire := protocol.Vent{ID: v.ID, SessionID: v.SessionID, Text: v.Text, Context: v.Context, CreatedAt: v.CreatedAt.UTC().Format(time.RFC3339Nano)}
	if !v.WithdrawnAt.IsZero() {
		wire.WithdrawnAt = protocol.Ptr(v.WithdrawnAt.UTC().Format(time.RFC3339Nano))
	}
	if !v.DismissedAt.IsZero() {
		wire.DismissedAt = protocol.Ptr(v.DismissedAt.UTC().Format(time.RFC3339Nano))
	}
	return wire
}
