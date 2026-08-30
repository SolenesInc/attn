package daemonctl

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// The kernel answers what lsof would, and a Linux box need not have lsof.
// Membership, not exclusivity, so Stop's own open fd is harmless.
func pidHoldsPIDFile(pid int, pidPath string) (bool, error) {
	info, err := os.Stat(pidPath)
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", pidPath, err)
	}
	target, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("stat %s: no device/inode identity", pidPath)
	}
	fdDir := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", fdDir, err)
	}
	for _, entry := range entries {
		// An fd closed mid-walk, or one that will not resolve, is not proof of
		// holding, so it is skipped instead of failing the whole check.
		openInfo, err := os.Stat(filepath.Join(fdDir, entry.Name()))
		if err != nil {
			continue
		}
		open, ok := openInfo.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}
		if open.Dev == target.Dev && open.Ino == target.Ino {
			return true, nil
		}
	}
	return false, nil
}
