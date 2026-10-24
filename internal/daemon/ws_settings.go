package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/headless"
	"github.com/victorarias/attn/internal/modeltiers"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func (d *Daemon) handleGetSettingsWS(client *wsClient) {
	d.logf("Getting settings")
	d.refreshTailscaleServeState()
	d.sendToClient(client, &protocol.SettingsUpdatedMessage{
		Event:    protocol.EventSettingsUpdated,
		Settings: d.daemonSettingsSnapshot(),
	})
}

func (d *Daemon) handleSetSettingWS(client *wsClient, msg *protocol.SetSettingMessage) {
	d.logf("Setting %s = %s", msg.Key, msg.Value)
	_, err := d.setSetting(msg)
	response := &protocol.SettingsUpdatedMessage{Event: protocol.EventSettingsUpdated, RequestID: msg.RequestID, ChangedKey: protocol.Ptr(msg.Key), Success: protocol.Ptr(err == nil)}
	if err != nil {
		d.logf("Setting validation failed: %v", err)
		response.Error = protocol.Ptr(err.Error())
		response.Settings = d.daemonSettingsSnapshot()
	}
	if err != nil || msg.RequestID != nil {
		d.sendToClient(client, response)
	}
}

func (d *Daemon) setSetting(msg *protocol.SetSettingMessage) (*protocol.SettingEntry, error) {
	spec, err := writableSetting(msg.Key)
	if err != nil {
		return nil, err
	}
	if err := spec.validate(d, msg.Key, msg.Value); err != nil {
		return nil, err
	}
	if msg.Key == SettingSharedPTYHostEnabled {
		err = d.setSharedPTYHostEnabled(parseBooleanSetting(msg.Value))
	} else {
		err = d.store.SetSettingChecked(msg.Key, msg.Value)
	}
	if err != nil {
		return nil, err
	}
	if msg.Key == string(settingOutpostsEnabled) {
		d.configureOutposts()
	}
	if msg.Key == string(settingOutpostListen) {
		d.configureOutpostListener()
	}
	if harness, ok := isAgentExecutableSettingKey(msg.Key); ok {
		d.invalidateHarnessModels(harness)
	}
	if isSessionCostPriceSetting(msg.Key) {
		d.publishSessionCostReprices()
	}
	if msg.Key == SettingTailscaleEnabled {
		d.ensureTailscaleServeFromSettings()
	}
	if msg.Key == SettingHeadlessContextWindowCap {
		d.applyHeadlessContextWindowCap()
	}
	if msg.Key == SettingHeadlessTasksEnabled {
		d.applyHeadlessTasksMode()
	}
	if monitorPolicySetting(msg.Key) {
		d.pushMonitorPolicies()
	}
	if msg.Key == SettingAutoSettleEnabled {
		if parseBooleanSetting(msg.Value) {
			d.armAutoSettleForRunningSessions()
		} else {
			d.cancelAllAutoSettle()
		}
	}
	if msg.Key == SettingActivityEnabled && !parseBooleanSetting(msg.Value) {
		d.clearAllSessionActivity()
	}
	d.publishSettingsFact(FactSettingChanged, msg.Key)
	d.codexShared().settingChanged(msg.Key)
	entry := settingEntry(spec, msg.Key, d.daemonSetting(daemonSettingKey(msg.Key)))
	return &entry, nil
}

func (d *Daemon) publishSettingsFact(name, subject string) {
	d.refreshTailscaleServeState()
	d.publishFact(name, subject, nil)
}

func (d *Daemon) projectSettingsUpdated(changedKey string) {
	d.projectSnapshot(snapshotSettings, func() {
		event := &protocol.SettingsUpdatedMessage{
			Event:    protocol.EventSettingsUpdated,
			Settings: d.daemonSettingsSnapshot(),
		}
		if strings.TrimSpace(changedKey) != "" {
			event.ChangedKey = protocol.Ptr(changedKey)
		}
		d.broadcastMessage(event)
	})
}

func executableSettingKey(agent string) string {
	return strings.TrimSpace(strings.ToLower(agent)) + "_executable"
}

func availabilitySettingKey(agent string) string {
	return strings.TrimSpace(strings.ToLower(agent)) + "_available"
}

func capabilitySettingKey(agent, capability string) string {
	return strings.TrimSpace(strings.ToLower(agent)) + "_cap_" + strings.TrimSpace(strings.ToLower(capability))
}

func isAgentExecutableSettingKey(key string) (agent string, ok bool) {
	lower := strings.TrimSpace(strings.ToLower(key))
	if !strings.HasSuffix(lower, "_executable") {
		return "", false
	}
	agent = strings.TrimSuffix(lower, "_executable")
	if agent == "" {
		return "", false
	}
	if agentdriver.Get(agent) == nil {
		return "", false
	}
	return agent, true
}

func canonicalExecutableSettingKey(agent string) string {
	return executableSettingKey(agent)
}

func (d *Daemon) daemonSettingsSnapshot() map[string]interface{} {
	stored := d.store.GetAllSettings()
	settings := make(map[string]interface{}, len(stored)+8)
	for k, v := range stored {
		settings[k] = v
	}

	for _, name := range agentdriver.List() {
		driver := agentdriver.Get(name)
		if driver == nil {
			continue
		}
		execKey := canonicalExecutableSettingKey(name)
		configured := strings.TrimSpace(stored[execKey])
		if configured == "" {
			configured = strings.TrimSpace(stored[executableSettingKey(name)])
		}
		available := isAgentExecutableAvailable(configured, driver.DefaultExecutable())
		settings[availabilitySettingKey(name)] = strconv.FormatBool(available)
		if available {
			if synced, err := agentdriver.EnsureAttnSkillInstalled(name); err != nil {
				d.logf("failed to ensure %s attn skill: %v", name, err)
			} else if !synced {
				d.logf("skipping user-global %s attn skill sync for instance %q", name, config.InstanceLabel())
			}
		}

		caps := agentdriver.EffectiveCapabilities(driver)
		settings[capabilitySettingKey(name, "hooks")] = strconv.FormatBool(caps.HasHooks)
		settings[capabilitySettingKey(name, "transcript")] = strconv.FormatBool(caps.HasTranscript)
		settings[capabilitySettingKey(name, "transcript_watcher")] = strconv.FormatBool(caps.HasTranscriptWatcher)
		settings[capabilitySettingKey(name, "classifier")] = strconv.FormatBool(caps.HasClassifier)
		settings[capabilitySettingKey(name, "initial_prompt")] = strconv.FormatBool(caps.HasInitialPrompt)
		settings[capabilitySettingKey(name, "resume")] = strconv.FormatBool(caps.HasResume)
		settings[capabilitySettingKey(name, "yolo")] = strconv.FormatBool(caps.HasYolo)
		hasHeadlessTask, _ := agentdriver.HeadlessTaskAvailability(driver)
		settings[capabilitySettingKey(name, "headless_task")] = strconv.FormatBool(hasHeadlessTask)
	}
	for _, driver := range d.ensurePluginRegistry().registeredDrivers() {
		settings[availabilitySettingKey(driver.Agent)] = "true"
		for capability, enabled := range driver.Capabilities {
			settings[capabilitySettingKey(driver.Agent, capability)] = strconv.FormatBool(enabled)
		}
	}
	if cfg, err := d.store.GetDelegationPreferences(); err == nil && cfg.WorkflowSkillEnabled {
		var harnesses []string
		for _, harness := range d.delegationHarnesses() {
			if harness.Available {
				harnesses = append(harnesses, harness.ID)
			}
		}
		if _, synced, err := agentdriver.EnsureWorkflowSkillsInstalled(harnesses); err != nil {
			d.logf("failed to ensure attn-workflow skill: %v", err)
		} else if !synced {
			d.logf("skipping user-global attn-workflow skill sync for instance %q", config.InstanceLabel())
		}
	}

	if _, ok := settings[SettingClaudeAvailable]; !ok {
		settings[SettingClaudeAvailable] = settings[availabilitySettingKey(string(protocol.SessionAgentClaude))]
	}
	if _, ok := settings[SettingCodexAvailable]; !ok {
		settings[SettingCodexAvailable] = settings[availabilitySettingKey(string(protocol.SessionAgentCodex))]
	}
	if _, ok := settings[SettingCopilotAvailable]; !ok {
		settings[SettingCopilotAvailable] = settings[availabilitySettingKey(string(protocol.SessionAgentCopilot))]
	}
	settings[SettingPTYBackendMode] = d.ptyBackendMode()
	sharedEnabled, sharedActive := d.sharedPTYHostSettings()
	settings[SettingSharedPTYHostEnabled] = strconv.FormatBool(sharedEnabled)
	settings[SettingSharedPTYHostActive] = strconv.FormatBool(sharedActive)
	if cfg, err := d.store.GetAutoModeConfig(); err == nil {
		settings[SettingAutoModeEnabledDefault] = strconv.FormatBool(cfg.EnabledDefault)
	}
	if root, err := d.notebookRoot(); err == nil {
		settings[SettingNotebookRootEffective] = root
	}
	d.lastBackupMu.Lock()
	lastBackupAt := d.lastBackupAt
	d.lastBackupMu.Unlock()
	if !lastBackupAt.IsZero() {
		settings[SettingDBLastBackupAt] = lastBackupAt.Format(time.RFC3339)
	}
	settings[SettingTailscaleEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingTailscaleEnabled]))
	settings[SettingQueueModeEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingQueueModeEnabled]))
	settings[SettingDirectAgentLaunchEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingDirectAgentLaunchEnabled]))
	settings[SettingQueueCrewEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingQueueCrewEnabled]))
	settings[SettingQueueShowHiddenSessions] = strconv.FormatBool(defaultOnBooleanSetting(stored[SettingQueueShowHiddenSessions]))
	settings[SettingQueueQuietNotifications] = strconv.FormatBool(parseBooleanSetting(stored[SettingQueueQuietNotifications]))
	settings[SettingCodexSharedEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingCodexSharedEnabled]))
	settings[SettingSidebarHarnessLogosEnabled] = strconv.FormatBool(defaultOnBooleanSetting(stored[SettingSidebarHarnessLogosEnabled]))
	settings[SettingAutoApproveEnabled] = strconv.FormatBool(defaultOnBooleanSetting(stored[SettingAutoApproveEnabled]))
	settings[SettingAutoSettleEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingAutoSettleEnabled]))
	settings[SettingCalloutsEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingCalloutsEnabled]))
	settings[SettingAsksEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingAsksEnabled]))
	settings[SettingAutoSettleArmSeconds] = strconv.Itoa(int(resolveAutoSettleSeconds(stored[SettingAutoSettleArmSeconds], defaultAutoSettleArmSeconds) / time.Second))
	settings[SettingAutoSettleCountdownSeconds] = strconv.Itoa(int(resolveAutoSettleSeconds(stored[SettingAutoSettleCountdownSeconds], defaultAutoSettleCountdownSeconds) / time.Second))
	settings[SettingOpenSentFilesEnabled] = strconv.FormatBool(d.openSentFilesEnabled())
	settings[SettingHeadlessTasksEnabled] = strconv.FormatBool(headless.Enabled())
	settings[SettingHeadlessTasksEnabledStored] = strconv.FormatBool(d.headlessTasksStored())
	if raw, ok := headless.Override(); ok {
		settings[SettingHeadlessTasksEnabledOverride] = raw
	}
	settings[SettingChiefContextWindowCap] = strconv.Itoa(resolveContextWindowCap(stored[SettingChiefContextWindowCap]))
	settings[SettingHeadlessContextWindowCap] = strconv.Itoa(resolveContextWindowCap(stored[SettingHeadlessContextWindowCap]))
	settings[SettingActivityEnabled] = strconv.FormatBool(parseBooleanSetting(stored[SettingActivityEnabled]))
	settings[SettingActivityPresenceIdleSeconds] = strconv.Itoa(int(d.presenceIdleLimit() / time.Second))
	if intervals, err := parseActivityIntervals(stored[SettingActivityIntervals]); err == nil {
		if encoded, err := json.Marshal(intervals); err == nil {
			settings[SettingActivityIntervals] = string(encoded)
		}
	}
	if advisor, err := parseGardenAdvisorConfig(stored[SettingGardenAdvisor]); err == nil {
		if encoded, err := json.Marshal(advisor); err == nil {
			settings[SettingGardenAdvisor] = string(encoded)
		}
	}
	settings[SettingCrewHeartbeatEnabled] = strconv.FormatBool(d.crewBoolSetting(SettingCrewHeartbeatEnabled))
	settings[SettingCrewAutoSleepEnabled] = strconv.FormatBool(d.crewBoolSetting(SettingCrewAutoSleepEnabled))
	settings[SettingCrewCacheTTLSeconds] = strconv.Itoa(int(d.crewCacheTTL("") / time.Second))
	settings[SettingCrewHeartbeatLeadSeconds] = strconv.Itoa(int(d.crewHeartbeatLead() / time.Second))
	settings[SettingCrewAwaySeconds] = strconv.Itoa(int(d.crewAwayLimit() / time.Second))
	crewWakes := d.crewWakeLedger()
	settings[SettingCrewWakeLimit] = strconv.Itoa(crewWakes.Limit)
	settings[SettingCrewWakeLimitWindowSeconds] = strconv.Itoa(int(crewWakes.Window / time.Second))

	settings[string(settingOutpostsEnabled)] = strconv.FormatBool(d.outpostsEnabled())
	state, home, lastError := d.outpostSettings()
	settings[string(settingOutpostState)] = state
	settings[string(settingOutpostHome)] = home
	settings[string(settingOutpostLastError)] = lastError
	tailscale := d.tailscaleStateSnapshot()
	if tailscale.status != "" {
		settings["tailscale_status"] = tailscale.status
	}
	if tailscale.domain != "" {
		settings["tailscale_domain"] = tailscale.domain
		settings["tailscale_url"] = "https://" + tailscale.domain + "/"
	}
	if tailscale.authURL != "" {
		settings["tailscale_auth_url"] = tailscale.authURL
	}
	if tailscale.lastError != "" {
		settings["tailscale_error"] = tailscale.lastError
	}
	return settings
}

func (d *Daemon) ptyBackendMode() string {
	if provider, ok := d.ptyBackend.(ptybackend.ModeProvider); ok {
		return provider.PTYBackendMode()
	}
	switch d.ptyBackend.(type) {
	case *ptybackend.EmbeddedBackend:
		return "embedded"
	default:
		return "unknown"
	}
}

func isAgentExecutableAvailable(configuredExecutable, defaultExecutable string) bool {
	executable := strings.TrimSpace(configuredExecutable)
	if executable == "" {
		executable = defaultExecutable
	}
	_, err := exec.LookPath(executable)
	return err == nil
}

func (d *Daemon) chiefLaunchModel(ctx context.Context, agent, executable string, chief bool) string {
	if !chief {
		return ""
	}
	explicit := strings.TrimSpace(d.daemonSetting(daemonSettingKey(SettingChiefModelPrefix + strings.ToLower(strings.TrimSpace(agent)))))
	if agent != "claude" && agent != "codex" {
		return explicit
	}
	return d.resolveTierModel(ctx, agent, executable, modeltiers.Deep, explicit, "")
}

func (d *Daemon) chiefLaunchEffort(agent string, chief bool) string {
	if !chief {
		return ""
	}
	if effort := strings.TrimSpace(d.daemonSetting(daemonSettingKey(SettingChiefEffortPrefix + strings.ToLower(strings.TrimSpace(agent))))); effort != "" {
		return effort
	}
	if agent == "claude" || agent == "codex" {
		return "low"
	}
	return ""
}

func settingShapesCrewLaunch(key string) bool {
	return strings.HasPrefix(key, SettingDefaultModelPrefix) || strings.HasPrefix(key, SettingDefaultEffortPrefix)
}

func (d *Daemon) defaultLaunchModel(agent string) string {
	return strings.TrimSpace(d.daemonSetting(daemonSettingKey(SettingDefaultModelPrefix + strings.ToLower(strings.TrimSpace(agent)))))
}

func (d *Daemon) defaultLaunchEffort(agent string) string {
	return strings.TrimSpace(d.daemonSetting(daemonSettingKey(SettingDefaultEffortPrefix + strings.ToLower(strings.TrimSpace(agent)))))
}

func (d *Daemon) resolveLaunchModel(ctx context.Context, agent, executable string, chief bool, requested string) string {
	if requested != "" {
		return requested
	}
	if model := d.chiefLaunchModel(ctx, agent, executable, chief); model != "" {
		return model
	}
	return d.defaultLaunchModel(agent)
}

func (d *Daemon) resolveLaunchEffort(agent string, chief bool, requested string) string {
	if requested != "" {
		return requested
	}
	if effort := d.chiefLaunchEffort(agent, chief); effort != "" {
		return effort
	}
	return d.defaultLaunchEffort(agent)
}

func (d *Daemon) launchContextWindowCap(sessionID protocol.SessionID, agent string, chief bool) int {
	if session := d.store.Get(protocol.TrimID(sessionID)); session != nil {
		if cap := protocol.Deref(session.ContextWindowCap); cap > 0 {
			return cap
		}
	}
	if chief {
		return resolveContextWindowCap(d.daemonSetting(SettingChiefContextWindowCap))
	}
	key := SettingDefaultContextWindowCapPrefix + strings.ToLower(strings.TrimSpace(agent))
	if v := strings.TrimSpace(d.store.GetSetting(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func (d *Daemon) applyHeadlessContextWindowCap() {
	if d.store == nil {
		return
	}
	agentdriver.SetHeadlessContextWindowCap(resolveContextWindowCap(d.daemonSetting(SettingHeadlessContextWindowCap)))
}

func (d *Daemon) headlessTasksStored() bool {
	if d.store == nil {
		return true
	}
	value, ok := headless.ParseSwitch(d.daemonSetting(SettingHeadlessTasksEnabled))
	return !ok || value
}

func (d *Daemon) applyHeadlessTasksMode() {
	if d.store == nil {
		return
	}
	headless.SetStoredEnabled(d.headlessTasksStored())
	d.logf("headless tasks: %s", headless.Describe())
}

func validateAutoSettleSeconds(label, value string, minSeconds, maxSeconds int) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return fmt.Errorf("%s must be a whole number of seconds: %s", label, value)
	}
	if n < minSeconds || n > maxSeconds {
		return fmt.Errorf("%s must be between %d and %d seconds", label, minSeconds, maxSeconds)
	}
	return nil
}

func validateNewSessionDestination(value string) error {
	switch strings.TrimSpace(value) {
	case "", DestinationNewWorktree, DestinationMainRepo:
		return nil
	default:
		return fmt.Errorf("new session destination must be %q or %q: %s", DestinationNewWorktree, DestinationMainRepo, value)
	}
}

func parseBooleanSetting(value string) bool {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func defaultOnBooleanSetting(value string) bool {
	return strings.TrimSpace(value) == "" || parseBooleanSetting(value)
}

func validateUIScale(value string) error {
	scale, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("invalid scale value: %s", value)
	}
	if scale < 0.5 || scale > 2.0 {
		return fmt.Errorf("scale must be between 0.5 and 2.0")
	}
	return nil
}

const (
	contextWindowCapMin = 10000
	contextWindowCapMax = 2000000
)

func validateContextWindowCap(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return fmt.Errorf("context window cap must be a whole number of tokens: %s", value)
	}
	if n < contextWindowCapMin || n > contextWindowCapMax {
		return fmt.Errorf("context window cap must be between %d and %d tokens", contextWindowCapMin, contextWindowCapMax)
	}
	return nil
}

func resolveContextWindowCap(stored string) int {
	if trimmed := strings.TrimSpace(stored); trimmed != "" {
		if n, err := strconv.Atoi(trimmed); err == nil && n > 0 {
			return n
		}
	}
	return agentdriver.DefaultContextWindowCap
}

func validateNotebookRoot(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	_, err := normalizeExternalRoot(value)
	if err != nil {
		return fmt.Errorf("notebook.root %w", err)
	}
	return nil
}

func normalizeExternalRoot(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	path := trimmed
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
		path = filepath.Join(home, path[2:])
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("must be an absolute path")
	}
	dataDir := config.DataDir()
	clean := filepath.Clean(path)
	if clean == dataDir || strings.HasPrefix(clean, dataDir+string(filepath.Separator)) {
		return "", fmt.Errorf("must be outside the attn data dir (%s)", dataDir)
	}
	canonRoot := canonicalizeForComparison(clean)
	canonData := canonicalizeForComparison(dataDir)
	if canonRoot == canonData || strings.HasPrefix(canonRoot, canonData+string(filepath.Separator)) {
		return "", fmt.Errorf("must be outside the attn data dir (%s)", dataDir)
	}
	return clean, nil
}

func canonicalizeForComparison(path string) string {
	clean := filepath.Clean(path)
	ancestor := clean
	var remainder []string
	for {
		if _, err := os.Stat(ancestor); err == nil {
			break
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return clean
		}
		remainder = append([]string{filepath.Base(ancestor)}, remainder...)
		ancestor = parent
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return clean
	}
	return filepath.Join(append([]string{resolved}, remainder...)...)
}

func validateProjectsDirectory(path string) error {
	if path == "" {
		return fmt.Errorf("projects directory cannot be empty")
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot determine home directory: %w", err)
		}
		path = filepath.Join(home, path[2:])
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("projects directory must be an absolute path")
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("cannot create directory: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot access directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path exists but is not a directory")
	}

	return nil
}

func validateExecutableSetting(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	path, err := exec.LookPath(value)
	if err != nil {
		return fmt.Errorf("executable not found: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot access executable: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("executable path points to a directory")
	}

	return nil
}

func validateEditorSetting(value string) error {
	editor := strings.TrimSpace(value)
	if editor == "" {
		return nil
	}

	binary := extractCommandBinary(editor)
	if binary == "" {
		return fmt.Errorf("invalid editor command")
	}

	path, err := exec.LookPath(binary)
	if err != nil {
		return fmt.Errorf("executable not found: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot access executable: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("executable path points to a directory")
	}

	return nil
}

func (d *Daemon) validateNewSessionAgent(value string) error {
	agent := strings.TrimSpace(strings.ToLower(value))
	if agent == "" {
		return nil
	}
	if agentdriver.Get(agent) == nil {
		if d.plugins != nil {
			if _, ok := d.plugins.driver(agent); ok {
				return nil
			}
		}
		return fmt.Errorf("unknown agent: %s", value)
	}
	return nil
}

func validateKeybindingsConfig(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if !json.Valid([]byte(trimmed)) {
		return fmt.Errorf("keybindings config must be valid JSON")
	}
	return nil
}

func validateTheme(value string) error {
	if value != "dark" && value != "light" && value != "system" {
		return fmt.Errorf("invalid theme: %s (must be dark, light, or system)", value)
	}
	return nil
}

func extractCommandBinary(command string) string {
	if command == "" {
		return ""
	}
	if command[0] == '"' || command[0] == '\'' {
		quote := command[0]
		for i := 1; i < len(command); i++ {
			if command[i] == quote {
				return command[1:i]
			}
		}
		return ""
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
