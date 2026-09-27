import './SessionTerminalDesktop.css';
import { forwardRef } from 'react';
import { DesktopContext } from './DesktopContext';
import { DesktopSurface } from './DesktopSurface';
import { useDesktopController } from './useDesktopController';
import type {
  SessionTerminalDesktopHandle,
  SessionTerminalDesktopProps,
} from './desktopTypes';
export type {
  SessionTerminalDesktopHandle,
  SessionTerminalDesktopProps,
} from './desktopTypes';

export const SessionTerminalDesktop = forwardRef<
  SessionTerminalDesktopHandle,
  SessionTerminalDesktopProps
>(function SessionTerminalDesktop(props, ref) {
  const state = useDesktopController(props, ref);
  return (
    <DesktopContext.Provider value={state}>
      <DesktopSurface />
    </DesktopContext.Provider>
  );
});
