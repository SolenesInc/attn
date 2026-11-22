package pty

import (
	"os"
	"strconv"
)

func openedFilePath(file *os.File) (string, error) {
	return os.Readlink("/proc/self/fd/" + strconv.FormatUint(uint64(file.Fd()), 10))
}
