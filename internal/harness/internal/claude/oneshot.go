package claude

import (
	"context"
	"fmt"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/headless"
)

func (h claudeHarness) Run(ctx context.Context, req harness.OneShotRequest) (harness.OneShotResult, error) {
	executable, err := h.host.Executable(h.Facts(), "")
	if err != nil {
		return harness.OneShotResult{}, fmt.Errorf("resolve claude executable: %w", err)
	}
	return headless.RunOneShot(ctx, claudeCLI{}, executable, req)
}

func (h claudeHarness) Models(ctx context.Context) (harness.Models, error) {
	executable, err := h.host.Executable(h.Facts(), "")
	if err != nil {
		return harness.Models{}, err
	}
	return harness.DiscoverModels(ctx, claudeModelDiscoverer{host: h.host}, executable)
}

func (h claudeHarness) StartSpare(ctx context.Context) (harness.Spare, error) {
	executable, err := h.host.Executable(h.Facts(), "")
	if err != nil {
		return nil, fmt.Errorf("claude executable was not found: %w", err)
	}
	return startSpare(ctx, h.host, executable)
}
