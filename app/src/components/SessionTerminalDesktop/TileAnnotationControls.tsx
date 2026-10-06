import { SessionPriority } from '../SessionPriority';
import { useTileAnnotations } from './useTileAnnotations';
import type { DesktopTileSessionOption } from './DesktopDockTile';
interface Props {
  annotations: ReturnType<typeof useTileAnnotations>;
  isSeed: boolean;
  path: string;
  desktopSessions: DesktopTileSessionOption[];
  seedNavigationError: string;
}
export function TileAnnotationControls({
  annotations,
  isSeed,
  path,
  desktopSessions,
  seedNavigationError,
}: Props) {
  const { annotationsSendRef, annotationCount, sendStatusMessage } = annotations;
  return (
    <div
      className="desktop-dock-tile-send"
      // The header is the drag handle; interacting with the send controls must not start a drag.
      onPointerDown={(event) => event.stopPropagation()}
    >
      {seedNavigationError && (
        <span
          className="desktop-dock-tile-send-status desktop-dock-tile-send-status--error"
          role="alert"
          title={seedNavigationError}
        >
          {seedNavigationError}
        </span>
      )}
      <button
        type="button"
        className="desktop-dock-tile-review-button desktop-dock-tile-review-button--overall"
        title="Add an overall note"
        onClick={(event) => annotationsSendRef.current?.openGlobalComment(event.currentTarget)}
      >
        Overall note
      </button>
      <button
        type="button"
        className="desktop-dock-tile-review-button"
        title="Show review notes"
        aria-label={`Notes ${annotationCount}`}
        onClick={() => annotationsSendRef.current?.openInspector()}
      >
        <NotesIcon />
        <span className="desktop-dock-tile-review-label">Notes</span>
        <span className="desktop-dock-tile-review-count">{annotationCount}</span>
      </button>
      {sendStatusMessage ? (
        <span className="desktop-dock-tile-send-status" role="status">
          {sendStatusMessage}
        </span>
      ) : null}
      {isSeed ? (
        <SeedAnnotationDestination annotations={annotations} path={path} />
      ) : (
        <SessionAnnotationDestination
          annotations={annotations}
          desktopSessions={desktopSessions}
        />
      )}
    </div>
  );
}
function NotesIcon() {
  return (
    <svg
      className="desktop-dock-tile-review-icon"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      aria-hidden="true"
    >
      <path d="M3.25 3.25h9.5v7H7l-3.75 2.5v-9.5Z" strokeLinejoin="round" />
    </svg>
  );
}

function SeedAnnotationDestination({ annotations, path }: Pick<Props, 'annotations' | 'path'>) {
  const {
    destinationGroupRef,
    seedTenderSessionId,
    sendStatus,
    sendHasProblem,
    sendDisabled,
    sendButtonTitle,
    sendNow,
    sendActionLabel,
    destinationCaretRef,
    destinationMenuOpen,
    setOpenDestinationMenuKey,
    destinationMenuKey,
    destinationMenuRef,
    closeDestinationMenu,
    sendAlternative,
    performAnnotationSend,
  } = annotations;
  return (
    <div
      ref={destinationGroupRef}
      className="desktop-dock-tile-destination-submit"
      role="group"
      aria-label="Submit seed annotations"
    >
      <button
        type="button"
        className={[
          'desktop-dock-tile-send-button',
          seedTenderSessionId ? 'desktop-dock-tile-send-button--split-primary' : '',
          sendStatus.kind === 'sent' ? 'desktop-dock-tile-send-button--ok' : '',
          sendHasProblem ? `desktop-dock-tile-send-button--${sendStatus.kind}` : '',
        ]
          .filter(Boolean)
          .join(' ')}
        disabled={sendDisabled}
        title={sendButtonTitle}
        onClick={sendNow}
      >
        {sendActionLabel}
        {sendHasProblem ? (
          <span className="desktop-dock-tile-send-alert" aria-hidden="true">
            !
          </span>
        ) : null}
      </button>
      {seedTenderSessionId ? (
        <>
          <button
            ref={destinationCaretRef}
            type="button"
            className="desktop-dock-tile-send-button desktop-dock-tile-send-button--split-caret"
            aria-label="More annotation destinations"
            aria-haspopup="menu"
            aria-expanded={destinationMenuOpen}
            disabled={sendDisabled}
            onClick={() =>
              setOpenDestinationMenuKey((openKey) =>
                openKey === destinationMenuKey ? null : destinationMenuKey,
              )
            }
          >
            ▾
          </button>
          {destinationMenuOpen ? (
            <div
              ref={destinationMenuRef}
              className="desktop-dock-tile-destination-menu"
              role="menu"
            >
              <button
                type="button"
                role="menuitem"
                onClick={() => {
                  closeDestinationMenu();
                  sendAlternative(() => performAnnotationSend({ kind: 'seed', seedId: path }));
                }}
              >
                Note on seed
              </button>
            </div>
          ) : null}
        </>
      ) : null}
    </div>
  );
}

function SessionAnnotationDestination({
  annotations,
  desktopSessions,
}: Pick<Props, 'annotations' | 'desktopSessions'>) {
  const {
    destinationGroupRef,
    destinationCaretRef,
    destinationMenuOpen,
    setOpenDestinationMenuKey,
    destinationMenuKey,
    destinationMenuRef,
    showSessionSendAction,
    targetSessionId,
    targetSessionLabel,
    retargetSession,
  } = annotations;
  return (
    <div
      ref={destinationGroupRef}
      className="desktop-dock-tile-destination-submit"
      role="group"
      aria-label="Send annotation destination"
    >
      {showSessionSendAction ? (
        <SessionAnnotationSendButton annotations={annotations} />
      ) : (
        <button
          ref={destinationCaretRef}
          type="button"
          className="desktop-dock-tile-target-button"
          title={`Annotations will be sent to ${targetSessionLabel}`}
          aria-label={`Annotation destination: ${targetSessionLabel}`}
          aria-haspopup="menu"
          aria-expanded={destinationMenuOpen}
          disabled={!destinationMenuKey}
          onClick={() =>
            setOpenDestinationMenuKey((openKey) =>
              openKey === destinationMenuKey ? null : destinationMenuKey,
            )
          }
        >
          <span className="desktop-dock-tile-send-target-prefix">To</span>
          <span className="desktop-dock-tile-send-target-name">{targetSessionLabel}</span>
          <span aria-hidden="true">▾</span>
        </button>
      )}
      {showSessionSendAction && destinationMenuKey ? (
        <button
          ref={destinationCaretRef}
          type="button"
          className="desktop-dock-tile-send-button desktop-dock-tile-send-button--split-caret"
          aria-label="Change annotation destination"
          aria-haspopup="menu"
          aria-expanded={destinationMenuOpen}
          onClick={() =>
            setOpenDestinationMenuKey((openKey) =>
              openKey === destinationMenuKey ? null : destinationMenuKey,
            )
          }
        >
          ▾
        </button>
      ) : null}
      {destinationMenuOpen ? (
        <SessionDestinationMenu
          destinationMenuRef={destinationMenuRef}
          desktopSessions={desktopSessions}
          targetSessionId={targetSessionId}
          retargetSession={retargetSession}
        />
      ) : null}
    </div>
  );
}

function SessionDestinationMenu({
  destinationMenuRef,
  desktopSessions,
  targetSessionId,
  retargetSession,
}: Pick<
  ReturnType<typeof useTileAnnotations>,
  'destinationMenuRef' | 'targetSessionId' | 'retargetSession'
> &
  Pick<Props, 'desktopSessions'>) {
  return (
    <div
      ref={destinationMenuRef}
      className="desktop-dock-tile-destination-menu"
      role="menu"
      aria-label="Send annotations to session"
      onKeyDown={(event) => {
        if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
        event.preventDefault();
        const items = Array.from(
          event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]'),
        );
        const current = items.indexOf(document.activeElement as HTMLButtonElement);
        const step = event.key === 'ArrowDown' ? 1 : -1;
        items[(current + step + items.length) % items.length]?.focus();
      }}
    >
      {desktopSessions.map((session) => (
        <button
          key={session.sessionId}
          type="button"
          role="menuitemradio"
          aria-checked={session.sessionId === targetSessionId}
          onClick={() => retargetSession(session.sessionId)}
        >
          <span className="desktop-dock-tile-destination-check" aria-hidden="true">
            {session.sessionId === targetSessionId ? '✓' : ''}
          </span>
          <span className="desktop-dock-tile-destination-label"><SessionPriority priority={session.priority} />{session.label}</span>
          {session.state === 'pending_approval' ? (
            <span className="desktop-dock-tile-destination-state">approval</span>
          ) : null}
        </button>
      ))}
    </div>
  );
}

function SessionAnnotationSendButton({ annotations }: Pick<Props, 'annotations'>) {
  const {
    sendStatus,
    sendHasProblem,
    sendDisabled,
    sendButtonTitle,
    sendNow,
    sendActionLabel,
    destinationMenuKey,
    targetSessionId,
    targetSessionLabel,
  } = annotations;
  return (
    <button
      type="button"
      className={[
        'desktop-dock-tile-send-button',
        destinationMenuKey ? 'desktop-dock-tile-send-button--split-primary' : '',
        sendStatus.kind === 'sent' ? 'desktop-dock-tile-send-button--ok' : '',
        sendHasProblem ? `desktop-dock-tile-send-button--${sendStatus.kind}` : '',
      ]
        .filter(Boolean)
        .join(' ')}
      disabled={sendDisabled}
      title={sendButtonTitle}
      aria-label={
        sendStatus.kind === 'sending' || sendStatus.kind === 'sent'
          ? sendActionLabel
          : targetSessionId
            ? `${sendActionLabel} to ${targetSessionLabel}`
            : sendActionLabel
      }
      onClick={sendNow}
    >
      <span className="desktop-dock-tile-send-action">{sendActionLabel}</span>
      {sendStatus.kind !== 'sending' && sendStatus.kind !== 'sent' ? (
        <span className="desktop-dock-tile-send-target">
          <span className="desktop-dock-tile-send-target-prefix">to</span>
          <span className="desktop-dock-tile-send-target-name">{targetSessionLabel}</span>
        </span>
      ) : null}
      {sendHasProblem ? (
        <span className="desktop-dock-tile-send-alert" aria-hidden="true">
          !
        </span>
      ) : null}
    </button>
  );
}
