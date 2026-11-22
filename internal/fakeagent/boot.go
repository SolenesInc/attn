package fakeagent

import "encoding/json"

type bootingResult struct {
	Exit         bool             `json:"exit,omitempty"`
	Code         int              `json:"code,omitempty"`
	Screen       string           `json:"screen,omitempty"`
	WaitForEnter bool             `json:"wait_for_enter,omitempty"`
	FolderTrust  bool             `json:"folder_trust,omitempty"`
	Protocol     int              `json:"protocol,omitempty"`
	Providers    []ServerProvider `json:"providers,omitempty"`
}

const claudeFolderTrustScreen = "Quick safety check: Is this a project you created or one you trust?\r\n\r\n" +
	"❯ No, exit\r\n  Yes, I trust this folder\r\n\r\nEnter to confirm · Esc to cancel"

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

// AnswerProtocolAtNextBoot makes the next app server that starts answer every initialize with this protocol version, as an older server would.
func (k *Kit) AnswerProtocolAtNextBoot(version int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.nextBootResult = &bootingResult{Protocol: version}
}

// ServeProvidersAtNextBoot makes the next app server that starts list these providers, in this order.
func (k *Kit) ServeProvidersAtNextBoot(providers ...ServerProvider) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.nextBootResult = &bootingResult{Providers: providers}
}

func (k *Kit) AskFolderTrustAtNextBoot() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.nextBootResult = &bootingResult{FolderTrust: true}
}

func (k *Kit) boot(params json.RawMessage) (any, error) {
	var booting bootingParams
	if err := json.Unmarshal(params, &booting); err != nil {
		return nil, err
	}
	k.awaitBoot(booting.Role)
	k.mu.Lock()
	defer k.mu.Unlock()
	result := bootingResult{}
	if booting.Role == roleCodexServer || booting.Role == roleCodexVersionProbe {
		return result, nil
	}
	if k.nextBootResult != nil {
		result, k.nextBootResult = *k.nextBootResult, nil
	}
	return result, nil
}
