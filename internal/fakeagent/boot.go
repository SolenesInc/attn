package fakeagent

import "encoding/json"

type bootingResult struct {
	Exit   bool   `json:"exit,omitempty"`
	Code   int    `json:"code,omitempty"`
	Screen string `json:"screen,omitempty"`
}

func (k *Kit) ExitAtNextBoot(code int, screen string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.nextExit = &bootingResult{Exit: true, Code: code, Screen: screen}
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
	if k.nextExit != nil {
		result, k.nextExit = *k.nextExit, nil
	}
	return result, nil
}
