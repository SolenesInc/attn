package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/activity"
	"github.com/victorarias/attn/internal/config"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "activity-bench-test-")
	if err != nil {
		panic(err)
	}
	config.ScopeTestEnvironment(dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestDefaultPromptsWorkWithoutASourceCheckout(t *testing.T) {
	t.Chdir(t.TempDir())
	templates, err := loadTemplates("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 1 || templates[0] != activity.Baseline() {
		t.Fatalf("default templates = %+v", templates)
	}
	if _, err := loadTemplates("", []string{"missing"}); err == nil {
		t.Fatal("unknown variant accepted")
	}
}

func TestCustomPromptDirectoryAndSelection(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(name+" {{WINDOW}}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	templates, err := loadTemplates(dir, []string{"second"})
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 1 || templates[0].Render(activity.Input{Window: "events"}).User != "second events" {
		t.Fatalf("custom templates = %+v", templates)
	}
}
