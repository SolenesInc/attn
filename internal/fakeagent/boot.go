package fakeagent

import "encoding/json"

type bootingResult struct {
	Exit         bool   `json:"exit,omitempty"`
	Code         int    `json:"code,omitempty"`
	Screen       string `json:"screen,omitempty"`
	WaitForEnter bool   `json:"wait_for_enter,omitempty"`
}

func (k *Kit) ExitAtNextBoot(code int, screen string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.nextBootResult = &bootingResult{Exit: true, Code: code, Screen: screen}
}

func (k *Kit) RequireEnterAtNextBoot(screen string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.nextBootResult = &bootingResult{WaitForEnter: true, Screen: screen}
}

func (k *Kit) boot(params json.RawMessage) (any, error) {
	var booting bootingParams
	if err := json.Unmarshal(params, &booting); err != nil {
		return nil, err
	}
	k.awaitBoot()
	k.mu.Lock()
	defer k.mu.Unlock()
	result := bootingResult{}
	if k.nextBootResult != nil {
		result, k.nextBootResult = *k.nextBootResult, nil
	}
	return result, nil
}
