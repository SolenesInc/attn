package agent

import (
	"slices"
	"testing"
)

func TestCodexBuildCommand_ConfigPlacement(t *testing.T) {
	for _, mode := range []struct {
		name, id string
		picker   bool
		prefix   []string
	}{
		{"fresh", "", false, nil},
		{"resume", "native-id", false, []string{"resume", "native-id"}},
		{"picker", "", true, []string{"resume"}},
		{"explicit wins over picker", "native-id", true, []string{"resume", "native-id"}},
	} {
		for _, settings := range []string{"hooks only", "effort", "compaction", "auto approve", "all", "yolo"} {
			t.Run(mode.name+"/"+settings, func(t *testing.T) {
				opts := SpawnOpts{
					Executable: "codex", CWD: "/tmp/a project",
					ResumeSessionID: mode.id, ResumePicker: mode.picker,
					ConfigOverrides: []string{" ", "features.hooks=true", "hooks.SessionStart=[]", ""},
				}
				want := append([]string{"codex"}, mode.prefix...)
				want = append(want, "-c", "features.hooks=true", "-c", "hooks.SessionStart=[]", "-C", opts.CWD)
				if settings == "all" || settings == "yolo" {
					opts.Model = " model "
					opts.AwarenessDirs = []string{" /tmp/another project ", "", "/tmp/third"}
					opts.InitialPrompt = "--literal prompt\nsecond line"
					want = append(want, "--add-dir", "/tmp/another project", "--add-dir", "/tmp/third", "--model", "model")
				}
				if settings == "compaction" || settings == "all" || settings == "yolo" {
					opts.AutoCompactWindow = 200000
					want = append(want, "-c", "model_auto_compact_token_limit=200000")
				}
				if settings == "effort" || settings == "all" || settings == "yolo" {
					opts.Effort = " low "
					want = append(want, "-c", `model_reasoning_effort="low"`)
				}
				if settings == "auto approve" || settings == "all" || settings == "yolo" {
					opts.AutoApprove = true
					if settings == "yolo" {
						opts.YoloMode = true
						want = append(want, "--dangerously-bypass-approvals-and-sandbox")
					} else {
						want = append(want, "-c", `approval_policy="on-request"`, "-c", `approvals_reviewer="auto_review"`)
					}
				}
				if opts.InitialPrompt != "" {
					want = append(want, "--", opts.InitialPrompt)
				}
				if got := (&Codex{}).BuildCommand(opts).Args; !slices.Equal(got, want) {
					t.Fatalf("argv = %#v\nwant = %#v", got, want)
				}
			})
		}
	}
}
