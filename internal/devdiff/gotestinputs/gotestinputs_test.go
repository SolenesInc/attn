package gotestinputs

import (
	"reflect"
	"testing"
)

func TestKeepsEveryPathAGoTestReads(t *testing.T) {
	read := []string{
		"internal/store/sqlite.go",
		"app/src-tauri/build_helper.go",
		"internal/prompts/content/agent.md",
		"apphost/src/index.ts",
		"scripts/test-go.sh",
		"changelog.d/some-change.yaml",
		"app/pnpm-lock.yaml",
		"app/src/hooks/useDaemonSocket.ts",
		"app/src/ghostty/testdata/native-snapshot.bin",
		"sdk/attn-app/package.json",
		"go.mod",
		"Makefile",
		"ghostty-vt.pin",
	}
	if got := Among(read); !reflect.DeepEqual(got, read) {
		t.Fatalf("Among dropped inputs: got %v", got)
	}
}

func TestDropsPathsNoGoTestReads(t *testing.T) {
	unread := []string{
		"docs/glossary.md",
		"AGENTS.md",
		"app/src/App.tsx",
		"app/package.json",
		"plugins/attn-pi/src/index.ts",
		"sdk/attn-app/src/index.ts",
		".github/workflows/ci.yml",
	}
	if got := Among(unread); len(got) != 0 {
		t.Fatalf("Among kept %v", got)
	}
}
