package agent

type Descriptor struct {
	Name       string `json:"name"`
	Plugin     string `json:"plugin,omitempty"`
	Executable string `json:"executable,omitempty"`
	Health     string `json:"health"`
	Detail     string `json:"detail,omitempty"`
	ModelPin   bool   `json:"model_pin"`
	EffortPin  bool   `json:"effort_pin"`
}

const (
	HealthHealthy   = "healthy"
	HealthUnhealthy = "unhealthy"
	HealthUnknown   = "unknown"
)
