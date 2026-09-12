package automode

import (
	"fmt"
	"strings"
)

type GuardianSelection struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
}

func ValidateGuardian(selection GuardianSelection) error {
	if (selection.Provider == "") != (selection.Model == "") {
		return fmt.Errorf("guardian provider and model must both be set, or both empty to follow the session model")
	}
	for _, value := range []string{selection.Provider, selection.Model} {
		if strings.ContainsAny(value, " \t\n\r") {
			return fmt.Errorf("guardian provider/model cannot contain whitespace: %q", value)
		}
	}
	switch selection.Effort {
	case "", "off", "minimal", "low", "medium", "high", "xhigh", "max":
		return nil
	default:
		return fmt.Errorf("unknown guardian effort %q (want default, off, minimal, low, medium, high, xhigh or max)", selection.Effort)
	}
}

func (s GuardianSelection) Describe() string {
	model := "follow session model"
	if s.Provider != "" {
		model = s.Provider + "/" + s.Model
	}
	effort := s.Effort
	if effort == "" {
		effort = "default"
	}
	return model + ", effort " + effort
}
