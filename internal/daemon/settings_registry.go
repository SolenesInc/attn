package daemon

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/prreadiness"
	"github.com/victorarias/attn/internal/sessioncost"
	"github.com/victorarias/attn/internal/setting"
)

const (
	DestinationNewWorktree = "new_worktree"
	DestinationMainRepo    = "main_repo"
)

func (d *Daemon) setting(s *setting.Setting) string {
	if s.Class == setting.Home {
		if status := d.enrollmentStatus(); !status.IsHome() {
			return s.WithDefault(d.store.CopiedSetting(s.Key, status.HomeDaemonID))
		}
	}
	return s.WithDefault(d.store.GetSetting(s.Key))
}

func (d *Daemon) on(s *setting.Setting) bool {
	return parseBooleanSetting(d.setting(s))
}

var settingValidators = map[*setting.Setting]func(*Daemon, string, string) error{
	setting.ChiefContextWindowCap: func(d *Daemon, key, value string) error { return validateContextWindowCap(value) },
	setting.ClaudeExecutable:      func(d *Daemon, key, value string) error { return d.validateExecutableSetting(value) },
	setting.CodexExecutable:       func(d *Daemon, key, value string) error { return d.validateExecutableSetting(value) },
	setting.ModelTierOverrides: func(d *Daemon, key, value string) error {
		_, err := modeltiers.ParseOverrides(value)
		return err
	},
	setting.ProjectsDirectory: func(d *Daemon, key, value string) error {
		return validateProjectsDirectory(value)
	},
	setting.UIScale: func(d *Daemon, key, value string) error {
		return validateUIScale(value)
	},
	setting.GardenScale: func(d *Daemon, key, value string) error {
		if strings.TrimSpace(value) == "" {
			return nil
		}
		return validateUIScale(value)
	},
	setting.EditorExecutable: func(d *Daemon, key, value string) error {
		return validateEditorSetting(value)
	},
	setting.NewSessionAgent: func(d *Daemon, key, value string) error {
		return d.validateNewSessionAgent(value)
	},
	setting.Theme: func(d *Daemon, key, value string) error {
		return validateTheme(value)
	},
	setting.NarrowestTileCols: func(d *Daemon, key, value string) error { return validateNarrowestTileCols(value) },
	setting.SidebarWidth:      func(d *Daemon, key, value string) error { return validateSidebarWidth(value) },
	setting.AutoSettleArmSeconds: func(d *Daemon, key, value string) error {
		return validateAutoSettleSeconds("auto-settle delay", value, autoSettleArmMinSeconds, autoSettleArmMaxSeconds)
	},
	setting.AutoSettleCountdownSeconds: func(d *Daemon, key, value string) error {
		return validateAutoSettleSeconds("auto-settle countdown", value, autoSettleCountdownMinSeconds, autoSettleCountdownMaxSeconds)
	},
	setting.HeadlessContextWindowCap: func(d *Daemon, key, value string) error {
		return validateContextWindowCap(value)
	},
	setting.ActivityConfig: func(d *Daemon, key, value string) error {
		return d.validateActivitySetting(value)
	},
	setting.GardenAdvisor: func(d *Daemon, key, value string) error {
		return d.validateGardenAdvisorSetting(value)
	},
	setting.DictationRefinementAgent: func(d *Daemon, key, value string) error {
		_, err := parseDictationRefinementAgent(value)
		return err
	},
	setting.ActivityIntervals: func(d *Daemon, key, value string) error {
		_, err := parseActivityIntervals(value)
		return err
	},
	setting.ActivityPresenceIdleSeconds: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting(
			"session activity presence idle",
			value,
			activityPresenceIdleMinSeconds,
			activityPresenceIdleMaxSeconds,
		)
	},
	setting.CrewCacheTTLSeconds: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew cache TTL", value, crewCacheTTLMinSeconds, crewCacheTTLMaxSeconds)
	},
	setting.CrewHeartbeatLeadSeconds: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew heartbeat lead", value, crewHeartbeatLeadMinSeconds, crewHeartbeatLeadMaxSeconds)
	},
	setting.CrewAwaySeconds: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew away threshold", value, crewAwayMinSeconds, crewAwayMaxSeconds)
	},
	setting.CrewWakeLimit: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew wake limit", value, 0, crewWakeLimitMax)
	},
	setting.CrewWakeLimitWindowSeconds: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew wake limit window", value, crewWakeLimitWindowMinSecs, crewWakeLimitWindowMaxSecs)
	},
	setting.NotebookRoot: func(d *Daemon, key, value string) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil
		}
		root, err := expandNotebookRoot(value)
		if err != nil {
			return fmt.Errorf("%s %w", key, err)
		}
		if config.HarnessNotebookRoot() != "" {
			if !filepath.IsAbs(root) {
				return fmt.Errorf("%s must be an absolute path", key)
			}
			return config.RequireWithinHarness(key, root)
		}
		return validateNotebookRoot(key, value)
	},
	setting.PullRequestOwnComments: func(d *Daemon, key, value string) error {
		_, err := prreadiness.ParseOwnComments(value)
		return err
	},
	setting.GardenTenderWake: func(d *Daemon, key, value string) error {
		_, err := parseTenderWakeSetting(value)
		return err
	},
	setting.KeybindingsConfig: func(d *Daemon, key, value string) error {
		return validateKeybindingsConfig(value)
	},
	setting.SessionsFilters: func(d *Daemon, key, value string) error {
		return validateSessionsFilters(value)
	},
	setting.Executable:              func(d *Daemon, key, value string) error { return d.validateExecutableSetting(value) },
	setting.NewSessionDestination:   func(d *Daemon, key, value string) error { return validateNewSessionDestination(value) },
	setting.DefaultContextWindowCap: func(d *Daemon, key, value string) error { return validateContextWindowCap(value) },
	setting.CrewCacheTTLFor: func(d *Daemon, key, value string) error {
		return validateBoundedIntSetting("crew cache TTL", value, crewCacheTTLMinSeconds, crewCacheTTLMaxSeconds)
	},
	setting.SessionCostPrice: func(d *Daemon, key, value string) error {
		_, err := sessioncost.ParseOverrides(map[string]string{key: value})
		return err
	},
	setting.SessionCostBilledAs: func(d *Daemon, key, value string) error { return sessioncost.ValidateBilledAs(key, value) },
}

func init() {
	for _, spec := range setting.All() {
		if spec.Kind == setting.Checked && settingValidators[spec] == nil {
			panic("setting " + spec.Key + " is declared Checked and has no validator in settingValidators")
		}
	}
}

func lookupSetting(key string) (*setting.Setting, bool) {
	spec, ok := setting.Lookup(key)
	if !ok || spec.Internal {
		return nil, false
	}
	if spec == setting.Executable && spec.Key != key {
		if _, known := isAgentExecutableSettingKey(key); !known {
			return nil, false
		}
	}
	return spec, true
}

func writableSetting(key string) (*setting.Setting, error) {
	spec, ok := lookupSetting(key)
	if !ok {
		return nil, fmt.Errorf("unknown setting: %s", key)
	}
	if spec.IsFamily() && spec.Key == key {
		return nil, fmt.Errorf("setting %s requires a concrete key; replace its placeholders", key)
	}
	if spec.ReadOnly() {
		return nil, fmt.Errorf("setting %s is read-only", key)
	}
	return spec, nil
}

func (d *Daemon) writableSettingHere(key string) (*setting.Setting, error) {
	spec, err := writableSetting(key)
	if err != nil {
		return nil, err
	}
	if spec == setting.OutpostsEnabled && !d.enrollmentStatus().IsHome() {
		return nil, fmt.Errorf("%s can only change at the home, because turning it off from here would cut this outpost's own connection to the home: run attn settings set %s there", key, key)
	}
	return spec, nil
}

func validateSetting(d *Daemon, spec *setting.Setting, key, value string) error {
	switch spec.Kind {
	case setting.Bool:
		if strings.TrimSpace(value) == "" {
			return nil
		}
		return validateBooleanSetting(value)
	case setting.Checked:
		return settingValidators[spec](d, key, value)
	default:
		return nil
	}
}

func validateBooleanSetting(value string) error {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "true", "false":
		return nil
	default:
		return fmt.Errorf("invalid boolean value: %s", value)
	}
}

func resolveBoundedIntSetting(value string, fallback, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < min || n > max {
		return fallback
	}
	return n
}

func validateBoundedIntSetting(label, value string, min, max int) error {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%s must be a whole number", label)
	}
	if n < min || n > max {
		return fmt.Errorf("%s must be between %d and %d", label, min, max)
	}
	return nil
}

const defaultNarrowestTileCols = 66

func validateNarrowestTileCols(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if n, err := strconv.Atoi(trimmed); err != nil || n < 1 {
		return fmt.Errorf("%s must be a positive whole number, got %q", setting.NarrowestTileCols, value)
	}
	return nil
}

func resolveNarrowestTileCols(value string) int {
	return resolveBoundedIntSetting(value, defaultNarrowestTileCols, 1, math.MaxInt)
}

const (
	defaultSidebarWidthPx = 240
	narrowestSidebarPx    = 200
	widestSidebarPx       = 600
)

func validateSidebarWidth(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if n, err := strconv.Atoi(trimmed); err != nil || n < narrowestSidebarPx || n > widestSidebarPx {
		return fmt.Errorf("%s must be a whole number from %d to %d, got %q", setting.SidebarWidth, narrowestSidebarPx, widestSidebarPx, value)
	}
	return nil
}

func resolveSidebarWidth(value string) int {
	return resolveBoundedIntSetting(value, defaultSidebarWidthPx, narrowestSidebarPx, widestSidebarPx)
}

func (d *Daemon) settingValues() map[string]string {
	values := d.storedSettingValues()
	setting.FillDefaults(values)
	return values
}

func (d *Daemon) storedSettingValues() map[string]string {
	values := d.store.GetAllSettings()
	status := d.enrollmentStatus()
	if status.IsHome() {
		return values
	}
	for key := range values {
		if setting.ClassOf(key) == setting.Home {
			delete(values, key)
		}
	}
	for key, value := range d.store.CopiedSettings(status.HomeDaemonID) {
		values[key] = value
	}
	return values
}
