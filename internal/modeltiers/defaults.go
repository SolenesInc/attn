package modeltiers

import "github.com/victorarias/attn/internal/protocol"

func FirstOf(models []protocol.HarnessModel, tier Tier) (protocol.HarnessModel, bool) {
	for _, model := range models {
		if model.Tier != nil && Tier(*model.Tier) == tier && model.TierSource != protocol.ModelTierSourceAlias && model.Access != protocol.ModelCapabilitySupportUnsupported {
			return model, true
		}
	}
	return protocol.HarnessModel{}, false
}
