import { useEffect, useState } from 'react';
import { usePeekReplies } from '../../store/peekReplies';
import { writeClipboardText } from '../../utils/clipboardBridge';
import type { DesktopAgentProps } from './desktopTypes';

export function DesktopOfflineNotice({ paneSession, keysNotSent, terminalId }: Pick<DesktopAgentProps, 'paneSession'> & { keysNotSent: number; terminalId: string }) {
  const reachable = (paneSession?.reach ?? 'reachable') === 'reachable';
  const [wasReachable, setWasReachable] = useState(reachable);
  useEffect(() => {
    if (reachable) setWasReachable(true);
  }, [reachable]);

  const reply = usePeekReplies((state) => state.byTerminal.get(terminalId));
  const unsentText = reply?.status === 'not_sent' ? reply.text : null;
  const offline = Boolean(paneSession?.offline);
  const notice = paneSession?.outpostNotice;
  if (!notice && keysNotSent === 0 && unsentText === null) return null;
  return (
    <div className="desktop-pane-offline-strip" role="status">
      {notice ? <span>{notice}</span> : null}
      {offline && !wasReachable ? <span>The screen is on {paneSession?.hostName}.</span> : null}
      {keysNotSent > 0 ? <span>{keysNotSent === 1 ? '1 key may not have been sent' : `${keysNotSent} keys may not have been sent`}</span> : null}
      {unsentText !== null ? (
        <span className="desktop-pane-unsent-reply" data-testid="unsent-peek-reply">
          <span>Unsent reply: {unsentText}</span>
          <button type="button" onClick={() => void writeClipboardText(unsentText)}>Copy</button>
          <button type="button" onClick={() => usePeekReplies.getState().forget(terminalId)}>Discard</button>
        </span>
      ) : null}
    </div>
  );
}
