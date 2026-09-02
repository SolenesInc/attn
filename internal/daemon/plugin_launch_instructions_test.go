package daemon

import (
	"os"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/hooks"
)

func TestPreparePluginLaunchInstructionsBeforeSessionPersistence(t *testing.T) {
	d := newEnrolledDaemon(t, "")
	t.Cleanup(func() { _ = d.store.Close() })
	addTestWorkspace(d, "workspace-a", t.TempDir())

	instructions, err := d.preparePluginLaunchInstructions("session-a", "workspace-a", false, true)
	if err != nil {
		t.Fatalf("preparePluginLaunchInstructions: %v", err)
	}
	if d.store.Get("session-a") != nil {
		t.Fatal("instruction preparation persisted a provisional session")
	}
	if instructions.Kind != pluginInstructionKindAgent || instructions.WorkspaceID != "workspace-a" {
		t.Fatalf("instructions = %+v, want agent kind for workspace-a", instructions)
	}
	if !strings.Contains(instructions.Content, hooks.AgentGuidance) || !strings.Contains(instructions.Content, hooks.GardenGuidance) {
		t.Fatalf("instructions content did not compose existing guidance: %q", instructions.Content)
	}
	if strings.Contains(instructions.Content, "shared context") {
		t.Fatalf("launch instructions still point at the workspace context: %q", instructions.Content)
	}
	if _, err := os.Stat(workspaceContextCheckoutDir(d.dataRoot, "session-a")); !os.IsNotExist(err) {
		t.Fatalf("launch preparation created a workspace checkout: %v", err)
	}
}

func TestPreparePluginChiefInstructionsUsesNotebook(t *testing.T) {
	d := newEnrolledDaemon(t, "")
	t.Cleanup(func() { _ = d.store.Close() })
	addTestWorkspace(d, "workspace-a", t.TempDir())
	notebookRoot := t.TempDir()
	d.store.SetSetting(SettingNotebookRoot, notebookRoot)

	instructions, err := d.preparePluginLaunchInstructions("session-a", "workspace-a", true, true)
	if err != nil {
		t.Fatalf("preparePluginLaunchInstructions: %v", err)
	}
	if instructions.Kind != pluginInstructionKindChief || instructions.NotebookRoot != notebookRoot {
		t.Fatalf("chief instructions = %+v", instructions)
	}
	if !strings.Contains(instructions.Content, "You are the chief of staff") || !strings.Contains(instructions.Content, notebookRoot) || !strings.Contains(instructions.Content, hooks.GardenGuidance) {
		t.Fatalf("chief guidance = %q", instructions.Content)
	}
	if _, err := os.Stat(workspaceContextCheckoutDir(d.dataRoot, "session-a")); !os.IsNotExist(err) {
		t.Fatalf("chief preparation created workspace checkout: %v", err)
	}
}

func TestPreparePluginLaunchInstructionsOutpostOmitsGarden(t *testing.T) {
	d := newEnrolledDaemon(t, "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	t.Cleanup(func() { _ = d.store.Close() })
	addTestWorkspace(d, "workspace-a", t.TempDir())

	instructions, err := d.preparePluginLaunchInstructions("session-a", "workspace-a", false, true)
	if err != nil {
		t.Fatalf("preparePluginLaunchInstructions: %v", err)
	}
	if strings.Contains(instructions.Content, hooks.GardenGuidance) {
		t.Fatalf("outpost launch carried home garden guidance: %q", instructions.Content)
	}
	if !strings.Contains(instructions.Content, hooks.AgentGuidance) {
		t.Fatalf("outpost launch lost the agent guidance: %q", instructions.Content)
	}
}

func TestPreparePluginLaunchInstructionsGatePullRequestSelfReporting(t *testing.T) {
	d := newEnrolledDaemon(t, "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	t.Cleanup(func() { _ = d.store.Close() })
	addTestWorkspace(d, "workspace-a", t.TempDir())

	told, err := d.preparePluginLaunchInstructions("session-a", "workspace-a", false, true)
	if err != nil {
		t.Fatalf("preparePluginLaunchInstructions: %v", err)
	}
	if !strings.Contains(told.Content, hooks.PullRequestSelfReportGuidance) {
		t.Fatal("a harness that reports nothing missed the pull request block")
	}

	quiet, err := d.preparePluginLaunchInstructions("session-b", "workspace-a", false, false)
	if err != nil {
		t.Fatalf("preparePluginLaunchInstructions: %v", err)
	}
	if strings.Contains(quiet.Content, hooks.PullRequestSelfReportGuidance) {
		t.Fatal("a reporting harness was told to record its own pull requests")
	}

	chief, err := d.preparePluginLaunchInstructions("session-a", "workspace-a", true, false)
	if err != nil {
		t.Fatalf("preparePluginLaunchInstructions: %v", err)
	}
	if strings.Contains(chief.Content, hooks.PullRequestSelfReportGuidance) {
		t.Fatal("a reporting chief was told to record its own pull requests")
	}
}
