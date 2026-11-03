package diag

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

func BootTime() (time.Time, error) {
	boot, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return time.Time{}, fmt.Errorf("read boot time: sysctl kern.boottime: %w", err)
	}
	return time.Unix(boot.Unix()), nil
}
