package harness

import (
	"context"
	"os"

	"github.com/victorarias/attn/internal/protocol"
)

type ModelDiscoverer interface {
	DiscoverHarnessModels(context.Context, string, string) ([]protocol.HarnessModel, error)
}

// DiscoverModels asks the harness CLI at executable for its models, from a scratch folder.
func DiscoverModels(ctx context.Context, discoverer ModelDiscoverer, executable string) (Models, error) {
	cwd, err := os.MkdirTemp("", "attn-model-discovery-")
	if err != nil {
		return Models{}, err
	}
	defer os.RemoveAll(cwd)
	models, err := discoverer.DiscoverHarnessModels(ctx, executable, cwd)
	if err != nil {
		return Models{}, err
	}
	return Models{List: models, Detail: "Models reported by this harness. Catalog membership does not confirm account access."}, nil
}
