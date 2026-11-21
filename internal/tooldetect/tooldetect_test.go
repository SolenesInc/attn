package tooldetect_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/victorarias/attn/internal/tooldetect"
)

func corpus[T any](t *testing.T, name string) []T {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "cli", "attn", "testdata", "hooks", name))
	if err != nil {
		t.Fatal(err)
	}
	var cases []T
	if err := json.Unmarshal(raw, &cases); err != nil || len(cases) == 0 {
		t.Fatalf("corpus %s: %d cases, %v", name, len(cases), err)
	}
	return cases
}

func TestMarkdownEditsAgainstTheSharedCorpus(t *testing.T) {
	for _, c := range corpus[struct {
		Name      string          `json:"name"`
		ToolName  string          `json:"tool_name"`
		ToolInput json.RawMessage `json:"tool_input"`
		Cwd       string          `json:"cwd"`
		Home      string          `json:"home"`
		Want      []string        `json:"want"`
	}](t, "markdown-edits.json") {
		if got := tooldetect.MarkdownEdits(c.ToolName, c.ToolInput, c.Cwd, c.Home); !slices.Equal(got, c.Want) && len(got)+len(c.Want) > 0 {
			t.Errorf("%s: got %q, want %q", c.Name, got, c.Want)
		}
	}
}

func TestPullRequestsAgainstTheSharedCorpus(t *testing.T) {
	for _, c := range corpus[struct {
		Name       string          `json:"name"`
		ToolName   string          `json:"tool_name"`
		ToolInput  json.RawMessage `json:"tool_input"`
		ToolOutput json.RawMessage `json:"tool_output"`
		Want       []string        `json:"want"`
	}](t, "pull-request-extraction.json") {
		if got := tooldetect.PullRequestsCreated(c.ToolName, c.ToolInput, string(c.ToolOutput)); !slices.Equal(got, c.Want) && len(got)+len(c.Want) > 0 {
			t.Errorf("%s: got %q, want %q", c.Name, got, c.Want)
		}
	}
}
