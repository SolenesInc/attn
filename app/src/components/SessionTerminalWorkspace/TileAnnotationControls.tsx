import { useTileAnnotations } from './useTileAnnotations';
import type { WorkspaceTileSessionOption } from './WorkspaceDockTile';
interface Props {
  annotations: ReturnType<typeof useTileAnnotations>;
  isSeed: boolean;
  path: string;
  workspaceSessions: WorkspaceTileSessionOption[];
  seedNavigationError: string;
}
export function TileAnnotationControls({
  annotations,
  isSeed,
  path,
  workspaceSessions,
  seedNavigationError,
}: Props) {
  const { annotationsSendRef, annotationCount, sendStatusMessage } = annotations;
  return (
    <div
      className="workspace-dock-tile-send"
      // The header is the drag handle; interacting with the send controls must not start a drag.
      onPointerDown={(event) => event.stopPropagation()}
    >
      {seedNavigationError && (
        <span
          className="workspace-dock-tile-send-status workspace-dock-tile-send-status--error"
          role="alert"
          title={seedNavigationError}
        >
          {seedNavigationError}
        </span>
      )}
      <button
        type="button"
        className="workspace-dock-tile-review-button workspace-dock-tile-review-button--overall"
        title="Add an overall note"
        onClick={(event) => annotationsSendRef.current?.openGlobalComment(event.currentTarget)}
      >
        Overall note
      </button>
      <button
        type="button"
        className="workspace-dock-tile-review-button"
        title="Show review notes"
        aria-label={`Notes ${annotationCount}`}
        onClick={() => annotationsSendRef.current?.openInspector()}
      >
        <NotesIcon />
        <span className="workspace-dock-tile-review-label">Notes</span>
        <span className="workspace-dock-tile-review-count">{annotationCount}</span>
      </button>
      {sendStatusMessage ? (
        <span className="workspace-dock-tile-send-status" role="status">
          {sendStatusMessage}
        </span>
      ) : null}
      {isSeed ? (
        <SeedAnnotationDestination annotations={annotations} path={path} />
      ) : (
        <SessionAnnotationDestination
          annotations={annotations}
          workspaceSessions={workspaceSessions}
        />
      )}
    </div>
  );
}
function NotesIcon() {
  return (
    <svg
      className="workspace-dock-tile-review-icon"
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
      className="workspace-dock-tile-destination-submit"
      role="group"
      aria-label="Submit seed annotations"
    >
      <button
        type="button"
        className={[
          'workspace-dock-tile-send-button',
          seedTenderSessionId ? 'workspace-dock-tile-send-button--split-primary' : '',
          sendStatus.kind === 'sent' ? 'workspace-dock-tile-send-button--ok' : '',
          sendHasProblem ? `workspace-dock-tile-send-button--${sendStatus.kind}` : '',
        ]
          .filter(Boolean)
          .join(' ')}
        disabled={sendDisabled}
        title={sendButtonTitle}
        onClick={sendNow}
      >
        {sendActionLabel}
        {sendHasProblem ? (
          <span className="workspace-dock-tile-send-alert" aria-hidden="true">
            !
          </span>
        ) : null}
      </button>
      {seedTenderSessionId ? (
        <>
          <button
            ref={destinationCaretRef}
            type="button"
            className="workspace-dock-tile-send-button workspace-dock-tile-send-button--split-caret"
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
              className="workspace-dock-tile-destination-menu"
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
  workspaceSessions,
}: Pick<Props, 'annotations' | 'workspaceSessions'>) {
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
      className="workspace-dock-tile-destination-submit"
      role="group"
      aria-label="Send annotation destination"
    >
      {showSessionSendAction ? (
        <SessionAnnotationSendButton annotations={annotations} />
      ) : (
        <button
          ref={destinationCaretRef}
          type="button"
          className="workspace-dock-tile-target-button"
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
          <span className="workspace-dock-tile-send-target-prefix">To</span>
          <span className="workspace-dock-tile-send-target-name">{targetSessionLabel}</span>
          <span aria-hidden="true">▾</span>
        </button>
      )}
      {showSessionSendAction && destinationMenuKey ? (
        <button
          ref={destinationCaretRef}
          type="button"
          className="workspace-dock-tile-send-button workspace-dock-tile-send-button--split-caret"
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
          workspaceSessions={workspaceSessions}
          targetSessionId={targetSessionId}
          retargetSession={retargetSession}
        />
      ) : null}
    </div>
  );
}

function SessionDestinationMenu({
  destinationMenuRef,
  workspaceSessions,
  targetSessionId,
  retargetSession,
}: Pick<
  ReturnType<typeof useTileAnnotations>,
  'destinationMenuRef' | 'targetSessionId' | 'retargetSession'
> &
  Pick<Props, 'workspaceSessions'>) {
  return (
    <div
      ref={destinationMenuRef}
      className="workspace-dock-tile-destination-menu"
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
      {workspaceSessions.map((session) => (
        <button
          key={session.sessionId}
          type="button"
          role="menuitemradio"
          aria-checked={session.sessionId === targetSessionId}
          onClick={() => retargetSession(session.sessionId)}
        >
          <span className="workspace-dock-tile-destination-check" aria-hidden="true">
            {session.sessionId === targetSessionId ? '✓' : ''}
          </span>
          <span className="workspace-dock-tile-destination-label">{session.label}</span>
          {session.state === 'pending_approval' ? (
            <span className="workspace-dock-tile-destination-state">approval</span>
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
        'workspace-dock-tile-send-button',
        destinationMenuKey ? 'workspace-dock-tile-send-button--split-primary' : '',
        sendStatus.kind === 'sent' ? 'workspace-dock-tile-send-button--ok' : '',
        sendHasProblem ? `workspace-dock-tile-send-button--${sendStatus.kind}` : '',
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
      <span className="workspace-dock-tile-send-action">{sendActionLabel}</span>
      {sendStatus.kind !== 'sending' && sendStatus.kind !== 'sent' ? (
        <span className="workspace-dock-tile-send-target">
          <span className="workspace-dock-tile-send-target-prefix">to</span>
          <span className="workspace-dock-tile-send-target-name">{targetSessionLabel}</span>
        </span>
      ) : null}
      {sendHasProblem ? (
        <span className="workspace-dock-tile-send-alert" aria-hidden="true">
          !
        </span>
      ) : null}
    </button>
  );
}
