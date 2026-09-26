package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestIndexRootCapsAfterFilteringAndSaysWhenItTruncated(t *testing.T) {
	root := t.TempDir()
	for i := range 12 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("noise%02d.txt", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"zz-late.md", "aa-early.MD"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d := &Daemon{gitExec: testGitExecutor(t, productionGitExecutorConfig)}
	for _, tc := range []struct {
		extensions    []string
		wantCount     int
		wantTruncated bool
		wantFiles     []string
	}{
		{nil, 5, true, nil},
		{[]string{".MD"}, 2, false, []string{"aa-early.MD", "zz-late.md"}},
	} {
		files, truncated, err := d.indexRoot(root, 5, tc.extensions)
		if err != nil || truncated != tc.wantTruncated || len(files) != tc.wantCount || (tc.wantFiles != nil && !equalStrings(files, tc.wantFiles)) {
			t.Errorf("indexRoot(cap 5, %v) = %v, truncated %v, err %v; want %d files, truncated %v", tc.extensions, files, truncated, err, tc.wantCount, tc.wantTruncated)
		}
	}
}
