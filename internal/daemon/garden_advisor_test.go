package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/harness"
)

type gardenAdvisorOneShotFunc func(context.Context, harness.OneShotRequest) (harness.OneShotResult, error)

func (f gardenAdvisorOneShotFunc) Run(ctx context.Context, request harness.OneShotRequest) (harness.OneShotResult, error) {
	return f(ctx, request)
}

func TestExecuteGardenAdvisorHonorsCancellationAndTimeout(t *testing.T) {
	oneShot := gardenAdvisorOneShotFunc(func(ctx context.Context, _ harness.OneShotRequest) (harness.OneShotResult, error) {
		<-ctx.Done()
		return harness.OneShotResult{}, ctx.Err()
	})
	config := gardenAdvisorConfig{Agent: "codex", Model: "gpt-5.6-luna", Effort: "xhigh"}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := executeGardenAdvisor(canceled, oneShot, config, gardenAdviceTask, gardenAdvisorInput{}, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled run error = %v, want context.Canceled", err)
	}

	if _, err := executeGardenAdvisor(context.Background(), oneShot, config, gardenAdviceTask, gardenAdvisorInput{}, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed out run error = %v, want context.DeadlineExceeded", err)
	}
}
