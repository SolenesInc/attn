package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/activity"
	agentdriver "github.com/victorarias/attn/internal/agent"
)

type Variant struct {
	Prompt string `json:"prompt"`
	Agent  string `json:"agent"`
	Model  string `json:"model"`
	Effort string `json:"effort,omitempty"`
}

func (v Variant) String() string {
	name := fmt.Sprintf("%s/%s/%s", v.Prompt, v.Agent, v.Model)
	if v.Effort != "" {
		name += "/" + v.Effort
	}
	return name
}

type Result struct {
	Variant       Variant              `json:"variant"`
	EntryID       string               `json:"entry_id"`
	State         string               `json:"state"`
	Line          string               `json:"line"`
	Violations    []activity.Violation `json:"violations,omitempty"`
	LatencyMS     int64                `json:"latency_ms"`
	CostUSD       float64              `json:"cost_usd"`
	PromptChar    int                  `json:"prompt_chars"`
	Error         string               `json:"error,omitempty"`
	FailureOutput string               `json:"failure_output,omitempty"`
}

func runMatrix(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	dir := fs.String("dir", defaultDir, "corpus directory")
	promptDir := fs.String("prompts", "", "custom prompt variant directory (default: embedded baseline)")
	prompts := fs.String("prompt", "", "comma-separated prompt names (default: all in --prompts)")
	agents := fs.String("agent", "claude", "comma-separated agents")
	models := fs.String("model", "claude-haiku-4-5", "comma-separated models")
	efforts := fs.String("effort", "", "comma-separated reasoning efforts (empty = provider default)")
	limit := fs.Int("limit", 0, "cap corpus entries used (0 = all)")
	state := fs.String("state", "", "only entries in this state")
	parallel := fs.Int("parallel", 4, "concurrent runs")
	out := fs.String("out", "", "results file (default: <dir>/results-<timestamp>.jsonl)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	corpus, err := loadCorpus(*dir)
	if err != nil {
		return err
	}
	if len(corpus) == 0 {
		return fmt.Errorf("corpus is empty — run `activity-bench corpus` first")
	}
	if *state != "" {
		var filtered []Entry
		for _, entry := range corpus {
			if entry.State == *state {
				filtered = append(filtered, entry)
			}
		}
		corpus = filtered
		if len(corpus) == 0 {
			return fmt.Errorf("no corpus entries in state %q", *state)
		}
	}
	if *limit > 0 && len(corpus) > *limit {
		corpus = corpus[len(corpus)-*limit:]
	}

	templates, err := loadTemplates(*promptDir, splitList(*prompts))
	if err != nil {
		return err
	}

	var variants []Variant
	for _, template := range templates {
		for _, agent := range splitList(*agents) {
			for _, model := range splitList(*models) {
				for _, effort := range splitListAllowEmpty(*efforts) {
					variants = append(variants, Variant{Prompt: template.Name, Agent: agent, Model: model, Effort: effort})
				}
			}
		}
	}

	type job struct {
		variant  Variant
		template activity.Template
		entry    Entry
	}
	var jobs []job
	byName := map[string]activity.Template{}
	for _, template := range templates {
		byName[template.Name] = template
	}
	for _, variant := range variants {
		for _, entry := range corpus {
			jobs = append(jobs, job{variant: variant, template: byName[variant.Prompt], entry: entry})
		}
	}

	fmt.Printf("%d variants x %d entries = %d runs\n", len(variants), len(corpus), len(jobs))

	results := make([]Result, len(jobs))
	sem := make(chan struct{}, *parallel)
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = execute(j.variant, j.template, j.entry)
			mu.Lock()
			done++
			fmt.Printf("\r%d/%d", done, len(jobs))
			mu.Unlock()
		}(i, j)
	}
	wg.Wait()
	fmt.Println()

	path := *out
	if path == "" {
		path = filepath.Join(*dir, fmt.Sprintf("results-%d.jsonl", time.Now().Unix()))
	}
	if err := writeResults(path, results); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n\n", path)
	summarize(results)
	return nil
}

func execute(variant Variant, template activity.Template, entry Entry) Result {
	result := Result{Variant: variant, EntryID: entry.ID, State: entry.State}

	prompt := template.Render(activity.Input{
		State:       entry.State,
		StateReason: entry.StateReason,
		Window:      entry.Window,
		Previous:    entry.Previous,
	})
	result.PromptChar = prompt.Chars()

	driver := agentdriver.Get(variant.Agent)
	if driver == nil {
		result.Error = "agent not installed: " + variant.Agent
		return result
	}
	provider, ok := driver.(agentdriver.HeadlessTaskProvider)
	if !ok {
		result.Error = "agent does not support headless tasks: " + variant.Agent
		return result
	}
	executable, err := exec.LookPath(driver.ResolveExecutable(""))
	if err != nil {
		result.Error = "executable not found: " + err.Error()
		return result
	}

	workDir, err := os.MkdirTemp("", "activity-bench-")
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer os.RemoveAll(workDir)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	start := time.Now()
	taskResult, err := provider.RunHeadlessTask(ctx, agentdriver.HeadlessTaskRequest{
		Executable:      executable,
		Model:           variant.Model,
		ReasoningEffort: variant.Effort,
		Prompt:          prompt.User,
		// SystemPrompt REPLACES Claude Code's own. Measured on the control prompt: the
		// default prefix bills ~24.8K tokens, this plus DisableTools bills ~2.3K.
		SystemPrompt: prompt.System,
		WorkDir:      workDir,
		// DisableTools is load-bearing: an empty AllowedTools without it re-enables the
		// driver's default tools. No OutputSchema: measured 10.6s/$0.0059 vs 14.2s/$0.0089.
		DisableTools: true,
		MaxTurns:     2,
		MaxBudgetUSD: "0.05",
	})
	result.LatencyMS = time.Since(start).Milliseconds()
	result.CostUSD = taskResult.TotalCostUSD
	if err != nil {
		result.Error = err.Error()
		result.FailureOutput = taskResult.FailureOutput
		return result
	}

	result.Line = extractLine(taskResult)
	result.Violations = activity.Check(result.Line, entry.State)
	return result
}

func extractLine(taskResult agentdriver.HeadlessTaskResult) string {
	if len(taskResult.StructuredOutput) > 0 {
		var payload struct {
			Activity string `json:"activity"`
		}
		if json.Unmarshal(taskResult.StructuredOutput, &payload) == nil && payload.Activity != "" {
			return strings.TrimSpace(payload.Activity)
		}
	}
	return strings.TrimSpace(taskResult.Text)
}

func loadTemplates(dir string, names []string) ([]activity.Template, error) {
	if dir == "" {
		if len(names) == 0 || (len(names) == 1 && names[0] == "baseline") {
			return []activity.Template{activity.Baseline()}, nil
		}
		return nil, fmt.Errorf("only baseline is built in; use --prompts for custom variants")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read prompt dir %s: %w", dir, err)
	}
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[name] = true
	}
	var templates []activity.Template
	for _, file := range entries {
		if file.IsDir() || filepath.Ext(file.Name()) != ".md" {
			continue
		}
		name := strings.TrimSuffix(file.Name(), ".md")
		if len(wanted) > 0 && !wanted[name] {
			continue
		}
		template, err := activity.LoadTemplate(name, filepath.Join(dir, file.Name()))
		if err != nil {
			return nil, err
		}
		templates = append(templates, template)
	}
	if len(templates) == 0 {
		return nil, fmt.Errorf("no prompt variants found in %s", dir)
	}
	return templates, nil
}

func writeResults(path string, results []Result) error {
	var b strings.Builder
	for _, result := range results {
		line, err := json.Marshal(result)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func splitList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitListAllowEmpty(raw string) []string {
	out := splitList(raw)
	if len(out) == 0 {
		return []string{""}
	}
	return out
}
