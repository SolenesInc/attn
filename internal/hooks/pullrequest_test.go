package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPullRequestCreated(t *testing.T) {
	tests := []struct {
		name         string
		toolName     string
		toolInput    string
		toolResponse string
		want         []string
	}{
		{
			name:         "claude bash reports the url gh printed",
			toolName:     "Bash",
			toolInput:    `{"command":"gh pr create --fill"}`,
			toolResponse: `{"stdout":"https://github.com/victorarias/attn/pull/71\n","stderr":"","interrupted":false}`,
			want:         []string{"https://github.com/victorarias/attn/pull/71"},
		},
		{
			name:         "codex sends the tool output as a plain string",
			toolName:     "Bash",
			toolInput:    `{"command":"gh pr create --fill"}`,
			toolResponse: `"https://github.com/victorarias/attn/pull/71\n"`,
			want:         []string{"https://github.com/victorarias/attn/pull/71"},
		},
		{
			name:         "codex reporting a failed gh call has no url to report",
			toolName:     "Bash",
			toolInput:    `{"command":"gh pr create --fill"}`,
			toolResponse: `"failed to run git: exit status 128\n"`,
			want:         nil,
		},
		{
			name:         "an argv-form command under another shell tool name",
			toolName:     "exec_command",
			toolInput:    `{"cmd":["bash","-lc","gh pr create --title x --body y"]}`,
			toolResponse: `{"output":"https://github.com/victorarias/attn/pull/8"}`,
			want:         []string{"https://github.com/victorarias/attn/pull/8"},
		},
		{
			name:         "an enterprise host is reported like any other",
			toolName:     "Bash",
			toolInput:    `{"command":"gh pr create"}`,
			toolResponse: `{"stdout":"https://ghe.example.test/acme/widget/pull/12"}`,
			want:         []string{"https://ghe.example.test/acme/widget/pull/12"},
		},
		{
			name:         "a chained command still reports",
			toolName:     "Bash",
			toolInput:    `{"command":"git push -u origin head && gh pr create --fill"}`,
			toolResponse: `{"stdout":"branch set up\nhttps://github.com/victorarias/attn/pull/71"}`,
			want:         []string{"https://github.com/victorarias/attn/pull/71"},
		},
		{
			name:         "two pull requests in one call are both reported, once each",
			toolName:     "Bash",
			toolInput:    `{"command":"gh pr create --fill; gh pr create --fill --repo other/repo"}`,
			toolResponse: `{"stdout":"https://github.com/a/b/pull/1\nhttps://github.com/a/b/pull/2\nhttps://github.com/a/b/pull/1"}`,
			want:         []string{"https://github.com/a/b/pull/1", "https://github.com/a/b/pull/2"},
		},
		{
			name:         "no url printed, nothing to report",
			toolName:     "Bash",
			toolInput:    `{"command":"gh pr create --fill"}`,
			toolResponse: `{"stdout":"","stderr":"a pull request for branch already exists"}`,
			want:         nil,
		},
		{
			name:         "a url from another gh command is not a creation",
			toolName:     "Bash",
			toolInput:    `{"command":"gh pr view --json url"}`,
			toolResponse: `{"stdout":"https://github.com/victorarias/attn/pull/71"}`,
			want:         nil,
		},
		{
			name:         "the tool has to be a shell",
			toolName:     "Edit",
			toolInput:    `{"command":"gh pr create"}`,
			toolResponse: `{"stdout":"https://github.com/victorarias/attn/pull/71"}`,
			want:         nil,
		},
		{
			name:         "a harness that sends no tool output reports nothing",
			toolName:     "Bash",
			toolInput:    `{"command":"gh pr create --fill"}`,
			toolResponse: ``,
			want:         nil,
		},
		{
			name:         "a repository whose name starts with gh is not a create",
			toolName:     "Bash",
			toolInput:    `{"command":"ghost pr create"}`,
			toolResponse: `{"stdout":"https://github.com/victorarias/attn/pull/71"}`,
			want:         nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := PullRequestCreated(tc.toolName, json.RawMessage(tc.toolInput), json.RawMessage(tc.toolResponse))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("PullRequestCreated = %v, want %v", got, tc.want)
			}
		})
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
