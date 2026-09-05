package agent

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"testing"

	"github.com/victorarias/attn/internal/prompttest"
)

func TestLegacyPromptCompatibility(t *testing.T) {
	out := map[string]string{}
	t.Setenv("ATTN_CLAUDE_PEER_MESSAGING", "false")
	for mask := 0; mask < 32; mask++ {
		for _, resume := range []bool{false, true} {
			opts := SpawnOpts{SessionID: "session-id", CWD: "/tmp/work", InitialPrompt: "Task λ {{literal}}\nsecond line", SettingsPath: "/tmp/settings.json", WrapperPath: "/tmp/attn", SocketPath: "/tmp/attn.sock", InjectWorkflowGuidance: mask&4 != 0, Garden: mask&8 != 0}
			if mask&1 != 0 {
				opts.NotebookRoot = "/tmp/book"
			}
			if mask&2 != 0 {
				opts.SelfReportPullRequests = true
			}
			if mask&16 != 0 {
				opts.CrewPriming = "Crew λ {{literal}}"
			}
			if resume {
				opts.ResumeSessionID = "resume-id"
			}
			for _, driver := range []Driver{&Claude{}, &Codex{}, &Copilot{}} {
				opts.Executable = driver.Name()
				opts.ConfigOverrides = nil
				if driver.Name() == "codex" {
					opts.ConfigOverrides = (&Codex{}).GenerateConfigOverrides(opts)
				}
				raw, err := json.Marshal(driver.BuildCommand(opts).Args)
				if err != nil {
					t.Fatal(err)
				}
				out[fmt.Sprintf("%s/%d/%t", driver.Name(), mask, resume)] = string(raw)
			}
		}
	}
	for _, system := range []string{"", " System λ {{literal}} "} {
		request := HeadlessTaskRequest{Prompt: "User message {{literal}}", SystemPrompt: system, WorkDir: "/tmp/run", CWD: "/tmp/work", Model: "model", DisableTools: true}
		for name, args := range map[string][]string{"claude": claudeHeadlessArgs(request), "codex": codexToolFreeHeadlessArgs(request, 0), "copilot": copilotToolFreeHeadlessArgs(request)} {
			raw, err := json.Marshal(args)
			if err != nil {
				t.Fatal(err)
			}
			out["headless/"+name+"/"+system] = string(raw)
		}
	}
	err := fs.WalkDir(attnSkillFiles, "attn_skill", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := fs.ReadFile(attnSkillFiles, path)
		out[path] = string(raw)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	prompttest.Equal(t, "agent-delivery", out)
}
