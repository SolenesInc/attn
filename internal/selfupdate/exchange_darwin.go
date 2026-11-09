package selfupdate

import "golang.org/x/sys/unix"

func ExchangePaths(a, b string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_SWAP)
}
