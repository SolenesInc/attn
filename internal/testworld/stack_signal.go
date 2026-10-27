package testworld

import "os"

func (s *Stack) Signal(signal os.Signal) {
	s.T.Helper()
	if s.daemon == nil {
		s.T.Fatal("Signal: daemon is not running")
	}
	if err := s.daemon.Signal(signal); err != nil {
		s.T.Fatal(err)
	}
}
