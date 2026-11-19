package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ModelCallRequest struct {
	Executable      string
	Model           string
	ReasoningEffort string
	SystemPrompt    string
	Input           string
	WorkDir         string
}

type ModelCallResult struct {
	Text                      string
	ModelTime                 time.Duration
	InputTokens, OutputTokens int
	CostUSD                   float64
}

func AnswerModelCall(ctx context.Context, provider HeadlessTaskProvider, request ModelCallRequest) (ModelCallResult, error) {
	run, err := provider.RunHeadlessTask(ctx, HeadlessTaskRequest{
		Executable:      request.Executable,
		Model:           request.Model,
		ReasoningEffort: request.ReasoningEffort,
		SystemPrompt:    request.SystemPrompt,
		Prompt:          request.Input,
		WorkDir:         request.WorkDir,
		DisableTools:    true,
	})
	if err != nil {
		if run.Diagnostics != "" {
			return ModelCallResult{}, errors.New(run.Diagnostics)
		}
		return ModelCallResult{}, err
	}
	return modelCallAnswer(run.Text, ModelCallResult{
		ModelTime: run.ModelTime, InputTokens: run.InputTokens, OutputTokens: run.OutputTokens, CostUSD: run.TotalCostUSD,
	})
}

func modelCallAnswer(text string, result ModelCallResult) (ModelCallResult, error) {
	result.Text = strings.TrimSpace(text)
	if result.Text == "" {
		return ModelCallResult{}, errors.New("the model returned no text")
	}
	return result, nil
}

// SpareStarter is a harness that can start its process before a model call
// exists and give it the call's model and instructions afterwards.
type SpareStarter interface {
	StartSpare(ctx context.Context, executable, workDir string) (Spare, error)
}

// Spare answers one model call; its process exits after Answer or Close.
type Spare interface {
	Answer(ctx context.Context, request ModelCallRequest) (ModelCallResult, error)
	Close()
}

// ErrSpareUnusable is returned by Spare.Answer when the spare failed before
// it received the call's input, so the call can still be answered cold.
var ErrSpareUnusable = errors.New("spare unusable")

func spareUnusable(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrSpareUnusable, fmt.Sprintf(format, args...))
}
