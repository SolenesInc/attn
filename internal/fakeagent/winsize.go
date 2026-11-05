package fakeagent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	creackpty "github.com/creack/pty"
)

const winsizeProgram = "attn-fake-winsize"

func runWinsize() int {
	size, err := creackpty.GetsizeFull(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", winsizeProgram, err)
		return 1
	}
	fmt.Printf("winsize cols=%d rows=%d xpixel=%d ypixel=%d\n", size.Cols, size.Rows, size.X, size.Y)
	return 0
}

func InstallWinsize(t testing.TB, binary, dir string) string {
	t.Helper()
	path := filepath.Join(dir, winsizeProgram)
	if err := os.Symlink(binary, path); err != nil {
		t.Fatal(err)
	}
	return path
}
