package bridge_test

import "golang.org/x/sys/unix"

func socketUnsent(fd uintptr, accepted int64) (int64, error) {
	info, err := unix.GetsockoptTCPConnectionInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_CONNECTION_INFO)
	if err != nil {
		return 0, err
	}
	return accepted - int64(info.Txbytes) + int64(info.Txretransmitbytes), nil
}
