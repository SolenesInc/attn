package ptyhosttest

import (
	"os"
	"path/filepath"
	"testing"
)

const DefaultSnapshotFormat = "test-default"

const variantsEnv = "ATTN_TEST_PTY_HOST_VARIANTS"

func Variant(t testing.TB, name string) string {
	t.Helper()
	dir := os.Getenv(variantsEnv)
	if dir == "" {
		t.Fatalf("%s is unset: run this test through scripts/test-go.sh, which provides the PTY host variants listed in pty-host/test-variants", variantsEnv)
	}
	binary := filepath.Join(dir, name, "attn-pty-host")
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("no PTY host variant %q in %s: add it to pty-host/test-variants (%v)", name, dir, err)
	}
	return binary
}
