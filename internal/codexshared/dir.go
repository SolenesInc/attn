package codexshared

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func SocketDir(dataDir string) string {
	sum := sha256.Sum256([]byte(dataDir))
	return filepath.Join("/tmp", "attn-cx-"+hex.EncodeToString(sum[:8]))
}

func socketBehindRemovedLink(link string) (string, bool) {
	temporaryRoot, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		return "", false
	}
	daemonDir := filepath.Join(temporaryRoot, fmt.Sprintf("codex-daemon-%d", os.Geteuid()))
	if !privateToThisUser(daemonDir) {
		return "", false
	}
	folder := filepath.Dir(link)
	folderParent, err := filepath.EvalSymlinks(filepath.Dir(folder))
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256([]byte(filepath.Join(folderParent, filepath.Base(folder), filepath.Base(link))))
	socket := filepath.Join(daemonDir, hex.EncodeToString(sum[:]))
	info, err := os.Lstat(socket)
	if err != nil || info.Mode().Type() != os.ModeSocket {
		return "", false
	}
	return socket, true
}

func privateToThisUser(dir string) bool {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return false
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(owner.Uid) == os.Geteuid()
}
