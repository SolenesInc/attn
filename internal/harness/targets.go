package harness

import (
	"context"

	"github.com/victorarias/attn/internal/protocol"
)

// Targets is the optional part of a harness whose sessions can run on an execution target it lists.
type Targets interface {
	Targets(ctx context.Context, cwd string) ([]protocol.ExecutionTarget, error)
}
