package daemon

import (
	"context"
	"fmt"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/headless"
)

func (h claudeHarness) cli() *agentdriver.Claude { return h.driver().(*agentdriver.Claude) }

func (h claudeHarness) Run(ctx context.Context, req harness.OneShotRequest) (harness.OneShotResult, error) {
	executable, err := h.d.machineAgentExecutable(h.Facts().ID, "")
	if err != nil {
		return harness.OneShotResult{}, fmt.Errorf("resolve claude executable: %w", err)
	}
	return headless.RunOneShot(ctx, h.cli(), executable, req)
}

func (h claudeHarness) Models(ctx context.Context) (harness.Models, error) {
	executable, err := h.d.machineAgentExecutable(h.Facts().ID, "")
	if err != nil {
		return harness.Models{}, err
	}
	return harness.DiscoverModels(ctx, h.cli(), executable)
}

func (h claudeHarness) StartSpare(ctx context.Context) (harness.Spare, error) {
	executable, err := h.d.machineAgentExecutable(h.Facts().ID, "")
	if err != nil {
		return nil, fmt.Errorf("claude executable was not found: %w", err)
	}
	return h.cli().StartSpare(ctx, executable)
}
