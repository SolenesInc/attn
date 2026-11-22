package daemon

import (
	"context"
	"strings"

	harnessapi "github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/protocol"
)

var installationOnWire = map[harnessapi.Installation]protocol.HarnessInstallation{
	harnessapi.Installed: protocol.HarnessInstallationInstalled,
	harnessapi.Missing:   protocol.HarnessInstallationMissing,
	harnessapi.Unknown:   protocol.HarnessInstallationChecking,
}

func (d *Daemon) harnessCatalog() []protocol.Harness {
	ctx := d.life.Context()
	catalog := []protocol.Harness{}
	for _, h := range d.harnesses() {
		catalog = append(catalog, harnessRecord(ctx, h))
	}
	return catalog
}

func harnessRecord(ctx context.Context, h harnessapi.Harness) protocol.Harness {
	facts := h.Facts()
	health := h.Health(ctx)
	_, oneShot := harnessapi.As[harnessapi.OneShot](h)
	record := protocol.Harness{
		ID:           facts.ID,
		Name:         facts.DisplayName,
		Offered:      health.Offered,
		Installation: installationOnWire[health.Installation],
		Options: protocol.HarnessOptions{
			Resume:                facts.Options.Resume,
			Yolo:                  facts.Options.Yolo,
			AutoApprove:           facts.Options.AutoApprove,
			AutoApproveUnreviewed: facts.Options.AutoApprove && facts.Options.AutoApproveUnreviewed,
			AutomationExecutable:  facts.Options.AutomationExecutable,
			InitialPrompt:         facts.Options.InitialPrompt,
			LaunchInstructions:    facts.Options.LaunchInstructions,
			ModelPin:              facts.Options.ModelPin,
			EffortPin:             facts.Options.EffortPin,
			Discovery:             facts.ModelDiscovery,
			ContextCap:            facts.Options.ContextCap,
			Provider:              facts.Options.Provider,
			ExecutableSetting:     facts.Executable.Default != "",
			OneShot:               oneShot,
			Activity:              facts.Activity,
			KeepsConversations:    keepsConversations(h),
		},
	}
	if effort := facts.Tiers[modeltiers.Light].Effort; effort != "" {
		record.ActivityEffort = protocol.Ptr(effort)
	}
	if effort := facts.Advisor.Effort; effort != "" {
		record.AdvisorEffort = protocol.Ptr(effort)
	}
	if hash := facts.Logo.Hash(); hash != "" {
		record.Logo = protocol.Ptr(hash)
	}
	return record
}

func (d *Daemon) harnessCatalogMessage() protocol.HarnessCatalogMessage {
	return protocol.HarnessCatalogMessage{Event: protocol.EventHarnessCatalog, Harnesses: d.harnessCatalog()}
}

func (d *Daemon) projectHarnessCatalog() {
	d.projectSnapshot(snapshotHarnessCatalog, func() {
		message := d.harnessCatalogMessage()
		d.wsHub.ForEachClient(func(client *wsClient) { d.sendToClient(client, message) })
	})
}

func (d *Daemon) handleHarnessLogo(client *wsClient, msg *protocol.HarnessLogoMessage) {
	result := protocol.HarnessLogoResultMessage{Event: protocol.EventHarnessLogoResult, RequestID: msg.RequestID}
	hash := strings.TrimSpace(msg.Hash)
	for _, h := range d.harnesses() {
		if logo := h.Facts().Logo; hash != "" && logo.Hash() == hash {
			result.Success = true
			result.Logo = &protocol.HarnessLogo{View: logo.View, Body: logo.Body}
			d.sendToClient(client, result)
			return
		}
	}
	result.Error = protocol.Ptr("no harness has logo " + hash)
	d.sendToClient(client, result)
}
