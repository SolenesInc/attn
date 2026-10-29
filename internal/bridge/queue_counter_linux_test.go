package bridge_test

import "golang.org/x/sys/unix"

func socketUnsent(fd uintptr, _ int64) (int64, error) {
	info, err := unix.GetsockoptTCPInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_INFO)
	if err != nil {
		return 0, err
	}
	return int64(info.Notsent_bytes), nil
}
