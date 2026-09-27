package fakeagent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	creackpty "github.com/creack/pty"
)

const winsizeProgram = "attn-fake-winsize"

func init() {
	if filepath.Base(os.Args[0]) != winsizeProgram {
		return
	}
	size, err := creackpty.GetsizeFull(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", winsizeProgram, err)
		os.Exit(1)
	}
	fmt.Printf("winsize cols=%d rows=%d xpixel=%d ypixel=%d\n", size.Cols, size.Rows, size.X, size.Y)
	os.Exit(0)
}

func InstallWinsize(t testing.TB, dir string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, winsizeProgram)
	if err := os.Symlink(self, path); err != nil {
		t.Fatal(err)
	}
	return path
}
