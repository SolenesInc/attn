package main_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

func TestAFailedBuildLeavesTheServingVersionInPlace(t *testing.T) {
	t.Parallel()
	s := testworld.NewStack(t)
	s.Start()
	applyApp(t, s, "regressing", subscribedApp("regressing", "ticket.*", false), "export default {}\n")
	var good protocol.AppStatusResult
	s.Attn("app", "status", "regressing", "--json").JSON(t, &good)
	if good.App.CurrentVersion == nil || good.Versions != 1 {
		t.Fatalf("after the first apply: %+v, want one version serving", good)
	}

	dir := s.Path("regressing")
	if err := os.WriteFile(filepath.Join(dir, "src", "index.ts"), []byte("export default {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if broken := s.Attn("app", "apply", dir); broken.Code == 0 {
		t.Fatalf("attn app apply of a source that does not build exited 0: %s", broken.Stdout)
	}

	var after protocol.AppStatusResult
	s.Attn("app", "status", "regressing", "--json").JSON(t, &after)
	if after.App.CurrentVersion == nil || after.App.CurrentVersion.ID != good.App.CurrentVersion.ID || after.Versions != 1 {
		t.Fatalf("after the failed build: serving %+v of %d versions, want version %d of one", after.App.CurrentVersion, after.Versions, good.App.CurrentVersion.ID)
	}
	if _, err := os.Stat(after.App.CurrentVersion.ArtifactPath); err != nil {
		t.Errorf("the serving version's artifact is gone: %v", err)
	}
}
