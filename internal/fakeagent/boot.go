package fakeagent

import (
	"encoding/json"
	"time"
)

type bootingResult struct {
	Exit   bool   `json:"exit,omitempty"`
	Code   int    `json:"code,omitempty"`
	Screen string `json:"screen,omitempty"`
}

func (k *Kit) ExitAtNextBoot(code int, screen string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.nextBootResult = &bootingResult{Exit: true, Code: code, Screen: screen}
}

func (k *Kit) ScreenAtNextBoot(screen string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.nextBootResult = &bootingResult{Screen: screen}
}

func (k *Kit) boot(params json.RawMessage) (any, error) {
	var booting bootingParams
	if err := json.Unmarshal(params, &booting); err != nil {
		return nil, err
	}
	k.awaitBoot(booting.AttnSessionID)
	k.mu.Lock()
	defer k.mu.Unlock()
	result := bootingResult{}
	if k.nextBootResult != nil {
		result, k.nextBootResult = *k.nextBootResult, nil
	}
	return result, nil
}

func (k *Kit) AwaitHeldBoot() {
	k.t.Helper()
	k.mu.Lock()
	ask := k.bootAsk
	k.mu.Unlock()
	if ask == nil {
		k.t.Fatal("no boot is held")
	}
	select {
	case <-ask:
	case <-time.After(HangGuard):
		k.t.Fatalf("no agent asked to boot within %s%s", HangGuard, k.failureSummary())
	}
}
