package fakeagent

func (k *Kit) AnswerHeadlessTasks(answer func(*HeadlessTask)) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.answerer = answer
}

func (k *Kit) headlessAnswerer() func(*HeadlessTask) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.answerer
}
