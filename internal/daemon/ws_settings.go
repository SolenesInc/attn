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
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
)

func (d *Daemon) handleGetSettingsWS(client *wsClient) {
	d.logf("Getting settings")
	d.refreshTailscaleServeState()
	d.sendToClient(client, &protocol.SettingsUpdatedMessage{
		Event:     protocol.EventSettingsUpdated,
		ProfileID: protocol.Ptr(client.selectedProfile()),
		Settings:  d.settingsSnapshot(client.selectedProfile()),
	})
}

func (d *Daemon) handleSetSettingWS(client *wsClient, msg *protocol.SetSettingMessage) {
	selected := client.selectedProfile()
	var err error
	if spec, ok := lookupSetting(msg.Key); ok && spec.scope == profileScope && protocol.Deref(msg.ProfileID) != "" && protocol.Deref(msg.ProfileID) != selected {
		err = fmt.Errorf("setting %s belongs to profile %q; selected profile is %q", msg.Key, protocol.Deref(msg.ProfileID), selected)
	} else {
		if spec, ok := lookupSetting(msg.Key); ok && spec.scope == profileScope {
			msg.ProfileID = protocol.Ptr(selected)
		}
		_, err = d.setSetting(msg)
	}
	response := &protocol.SettingsUpdatedMessage{Event: protocol.EventSettingsUpdated, ProfileID: protocol.Ptr(client.selectedProfile()), RequestID: msg.RequestID, ChangedKey: protocol.Ptr(msg.Key), Success: protocol.Ptr(err == nil)}
	if err != nil {
		response.Error = protocol.Ptr(err.Error())
		response.Settings = d.settingsSnapshot(client.selectedProfile())
	}
	if err != nil || msg.RequestID != nil {
		d.sendToClient(client, response)
	}
}

func (d *Daemon) setSetting(msg *protocol.SetSettingMessage) (*protocol.SettingEntry, error) {
	return Call[*protocol.SettingEntry](d.life.Context(), d.router(), *msg)
}

func (d *Daemon) applySetting(_ context.Context, value protocol.SetSettingMessage) (*protocol.SettingEntry, error) {
	msg := &value
	spec, err := writableSetting(msg.Key)
	if err != nil {
		return nil, err
	}
	var profile profiles.Profile
	if d.enrollmentStatus().IsHome() && (spec.scope == profileScope || protocol.Deref(msg.SourceSessionID) != "" || protocol.Deref(msg.ProfileID) != "") {
		bindings, bindingsErr := d.bindings()
		if bindingsErr != nil {
			return nil, bindingsErr
		}
		request, requestErr := d.requestFromMessage(msg.SourceSessionID, msg.ProfileID, bindings)
		if requestErr != nil {
			if requested := protocol.Deref(msg.ProfileID); requested != "" {
				requestErr = fmt.Errorf("settings --profile %q: %w", requested, requestErr)
			}
			return nil, requestErr
		}
		profile, err = d.store.LiveProfile(request.ProfileID())
		if err != nil {
			return nil, err
		}
	}
	if err := spec.validate(d, msg.Key, msg.Value); err != nil {
		return nil, fmt.Errorf("setting %s: %w", msg.Key, err)
	}
	profileID := ""
	if spec.scope == profileScope && d.enrollmentStatus().IsHome() {
		profileID = profile.ID
	}
	if msg.Key == string(SettingNotebookRoot) {
		d.notebookRootMu.Lock()
		if d.enrollmentStatus().IsHome() && strings.TrimSpace(msg.Value) == "" {
			msg.Value, err = d.defaultNotebookRoot(profile.Name, d.profileSetting(profile.ID, SettingNotebookRoot))
			if err != nil {
				d.notebookRootMu.Unlock()
				return nil, err
			}
		}
		currentValue := d.settingValue(profileID, spec, msg.Key, nil)
		currentRoot, currentErr := normalizeNotebookRoot(currentValue)
		var proposedRoot string
		proposedRoot, err = normalizeNotebookRoot(msg.Value)
		if err == nil && currentErr == nil && proposedRoot == currentRoot {
			d.notebookRootMu.Unlock()
			entry := d.settingEntry(spec, msg.Key, currentValue)
			return &entry, nil
		}
		if err == nil && d.enrollmentStatus().IsHome() {
			err = d.validatePairedNotebookRoots(profile.ID, profile.Name, msg.Value)
		}
		if err == nil {
			err = d.notebookRootChangeAllowed()
		}
		if err == nil {
			if d.enrollmentStatus().IsHome() {
				err = d.store.SetProfileSetting(profileID, msg.Key, msg.Value)
			} else {
				err = d.store.SetSettingChecked(msg.Key, msg.Value)
			}
		}
		d.notebookRootMu.Unlock()
	} else if spec.scope == profileScope && d.enrollmentStatus().IsHome() {
		err = d.store.SetProfileSetting(profileID, msg.Key, msg.Value)
	} else if msg.Key == string(SettingSharedPTYHostEnabled) {
		err = d.setSharedPTYHostEnabled(parseBooleanSetting(msg.Value))
	} else {
		err = d.store.SetSettingChecked(msg.Key, msg.Value)
	}
	if err != nil {
		return nil, fmt.Errorf("setting %s: %w", msg.Key, err)
	}
	if msg.Key == string(SettingNotebookRoot) {
		d.pruneNotebookRoots()
	}
	d.settingChanged(msg.Key, msg.Value)
	entry := d.settingEntry(spec, msg.Key, d.settingValue(profileID, spec, msg.Key, nil))
	return &entry, nil
}

func (d *Daemon) settingChanged(key, value string) {
	if key == string(SettingOutpostsEnabled) {
		d.configureOutposts()
	}
	if key == string(SettingOutpostListen) {
		d.configureOutpostListener()
	}
	if harness, ok := isAgentExecutableSettingKey(key); ok {
		d.invalidateHarnessModels(harness)
	}
	if isSessionCostPriceSetting(key) {
		d.publishSessionCostReprices()
	}
	if key == string(SettingAnalyticsEnabled) {
		d.configureAnalytics()
	}
	if key == string(SettingAnalyticsEnabled) || key == string(SettingAnalyticsSharingEnabled) {
		if !d.analyticsSharing() {
			d.analyticsUpload.withdraw()
		}
		d.queueAnalyticsShip()
	}
	if key == string(SettingTailscaleEnabled) {
		d.ensureTailscaleServeFromSettings()
	}
	if key == string(SettingHeadlessContextWindowCap) {
		d.applyHeadlessContextWindowCap()
	}
	if key == string(SettingHeadlessTasksEnabled) {
		d.applyHeadlessTasksMode()
	}
	if monitorPolicySetting(key) {
		d.pushMonitorPolicies()
	}
	if key == string(SettingAutoSettleEnabled) {
		if parseBooleanSetting(value) {
			d.armAutoSettleForRunningSessions()
		} else {
			d.cancelAllAutoSettle()
		}
	}
	if key == string(SettingActivityEnabled) && !parseBooleanSetting(value) {
		d.clearAllSessionActivity()
	}
	d.publishSettingsFact(FactSettingChanged, key)
	d.codexShared().settingChanged(key)
}

func (d *Daemon) refreshChangedSettings(before map[string]string) {
	after := d.settingValues()
	for key := range before {
		if _, ok := after[key]; !ok {
			after[key] = ""
		}
	}
	for key, value := range after {
		if before[key] != value {
			d.settingChanged(key, value)
		}
	}
}

func (d *Daemon) publishSettingsFact(name, subject string) {
	d.refreshTailscaleServeState()
	d.publishFact(name, subject, nil)
}

func (d *Daemon) projectSettingsUpdated(changedKey string) {
	d.projectSnapshot(snapshotSettings, func() {
		base := d.daemonSettingsSnapshot()
		snapshots := make(map[string]map[string]interface{})
		d.wsHub.ForEachClient(func(client *wsClient) {
			id := client.selectedProfile()
			snapshot := snapshots[id]
			if snapshot == nil {
				snapshot = make(map[string]interface{}, len(base))
				for k, v := range base {
					snapshot[k] = v
				}
				for k, v := range d.profileSettingsOverlay(id) {
					snapshot[k] = v
				}
				snapshots[id] = snapshot
			}
			event := &protocol.SettingsUpdatedMessage{Event: protocol.EventSettingsUpdated, ProfileID: protocol.Ptr(id), Settings: snapshot}
			if strings.TrimSpace(changedKey) != "" {
				event.ChangedKey = &changedKey
			}
			d.sendToClient(client, event)
		})
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
	stored := d.settingValues()
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
		settings[capabilitySettingKey(name, "launch_instructions")] = strconv.FormatBool(caps.HasLaunchInstructions)
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

	if _, ok := settings[string(SettingClaudeAvailable)]; !ok {
		settings[string(SettingClaudeAvailable)] = settings[availabilitySettingKey(string(protocol.SessionAgentClaude))]
	}
	if _, ok := settings[string(SettingCodexAvailable)]; !ok {
		settings[string(SettingCodexAvailable)] = settings[availabilitySettingKey(string(protocol.SessionAgentCodex))]
	}
	settings[string(SettingPTYBackendMode)] = d.ptyBackendMode()
	sharedEnabled, sharedActive := d.sharedPTYHostSettings()
	settings[string(SettingSharedPTYHostEnabled)] = strconv.FormatBool(sharedEnabled)
	settings[string(SettingSharedPTYHostActive)] = strconv.FormatBool(sharedActive)
	if cfg, err := d.store.GetAutoModeConfig(); err == nil {
		settings[string(SettingAutoModeEnabledDefault)] = strconv.FormatBool(cfg.EnabledDefault)
	}
	d.lastBackupMu.Lock()
	lastBackupAt := d.lastBackupAt
	d.lastBackupMu.Unlock()
	if !lastBackupAt.IsZero() {
		settings[string(SettingDBLastBackupAt)] = lastBackupAt.Format(time.RFC3339)
	}
	settings[string(SettingTailscaleEnabled)] = strconv.FormatBool(parseBooleanSetting(stored[string(SettingTailscaleEnabled)]))
	settings[string(SettingQueueModeEnabled)] = strconv.FormatBool(parseBooleanSetting(stored[string(SettingQueueModeEnabled)]))
	settings[string(SettingDirectAgentLaunchEnabled)] = strconv.FormatBool(parseBooleanSetting(stored[string(SettingDirectAgentLaunchEnabled)]))
	settings[string(SettingQueueCrewEnabled)] = strconv.FormatBool(parseBooleanSetting(stored[string(SettingQueueCrewEnabled)]))
	settings[string(SettingQueueShowHiddenSessions)] = strconv.FormatBool(defaultOnBooleanSetting(stored[string(SettingQueueShowHiddenSessions)]))
	settings[string(SettingQueueQuietNotifications)] = strconv.FormatBool(parseBooleanSetting(stored[string(SettingQueueQuietNotifications)]))
	settings[string(SettingCodexSharedEnabled)] = strconv.FormatBool(parseBooleanSetting(stored[string(SettingCodexSharedEnabled)]))
	settings[string(SettingSidebarHarnessLogosEnabled)] = strconv.FormatBool(defaultOnBooleanSetting(stored[string(SettingSidebarHarnessLogosEnabled)]))
	settings[string(SettingAutoApproveEnabled)] = strconv.FormatBool(defaultOnBooleanSetting(stored[string(SettingAutoApproveEnabled)]))
	settings[string(SettingAutoSettleEnabled)] = strconv.FormatBool(parseBooleanSetting(stored[string(SettingAutoSettleEnabled)]))
	settings[string(SettingCalloutsEnabled)] = strconv.FormatBool(parseBooleanSetting(stored[string(SettingCalloutsEnabled)]))
	settings[string(SettingAsksEnabled)] = strconv.FormatBool(parseBooleanSetting(stored[string(SettingAsksEnabled)]))
	settings[string(SettingAnalyticsEnabled)] = strconv.FormatBool(parseBooleanSetting(stored[string(SettingAnalyticsEnabled)]))
	settings[string(SettingAnalyticsSharingEnabled)] = strconv.FormatBool(parseBooleanSetting(stored[string(SettingAnalyticsSharingEnabled)]))
	settings[string(SettingAnalyticsShareVents)] = strconv.FormatBool(parseBooleanSetting(stored[string(SettingAnalyticsShareVents)]))
	settings[string(SettingAnalyticsToolVents)] = strconv.FormatBool(parseBooleanSetting(stored[string(SettingAnalyticsToolVents)]))
	settings[string(SettingAutoSettleArmSeconds)] = strconv.Itoa(int(resolveAutoSettleSeconds(stored[string(SettingAutoSettleArmSeconds)], defaultAutoSettleArmSeconds) / time.Second))
	settings[string(SettingAutoSettleCountdownSeconds)] = strconv.Itoa(int(resolveAutoSettleSeconds(stored[string(SettingAutoSettleCountdownSeconds)], defaultAutoSettleCountdownSeconds) / time.Second))
	settings[string(SettingOpenSentFilesEnabled)] = strconv.FormatBool(d.openSentFilesEnabled())
	settings[string(SettingHeadlessTasksEnabled)] = strconv.FormatBool(headless.Enabled())
	settings[string(SettingHeadlessTasksEnabledStored)] = strconv.FormatBool(d.headlessTasksStored())
	if raw, ok := headless.Override(); ok {
		settings[string(SettingHeadlessTasksEnabledOverride)] = raw
	}
	settings[string(SettingHeadlessContextWindowCap)] = strconv.Itoa(resolveContextWindowCap(stored[string(SettingHeadlessContextWindowCap)]))
	settings[string(SettingActivityEnabled)] = strconv.FormatBool(parseBooleanSetting(stored[string(SettingActivityEnabled)]))
	settings[string(SettingActivityPresenceIdleSeconds)] = strconv.Itoa(int(d.presenceIdleLimit() / time.Second))
	if intervals, err := parseActivityIntervals(stored[string(SettingActivityIntervals)]); err == nil {
		if encoded, err := json.Marshal(intervals); err == nil {
			settings[string(SettingActivityIntervals)] = string(encoded)
		}
	}
	if advisor, err := parseGardenAdvisorConfig(stored[string(SettingGardenAdvisor)]); err == nil {
		if encoded, err := json.Marshal(advisor); err == nil {
			settings[string(SettingGardenAdvisor)] = string(encoded)
		}
	}
	settings[string(SettingCrewHeartbeatEnabled)] = strconv.FormatBool(d.crewBoolSetting(SettingCrewHeartbeatEnabled))
	settings[string(SettingCrewAutoSleepEnabled)] = strconv.FormatBool(d.crewBoolSetting(SettingCrewAutoSleepEnabled))
	settings[string(SettingCrewCacheTTLSeconds)] = strconv.Itoa(int(d.crewCacheTTL("") / time.Second))
	settings[string(SettingCrewHeartbeatLeadSeconds)] = strconv.Itoa(int(d.crewHeartbeatLead() / time.Second))
	settings[string(SettingCrewAwaySeconds)] = strconv.Itoa(int(d.crewAwayLimit() / time.Second))
	crewWakes := d.crewWakeLedger()
	settings[string(SettingCrewWakeLimit)] = strconv.Itoa(crewWakes.Limit)
	settings[string(SettingCrewWakeLimitWindowSeconds)] = strconv.Itoa(int(crewWakes.Window / time.Second))

	settings[string(SettingOutpostsEnabled)] = strconv.FormatBool(d.outpostsEnabled())
	state, home, lastError := d.outpostSettings()
	settings[string(SettingOutpostState)] = state
	settings[string(SettingOutpostHome)] = home
	settings[string(SettingOutpostLastError)] = lastError
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

func settingShapesCrewLaunch(key string) bool {
	return strings.HasPrefix(key, string(SettingDefaultModelPrefix)) || strings.HasPrefix(key, string(SettingDefaultEffortPrefix))
}

func (d *Daemon) defaultLaunchModel(agent string) string {
	return strings.TrimSpace(d.homeSetting(SettingDefaultModelPrefix + HomeSetting(strings.ToLower(strings.TrimSpace(agent)))))
}

func (d *Daemon) defaultLaunchEffort(agent string) string {
	return strings.TrimSpace(d.homeSetting(SettingDefaultEffortPrefix + HomeSetting(strings.ToLower(strings.TrimSpace(agent)))))
}

func (d *Daemon) resolveLaunchModel(agent, requested string) string {
	if requested != "" {
		return requested
	}
	return d.defaultLaunchModel(agent)
}

func (d *Daemon) resolveLaunchEffort(agent, requested string) string {
	if requested != "" {
		return requested
	}
	return d.defaultLaunchEffort(agent)
}

func (d *Daemon) launchContextWindowCap(sessionID protocol.SessionID, agent string) int {
	if session := d.store.Get(protocol.TrimID(sessionID)); session != nil {
		if cap := protocol.Deref(session.ContextWindowCap); cap > 0 {
			return cap
		}
	}
	key := string(SettingDefaultContextWindowCapPrefix) + strings.ToLower(strings.TrimSpace(agent))
	if v := strings.TrimSpace(d.homeSetting(HomeSetting(key))); v != "" {
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
	agentdriver.SetHeadlessContextWindowCap(resolveContextWindowCap(d.homeSetting(SettingHeadlessContextWindowCap)))
}

func (d *Daemon) headlessTasksStored() bool {
	if d.store == nil {
		return true
	}
	value, ok := headless.ParseSwitch(d.homeSetting(SettingHeadlessTasksEnabled))
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
	_, err := normalizeNotebookRoot(value)
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

func normalizeNotebookRoot(value string) (string, error) {
	if config.HarnessNotebookRoot() == "" {
		return normalizeExternalRoot(value)
	}
	path := strings.TrimSpace(value)
	if path == "" {
		return "", nil
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("must be an absolute path")
	}
	return filepath.Clean(path), nil
}
