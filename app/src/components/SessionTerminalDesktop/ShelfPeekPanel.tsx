import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type KeyboardEvent } from 'react';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { useDesktopShelfStore, type ShelfPeekAnchor, type ShelfTile } from '../../store/desktopShelf';
import { deliverPeekReply, usePeekReplies, type PeekReply } from '../../store/peekReplies';
import { sessionHasStopped, useSessionStore } from '../../store/sessions';
import { tileKindLabel } from '../../utils/tilePresentation';
import { StateIndicator } from '../StateIndicator';
import { DesktopAgentSubline } from './DesktopAgentSubline';
import { useDesktopContext } from './DesktopContext';
import { closeShelfPeek, keepShelfPeekOpen, leaveShelfPeek, offScreenColumns } from './useShelfPeekHover';
import './ShelfPeekPanel.css';

const REFRESH_MS = 1000;
const EDGE_PX = 8;
const ANCHOR_GAP_PX = 6;
const SIDEBAR_ROW_LIFT_PX = 60;

export function ShelfPeekPanel() {
  const { desktopId } = useDesktopContext();
  const peek = useDesktopShelfStore((state) => (state.peek?.desktopId === desktopId ? state.peek : null));
  const tile = useDesktopShelfStore((state) => {
    if (!peek) return undefined;
    return offScreenColumns(state.shelfByDesktop[desktopId])
      .flatMap((column) => column.tiles)
      .find((candidate) => candidate.leafId === peek.leafId);
  });
  useEffect(() => {
    if (peek && !tile) closeShelfPeek();
  }, [peek, tile]);
  if (!peek || !tile) return null;
  return <OpenPeek key={tile.leafId} tile={tile} anchor={peek.anchor} />;
}

function placeWithin(layer: DOMRect, anchor: ShelfPeekAnchor | null, width: number, height: number): CSSProperties {
  let x = (layer.width - width) / 2;
  let y = EDGE_PX;
  if (anchor) {
    const rightOfAnchor = anchor.right - layer.left <= 0;
    x = rightOfAnchor ? EDGE_PX : anchor.left - layer.left;
    y = rightOfAnchor ? anchor.top - layer.top - SIDEBAR_ROW_LIFT_PX : anchor.bottom - layer.top + ANCHOR_GAP_PX;
  }
  return {
    left: Math.max(EDGE_PX, Math.min(layer.width - width - EDGE_PX, x)),
    top: Math.max(EDGE_PX, Math.min(layer.height - height - EDGE_PX, y)),
    width,
  };
}

function OpenPeek({ tile, anchor }: { tile: ShelfTile; anchor: ShelfPeekAnchor | null }) {
  const { desktopId, agentPaneById, sessionById, focusActiveLeaf } = useDesktopContext();
  const layer = useRef<HTMLDivElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const [place, setPlace] = useState<CSSProperties>({ visibility: 'hidden' });
  const terminalId = tile.terminalId;

  useLayoutEffect(() => {
    if (!layer.current || !panel.current) return;
    const bounds = layer.current.getBoundingClientRect();
    const width = Math.min(780, bounds.width * 0.62);
    const height = terminalId ? Math.min(640, bounds.height * 0.68) : panel.current.offsetHeight;
    setPlace({ ...placeWithin(bounds, anchor, width, height), ...(terminalId ? { height } : {}) });
  }, [anchor, terminalId]);

  const dismiss = () => {
    const replying = panel.current?.contains(document.activeElement) ?? false;
    closeShelfPeek();
    if (replying) focusActiveLeaf();
  };
  const close = () => {
    if (terminalId && usePeekReplies.getState().byTerminal.get(terminalId)?.status === 'draft') usePeekReplies.getState().forget(terminalId);
    dismiss();
  };
  useEscapeStack(close, true);
  useLayoutEffect(() => {
    const closeOutside = (event: PointerEvent) => {
      if (!panel.current?.contains(event.target as Node)) closeShelfPeek();
    };
    window.addEventListener('pointerdown', closeOutside, true);
    return () => window.removeEventListener('pointerdown', closeOutside, true);
  }, []);

  const open = () => {
    if (terminalId) keepReplyAcrossOpening(terminalId);
    closeShelfPeek();
    useSessionStore.getState().selectLeaf(desktopId, tile.leafId);
  };
  const agentPane = agentPaneById.get(tile.leafId);
  const session = agentPane ? sessionById.get(agentPane.sessionId) : undefined;
  const blockedReason = session && sessionHasStopped(session)
    ? 'Agent stopped. Open its tile to resume it; your reply stays there.'
    : session && session.reach !== 'reachable' ? session.outpostNotice : undefined;

  return (
    <div className="shelf-peek-layer" ref={layer}>
      <div
        ref={panel}
        className="shelf-peek"
        role="dialog"
        aria-label={tile.title}
        data-testid="shelf-peek"
        data-leaf-id={tile.leafId}
        style={place}
        onPointerEnter={keepShelfPeekOpen}
        onPointerLeave={leaveShelfPeek}
      >
        <div className="desktop-pane-header desktop-pane-header--static shelf-peek-header">
          <div className="desktop-pane-headline">
            {tile.state ? (
              <span className="desktop-pane-dot">
                <StateIndicator state={tile.state} offline={tile.offline} size="sm" seed={tile.askHolder?.id} />
              </span>
            ) : null}
            <span className="desktop-pane-title-slot">
              <span className="desktop-pane-title">{tile.title}</span>
            </span>
          </div>
          {agentPane ? (
            <DesktopAgentSubline agentPane={agentPane} paneSession={session} paneTitle={tile.title} />
          ) : (
            <div className="desktop-pane-subline" data-testid="shelf-peek-kind">{tileKindLabel(tile.kind).word}</div>
          )}
        </div>
        {terminalId ? (
          <PeekTerminal terminalId={terminalId} title={tile.title} blockedReason={blockedReason} onOpen={open} onSent={dismiss} onClose={close} />
        ) : null}
      </div>
    </div>
  );
}

function keepReplyAcrossOpening(terminalId: string) {
  const { byTerminal, put, forget } = usePeekReplies.getState();
  const reply = byTerminal.get(terminalId);
  if (reply?.status === 'typed') forget(terminalId);
  if (reply?.status === 'draft') put(terminalId, { status: 'not_sent', text: reply.text, reason: null });
}

function usePeekText(terminalId: string) {
  const { readTerminalScreen } = useDaemonApi();
  const [text, setText] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  useEffect(() => {
    let live = true;
    let timer: number | undefined;
    const read = () => {
      readTerminalScreen(terminalId)
        .then((screen) => {
          if (!live) return;
          setText(screen.trimEnd());
          setFailure(null);
        })
        .catch((error: unknown) => {
          if (live) setFailure(error instanceof Error ? error.message : String(error));
        })
        .finally(() => {
          if (live) timer = window.setTimeout(read, REFRESH_MS);
        });
    };
    read();
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
  }, [readTerminalScreen, terminalId]);
  return { text, failure };
}

function replyNotice(reply: PeekReply | undefined, blockedReason: string | undefined) {
  if (reply?.status === 'sending') return 'Sending…';
  if (blockedReason) return blockedReason;
  if (reply?.status === 'typed') return `The text reached the terminal, but Return did not: ${reply.reason}. ↵ sends Return.`;
  if (reply?.status === 'not_sent' && reply.reason === null) return 'This reply was not sent. ↵ sends it.';
  if (reply?.status === 'not_sent') return `This reply may not have arrived: ${reply.reason}. Check the screen; ↵ sends it again.`;
  return null;
}

function PeekTerminal({ terminalId, title, blockedReason, onOpen, onSent, onClose }: { terminalId: string; title: string; blockedReason?: string; onOpen: () => void; onSent: () => void; onClose: () => void }) {
  const { writeConfirmedTerminalInput } = useDaemonApi();
  const { text, failure } = usePeekText(terminalId);
  const reply = usePeekReplies((state) => state.byTerminal.get(terminalId));
  const draft = reply?.text ?? '';
  const editable = reply?.status !== 'sending' && reply?.status !== 'typed';
  const notice = replyNotice(reply, blockedReason);
  const screen = useRef<HTMLPreElement>(null);
  const pinnedToEnd = useRef(true);

  useLayoutEffect(() => {
    if (screen.current && pinnedToEnd.current) screen.current.scrollTop = screen.current.scrollHeight;
  }, [text]);

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key !== 'Enter' || event.nativeEvent.isComposing) return;
    event.preventDefault();
    if (reply?.status === 'sending') return;
    const line = draft.trim();
    if (!line) {
      onOpen();
      return;
    }
    const { put } = usePeekReplies.getState();
    if (blockedReason) {
      if (reply?.status === 'draft') put(terminalId, { status: 'not_sent', text: draft, reason: null });
      return;
    }
    void deliverPeekReply(terminalId, title, reply?.status === 'typed' ? reply : { status: 'draft', text: line }, writeConfirmedTerminalInput);
    onSent();
  };

  const edit = (next: string) => {
    const { put, forget } = usePeekReplies.getState();
    if (!next) forget(terminalId);
    else put(terminalId, reply?.status === 'not_sent' ? { ...reply, text: next } : { status: 'draft', text: next });
  };

  return (
    <>
      <pre
        ref={screen}
        className="shelf-peek-screen"
        data-testid="shelf-peek-screen"
        onScroll={(event) => {
          const element = event.currentTarget;
          pinnedToEnd.current = element.scrollHeight - element.scrollTop - element.clientHeight < 1;
        }}
      >
        {failure && text === null ? `Could not read this terminal: ${failure}` : text}
      </pre>
      {notice ? <div className="shelf-peek-notice" role="status" data-testid="shelf-peek-notice">{notice}</div> : null}
      <div className="shelf-peek-reply">
        <span aria-hidden="true">❯</span>
        <input
          aria-label="Reply"
          data-testid="shelf-peek-input"
          placeholder={blockedReason ? 'A reply cannot be sent now' : 'Reply without changing the layout'}
          autoComplete="off"
          spellCheck={false}
          readOnly={!editable}
          value={draft}
          onChange={(event) => edit(event.target.value)}
          onKeyDown={onKeyDown}
        />
        <button type="button" onMouseDown={(event) => event.preventDefault()} onClick={onOpen}>Open<kbd>↵</kbd></button>
        <button type="button" onMouseDown={(event) => event.preventDefault()} onClick={onClose}>Close<kbd>esc</kbd></button>
      </div>
    </>
  );
}
