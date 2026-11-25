package builtin

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"testing"

	"github.com/victorarias/attn/internal/harness"
	"github.com/victorarias/attn/internal/harness/internal/claude"
	"github.com/victorarias/attn/internal/harness/internal/codex"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/prompttest"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	out := map[string]string{}
	for mask := 0; mask < 16; mask += 2 {
		for _, resume := range []bool{false, true} {
			opts := harness.DirectLaunchOpts{TerminalID: "session-id", CWD: "/tmp/work", InitialPrompt: "Task λ {{literal}}\nsecond line", WrapperPath: "/tmp/attn", SocketPath: "/tmp/attn.sock", Guidance: prompts.Launch{Garden: mask&4 != 0}}
			if mask&2 != 0 {
				opts.Guidance.SelfReportPullRequests = true
			}
			if mask&8 != 0 {
				opts.Guidance.Crew = "Crew λ {{literal}}"
			}
			if resume {
				opts.ResumeSessionID = "resume-id"
			}
			for _, tc := range []struct {
				name string
				args func(harness.DirectLaunchOpts) []string
			}{
				{name: "claude", args: func(o harness.DirectLaunchOpts) []string { return claude.BuildArgsForPromptTest(o, "{}") }},
				{name: "codex", args: codex.BuildArgsForPromptTest},
			} {
				opts.Executable = tc.name
				raw, err := json.Marshal(tc.args(opts))
				if err != nil {
					t.Fatal(err)
				}
				out[fmt.Sprintf("%s/%d/%t", tc.name, mask, resume)] = string(raw)
			}
		}
	}
	for system, instructionsPath := range map[string]string{"": "", " System λ {{literal}} ": "/tmp/run/attn-instructions.md"} {
		request := headless.TaskRequest{Prompt: "User message {{literal}}", SystemPrompt: system, WorkDir: "/tmp/run", Model: "model", DisableTools: true}
		for name, args := range map[string][]string{"claude": claude.HeadlessArgs(request, instructionsPath), "codex": codex.HeadlessArgs(request, 0, instructionsPath)} {
			raw, err := json.Marshal(args)
			if err != nil {
				t.Fatal(err)
			}
			out["headless/"+name+"/"+system] = string(raw)
		}
	}
	err := fs.WalkDir(prompts.AttnSkillFiles(), "attn_skill", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := fs.ReadFile(prompts.AttnSkillFiles(), path)
		out[path] = string(raw)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	prompttest.Equal(t, "agent-delivery", out)
}
