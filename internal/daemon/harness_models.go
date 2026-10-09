package daemon

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/protocol"
)

type harnessModelCatalog struct {
	Models       []protocol.HarnessModel `json:"models"`
	Detail       string                  `json:"detail"`
	TierDefaults protocol.TierDefaults   `json:"tier_defaults"`
}

type harnessDiscovery struct {
	Catalog harnessModelCatalog
	Err     error
}

func (d *Daemon) loadHarnessModels(ctx context.Context, harness, executable string, refresh bool) (harnessModelCatalog, error) {
	if err := ctx.Err(); err != nil {
		return harnessModelCatalog{}, err
	}
	if err := delegationprefs.ValidateSelection(delegationprefs.Selection{Harness: harness}, true); err != nil {
		return harnessModelCatalog{}, err
	}
	if executable == "" {
		executable = d.store.GetSetting(executableSettingKey(harness))
	}
	key := harness + "\x00" + executable
	if refresh {
		d.harnessModelCatalogs.Delete(key)
	}
	if cached, ok := d.harnessModelCatalogs.Load(key); ok {
		entry := cached.(harnessDiscovery)
		return entry.Catalog, entry.Err
	}
	flight := d.harnessModelQueries.DoChan(key, func() (any, error) {
		var entry harnessDiscovery
		if !d.life.Do("harness model discovery", func() {
			if cached, ok := d.harnessModelCatalogs.Load(key); ok {
				entry = cached.(harnessDiscovery)
				return
			}
			entry.Catalog, entry.Err = d.queryHarnessModels(harness, executable)
			d.harnessModelCatalogs.Store(key, entry)
		}) {
			return nil, errDaemonStopping
		}
		return entry, nil
	})
	select {
	case <-ctx.Done():
		return harnessModelCatalog{}, ctx.Err()
	case result := <-flight:
		if result.Err != nil {
			return harnessModelCatalog{}, result.Err
		}
		entry := result.Val.(harnessDiscovery)
		return entry.Catalog, entry.Err
	}
}

func (d *Daemon) queryHarnessModels(harness, executable string) (harnessModelCatalog, error) {
	ctx, cancel := context.WithTimeout(d.life.Context(), time.Minute)
	defer cancel()
	result := harnessModelCatalog{Models: []protocol.HarnessModel{}}
	if plugin, ok := d.ensurePluginRegistry().driver(harness); ok {
		if !plugin.Capabilities["model_discovery"] {
			result.Detail = "This harness does not expose model discovery. Add an exact model or use its default."
			return result, nil
		}
		if err := d.callPlugin(ctx, plugin.PluginName, "driver.models", map[string]string{"agent": harness}, &result); err != nil {
			return harnessModelCatalog{}, err
		}
	} else {
		driver := agentdriver.Get(harness)
		if driver == nil {
			return harnessModelCatalog{}, fmt.Errorf("harness %q is not available", harness)
		}
		discoverer, ok := driver.(agentdriver.ModelDiscoverer)
		if !ok {
			result.Detail = "This harness does not expose model discovery. Add an exact model or use its default."
			return result, nil
		}
		cwd, err := os.MkdirTemp("", "attn-model-discovery-")
		if err != nil {
			return harnessModelCatalog{}, err
		}
		defer os.RemoveAll(cwd)
		result.Models, err = discoverer.DiscoverHarnessModels(ctx, executable, cwd)
		if err != nil {
			return harnessModelCatalog{}, err
		}
		result.Detail = "Models reported by this harness. Catalog membership does not confirm account access."
	}
	if result.Models == nil {
		result.Models = []protocol.HarnessModel{}
	}
	for i := range result.Models {
		m := &result.Models[i]
		if m.Harness != harness || strings.TrimSpace(m.ID) == "" {
			return harnessModelCatalog{}, fmt.Errorf("model discovery returned an invalid identity")
		}
		if err := delegationprefs.ValidateSelection(delegationprefs.Selection{Harness: harness, Provider: m.Provider, Model: m.ID}, true); err != nil {
			return harnessModelCatalog{}, err
		}
		if m.EffortSupport != "supported" && m.EffortSupport != "unsupported" {
			m.EffortSupport = protocol.ModelCapabilitySupportUnknown
		}
		if m.Access != "supported" && m.Access != "unsupported" {
			m.Access = protocol.ModelCapabilitySupportUnknown
		}
		if m.EffortLevels == nil {
			m.EffortLevels = []string{}
		}
	}
	return result, nil
}

func (d *Daemon) invalidateHarnessModels(harness string) {
	d.harnessModelCatalogs.Range(func(key, value any) bool {
		if strings.HasPrefix(key.(string), harness+"\x00") {
			d.harnessModelCatalogs.Delete(key)
		}
		return true
	})
}

func (d *Daemon) discoverHarnessModels(ctx context.Context, harness string) (harnessModelCatalog, error) {
	return d.harnessModels(ctx, harness, "", false)
}

func (d *Daemon) harnessModels(ctx context.Context, harness, executable string, refresh bool) (harnessModelCatalog, error) {
	catalog, err := d.loadHarnessModels(ctx, harness, executable, refresh)
	if err != nil {
		return harnessModelCatalog{}, err
	}
	overrides, err := modeltiers.ParseOverrides(d.store.GetSetting(SettingModelTierOverrides))
	if err != nil {
		return harnessModelCatalog{}, err
	}
	catalog.Models = append([]protocol.HarnessModel{}, catalog.Models...)
	catalog.TierDefaults = protocol.TierDefaults{}
	for i := range catalog.Models {
		model := &catalog.Models[i]
		assignment := modeltiers.Assign(overrides, modeltiers.Key{Harness: harness, Provider: model.Provider, Model: model.ID})
		model.Tier, model.ShippedTier = nil, nil
		if assignment.Tier != "" {
			model.Tier = protocol.Ptr(protocol.ModelTier(assignment.Tier))
		}
		if assignment.Shipped != "" {
			model.ShippedTier = protocol.Ptr(protocol.ModelTier(assignment.Shipped))
		}
		model.TierSource = protocol.ModelTierSource(assignment.Source)
	}
	if model, ok := modeltiers.FirstOf(catalog.Models, modeltiers.Light); ok {
		catalog.TierDefaults.Light = protocol.Ptr(model.ID)
	}
	if model, ok := modeltiers.FirstOf(catalog.Models, modeltiers.Standard); ok {
		catalog.TierDefaults.Standard = protocol.Ptr(model.ID)
	}
	if model, ok := modeltiers.FirstOf(catalog.Models, modeltiers.Deep); ok {
		catalog.TierDefaults.Deep = protocol.Ptr(model.ID)
	}
	return catalog, nil
}

func (d *Daemon) resolveTierModel(ctx context.Context, harness, executable string, tier modeltiers.Tier, explicit, fallback string) string {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return explicit
	}
	catalog, err := d.harnessModels(ctx, harness, executable, false)
	if err != nil {
		d.logf("%s %s model discovery failed: %v; using fallback %q", harness, tier, err, fallback)
		return fallback
	}
	if model, ok := modeltiers.FirstOf(catalog.Models, tier); ok {
		if model.Provider != "" {
			return model.Provider + "/" + model.ID
		}
		return model.ID
	}
	return fallback
}

func (d *Daemon) handleHarnessModels(client *wsClient, msg *protocol.HarnessModelsMessage) {
	result := protocol.HarnessModelsResultMessage{Event: protocol.EventHarnessModelsResult, RequestID: msg.RequestID, Models: []protocol.HarnessModel{}}
	if strings.TrimSpace(msg.RequestID) == "" {
		result.Error = protocol.Ptr("missing request id")
		d.sendToClient(client, result)
		return
	}
	catalog, err := d.harnessModels(context.Background(), strings.TrimSpace(msg.Harness), "", protocol.Deref(msg.Refresh))
	if err != nil {
		result.Error = protocol.Ptr(err.Error() + ". Refresh models after fixing the harness.")
	} else {
		result.Success = true
		result.Models = catalog.Models
		result.Detail = catalog.Detail
		result.TierDefaults = catalog.TierDefaults
	}
	d.sendToClient(client, result)
}
