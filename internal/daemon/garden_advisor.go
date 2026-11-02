package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/jobs"
	"github.com/victorarias/attn/internal/prompts"
)

const (
	gardenAdvisorTimeout            = 4 * time.Minute
	gardenAdvisorIDMaxChars         = 128
	gardenAdvisorTitleMaxChars      = 400
	gardenAdvisorBodyMaxChars       = 16000
	gardenAdvisorEvidenceMaxItems   = 16
	gardenAdvisorEvidenceLabelChars = 120
	gardenAdvisorEvidenceTextChars  = 1200
)

const (
	gardenAdviceExplanationMaxChars = 1200
	gardenAdviceEvidenceMaxItems    = 8
	gardenAdviceEvidenceMaxChars    = 500
	gardenHandoffMaxChars           = 6000
)

var gardenAdviceRecommendations = []string{"resume", "handover", "keep_growing", "park", "harvest", "wither"}

func gardenAdvisorSystemPrompt() string {
	return prompts.RenderText("garden-advisor", "instructions", nil)
}

type gardenAdvisorEvidence struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

type gardenAdvisorInput struct {
	SeedID           string                  `json:"seed_id"`
	Title            string                  `json:"title"`
	Body             string                  `json:"body"`
	Evidence         []gardenAdvisorEvidence `json:"evidence"`
	AvailableActions []string                `json:"available_actions,omitempty"`
}

type gardenAdvice struct {
	Recommendation string   `json:"recommendation"`
	Explanation    string   `json:"explanation"`
	Evidence       []string `json:"evidence"`
}

type gardenHandoffDraft struct {
	Handoff string `json:"handoff"`
}

type gardenAdvisorTask struct {
	name   string
	schema map[string]any
}

func (task gardenAdvisorTask) prompt(evidence string) string {
	return prompts.RenderText("garden-advisor", task.name, prompts.Values{"evidence": evidence})
}

var (
	gardenAdviceTask = gardenAdvisorTask{
		name: "advice",
		schema: gardenAdvisorObjectSchema(map[string]any{
			"recommendation": map[string]any{"type": "string", "enum": gardenAdviceRecommendations},
			"explanation":    gardenAdvisorTextSchema(gardenAdviceExplanationMaxChars),
			"evidence": map[string]any{
				"type":     "array",
				"items":    gardenAdvisorTextSchema(gardenAdviceEvidenceMaxChars),
				"minItems": 1,
				"maxItems": gardenAdviceEvidenceMaxItems,
			},
		}),
	}
	gardenHandoffTask = gardenAdvisorTask{
		name: "handoff",
		schema: gardenAdvisorObjectSchema(map[string]any{
			"handoff": gardenAdvisorTextSchema(gardenHandoffMaxChars),
		}),
	}
)

func (d *Daemon) adviseGardenSeedWithConfig(
	ctx context.Context,
	input gardenAdvisorInput,
	config gardenAdvisorConfig,
) (gardenAdvice, error) {
	raw, err := d.runGardenAdvisorWithConfig(ctx, gardenAdviceTask, input, config)
	if err != nil {
		return gardenAdvice{}, err
	}
	advice, err := parseGardenAdvice(raw)
	if err != nil {
		return gardenAdvice{}, fmt.Errorf("garden advisor returned invalid advice: %w", err)
	}
	if len(input.AvailableActions) > 0 && !slices.Contains(input.AvailableActions, advice.Recommendation) {
		return gardenAdvice{}, fmt.Errorf("garden advisor recommended unavailable action %q", advice.Recommendation)
	}
	advice.Explanation = strings.TrimSpace(advice.Explanation)
	for i := range advice.Evidence {
		advice.Evidence[i] = strings.TrimSpace(advice.Evidence[i])
		if advice.Evidence[i] == "" {
			return gardenAdvice{}, errors.New("garden advisor returned empty evidence")
		}
	}
	return advice, nil
}

func (d *Daemon) draftGardenHandoffWithConfig(
	ctx context.Context,
	input gardenAdvisorInput,
	config gardenAdvisorConfig,
) (string, error) {
	raw, err := d.runGardenAdvisorWithConfig(ctx, gardenHandoffTask, input, config)
	if err != nil {
		return "", err
	}
	draft, err := parseGardenHandoffDraft(raw)
	if err != nil {
		return "", fmt.Errorf("garden advisor returned an invalid handoff: %w", err)
	}
	draft.Handoff = strings.TrimSpace(draft.Handoff)
	if draft.Handoff == "" {
		return "", errors.New("garden advisor returned an empty handoff")
	}
	return draft.Handoff, nil
}

func (d *Daemon) runGardenAdvisorWithConfig(
	ctx context.Context,
	task gardenAdvisorTask,
	input gardenAdvisorInput,
	config gardenAdvisorConfig,
) ([]byte, error) {
	provider, executable, err := d.resolveGardenAdvisor(config)
	if err != nil {
		return nil, err
	}
	return executeGardenAdvisor(ctx, provider, executable, config, task, input, gardenAdvisorTimeout)
}

func executeGardenAdvisor(
	ctx context.Context,
	provider agentdriver.HeadlessTaskProvider,
	executable string,
	config gardenAdvisorConfig,
	task gardenAdvisorTask,
	input gardenAdvisorInput,
	timeout time.Duration,
) ([]byte, error) {
	workDir, err := os.MkdirTemp("", "attn-garden-advisor-*")
	if err != nil {
		return nil, fmt.Errorf("create Garden advisor scratch directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	bounded := boundGardenAdvisorInput(input)
	payload, err := json.MarshalIndent(bounded, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Garden advisor input: %w", err)
	}
	request := agentdriver.HeadlessTaskRequest{
		Executable:      executable,
		Model:           config.Model,
		ReasoningEffort: config.Effort,
		Prompt:          task.prompt(string(payload)),
		SystemPrompt:    gardenAdvisorSystemPrompt(),
		WorkDir:         workDir,
		DisableTools:    true,
		OutputSchema:    task.outputSchema(),
	}
	if config.Agent == "claude" {
		request.MaxTurns = 4
		request.MaxBudgetUSD = "0.25"
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := provider.RunHeadlessTask(runCtx, request)
	if err != nil {
		if runCtx.Err() != nil {
			return nil, fmt.Errorf("garden advisor %s canceled: %w", task.name, runCtx.Err())
		}
		cause := fmt.Errorf("run Garden advisor %s: %w (%s)", task.name, err, result.Diagnostics)
		return nil, jobs.WithDiagnostic(cause, result.FailureOutput)
	}
	if runCtx.Err() != nil {
		return nil, fmt.Errorf("garden advisor %s canceled: %w", task.name, runCtx.Err())
	}
	if len(result.StructuredOutput) > 0 {
		return result.StructuredOutput, nil
	}
	if text := strings.TrimSpace(result.Text); text != "" {
		return []byte(text), nil
	}
	return nil, fmt.Errorf("garden advisor %s returned no output", task.name)
}

func boundGardenAdvisorInput(input gardenAdvisorInput) gardenAdvisorInput {
	bounded := gardenAdvisorInput{
		SeedID:           truncateGardenAdvisorText(input.SeedID, gardenAdvisorIDMaxChars),
		Title:            truncateGardenAdvisorText(input.Title, gardenAdvisorTitleMaxChars),
		Body:             truncateGardenAdvisorText(input.Body, gardenAdvisorBodyMaxChars),
		AvailableActions: slices.Clone(input.AvailableActions),
	}
	limit := len(input.Evidence)
	if limit > gardenAdvisorEvidenceMaxItems {
		limit = gardenAdvisorEvidenceMaxItems
	}
	bounded.Evidence = make([]gardenAdvisorEvidence, 0, limit)
	for _, item := range input.Evidence[:limit] {
		bounded.Evidence = append(bounded.Evidence, gardenAdvisorEvidence{
			Label: truncateGardenAdvisorText(item.Label, gardenAdvisorEvidenceLabelChars),
			Text:  truncateGardenAdvisorText(item.Text, gardenAdvisorEvidenceTextChars),
		})
	}
	return bounded
}

func truncateGardenAdvisorText(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	if limit == 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
}

func gardenAdvisorTextSchema(maxChars int) map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": maxChars}
}

func gardenAdvisorObjectSchema(properties map[string]any) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             slices.Sorted(maps.Keys(properties)),
		"additionalProperties": false,
	}
}

func (task gardenAdvisorTask) outputSchema() json.RawMessage {
	schema, err := json.Marshal(task.schema)
	if err != nil {
		panic(fmt.Sprintf("encode Garden advisor %s schema: %v", task.name, err))
	}
	return schema
}

func parseGardenAdvice(raw []byte) (gardenAdvice, error) {
	var advice gardenAdvice
	if err := decodeGardenAdvisorOutput(raw, &advice); err != nil {
		return gardenAdvice{}, err
	}
	if !slices.Contains(gardenAdviceRecommendations, advice.Recommendation) {
		return gardenAdvice{}, gardenAdvisorSchemaMismatch("recommendation %q is not one of %s", advice.Recommendation, strings.Join(gardenAdviceRecommendations, ", "))
	}
	if err := checkGardenAdvisorText("explanation", advice.Explanation, gardenAdviceExplanationMaxChars); err != nil {
		return gardenAdvice{}, err
	}
	if n := len(advice.Evidence); n < 1 || n > gardenAdviceEvidenceMaxItems {
		return gardenAdvice{}, gardenAdvisorSchemaMismatch("evidence has %d items, want 1 to %d", n, gardenAdviceEvidenceMaxItems)
	}
	for i, item := range advice.Evidence {
		if err := checkGardenAdvisorText(fmt.Sprintf("evidence[%d]", i), item, gardenAdviceEvidenceMaxChars); err != nil {
			return gardenAdvice{}, err
		}
	}
	return advice, nil
}

func parseGardenHandoffDraft(raw []byte) (gardenHandoffDraft, error) {
	var draft gardenHandoffDraft
	if err := decodeGardenAdvisorOutput(raw, &draft); err != nil {
		return gardenHandoffDraft{}, err
	}
	if err := checkGardenAdvisorText("handoff", draft.Handoff, gardenHandoffMaxChars); err != nil {
		return gardenHandoffDraft{}, err
	}
	return draft, nil
}

func decodeGardenAdvisorOutput(raw []byte, target any) error {
	if !json.Valid(raw) {
		return errors.New("output is not JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return gardenAdvisorSchemaMismatch("%v", err)
	}
	return nil
}

func checkGardenAdvisorText(field, value string, maxChars int) error {
	if n := utf8.RuneCountInString(value); n < 1 || n > maxChars {
		return gardenAdvisorSchemaMismatch("%s has %d characters, want 1 to %d", field, n, maxChars)
	}
	return nil
}

func gardenAdvisorSchemaMismatch(format string, args ...any) error {
	return fmt.Errorf("output does not match its schema: "+format, args...)
}
