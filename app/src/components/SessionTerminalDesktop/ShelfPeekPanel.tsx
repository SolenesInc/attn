import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type KeyboardEvent } from 'react';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { useEscapeStack } from '../../hooks/useEscapeStack';
import { useDesktopShelfStore, type ShelfPeekAnchor, type ShelfTile } from '../../store/desktopShelf';
import { useSessionStore } from '../../store/sessions';
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

  const close = () => {
    const replying = panel.current?.contains(document.activeElement) ?? false;
    closeShelfPeek();
    if (replying) focusActiveLeaf();
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
    closeShelfPeek();
    useSessionStore.getState().selectLeaf(desktopId, tile.leafId);
  };
  const agentPane = agentPaneById.get(tile.leafId);
  const session = agentPane ? sessionById.get(agentPane.sessionId) : undefined;

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
          <PeekTerminal terminalId={terminalId} offline={tile.offline} onOpen={open} onClose={close} />
        ) : null}
      </div>
    </div>
  );
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

function PeekTerminal({ terminalId, offline, onOpen, onClose }: { terminalId: string; offline?: boolean; onOpen: () => void; onClose: () => void }) {
  const { sendRuntimeInput } = useDaemonApi();
  const { text, failure } = usePeekText(terminalId);
  const [reply, setReply] = useState('');
  const screen = useRef<HTMLPreElement>(null);
  const pinnedToEnd = useRef(true);

  useLayoutEffect(() => {
    if (screen.current && pinnedToEnd.current) screen.current.scrollTop = screen.current.scrollHeight;
  }, [text]);

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key !== 'Enter' || event.nativeEvent.isComposing) return;
    event.preventDefault();
    const line = reply.trim();
    if (!line) {
      onOpen();
      return;
    }
    sendRuntimeInput(terminalId, line, 'user');
    sendRuntimeInput(terminalId, '\r', 'user');
    onClose();
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
      <div className="shelf-peek-reply">
        <span aria-hidden="true">❯</span>
        <input
          aria-label="Reply"
          data-testid="shelf-peek-input"
          placeholder={offline ? 'Offline: a reply cannot be sent' : 'Reply without changing the layout'}
          autoComplete="off"
          spellCheck={false}
          disabled={offline}
          value={reply}
          onChange={(event) => setReply(event.target.value)}
          onKeyDown={onKeyDown}
        />
        <button type="button" onMouseDown={(event) => event.preventDefault()} onClick={onOpen}>Open<kbd>↵</kbd></button>
        <button type="button" onMouseDown={(event) => event.preventDefault()} onClick={onClose}>Close<kbd>esc</kbd></button>
      </div>
    </>
  );
}
