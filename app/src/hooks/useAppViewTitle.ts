import { useCallback } from 'react';
import { useDaemonStore } from '../store/daemonSessions';

export function useAppViewTitleResolver(): (app: string, view: string) => string | undefined {
  const apps = useDaemonStore((state) => state.apps);
  return useCallback(
    (app: string, view: string) =>
      apps.find((a) => a.name === app)?.views?.find((v) => v.name === view)?.title,
    [apps],
  );
}
