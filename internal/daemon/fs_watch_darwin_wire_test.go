//go:build darwin

package daemon_test

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestFsWatchDescriptorCountDoesNotGrowWithTreeSize(t *testing.T) {
	w := newFsWorld(t)
	app := pickerApp(w)
	root := fsDir(t, "large")
	for i := range 3000 {
		fsWriteFile(t, fmt.Sprintf("%s/dir-%d/file-%d.txt", root, i/100, i), []byte("x"))
	}
	count := func() int {
		t.Helper()
		output, err := exec.Command("lsof", "-nP", "-a", "-p", strconv.Itoa(os.Getpid()), "-F", "f").Output()
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, line := range strings.Split(string(output), "\n") {
			if len(line) > 1 && line[0] == 'f' && line[1] >= '0' && line[1] <= '9' {
				n++
			}
		}
		return n
	}
	before := count()
	fsMustWatch(t, app, root)
	if added := count() - before; added >= 100 {
		t.Fatalf("watching 3000 files added %d descriptors, want fewer than 100", added)
	}
	fsAwaitLaterChange(t, app, root)
}
