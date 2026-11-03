package testworld

import "syscall"

func (s *Stack) StartWritingFilesOfAtMost(bytes uint64) {
	s.T.Helper()
	var unlimited syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &unlimited); err != nil {
		s.T.Fatal(err)
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: bytes, Max: unlimited.Max}); err != nil {
		s.T.Fatal(err)
	}
	s.launch()
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &unlimited); err != nil {
		s.T.Fatal(err)
	}
	s.checkStarted()
}
