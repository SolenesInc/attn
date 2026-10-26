package testworld

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/store"
)

func AttnOutpostFixture(t testing.TB, kind string) string {
	t.Helper()
	key := "outpost-fixture-" + kind
	once, _ := versionedBinaries.LoadOrStore(key, sync.OnceValues(func() (string, error) { return buildOutpostFixture(kind) }))
	binary, err := once.(func() (string, error))()
	if err != nil {
		t.Fatal(err)
	}
	return binary
}

func buildOutpostFixture(kind string) (string, error) {
	module, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	root := filepath.Dir(strings.TrimSpace(string(module)))
	name, transform := "", func(s string) string { return s }
	switch kind {
	case "newer-schema":
		name = "internal/store/migrations.go"
		transform = func(s string) string {
			return s + fmt.Sprintf("\nfunc init(){registerMigration(migration{version:%d,desc:\"future outpost schema\",sql:\"SELECT 1\"})}\n", store.LatestSchemaVersion()+1)
		}
	case "before-updates":
		name = "internal/daemon/outpost_listen.go"
		transform = func(s string) string { return strings.ReplaceAll(s, "d.outpostUpdateServer()", "nil") }
	default:
		return "", fmt.Errorf("unknown outpost binary fixture %q", kind)
	}
	source := filepath.Join(root, name)
	data, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(processDir, kind+"-")
	if err != nil {
		return "", err
	}
	replacement := filepath.Join(dir, "source.go")
	if err := os.WriteFile(replacement, []byte(transform(string(data))), 0600); err != nil {
		return "", err
	}
	overlay, _ := json.Marshal(map[string]any{"Replace": map[string]string{source: replacement}})
	overlayPath := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0600); err != nil {
		return "", err
	}
	binary := filepath.Join(dir, "attn")
	build := exec.Command("go", "build", "-overlay", overlayPath, "-ldflags", "-X github.com/victorarias/attn/internal/buildinfo.Version=fixture-"+kind, "-o", binary, "github.com/victorarias/attn/cmd/attn")
	if out, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build %s: %w: %s", kind, err, out)
	}
	return binary, nil
}
