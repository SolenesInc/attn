package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/prompts"
)

type headlessRunner interface {
	Run(ctx context.Context, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error)
}

const resultToolName = "return_result"

const defaultDriverAgentRetries = 2

type driverAgent struct {
	runner     headlessRunner
	executable string
	model      string
	attnExec   string
	runTmpDir  string
	maxRetries int

	workingTree       string
	sessionMCPServers []agentdriver.MCPServerSpec
	log               func(format string, args ...interface{})
}

type DriverAgentOptions struct {
	Provider          string
	Executable        string
	Model             string
	RunTmpDir         string
	AttnExecutable    string
	MaxRetries        int
	Runner            headlessRunner
	WorkingTree       string
	SessionMCPServers []agentdriver.MCPServerSpec
	LogFunc           func(format string, args ...interface{})
}

var _ AgentStub = (*driverAgent)(nil)

func NewDriverAgent(opts DriverAgentOptions) (*driverAgent, error) {
	provider := strings.TrimSpace(opts.Provider)
	if provider == "" {
		return nil, errors.New("driver agent: provider is required")
	}

	runner := opts.Runner
	executable := strings.TrimSpace(opts.Executable)
	if runner == nil {
		driver := agentdriver.Get(provider)
		if driver == nil {
			return nil, fmt.Errorf("driver agent: unknown provider %q", provider)
		}
		hp, ok := driver.(agentdriver.HeadlessTaskProvider)
		if !ok {
			return nil, fmt.Errorf("driver agent: provider %q does not support headless tasks", provider)
		}
		runner = headlessProviderRunner{provider: hp}

		if executable == "" {
			resolved := driver.ResolveExecutable("")
			path, err := exec.LookPath(resolved)
			if err != nil {
				return nil, fmt.Errorf("driver agent: resolve %s executable: %w", provider, err)
			}
			executable = path
		}
	}

	attnExec := strings.TrimSpace(opts.AttnExecutable)
	if attnExec == "" {
		self, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("driver agent: resolve attn executable: %w", err)
		}
		attnExec = self
	}

	runTmpDir := strings.TrimSpace(opts.RunTmpDir)
	if runTmpDir == "" {
		dir, err := os.MkdirTemp("", "attn-workflow-agent-*")
		if err != nil {
			return nil, fmt.Errorf("driver agent: create run temp dir: %w", err)
		}
		runTmpDir = dir
	} else if err := os.MkdirAll(runTmpDir, 0o700); err != nil {
		return nil, fmt.Errorf("driver agent: create run temp dir: %w", err)
	}

	maxRetries := opts.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultDriverAgentRetries
	}

	return &driverAgent{
		runner:            runner,
		executable:        executable,
		model:             strings.TrimSpace(opts.Model),
		attnExec:          attnExec,
		runTmpDir:         runTmpDir,
		maxRetries:        maxRetries,
		workingTree:       strings.TrimSpace(opts.WorkingTree),
		sessionMCPServers: opts.SessionMCPServers,
		log:               opts.LogFunc,
	}, nil
}

func (d *driverAgent) defaultRunCWD() string {
	if d.workingTree != "" {
		return d.workingTree
	}
	return d.runTmpDir
}

func (d *driverAgent) Run(ctx context.Context, call AgentCall) (json.RawMessage, error) {
	model := d.model
	if call.Model != "" {
		model = call.Model
	}
	if call.Isolation == "worktree" {
		return d.runIsolated(ctx, call, model)
	}
	return d.runInCWD(ctx, call, d.defaultRunCWD(), model)
}

func (d *driverAgent) runInCWD(ctx context.Context, call AgentCall, cwd, model string) (json.RawMessage, error) {
	if len(call.Schema) == 0 {
		return d.runNoSchema(ctx, call.Prompt, cwd, model)
	}
	return d.runWithSchema(ctx, call.Ordinal, call.Prompt, call.Schema, cwd, model)
}

func (d *driverAgent) runIsolated(ctx context.Context, call AgentCall, model string) (json.RawMessage, error) {
	repoRoot := git.ResolveMainRepoPath(d.defaultRunCWD())
	if repoRoot == "" {
		return nil, fmt.Errorf("worktree isolation: cannot resolve repo root from working tree %q", d.defaultRunCWD())
	}

	branch := worktreeBranchFor(call.Ordinal)
	path := git.GenerateWorktreePath(repoRoot, branch)
	if err := git.CreateWorktree(repoRoot, branch, path); err != nil {
		// Fail closed: falling back to the shared tree would let parallel mutators collide.
		return nil, fmt.Errorf("worktree isolation: create worktree for %s: %w", call.Ordinal.String(), err)
	}

	result, runErr := d.runInCWD(ctx, call, path, model)

	clean, cleanErr := git.IsWorktreeClean(path)
	switch {
	case cleanErr != nil:
		d.logf("worktree isolation: could not determine cleanliness of %q (%v); keeping it", path, cleanErr)
	case clean:
		if err := git.DeleteWorktree(repoRoot, path, true); err != nil {
			d.logf("worktree isolation: remove clean worktree %q failed: %v", path, err)
		} else {
			_ = git.DeleteBranch(repoRoot, branch, true)
		}
	default:
		d.logf("worktree isolation: agent left changes; keeping worktree %q (branch %s)", path, branch)
	}

	return result, runErr
}

func worktreeBranchFor(ordinal OrdinalPath) string {
	sum := sha256.Sum256([]byte(ordinal.String()))
	return "attn-wf/" + hex.EncodeToString(sum[:])[:12]
}

func (d *driverAgent) logf(format string, args ...interface{}) {
	if d.log == nil {
		return
	}
	d.log(format, args...)
}

func (d *driverAgent) runNoSchema(ctx context.Context, prompt, cwd, model string) (json.RawMessage, error) {
	req := agentdriver.HeadlessTaskRequest{
		Executable:      d.executable,
		Model:           model,
		Prompt:          prompt,
		WorkDir:         d.runTmpDir,
		CWD:             cwd,
		Sandbox:         "workspace-write",
		ExtraMCPServers: d.sessionMCPServers,
	}
	res, err := d.runner.Run(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("headless agent failed: %s", diagnosticsOf(res, err))
	}
	encoded, encErr := json.Marshal(res.Text)
	if encErr != nil {
		return nil, fmt.Errorf("encode agent text: %w", encErr)
	}
	return encoded, nil
}

func (d *driverAgent) runWithSchema(ctx context.Context, ordinal OrdinalPath, prompt string, schema json.RawMessage, cwd, model string) (json.RawMessage, error) {
	base := ordinalFileBase(ordinal)
	schemaPath := filepath.Join(d.runTmpDir, base+".schema.json")
	resultPath := filepath.Join(d.runTmpDir, base+".result.json")
	defer os.Remove(schemaPath)
	defer os.Remove(resultPath)

	if err := os.WriteFile(schemaPath, schema, 0o600); err != nil {
		return nil, fmt.Errorf("write result schema: %w", err)
	}
	// A stale result file at the same ordinal would read as a false success.
	_ = os.Remove(resultPath)

	var lastDiag string
	for attempt := 0; attempt <= d.maxRetries; attempt++ {
		fullPrompt := prompts.RenderText("workflow-agent", "run", prompts.Values{"brief": prompt, "retry": fmt.Sprint(attempt > 0)})

		req := agentdriver.HeadlessTaskRequest{
			Executable:       d.executable,
			Model:            model,
			Prompt:           fullPrompt,
			WorkDir:          d.runTmpDir,
			CWD:              cwd,
			Sandbox:          "workspace-write",
			MCPServerName:    "attn_workflow_result",
			ToolName:         resultToolName,
			Schema:           schema,
			ResultPath:       resultPath,
			MCPServerCommand: d.attnExec,
			// Scratch paths stay absolute so the sink resolves them from any CWD.
			MCPServerArgs: []string{
				"_workflow-result-mcp",
				"--tool-name", resultToolName,
				"--schema-file", schemaPath,
				"--result-file", resultPath,
			},
			ExtraMCPServers: d.sessionMCPServers,
		}

		res, runErr := d.runner.Run(ctx, req)
		if runErr != nil {
			lastDiag = diagnosticsOf(res, runErr)
		} else {
			lastDiag = ""
		}

		if bytes, ok := readResultFile(resultPath); ok {
			return bytes, nil
		}
	}

	if lastDiag == "" {
		lastDiag = "agent never produced a schema-valid result"
	}
	return nil, fmt.Errorf("headless agent produced no result after %d attempts: %s", d.maxRetries+1, lastDiag)
}

var schemaCallInstruction = prompts.RenderText("workflow-agent", "result-instruction", prompts.Values{})

var correctiveInstruction = prompts.RenderText("workflow-agent", "retry-instruction", prompts.Values{})

func readResultFile(path string) (json.RawMessage, bool) {
	b, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return nil, false
	}
	return json.RawMessage(b), true
}

func ordinalFileBase(ordinal OrdinalPath) string {
	sum := sha256.Sum256([]byte(ordinal.String()))
	return "call-" + hex.EncodeToString(sum[:])[:16]
}

func diagnosticsOf(res agentdriver.HeadlessTaskResult, err error) string {
	if d := strings.TrimSpace(res.Diagnostics); d != "" {
		return d
	}
	if err != nil {
		return err.Error()
	}
	return "unknown failure"
}

type headlessProviderRunner struct {
	provider agentdriver.HeadlessTaskProvider
}

func (r headlessProviderRunner) Run(ctx context.Context, req agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
	return r.provider.RunHeadlessTask(ctx, req)
}
