package testworld

func (s *Stack) Kill() {
	s.T.Helper()
	if s.daemon == nil {
		s.T.Fatal("Kill: the stack's daemon is not running")
	}
	_ = s.daemon.Kill()
	<-s.exited
	s.ClosePeers()
	s.daemon, s.exited = nil, nil
}
