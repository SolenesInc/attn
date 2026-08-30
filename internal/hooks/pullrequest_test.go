package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPullRequestCreatedAgainstTheSharedCorpus(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "pull-request-extraction.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name       string          `json:"name"`
		ToolName   string          `json:"tool_name"`
		ToolInput  json.RawMessage `json:"tool_input"`
		ToolOutput json.RawMessage `json:"tool_output"`
		Want       []string        `json:"want"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("the shared corpus is empty")
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			want := tc.Want
			if len(want) == 0 {
				want = nil
			}
			got := PullRequestCreated(tc.ToolName, tc.ToolInput, tc.ToolOutput)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("PullRequestCreated = %v, want %v", got, want)
			}
		})
	}
}

func TestPullRequestCreatedWithoutAToolResponse(t *testing.T) {
	got := PullRequestCreated("Bash", json.RawMessage(`{"command":"gh pr create --fill"}`), nil)
	if got != nil {
		t.Errorf("PullRequestCreated = %v, want nothing", got)
	}
}

func TestPullRequestCreatedAgainstCapturedCodexPayloads(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "codex-post-tool-use.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("fixture has %d payloads, want the two captured ones", len(lines))
	}

	for _, line := range lines {
		var payload struct {
			ToolName     string          `json:"tool_name"`
			ToolInput    json.RawMessage `json:"tool_input"`
			ToolResponse json.RawMessage `json:"tool_response"`
		}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatalf("decode captured payload: %v", err)
		}
		if payload.ToolName != "Bash" {
			t.Errorf("tool_name = %q, want Bash", payload.ToolName)
		}
		if got := PullRequestCreated(payload.ToolName, payload.ToolInput, payload.ToolResponse); got != nil {
			t.Errorf("`gh pr view` reported %v, want nothing", got)
		}

		created := PullRequestCreated(payload.ToolName, json.RawMessage(`{"command":"gh pr create --fill"}`), payload.ToolResponse)
		want := strings.Contains(string(payload.ToolResponse), "/pull/71")
		if want && (len(created) != 1 || created[0] != "https://github.com/victorarias/attn/pull/71") {
			t.Errorf("created = %v, want the url the capture printed", created)
		}
		if !want && created != nil {
			t.Errorf("created = %v, want nothing from a capture that printed an error", created)
		}
	}
}
