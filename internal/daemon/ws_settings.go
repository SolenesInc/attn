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
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/ptybackend"
	"github.com/victorarias/attn/internal/setting"
)

func (d *Daemon) handleGetSettingsWS(client *wsClient) {
	d.logf("Getting settings")
	d.sendToClient(client, &protocol.SettingsUpdatedMessage{
		Event:    protocol.EventSettingsUpdated,
		Settings: d.settingsSnapshot(d.profileForClient(client)),
	})
}

func (d *Daemon) handleSetSettingWS(client *wsClient, msg *protocol.SetSettingMessage) {
	d.logf("Setting %s = %s", msg.Key, msg.Value)
	if spec, ok := lookupSetting(msg.Key); ok && spec.Scope == setting.PerProfile && protocol.Deref(msg.ProfileID) == "" {
		msg.ProfileID = protocol.Ptr(d.profileForClient(client))
	}
	_, err := d.setSetting(msg)
	response := &protocol.SettingsUpdatedMessage{Event: protocol.EventSettingsUpdated, RequestID: msg.RequestID, ChangedKey: protocol.Ptr(msg.Key), Success: protocol.Ptr(err == nil)}
	if err != nil {
		d.logf("Setting validation failed: %v", err)
		response.Error = protocol.Ptr(err.Error())
		response.Settings = d.settingsSnapshot(d.profileForClient(client))
	}
	if err != nil || msg.RequestID != nil {
		d.sendToClient(client, response)
	}
}

func (d *Daemon) setSetting(msg *protocol.SetSettingMessage) (*protocol.SettingEntry, error) {
	return Call[*protocol.SettingEntry](d.life.Context(), d.router(), *msg)
}

func (d *Daemon) applySetting(_ context.Context, msg protocol.SetSettingMessage) (*protocol.SettingEntry, error) {
	spec, err := writableSetting(msg.Key)
	if err != nil {
		return nil, err
	}
	if err := validateSetting(d, spec, msg.Key, msg.Value); err != nil {
		return nil, err
	}
	if err := d.refuseNestedNotebookSetting(msg); err != nil {
		return nil, err
	}
	if spec.Scope == setting.PerProfile {
		return d.applyProfileSetting(spec, msg)
	}
	if spec == setting.OutpostsEnabled && d.on(spec) == parseBooleanSetting(msg.Value) {
		entry := d.settingEntry(spec, msg.Key, d.settingValue(spec, msg.Key, nil, ""))
		return &entry, nil
	}
	if spec.Kind == setting.Bool && strings.TrimSpace(msg.Value) != "" {
		msg.Value = strconv.FormatBool(parseBooleanSetting(msg.Value))
	}
	if msg.Key == setting.SharedPTYHostEnabled.Key {
		err = d.setSharedPTYHostEnabled(msg.Value)
	} else {
		err = d.store.SetSettingChecked(msg.Key, msg.Value)
	}
	if err != nil {
		return nil, err
	}
	d.settingChanged(msg.Key, msg.Value)

	entry := d.settingEntry(spec, msg.Key, d.settingValue(spec, msg.Key, nil, ""))
	return &entry, nil
}

func (d *Daemon) applyProfileSetting(spec *setting.Setting, msg protocol.SetSettingMessage) (*protocol.SettingEntry, error) {
	profile, err := d.settingsProfile(msg.SourceSessionID, msg.ProfileID)
	if err != nil {
		return nil, err
	}
	if err := d.store.SetProfileSetting(profile.ID, msg.Key, strings.TrimSpace(msg.Value)); err != nil {
		return nil, err
	}
	if msg.Key == setting.NotebookRoot.Key {
		d.notebookFoldersChanged()
	}
	d.publishFact(FactSettingChanged, msg.Key, nil)
	entry := d.settingEntry(spec, msg.Key, d.settingValue(spec, msg.Key, nil, profile.ID))
	return &entry, nil
}

func (d *Daemon) settingChanged(key, value string) {
	if key == setting.OutpostsEnabled.Key {
		d.configureOutposts()
	}
	if harness, ok := isAgentExecutableSettingKey(key); ok {
		d.invalidateHarnessModels(harness)
		d.ensureAvailableAgentSkills()
	}
	if isSessionCostPriceSetting(key) {
		d.publishSessionCostReprices()
	}
	if key == setting.AnalyticsEnabled.Key {
		d.configureAnalytics()
		if d.enrollmentStatus().IsHome() && !d.analyticsEnabled() {
			if err := d.endOutpostAnalyticsGeneration(); err != nil {
				d.logf("analytics: tell outposts to drop what they recorded: %v", err)
			}
		}
	}
	if key == setting.AnalyticsSubjectSecret.Key {
		d.analyticsSecretChanged()
	}
	if key == setting.PhoneAccessEnabled.Key {
		d.configurePhoneAccess()
	}
	if key == setting.AnalyticsEnabled.Key || key == setting.AnalyticsSharingEnabled.Key {
		if !d.analyticsSharing() {
			d.analyticsUpload.withdraw()
		}
		d.queueAnalyticsShip()
	}
	if key == setting.HeadlessContextWindowCap.Key {
		d.applyHeadlessContextWindowCap()
	}
	if key == setting.HeadlessTasksEnabled.Key {
		d.applyHeadlessTasksMode()
	}
	if monitorPolicySetting(key) {
		d.pushMonitorPolicies()
	}
	if key == setting.AutoSettleEnabled.Key {
		if parseBooleanSetting(value) {
			d.armAutoSettleForRunningSessions()
		} else {
			d.cancelAllAutoSettle()
		}
	}
	if key == setting.ActivityEnabled.Key && !parseBooleanSetting(value) {
		d.clearAllSessionActivity()
	}
	d.publishFact(FactSettingChanged, key, nil)
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
			if key == setting.SharedPTYHostEnabled.Key {
				d.life.Go("followSharedPTYHost", d.followSharedPTYHost)
			}
		}
	}
}

func (d *Daemon) projectSettingsUpdated(changedKey string) {
	d.projectSnapshot(snapshotSettings+":"+changedKey, func() {
		base := d.daemonSettingsSnapshot()
		snapshots := map[string]map[string]interface{}{}
		d.wsHub.ForEachClient(func(client *wsClient) {
			profileID := d.profileForClient(client)
			snapshot, ok := snapshots[profileID]
			if !ok {
				snapshot = d.withProfileSettings(base, profileID)
				snapshots[profileID] = snapshot
			}
			event := &protocol.SettingsUpdatedMessage{Event: protocol.EventSettingsUpdated, Settings: snapshot}
			if strings.TrimSpace(changedKey) != "" {
				event.ChangedKey = protocol.Ptr(changedKey)
			}
			d.sendToClient(client, event)
		})
	})
}

func (d *Daemon) settingsSnapshot(profileID string) map[string]interface{} {
	return d.withProfileSettings(d.daemonSettingsSnapshot(), profileID)
}

func (d *Daemon) withProfileSettings(base map[string]interface{}, profileID string) map[string]interface{} {
	settings := make(map[string]interface{}, len(base)+2)
	for k, v := range base {
		settings[k] = v
	}
	if own := d.profileNotebookRoot(profileID); own != "" {
		settings[setting.NotebookRoot.Key] = own
	}
	settings[setting.PullRequestOwnComments.Key] = string(d.pullRequestOwnComments(profileID))
	settings[setting.GardenTenderResume.Key] = string(d.tenderResumeSetting(profileID))
	if root, err := d.notebookRoot(profileID); err == nil {
		settings[setting.NotebookRootEffective.Key] = root
	}
	return settings
}

func executableSetting(agent string) *setting.Setting {
	return setting.Executable.For(strings.TrimSpace(strings.ToLower(agent)))
}

func availabilitySettingKey(agent string) string {
	return setting.Available.For(strings.TrimSpace(strings.ToLower(agent))).Key
}

func capabilitySettingKey(agent, capability string) string {
	return setting.Capability.For(strings.TrimSpace(strings.ToLower(agent))).For(strings.TrimSpace(strings.ToLower(capability))).Key
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

func (d *Daemon) daemonSettingsSnapshot() map[string]interface{} {
	stored := d.settingValues()
	settings := make(map[string]interface{}, len(stored)+8)
	for k, v := range stored {
		settings[k] = v
	}
	delete(settings, setting.AnalyticsSubjectSecret.Key)
	delete(settings, setting.AnalyticsClearedAt.Key)

	for _, name := range agentdriver.List() {
		driver := agentdriver.Get(name)
		if driver == nil || !d.agentOffered(name) {
			continue
		}
		available := d.harnessAvailability(driver)
		settings[availabilitySettingKey(name)] = string(available)

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
		settings[capabilitySettingKey(name, "launch_instructions")] = strconv.FormatBool(d.agentSupportsChiefGuidance(name))
	}
	for _, driver := range d.ensurePluginRegistry().registeredDrivers() {
		settings[availabilitySettingKey(driver.Agent)] = "true"
		for capability, enabled := range driver.Capabilities {
			settings[capabilitySettingKey(driver.Agent, capability)] = strconv.FormatBool(enabled)
		}
	}

	settings[setting.PTYBackendMode.Key] = d.ptyBackendMode()
	sharedEnabled, sharedActive := d.sharedPTYHostSettings()
	settings[setting.SharedPTYHostEnabled.Key] = strconv.FormatBool(sharedEnabled)
	settings[setting.SharedPTYHostActive.Key] = strconv.FormatBool(sharedActive)
	settings[setting.NotebookSyncState.Key], settings[setting.NotebookSyncLastError.Key] = d.notebookSyncSettings()
	d.lastBackupMu.Lock()
	lastBackupAt := d.lastBackupAt
	d.lastBackupMu.Unlock()
	if !lastBackupAt.IsZero() {
		settings[setting.DBLastBackupAt.Key] = lastBackupAt.Format(time.RFC3339)
	}
	for _, spec := range setting.All() {
		if spec.Kind == setting.Bool && !spec.IsFamily() {
			settings[spec.Key] = strconv.FormatBool(parseBooleanSetting(stored[spec.Key]))
		}
	}
	settings[setting.NarrowestTileCols.Key] = strconv.Itoa(resolveNarrowestTileCols(stored[setting.NarrowestTileCols.Key]))
	settings[setting.SidebarWidth.Key] = strconv.Itoa(resolveSidebarWidth(stored[setting.SidebarWidth.Key]))
	settings[setting.AutoSettleArmSeconds.Key] = strconv.Itoa(int(resolveAutoSettleSeconds(stored[setting.AutoSettleArmSeconds.Key], defaultAutoSettleArmSeconds) / time.Second))
	settings[setting.AutoSettleCountdownSeconds.Key] = strconv.Itoa(int(resolveAutoSettleSeconds(stored[setting.AutoSettleCountdownSeconds.Key], defaultAutoSettleCountdownSeconds) / time.Second))
	settings[setting.OpenSentFilesEnabled.Key] = strconv.FormatBool(d.openSentFilesEnabled())
	settings[setting.HeadlessTasksEnabled.Key] = strconv.FormatBool(headless.Enabled())
	settings[setting.HeadlessTasksEnabledStored.Key] = strconv.FormatBool(d.headlessTasksStored())
	if raw, ok := headless.Override(); ok {
		settings[setting.HeadlessTasksEnabledOverride.Key] = raw
	}
	settings[setting.HeadlessContextWindowCap.Key] = strconv.Itoa(resolveContextWindowCap(stored[setting.HeadlessContextWindowCap.Key]))
	settings[setting.ActivityPresenceIdleSeconds.Key] = strconv.Itoa(int(d.presenceIdleLimit() / time.Second))
	if intervals, err := parseActivityIntervals(stored[setting.ActivityIntervals.Key]); err == nil {
		if encoded, err := json.Marshal(intervals); err == nil {
			settings[setting.ActivityIntervals.Key] = string(encoded)
		}
	}
	if advisor, err := parseGardenAdvisorConfig(stored[setting.GardenAdvisor.Key]); err == nil {
		if encoded, err := json.Marshal(advisor); err == nil {
			settings[setting.GardenAdvisor.Key] = string(encoded)
		}
	}
	settings[setting.CrewHeartbeatEnabled.Key] = strconv.FormatBool(d.crewBoolSetting(setting.CrewHeartbeatEnabled))
	settings[setting.CrewAwayCloseEnabled.Key] = strconv.FormatBool(d.crewBoolSetting(setting.CrewAwayCloseEnabled))
	settings[setting.CrewCacheTTLSeconds.Key] = strconv.Itoa(int(d.crewCacheTTL("") / time.Second))
	settings[setting.CrewHeartbeatLeadSeconds.Key] = strconv.Itoa(int(d.crewHeartbeatLead() / time.Second))
	settings[setting.CrewAwaySeconds.Key] = strconv.Itoa(int(d.crewAwayLimit() / time.Second))
	crewStarts := d.crewStartLedger()
	settings[setting.CrewMailStartLimit.Key] = strconv.Itoa(crewStarts.Limit)
	settings[setting.CrewMailStartWindowSeconds.Key] = strconv.Itoa(int(crewStarts.Window / time.Second))

	settings[setting.OutpostsEnabled.Key] = strconv.FormatBool(d.outpostsEnabled())
	state, home, lastError := d.outpostSettings()
	settings[setting.OutpostState.Key] = state
	settings[setting.OutpostHome.Key] = home
	settings[setting.OutpostLastError.Key] = lastError
	d.addUpdateSettings(settings)
	return settings
}

func (d *Daemon) ptyBackendMode() string {
	if provider, ok := d.ptyBackend.(ptybackend.ModeProvider); ok {
		return provider.PTYBackendMode()
	}
	return "unknown"
}

type harnessAvailability string

const (
	harnessAvailable harnessAvailability = "true"
	harnessMissing   harnessAvailability = "false"
	harnessChecking  harnessAvailability = "checking"
)

func (d *Daemon) harnessAvailability(driver agentdriver.Driver) harnessAvailability {
	if d.capturingLoginEnv() {
		return harnessChecking
	}
	if _, err := d.machineAgentExecutable(driver, ""); err != nil {
		if d.loginEnvFailed() {
			return harnessChecking
		}
		return harnessMissing
	}
	return harnessAvailable
}

func settingShapesCrewLaunch(key string) bool {
	return setting.DefaultModel.Matches(key) || setting.DefaultEffort.Matches(key)
}

func (d *Daemon) defaultLaunchModel(agent string) string {
	return strings.TrimSpace(d.setting(setting.DefaultModel.For(strings.ToLower(strings.TrimSpace(agent)))))
}

func (d *Daemon) defaultLaunchEffort(agent string) string {
	return strings.TrimSpace(d.setting(setting.DefaultEffort.For(strings.ToLower(strings.TrimSpace(agent)))))
}

func (d *Daemon) resolveLaunchModel(agent string, requested string) string {
	if requested != "" {
		return requested
	}
	return d.defaultLaunchModel(agent)
}

func (d *Daemon) resolveLaunchEffort(agent string, requested string) string {
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
	if v := strings.TrimSpace(d.setting(setting.DefaultContextWindowCap.For(strings.ToLower(strings.TrimSpace(agent))))); v != "" {
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
	agentdriver.SetHeadlessContextWindowCap(resolveContextWindowCap(d.setting(setting.HeadlessContextWindowCap)))
}

func (d *Daemon) headlessTasksStored() bool {
	if d.store == nil {
		return true
	}
	value, ok := headless.ParseSwitch(d.setting(setting.HeadlessTasksEnabled))
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

func validateNotebookRoot(key, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	_, err := normalizeExternalRoot(value)
	if err != nil {
		return fmt.Errorf("%s %w", key, err)
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

func (d *Daemon) validateExecutableSetting(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	_, err := d.machineExecutable(value)
	if err != nil {
		return fmt.Errorf("executable not found: %w", err)
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
