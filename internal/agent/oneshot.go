package agent

import (
	"context"
	"fmt"
	"os"

	"github.com/victorarias/attn/internal/harness"
)

// RunOneShot answers req with the harness CLI at executable, its tools off, in a scratch folder when req.Dir is "".
func RunOneShot(ctx context.Context, provider HeadlessTaskProvider, executable string, req harness.OneShotRequest) (harness.OneShotResult, error) {
	dir := req.Dir
	if dir == "" {
		scratch, err := os.MkdirTemp("", "attn-one-shot-*")
		if err != nil {
			return harness.OneShotResult{}, fmt.Errorf("create the call's scratch directory: %w", err)
		}
		defer os.RemoveAll(scratch)
		dir = scratch
	}
	run, err := provider.RunHeadlessTask(ctx, HeadlessTaskRequest{
		Executable:      executable,
		Model:           req.Model,
		ReasoningEffort: req.Effort,
		SystemPrompt:    req.System,
		Prompt:          req.Input,
		WorkDir:         dir,
		DisableTools:    true,
		MaxTurns:        req.Bound.MaxTurns,
		MaxBudgetUSD:    req.Bound.MaxCostUSD,
		OutputSchema:    req.Schema,
	})
	return harness.OneShotResult{
		Text:          run.Text,
		Structured:    run.StructuredOutput,
		Diagnostics:   run.Diagnostics,
		FailureOutput: run.FailureOutput,
		Usage: harness.OneShotUsage{
			In: run.InputTokens, Out: run.OutputTokens, CostUSD: run.TotalCostUSD, ModelTime: run.ModelTime, Turns: run.NumTurns,
		},
	}, err
}
