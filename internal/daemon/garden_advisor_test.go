package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
)

type gardenAdvisorProviderFunc func(
	context.Context,
	agentdriver.HeadlessTaskRequest,
) (agentdriver.HeadlessTaskResult, error)

func (f gardenAdvisorProviderFunc) RunHeadlessTask(
	ctx context.Context,
	request agentdriver.HeadlessTaskRequest,
) (agentdriver.HeadlessTaskResult, error) {
	return f(ctx, request)
}

func TestExecuteGardenAdvisorHonorsCancellationAndTimeout(t *testing.T) {
	provider := gardenAdvisorProviderFunc(func(ctx context.Context, _ agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		<-ctx.Done()
		return agentdriver.HeadlessTaskResult{}, ctx.Err()
	})
	config := gardenAdvisorConfig{Agent: "codex", Model: "gpt-5.6-luna", Effort: "xhigh"}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := executeGardenAdvisor(canceled, provider, "fake", config, gardenAdviceTask, gardenAdvisorInput{}, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled run error = %v, want context.Canceled", err)
	}

	if _, err := executeGardenAdvisor(context.Background(), provider, "fake", config, gardenAdviceTask, gardenAdvisorInput{}, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed out run error = %v, want context.DeadlineExceeded", err)
	}
}
