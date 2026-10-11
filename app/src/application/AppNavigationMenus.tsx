import { openPath } from '@tauri-apps/plugin-opener';
import { SnoozeMenu } from '../components/SnoozeMenu';
import { MarkdownOpener } from '../components/palette/MarkdownOpener';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { AppPalette } from './AppPalette';
import {
  useAttentionQueueContext,
  useDesktopTilesContext,
} from './AppContexts';
import { openThenShow } from './openThenShow';

export function AppNavigationMenus() {
  const {
    markdownOpenerOpen,
    markdownOpenerTarget,
    loadOpenerRecents,
    loadOpenerIndex,
    setMarkdownOpenerOpen,
  } = useDesktopTilesContext();
  const { sendBrowseDirectory, sendOpenMarkdown, sendSnoozeTurn } = useDaemonApi();
  const { snoozeMenu, setSnoozeMenu, restoreSnoozeFocus } = useAttentionQueueContext();
  return (
    <>
      {markdownOpenerOpen && (
        <MarkdownOpener
          root={markdownOpenerTarget.root}
          loadRecents={loadOpenerRecents}
          loadIndex={loadOpenerIndex}
          browseDirectory={sendBrowseDirectory}
          onClose={() => setMarkdownOpenerOpen(false)}
          onPick={(path) => {
            setMarkdownOpenerOpen(false);
            void openThenShow(() => sendOpenMarkdown(path, markdownOpenerTarget.sessionId))
              .catch((error) => {
                console.error(
                  '[MarkdownOpener] in-app open failed, falling back to OS open:',
                  error,
                );
                void openPath(path).catch((openError) => {
                  console.error('[MarkdownOpener] OS open fallback failed:', openError);
                });
              });
          }}
        />
      )}
      {snoozeMenu && (
        <SnoozeMenu
          sessionLabel={snoozeMenu.session.label}
          placement={snoozeMenu.placement}
          onSnooze={(until) => sendSnoozeTurn(snoozeMenu.session.id, until)}
          onClose={() => setSnoozeMenu(null)}
          onRestoreFocus={restoreSnoozeFocus}
        />
      )}
      <AppPalette />
    </>
  );
}
