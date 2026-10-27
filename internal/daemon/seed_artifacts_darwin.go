package daemon

import "golang.org/x/sys/unix"

func renameSeedArtifactStage(source, destination string) error {
	return unix.RenamexNp(source, destination, unix.RENAME_EXCL)
}
