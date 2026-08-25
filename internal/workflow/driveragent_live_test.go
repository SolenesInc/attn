package workflow

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDriverAgentLiveCodexRoundTrip(t *testing.T) {
	if strings.TrimSpace(os.Getenv("ATTN_RUN_LIVE_CODEX")) == "" {
		t.Skip("set ATTN_RUN_LIVE_CODEX=1 to run the live codex round-trip")
	}
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex not installed")
	}
	if _, err := os.Stat(codexAuthPath()); err != nil {
		t.Skip("codex auth.json not found; skipping live round-trip")
	}

	tmp := t.TempDir()
	attnPath := filepath.Join(tmp, "attn")
	build := exec.Command("go", "build", "-o", attnPath, "github.com/victorarias/attn/cmd/attn")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build attn: %v", err)
	}

	da, err := NewDriverAgent(DriverAgentOptions{
		Provider:       "codex",
		Executable:     codexPath,
		Model:          codexLiveModel(),
		RunTmpDir:      tmp,
		AttnExecutable: attnPath,
		MaxRetries:     1,
	})
	if err != nil {
		t.Fatalf("NewDriverAgent: %v", err)
	}

	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["answer"],"properties":{"answer":{"type":"string"}}}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	result, runErr := da.runWithSchema(ctx, ordForTest(), "Respond with the word PONG in the answer field.", schema, da.defaultRunCWD(), da.model)
	if runErr != nil {
		t.Fatalf("live codex round-trip failed: %v", runErr)
	}
	var obj struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(result, &obj); err != nil {
		t.Fatalf("result is not the schema object: %s (%v)", result, err)
	}
	if strings.TrimSpace(obj.Answer) == "" {
		t.Fatalf("schema object has empty answer: %s", result)
	}
	t.Logf("live codex round-trip returned: %s", result)
}

func TestDriverAgentLiveWritableCodexRoundTrip(t *testing.T) {
	if strings.TrimSpace(os.Getenv("ATTN_RUN_LIVE_CODEX")) == "" {
		t.Skip("set ATTN_RUN_LIVE_CODEX=1 to run the live writable codex round-trip (otherwise verified manually / in E4)")
	}
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex not installed")
	}
	if _, err := os.Stat(codexAuthPath()); err != nil {
		t.Skip("codex auth.json not found; skipping live writable round-trip")
	}

	tmp := t.TempDir()
	attnPath := filepath.Join(tmp, "attn")
	build := exec.Command("go", "build", "-o", attnPath, "github.com/victorarias/attn/cmd/attn")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build attn: %v", err)
	}

	tree := t.TempDir()
	target := filepath.Join(tree, "OUTPUT.txt")

	da, err := NewDriverAgent(DriverAgentOptions{
		Provider:       "codex",
		Executable:     codexPath,
		Model:          codexLiveModel(),
		RunTmpDir:      tmp,
		AttnExecutable: attnPath,
		MaxRetries:     1,
		WorkingTree:    tree,
	})
	if err != nil {
		t.Fatalf("NewDriverAgent: %v", err)
	}

	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["wrote"],"properties":{"wrote":{"type":"string"}}}`)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	prompt := "Create a file named OUTPUT.txt in the current working directory containing exactly the word PONG. " +
		"Then return a result with the field `wrote` set to the absolute path of the file you created."
	result, runErr := da.runWithSchema(ctx, ordForTest(), prompt, schema, da.defaultRunCWD(), da.model)
	if runErr != nil {
		t.Fatalf("live writable codex round-trip failed: %v", runErr)
	}

	var obj struct {
		Wrote string `json:"wrote"`
	}
	if err := json.Unmarshal(result, &obj); err != nil {
		t.Fatalf("result is not the schema object: %s (%v)", result, err)
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("subagent did not write OUTPUT.txt in the working tree: %v", err)
	}
	if !strings.Contains(string(contents), "PONG") {
		t.Fatalf("OUTPUT.txt contents = %q, want it to contain PONG", contents)
	}
	t.Logf("live writable round-trip: result=%s, file=%q", result, contents)
}

func codexAuthPath() string {
	if home := os.Getenv("CODEX_HOME"); strings.TrimSpace(home) != "" {
		return filepath.Join(home, "auth.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "auth.json")
}

func codexLiveModel() string {
	if m := strings.TrimSpace(os.Getenv("ATTN_LIVE_CODEX_MODEL")); m != "" {
		return m
	}
	return "gpt-5-codex"
}
