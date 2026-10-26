import { useEffect, useState } from 'react';
import type { DesktopAgentProps } from './desktopTypes';

export function DesktopOfflineNotice({ paneSession, keysNotSent }: Pick<DesktopAgentProps, 'paneSession'> & { keysNotSent: number }) {
  const reachable = (paneSession?.reach ?? 'reachable') === 'reachable';
  const [wasReachable, setWasReachable] = useState(reachable);
  useEffect(() => {
    if (reachable) setWasReachable(true);
  }, [reachable]);

  const offline = Boolean(paneSession?.offline);
  if (!offline && keysNotSent === 0) return null;
  return (
    <div className="desktop-pane-offline-strip" role="status">
      {offline ? <span>{paneSession?.hostName} unreachable · typing is not sent</span> : null}
      {offline && !wasReachable ? <span>The screen is on {paneSession?.hostName}.</span> : null}
      {keysNotSent > 0 ? <span>{keysNotSent === 1 ? '1 key may not have been sent' : `${keysNotSent} keys may not have been sent`}</span> : null}
    </div>
  );
}
