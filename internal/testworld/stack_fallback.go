package testworld

func (s *Stack) StartAcceptingAFallbackPTYBackend() {
	s.T.Helper()
	s.allowFallback = true
	defer func() { s.allowFallback = false }()
	s.start()
}
