import { useEffect, useState } from 'react';
import type { DesktopAgentProps } from './desktopTypes';

export function DesktopOfflineNotice({ paneSession }: Pick<DesktopAgentProps, 'paneSession'>) {
  const online = Boolean(paneSession && !paneSession.offline);
  const [wasOnline, setWasOnline] = useState(online);
  useEffect(() => {
    if (online) setWasOnline(true);
  }, [online]);

  if (!paneSession?.offline) return null;
  return (
    <div className="desktop-pane-offline-strip" role="status">
      <span>{paneSession.hostName} unreachable · typing is not sent</span>
      {!wasOnline ? <span>The screen is on {paneSession.hostName}.</span> : null}
    </div>
  );
}
