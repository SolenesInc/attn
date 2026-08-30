package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	agentdriver "github.com/victorarias/attn/internal/agent"
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

const gardenAdviceOutputSchema = `{
	"type": "object",
	"properties": {
		"recommendation": {
			"type": "string",
			"enum": ["resume", "handover", "park", "harvest", "wither"]
		},
		"explanation": { "type": "string", "minLength": 1, "maxLength": 1200 },
		"evidence": {
			"type": "array",
			"items": { "type": "string", "minLength": 1, "maxLength": 500 },
			"minItems": 1,
			"maxItems": 8
		}
	},
	"required": ["recommendation", "explanation", "evidence"],
	"additionalProperties": false
}`

const gardenHandoffOutputSchema = `{
	"type": "object",
	"properties": {
		"handoff": { "type": "string", "minLength": 1, "maxLength": 6000 }
	},
	"required": ["handoff"],
	"additionalProperties": false
}`

const gardenAdvisorSystemPrompt = `Review one open Garden seed using only the supplied seed and evidence. Return one JSON object matching the requested shape, with no markdown or surrounding text.`

var (
	compiledGardenAdviceSchema  = mustCompileGardenAdvisorSchema("advice", gardenAdviceOutputSchema)
	compiledGardenHandoffSchema = mustCompileGardenAdvisorSchema("handoff", gardenHandoffOutputSchema)
)

type gardenAdvisorEvidence struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

type gardenAdvisorInput struct {
	SeedID   string                  `json:"seed_id"`
	Title    string                  `json:"title"`
	Body     string                  `json:"body"`
	Evidence []gardenAdvisorEvidence `json:"evidence"`
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
	schema string
	prompt string
}

var (
	gardenAdviceTask = gardenAdvisorTask{
		name:   "advice",
		schema: gardenAdviceOutputSchema,
		prompt: `Recommend one next action:
- resume: continue the saved conversation
- handover: give this seed to a new agent
- park: keep the work without starting an agent now
- harvest: the seed's stated outcome and required verification are complete
- wither: the work should be abandoned

Explain the recommendation and cite the supplied evidence.`,
	}
	gardenHandoffTask = gardenAdvisorTask{
		name:   "handoff",
		schema: gardenHandoffOutputSchema,
		prompt: `Draft a handoff for the new agent that will tend this seed. State the outcome, useful current state, next work, and required verification.`,
	}
)

func (d *Daemon) adviseGardenSeed(ctx context.Context, input gardenAdvisorInput) (gardenAdvice, error) {
	raw, err := d.runGardenAdvisor(ctx, gardenAdviceTask, input)
	if err != nil {
		return gardenAdvice{}, err
	}
	var advice gardenAdvice
	if err := validateGardenAdvisorOutput(compiledGardenAdviceSchema, raw, &advice); err != nil {
		return gardenAdvice{}, fmt.Errorf("garden advisor returned invalid advice: %w", err)
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

func (d *Daemon) draftGardenHandoff(ctx context.Context, input gardenAdvisorInput) (string, error) {
	raw, err := d.runGardenAdvisor(ctx, gardenHandoffTask, input)
	if err != nil {
		return "", err
	}
	var draft gardenHandoffDraft
	if err := validateGardenAdvisorOutput(compiledGardenHandoffSchema, raw, &draft); err != nil {
		return "", fmt.Errorf("garden advisor returned an invalid handoff: %w", err)
	}
	draft.Handoff = strings.TrimSpace(draft.Handoff)
	if draft.Handoff == "" {
		return "", errors.New("garden advisor returned an empty handoff")
	}
	return draft.Handoff, nil
}

func (d *Daemon) runGardenAdvisor(
	ctx context.Context,
	task gardenAdvisorTask,
	input gardenAdvisorInput,
) ([]byte, error) {
	config, err := d.gardenAdvisorConfig()
	if err != nil {
		return nil, err
	}
	resolve := d.resolveGardenAdvisor
	if d.gardenAdvisorResolve != nil {
		resolve = d.gardenAdvisorResolve
	}
	provider, executable, err := resolve(config)
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
		Prompt:          task.prompt + "\n\nSeed and evidence:\n" + string(payload),
		SystemPrompt:    gardenAdvisorSystemPrompt,
		WorkDir:         workDir,
		DisableTools:    true,
		OutputSchema:    json.RawMessage(task.schema),
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
		return nil, fmt.Errorf("run Garden advisor %s: %w (%s)", task.name, err, result.Diagnostics)
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
		SeedID: truncateGardenAdvisorText(input.SeedID, gardenAdvisorIDMaxChars),
		Title:  truncateGardenAdvisorText(input.Title, gardenAdvisorTitleMaxChars),
		Body:   truncateGardenAdvisorText(input.Body, gardenAdvisorBodyMaxChars),
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

func mustCompileGardenAdvisorSchema(name, raw string) *jsonschema.Schema {
	schemaObject, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
	if err != nil {
		panic(fmt.Sprintf("parse Garden advisor %s schema: %v", name, err))
	}
	compiler := jsonschema.NewCompiler()
	location := "mem://garden-advisor/" + name
	if err := compiler.AddResource(location, schemaObject); err != nil {
		panic(fmt.Sprintf("load Garden advisor %s schema: %v", name, err))
	}
	compiled, err := compiler.Compile(location)
	if err != nil {
		panic(fmt.Sprintf("compile Garden advisor %s schema: %v", name, err))
	}
	return compiled
}

func validateGardenAdvisorOutput(schema *jsonschema.Schema, raw []byte, target any) error {
	instance, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		return errors.New("output is not JSON")
	}
	if err := schema.Validate(instance); err != nil {
		return fmt.Errorf("output does not match its schema: %w", err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return errors.New("output could not be decoded")
	}
	return nil
}
