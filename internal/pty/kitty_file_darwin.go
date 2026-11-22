package pty

import (
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

func openedFilePath(file *os.File) (string, error) {
	path := make([]byte, unix.PathMax)
	_, err := unix.FcntlInt(file.Fd(), unix.F_GETPATH, int(uintptr(unsafe.Pointer(&path[0]))))
	runtime.KeepAlive(path)
	if err != nil {
		return "", err
	}
	return unix.ByteSliceToString(path), nil
}
