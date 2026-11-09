import { invoke } from '@tauri-apps/api/core';
import { useEffect, useMemo, useRef } from 'react';
import { useDaemonApi } from '../contexts/DaemonApiContext';
import { useProfilesStore } from '../store/profiles';
import { leafSlotId, poppedLeaves } from '../types/desktop';
import type { Desktop, TileWindowFrame } from '../types/generated';
import { desktopTerminalState } from '../utils/desktops';
import { recordUiDiag } from '../utils/uiDiagnosticsLog';
import { heldLeafKeys, hideTileWindow, listenToTileWindows, showTileWindow, tileWindowKey } from './pool';

interface WantedWindow {
  key: string;
  desktopId: string;
  leafId: string;
  frame: TileWindowFrame;
  title: string;
}

function wantedWindows(desktops: readonly Desktop[]): WantedWindow[] {
  return desktops.flatMap((desktop) => {
    const state = desktopTerminalState(desktop);
    return poppedLeaves(state.layoutTree).map((leaf) => {
      const leafId = leafSlotId(leaf);
      const title = leaf.type === 'pane'
        ? state.agents.find((agent) => agent.id === leafId)?.title || 'Terminal'
        : leaf.tileKind;
      return { key: tileWindowKey(desktop.id, leafId), desktopId: desktop.id, leafId, frame: leaf.window, title };
    });
  });
}

export function frameOverLeaf(desktopId: string, leafId: string): Promise<TileWindowFrame> {
  const pane = document.querySelector(
    `[data-session-terminal-desktop="${CSS.escape(desktopId)}"] [data-pane-id="${CSS.escape(leafId)}"]`,
  );
  const rect = pane?.getBoundingClientRect() ?? { x: 0, y: 0, width: 0, height: 0 };
  return invoke<TileWindowFrame>('tile_window_frame_over', {
    rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height },
  });
}

export function useTileWindows(enabled: boolean, showError: (message: string) => void): void {
  const desktops = useProfilesStore((state) => state.desktops);
  const { sendDesktopPopOutLeaf, sendDesktopBringBackLeaf } = useDaemonApi();
  const wanted = useMemo(() => (enabled ? wantedWindows(desktops) : []), [desktops, enabled]);
  const wantedRef = useRef(wanted);
  wantedRef.current = wanted;

  useEffect(() => {
    const keys = new Set(wanted.map((entry) => entry.key));
    for (const key of heldLeafKeys()) {
      if (!keys.has(key)) {
        void hideTileWindow(key).catch((error) => recordUiDiag({ kind: 'tile_window_error', action: 'hide', key, error: String(error) }));
      }
    }
    for (const entry of wanted) {
      void showTileWindow(entry.key, { desktopId: entry.desktopId, leafId: entry.leafId }, entry.frame, entry.title).catch((error) => {
        recordUiDiag({ kind: 'tile_window_error', action: 'show', key: entry.key, error: String(error) });
        sendDesktopBringBackLeaf({ desktopId: entry.desktopId, leafId: entry.leafId });
      });
    }
  }, [sendDesktopBringBackLeaf, wanted]);

  useEffect(() => listenToTileWindows({
    onFrameSettled: (key, frame) => {
      const entry = wantedRef.current.find((candidate) => candidate.key === key);
      if (entry) sendDesktopPopOutLeaf({ desktopId: entry.desktopId, leafId: entry.leafId, frame });
    },
    onCloseRequested: (key) => {
      const entry = wantedRef.current.find((candidate) => candidate.key === key);
      if (!entry) return;
      void Promise.resolve(sendDesktopBringBackLeaf({ desktopId: entry.desktopId, leafId: entry.leafId }))
        .catch((error) => {
          recordUiDiag({ kind: 'tile_window_error', action: 'bring_back', key, error: String(error) });
          showError(`Could not bring the tile back: ${String(error)}`);
        });
    },
  }), [sendDesktopBringBackLeaf, sendDesktopPopOutLeaf, showError]);
}
