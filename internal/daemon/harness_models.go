package daemon

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/protocol"
)

type harnessModelCatalog struct {
	Models []protocol.HarnessModel `json:"models"`
	Detail string                  `json:"detail"`
}

func (d *Daemon) discoverHarnessModels(ctx context.Context, harness string) (harnessModelCatalog, error) {
	if err := delegationprefs.ValidateSelection(delegationprefs.Selection{Harness: harness}, true); err != nil {
		return harnessModelCatalog{}, err
	}
	executable := d.store.GetSetting(executableSettingKey(harness))
	value, err, _ := d.harnessModelQueries.Do(harness+"\x00"+executable, func() (any, error) {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		defer context.AfterFunc(d.life.Context(), cancel)()
		result := harnessModelCatalog{Models: []protocol.HarnessModel{}}
		if plugin, ok := d.ensurePluginRegistry().driver(harness); ok {
			if !plugin.Capabilities["model_discovery"] {
				result.Detail = "This harness does not expose model discovery. Add an exact model or use its default."
				return result, nil
			}
			if err := d.callPlugin(ctx, plugin.PluginName, "driver.models", map[string]string{"agent": harness}, &result); err != nil {
				return nil, err
			}
		} else {
			driver := agentdriver.Get(harness)
			if driver == nil {
				return nil, fmt.Errorf("harness %q is not available", harness)
			}
			discoverer, ok := driver.(agentdriver.ModelDiscoverer)
			if !ok {
				result.Detail = "This harness does not expose model discovery. Add an exact model or use its default."
				return result, nil
			}
			cwd, err := os.MkdirTemp("", "attn-model-discovery-")
			if err != nil {
				return nil, err
			}
			defer os.RemoveAll(cwd)
			result.Models, err = discoverer.DiscoverHarnessModels(ctx, executable, cwd)
			if err != nil {
				return nil, err
			}
			result.Detail = "Models reported by this harness. Catalog membership does not confirm account access."
		}
		if result.Models == nil {
			result.Models = []protocol.HarnessModel{}
		}
		for i := range result.Models {
			m := &result.Models[i]
			if m.Harness != harness || strings.TrimSpace(m.ID) == "" {
				return nil, fmt.Errorf("model discovery returned an invalid identity")
			}
			if err := delegationprefs.ValidateSelection(delegationprefs.Selection{Harness: harness, Provider: m.Provider, Model: m.ID}, true); err != nil {
				return nil, err
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
	})
	if err != nil {
		return harnessModelCatalog{}, err
	}
	return value.(harnessModelCatalog), nil
}

func (d *Daemon) handleHarnessModels(client *wsClient, msg *protocol.HarnessModelsMessage) {
	result := protocol.HarnessModelsResultMessage{Event: protocol.EventHarnessModelsResult, RequestID: msg.RequestID, Models: []protocol.HarnessModel{}}
	if strings.TrimSpace(msg.RequestID) == "" {
		result.Error = protocol.Ptr("missing request id")
		d.sendToClient(client, result)
		return
	}
	catalog, err := d.discoverHarnessModels(context.Background(), strings.TrimSpace(msg.Harness))
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
		result.Models = catalog.Models
		result.Detail = catalog.Detail
	}
	d.sendToClient(client, result)
}
