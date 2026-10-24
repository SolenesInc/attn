import { SettingsModal } from '../components/SettingsModal';
import { ShortcutEditorModal } from '../components/ShortcutEditorModal';
import { ShortcutsModal } from '../components/ShortcutsModal';
import { WhatsNewModal } from '../components/WhatsNewModal';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import {
  useAppAppearanceContext,
  useAppGardenActionsContext,
  useAppInputs,
  useAppPanelsContext,
  useNavigationContext,
} from './AppContexts';

export function AppPreferences() {
  const {
    shortcutsOpen,
    setShortcutsOpen,
    setShortcutEditorOpen,
    shortcutEditorOpen,
    whatsNew,
    settingsModalRef,
    settingsOpen,
    setSettingsOpen,
  } = useAppPanelsContext();
  const {
    mutedRepos,
    mutedAuthors,
    themePreference,
    setTheme,
    scale,
    increaseScale,
    decreaseScale,
    resetScale,
    gardenScale,
  } = useAppAppearanceContext();
  const {
    daemonGitHubHosts,
    settings,
    daemonPlugins,
    daemonPluginIssues,
    notebookTaskChangeSignal,
  } = useAppInputs();
  const {
    sendMuteRepo,
    sendMuteAuthor,
    sendListPlugins,
    sendInstallPlugin,
    sendInstallBundledPlugin,
    sendUninstallPlugin,
    sendRemovePlugin,
    sendSetPluginPriority,
    sendSaveSetting,
    sendTaskList,
    sendTaskRetry,
  } = useDaemonApi();
  const { handleSelectSession } = useNavigationContext();
  const { handleRevealSeedInGarden } = useAppGardenActionsContext();
  return (
    <>
      <ShortcutsModal
        isOpen={shortcutsOpen}
        onClose={() => setShortcutsOpen(false)}
        onEdit={() => {
          setShortcutsOpen(false);
          setShortcutEditorOpen(true);
        }}
      />
      <ShortcutEditorModal
        isOpen={shortcutEditorOpen}
        onClose={() => setShortcutEditorOpen(false)}
      />
      <WhatsNewModal
        isOpen={whatsNew.isOpen}
        onClose={whatsNew.dismiss}
        onViewShortcuts={() => {
          whatsNew.dismiss();
          setShortcutsOpen(true);
        }}
      />
      <SettingsModal
        ref={settingsModalRef}
        isOpen={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        mutedRepos={mutedRepos}
        githubHosts={daemonGitHubHosts}
        onUnmuteRepo={sendMuteRepo}
        mutedAuthors={mutedAuthors}
        onUnmuteAuthor={sendMuteAuthor}
        settings={settings}
        plugins={daemonPlugins}
        pluginIssues={daemonPluginIssues}
        onListPlugins={sendListPlugins}
        onInstallPlugin={sendInstallPlugin}
        onInstallBundledPlugin={sendInstallBundledPlugin}
        onUninstallPlugin={sendUninstallPlugin}
        onRemovePlugin={sendRemovePlugin}
        onSetPluginPriority={sendSetPluginPriority}
        onSetSetting={sendSaveSetting}
        themePreference={themePreference}
        onSetTheme={setTheme}
        uiScale={scale}
        onIncreaseUIScale={increaseScale}
        onDecreaseUIScale={decreaseScale}
        onResetUIScale={resetScale}
        gardenScale={gardenScale.scale}
        effectiveGardenScale={gardenScale.effectiveScale}
        onIncreaseGardenScale={gardenScale.increaseScale}
        onDecreaseGardenScale={gardenScale.decreaseScale}
        onMatchAppGardenScale={gardenScale.matchApp}
        listTasks={sendTaskList}
        retryTask={sendTaskRetry}
        taskChangeSignal={notebookTaskChangeSignal}
        onOpenSession={handleSelectSession}
        onOpenSeed={handleRevealSeedInGarden}
      />
    </>
  );
}
