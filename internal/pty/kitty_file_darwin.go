package pty

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

func openedFilePath(file *os.File) (string, error) {
	var path [unix.PathMax]byte
	if err := fcntlPointer(file.Fd(), unix.F_GETPATH, unsafe.Pointer(&path[0])); err != nil {
		return "", err
	}
	return unix.ByteSliceToString(path[:]), nil
}

func fcntlPointer(fd uintptr, cmd int, arg unsafe.Pointer) error {
	return unix.FcntlFstore(fd, cmd, (*unix.Fstore_t)(arg))
}
