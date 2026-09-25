package daemon

import (
	"sync"
	"time"
)

type FakeClassifier struct {
	mu           sync.Mutex
	defaultState string
	calls        int
}

func NewFakeClassifier(defaultState string) *FakeClassifier {
	return &FakeClassifier{defaultState: defaultState}
}

func (f *FakeClassifier) Classify(_ string, _ time.Duration) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.defaultState, nil
}

func (f *FakeClassifier) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}
