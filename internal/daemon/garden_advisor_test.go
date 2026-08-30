package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/protocol"
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

func TestParseGardenAdvisorConfigResolvesHarnessDefaults(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want gardenAdvisorConfig
	}{
		{
			name: "blank uses Codex",
			want: gardenAdvisorConfig{Agent: "codex", Model: "gpt-5.6-luna", Effort: "xhigh"},
		},
		{
			name: "Claude",
			raw:  `{"agent":" CLAUDE "}`,
			want: gardenAdvisorConfig{Agent: "claude", Model: "sonnet", Effort: "medium"},
		},
		{
			name: "Copilot",
			raw:  `{"agent":"copilot","effort":"MAX"}`,
			want: gardenAdvisorConfig{Agent: "copilot", Model: "claude-sonnet-4.6", Effort: "max"},
		},
		{
			name: "custom Codex recipe",
			raw:  `{"agent":"codex","model":"gpt-custom","effort":"HIGH"}`,
			want: gardenAdvisorConfig{Agent: "codex", Model: "gpt-custom", Effort: "high"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseGardenAdvisorConfig(test.raw)
			if err != nil {
				t.Fatalf("parseGardenAdvisorConfig() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("config = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestParseGardenAdvisorConfigRejectsUnsupportedRecipes(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"agent":"shell","model":"anything"}`,
		`{"agent":"codex","unknown":true}`,
		`{"agent":"codex"} trailing`,
	} {
		if _, err := parseGardenAdvisorConfig(raw); err == nil {
			t.Fatalf("parseGardenAdvisorConfig(%q) succeeded", raw)
		}
	}
}

func TestToolUsingHeadlessConfigsRejectCopilot(t *testing.T) {
	if _, err := parseNotebookNarrationConfig(
		notebookNarrateWorkspaceKind,
		`{"agent":"copilot","model":"claude-sonnet-4.6"}`,
	); err == nil || !strings.Contains(err.Error(), "tool-free") {
		t.Fatalf("notebook narration error = %v, want tool-free capability error", err)
	}
	if _, err := parseKeeperCompactConfig(
		`{"agent":"copilot","model":"claude-sonnet-4.6"}`,
	); err == nil || !strings.Contains(err.Error(), "tool-free") {
		t.Fatalf("keeper compact error = %v, want tool-free capability error", err)
	}
}

func TestValidateGardenAdvisorSettingUsesConfiguredExecutable(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "advisor.sock"))
	t.Cleanup(d.stopEventBus)
	d.store.SetSetting(SettingCodexExecutable, "missing-codex")
	if err := d.validateGardenAdvisorSetting(`{"agent":"codex"}`); err == nil {
		t.Fatal("missing configured executable was accepted")
	}

	executable := filepath.Join(t.TempDir(), "custom-codex")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake executable: %v", err)
	}
	d.store.SetSetting(SettingCodexExecutable, executable)
	if err := d.validateGardenAdvisorSetting(`{"agent":"codex"}`); err != nil {
		t.Fatalf("configured executable was rejected: %v", err)
	}
}

func TestInvalidGardenAdvisorReplacementPreservesSavedRecipe(t *testing.T) {
	t.Setenv("PATH", "")
	d := NewForTesting(filepath.Join(t.TempDir(), "advisor.sock"))
	t.Cleanup(d.stopEventBus)
	valid := `{"agent":"claude","model":"sonnet","effort":"medium"}`
	d.store.SetSetting(SettingGardenAdvisor, valid)
	client := &wsClient{send: make(chan outboundMessage, 1)}

	d.handleSetSettingWS(client, &protocol.SetSettingMessage{
		Cmd:   protocol.CmdSetSetting,
		Key:   SettingGardenAdvisor,
		Value: `{"agent":"shell","model":"anything"}`,
	})

	if got := d.store.GetSetting(SettingGardenAdvisor); got != valid {
		t.Fatalf("saved recipe = %q, want preserved %q", got, valid)
	}
	select {
	case outbound := <-client.send:
		var message protocol.SettingsUpdatedMessage
		if err := json.Unmarshal(outbound.payload, &message); err != nil {
			t.Fatalf("decode settings response: %v", err)
		}
		if message.Success == nil || *message.Success {
			t.Fatalf("settings response success = %v, want false", message.Success)
		}
		if message.Error == nil || !strings.Contains(*message.Error, "not supported") {
			t.Fatalf("settings error = %v, want unsupported agent error", message.Error)
		}
	default:
		t.Fatal("invalid setting returned no response")
	}
}

func TestSettingsPublishEffectiveGardenAdvisorDefault(t *testing.T) {
	t.Setenv("PATH", "")
	d := NewForTesting(filepath.Join(t.TempDir(), "advisor.sock"))
	t.Cleanup(d.stopEventBus)

	raw, ok := d.settingsWithAgentAvailability()[SettingGardenAdvisor].(string)
	if !ok {
		t.Fatal("settings did not include the Garden advisor recipe")
	}
	var got gardenAdvisorConfig
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("decode effective recipe: %v", err)
	}
	want := gardenAdvisorConfig{Agent: "codex", Model: "gpt-5.6-luna", Effort: "xhigh"}
	if got != want {
		t.Fatalf("effective recipe = %+v, want %+v", got, want)
	}
}

func TestExecuteGardenAdvisorPinsRecipeAndIsolation(t *testing.T) {
	var workDir string
	provider := gardenAdvisorProviderFunc(func(_ context.Context, request agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		workDir = request.WorkDir
		if info, err := os.Stat(workDir); err != nil || !info.IsDir() {
			t.Fatalf("scratch workdir = %q, stat = %v", workDir, err)
		}
		if request.Executable != "/fake/codex" || request.Model != "gpt-5.6-luna" || request.ReasoningEffort != "xhigh" {
			t.Fatalf("request did not pin recipe: %+v", request)
		}
		if !request.DisableTools || len(request.AllowedTools) != 0 || len(request.ExtraWritableRoots) != 0 {
			t.Fatalf("request exposed tools or writable roots: %+v", request)
		}
		if request.SystemPrompt != gardenAdvisorSystemPrompt {
			t.Fatalf("system prompt = %q", request.SystemPrompt)
		}
		if string(request.OutputSchema) != gardenAdviceOutputSchema {
			t.Fatalf("output schema = %s", request.OutputSchema)
		}
		if strings.Contains(request.Prompt, strings.Repeat("x", gardenAdvisorBodyMaxChars+1)) {
			t.Fatal("prompt carried the unbounded seed body")
		}
		return agentdriver.HeadlessTaskResult{
			Text: `{"recommendation":"resume","explanation":"The saved conversation still contains the work.","evidence":["execution: resumable"]}`,
		}, nil
	})

	raw, err := executeGardenAdvisor(
		context.Background(),
		provider,
		"/fake/codex",
		gardenAdvisorConfig{Agent: "codex", Model: "gpt-5.6-luna", Effort: "xhigh"},
		gardenAdviceTask,
		gardenAdvisorInput{
			SeedID: "s-test",
			Title:  "Continue the work",
			Body:   strings.Repeat("x", gardenAdvisorBodyMaxChars+100),
			Evidence: []gardenAdvisorEvidence{{
				Label: "execution",
				Text:  "resumable",
			}},
		},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("executeGardenAdvisor() error = %v", err)
	}
	var advice gardenAdvice
	if err := validateGardenAdvisorOutput(compiledGardenAdviceSchema, raw, &advice); err != nil {
		t.Fatalf("validate advice: %v", err)
	}
	if advice.Recommendation != "resume" {
		t.Fatalf("recommendation = %q, want resume", advice.Recommendation)
	}
	if _, err := os.Stat(workDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratch workdir survived run: %v", err)
	}
}

func TestGardenAdvisorUsesStructuredOutputAndValidatesIt(t *testing.T) {
	valid := json.RawMessage(`{"recommendation":"harvest","explanation":"Done.","evidence":["verification passed"]}`)
	provider := gardenAdvisorProviderFunc(func(context.Context, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{
			Text:             "not json",
			StructuredOutput: valid,
		}, nil
	})
	d := gardenAdvisorTestDaemon(t, provider)

	advice, err := d.adviseGardenSeed(context.Background(), gardenAdvisorInput{SeedID: "s-test"})
	if err != nil {
		t.Fatalf("adviseGardenSeed() error = %v", err)
	}
	if advice.Recommendation != "harvest" {
		t.Fatalf("recommendation = %q, want harvest", advice.Recommendation)
	}
}

func TestGardenAdvisorCanRecommendKeepingASeedGrowing(t *testing.T) {
	provider := gardenAdvisorProviderFunc(func(context.Context, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{
			Text: `{"recommendation":"keep_growing","explanation":"The maintainer is still deciding.","evidence":["The seed log asks for a later decision."]}`,
		}, nil
	})
	d := gardenAdvisorTestDaemon(t, provider)

	advice, err := d.adviseGardenSeed(context.Background(), gardenAdvisorInput{
		SeedID: "s-test", AvailableActions: []string{"keep_growing", "park"},
	})
	if err != nil || advice.Recommendation != "keep_growing" {
		t.Fatalf("keep-growing advice = %+v err=%v", advice, err)
	}
}

func TestGardenAdvisorRejectsMalformedOutputs(t *testing.T) {
	for _, output := range []string{
		"not json",
		`{"recommendation":"delete","explanation":"No.","evidence":["guess"]}`,
		`{"recommendation":"park","explanation":"Later.","evidence":["age"],"extra":true}`,
		`{"recommendation":"park","explanation":"Later.","evidence":[" "]}`,
	} {
		t.Run(output, func(t *testing.T) {
			provider := gardenAdvisorProviderFunc(func(context.Context, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
				return agentdriver.HeadlessTaskResult{Text: output}, nil
			})
			d := gardenAdvisorTestDaemon(t, provider)
			if _, err := d.adviseGardenSeed(context.Background(), gardenAdvisorInput{SeedID: "s-test"}); err == nil {
				t.Fatal("malformed advice was accepted")
			}
		})
	}
}

func TestGardenAdvisorRejectsUnavailableRecommendation(t *testing.T) {
	provider := gardenAdvisorProviderFunc(func(context.Context, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{
			Text: `{"recommendation":"resume","explanation":"Continue it.","evidence":["saved conversation"]}`,
		}, nil
	})
	d := gardenAdvisorTestDaemon(t, provider)
	_, err := d.adviseGardenSeed(context.Background(), gardenAdvisorInput{
		SeedID: "s-test", AvailableActions: []string{"handover", "park", "harvest", "wither"},
	})
	if err == nil || !strings.Contains(err.Error(), "unavailable action") {
		t.Fatalf("unavailable recommendation error = %v", err)
	}
}

func TestGardenAdvisorDraftsEditableHandoff(t *testing.T) {
	provider := gardenAdvisorProviderFunc(func(context.Context, agentdriver.HeadlessTaskRequest) (agentdriver.HeadlessTaskResult, error) {
		return agentdriver.HeadlessTaskResult{Text: `{"handoff":"  Continue from the failing integration test.  "}`}, nil
	})
	d := gardenAdvisorTestDaemon(t, provider)

	draft, err := d.draftGardenHandoff(context.Background(), gardenAdvisorInput{SeedID: "s-test"})
	if err != nil {
		t.Fatalf("draftGardenHandoff() error = %v", err)
	}
	if draft != "Continue from the failing integration test." {
		t.Fatalf("draft = %q", draft)
	}
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

func TestBoundGardenAdvisorInputCapsEveryField(t *testing.T) {
	evidence := make([]gardenAdvisorEvidence, gardenAdvisorEvidenceMaxItems+3)
	for i := range evidence {
		evidence[i] = gardenAdvisorEvidence{
			Label: strings.Repeat("🌱", gardenAdvisorEvidenceLabelChars+1),
			Text:  strings.Repeat("x", gardenAdvisorEvidenceTextChars+1),
		}
	}
	bounded := boundGardenAdvisorInput(gardenAdvisorInput{
		SeedID:   strings.Repeat("i", gardenAdvisorIDMaxChars+1),
		Title:    strings.Repeat("t", gardenAdvisorTitleMaxChars+1),
		Body:     strings.Repeat("b", gardenAdvisorBodyMaxChars+1),
		Evidence: evidence,
	})

	if len([]rune(bounded.SeedID)) != gardenAdvisorIDMaxChars ||
		len([]rune(bounded.Title)) != gardenAdvisorTitleMaxChars ||
		len([]rune(bounded.Body)) != gardenAdvisorBodyMaxChars {
		t.Fatalf("top-level caps failed: id=%d title=%d body=%d",
			len([]rune(bounded.SeedID)), len([]rune(bounded.Title)), len([]rune(bounded.Body)))
	}
	if len(bounded.Evidence) != gardenAdvisorEvidenceMaxItems {
		t.Fatalf("evidence count = %d, want %d", len(bounded.Evidence), gardenAdvisorEvidenceMaxItems)
	}
	for _, item := range bounded.Evidence {
		if len([]rune(item.Label)) != gardenAdvisorEvidenceLabelChars || len([]rune(item.Text)) != gardenAdvisorEvidenceTextChars {
			t.Fatalf("evidence caps failed: label=%d text=%d", len([]rune(item.Label)), len([]rune(item.Text)))
		}
	}
}

func gardenAdvisorTestDaemon(t *testing.T, provider agentdriver.HeadlessTaskProvider) *Daemon {
	t.Helper()
	d := NewForTesting(filepath.Join(t.TempDir(), "advisor.sock"))
	t.Cleanup(d.stopEventBus)
	d.store.SetSetting(SettingGardenAdvisor, `{"agent":"codex","model":"gpt-test","effort":"xhigh"}`)
	d.store.SetSetting(SettingCodexExecutable, "/fake/codex")

	d.gardenAdvisorResolve = func(gardenAdvisorConfig) (agentdriver.HeadlessTaskProvider, string, error) {
		return provider, "/fake/codex", nil
	}
	return d
}
