import { openUrl } from '@tauri-apps/plugin-opener';
import { useCallback, useEffect, useRef, useState, type MouseEvent, type PointerEvent, type ReactElement } from 'react';
import type {
  AutomationProvenance as AutomationProvenanceValue,
  SessionPullRequest,
} from '../types/generated';
import { formatShortcut } from '../shortcuts/formatShortcut';
import type { DelegationSession, DispatcherLink } from '../utils/delegationLinks';
import {
  describeSessionPullRequest,
  pickSessionPullRequest,
  sessionPullRequestRepositoryName,
  sortSessionPullRequests,
} from '../utils/sessionPullRequest';
import { SessionPullRequestPopover, type PopoverAnchor } from './SessionPullRequestPopover';
import { SessionDelegatesPopover, type SessionDelegateLink } from './SessionDelegatesPopover';
import './SessionProvenance.css';

export type SessionProvenanceDensity = 'badge' | 'compact' | 'line' | 'detail';

type ProvenanceEntry =
  | { kind: 'automation'; automation: AutomationProvenanceValue }
  | { kind: 'pull-request'; pullRequest: SessionPullRequest; all: SessionPullRequest[] };

const HOVER_CLOSE_DELAY_MS = 120;
const EMPTY_DELEGATES: readonly SessionDelegateLink[] = [];

function shortDefinitionName(name: string): string {
  return name.replace(/^requested pr review\s*[-—:]\s*/i, '').trim() || name;
}

export function automationProvenanceDescription(provenance: AutomationProvenanceValue): string {
  const parts = [`Automation: ${provenance.definition_name}`];
  const pr = provenance.pull_request;
  if (pr) {
    parts.push(`${pr.repository}#${pr.number}`);
    if (pr.title) parts.push(pr.title);
  }
  return parts.join(' · ');
}

export function sessionPullRequestDescription(pr: SessionPullRequest): string {
  const parts = [`PR ${sessionPullRequestRepositoryName(pr.repository)}#${pr.number}`];
  parts.push(describeSessionPullRequest(pr).label);
  if (pr.title) parts.push(pr.title);
  return parts.join(' · ');
}

function provenanceEntries(
  automation?: AutomationProvenanceValue,
  pullRequests?: readonly SessionPullRequest[],
): ProvenanceEntry[] {
  const entries: ProvenanceEntry[] = [];
  if (automation) entries.push({ kind: 'automation', automation });
  const pullRequest = pickSessionPullRequest(pullRequests);
  if (pullRequest && pullRequests) {
    entries.push({ kind: 'pull-request', pullRequest, all: sortSessionPullRequests(pullRequests) });
  }
  return entries;
}

function provenanceDescription(
  entries: readonly ProvenanceEntry[],
  dispatcher: DispatcherLink<DelegationSession> | null | undefined,
  delegates: readonly SessionDelegateLink[],
): string {
  const parts = entries.map((entry) => (entry.kind === 'automation'
    ? automationProvenanceDescription(entry.automation)
    : sessionPullRequestDescription(entry.pullRequest)));
  if (dispatcher) {
    parts.push(`Delegated by ${dispatcher.name}${dispatcher.session ? '' : ' · earlier session'}`);
  }
  if (delegates.length > 0) {
    parts.push(`${delegates.length} ${delegates.length === 1 ? 'delegate' : 'delegates'}`);
  }
  return parts.join(' · ');
}

export function SessionProvenance({
  automation,
  pullRequests,
  dispatcher,
  delegates = EMPTY_DELEGATES,
  onSelectSession,
  density = 'line',
  interactive = false,
}: {
  automation?: AutomationProvenanceValue;
  pullRequests?: readonly SessionPullRequest[];
  dispatcher?: DispatcherLink<DelegationSession> | null;
  delegates?: readonly SessionDelegateLink[];
  onSelectSession?: (sessionId: string) => void;
  density?: SessionProvenanceDensity;
  interactive?: boolean;
}) {
  const [popover, setPopover] = useState<{ anchor: PopoverAnchor; focused: boolean } | null>(null);
  const [delegatesPopover, setDelegatesPopover] = useState<PopoverAnchor | null>(null);
  const closeTimer = useRef<number | null>(null);

  useEffect(() => () => {
    if (closeTimer.current !== null) window.clearTimeout(closeTimer.current);
  }, []);

  const cancelClose = useCallback(() => {
    if (closeTimer.current !== null) {
      window.clearTimeout(closeTimer.current);
      closeTimer.current = null;
    }
  }, []);

  const closePopover = useCallback(() => {
    cancelClose();
    setPopover(null);
  }, [cancelClose]);

  const scheduleClose = useCallback(() => {
    cancelClose();
    closeTimer.current = window.setTimeout(() => setPopover((open) => (
      open?.focused ? open : null
    )), HOVER_CLOSE_DELAY_MS);
  }, [cancelClose]);

  const entries = provenanceEntries(automation, pullRequests);
  const pullRequestEntry = entries.find((entry) => entry.kind === 'pull-request');

  const anchorFrom = (element: HTMLElement): PopoverAnchor => {
    const rect = element.getBoundingClientRect();
    return { top: rect.bottom + 4, left: rect.left };
  };

  const openOnHover = (event: PointerEvent<HTMLElement>) => {
    if (!interactive) return;
    cancelClose();
    setDelegatesPopover(null);
    const anchor = anchorFrom(event.currentTarget);
    setPopover((open) => (open ? open : { anchor, focused: false }));
  };

  const openOnClick = (event: MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation();
    if (!interactive) return;
    cancelClose();
    setDelegatesPopover(null);
    setPopover({ anchor: anchorFrom(event.currentTarget), focused: true });
  };

  const openDelegates = (event: MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation();
    if (!interactive || !onSelectSession) return;
    closePopover();
    setDelegatesPopover((open) => (open ? null : anchorFrom(event.currentTarget)));
  };

  const description = provenanceDescription(entries, dispatcher, delegates);

  if (entries.length === 0 && !dispatcher && delegates.length === 0) return null;

  if (density === 'badge') {
    return <BadgeProvenance entries={entries} dispatcher={dispatcher} delegates={delegates} description={description} />;
  }

  if (density === 'compact') {
    return <CompactProvenance entries={entries} dispatcher={dispatcher} delegates={delegates} description={description} />;
  }

  return (
    <>
      <span className={`session-provenance session-provenance--${density}`} title={description}>
        {entries.flatMap((entry) => (entry.kind === 'automation'
          ? automationLineParts(entry.automation, interactive)
          : pullRequestLineParts(entry.pullRequest, interactive, openOnHover, scheduleClose, openOnClick)))}
        {dispatcherLinePart(dispatcher, interactive, onSelectSession)}
        {delegatesLinePart(delegates, interactive, Boolean(delegatesPopover), onSelectSession, openDelegates)}
      </span>
      {popover && (
        <SessionPullRequestPopover
          pullRequests={pullRequestEntry?.all ?? []}
          anchor={popover.anchor}
          autoFocus={popover.focused}
          onClose={closePopover}
          onPointerEnter={cancelClose}
          onPointerLeave={scheduleClose}
        />
      )}
      {delegatesPopover && onSelectSession && (
        <SessionDelegatesPopover
          delegates={delegates}
          anchor={delegatesPopover}
          onSelectSession={onSelectSession}
          onClose={() => setDelegatesPopover(null)}
        />
      )}
    </>
  );
}

function BadgeProvenance({
  entries,
  dispatcher,
  delegates,
  description,
}: {
  entries: readonly ProvenanceEntry[];
  dispatcher: DispatcherLink<DelegationSession> | null | undefined;
  delegates: readonly SessionDelegateLink[];
  description: string;
}) {
  return (
    <span className="session-provenance session-provenance--badge" title={description} aria-label={description}>
      {entries.map((entry) => (entry.kind === 'automation' ? (
        <span key="automation" className="session-provenance__badge-mark" aria-hidden="true">⚡</span>
      ) : (
        <span
          key="pull-request"
          className="session-provenance__badge-mark session-provenance__badge-mark--pr"
          data-tone={describeSessionPullRequest(entry.pullRequest).tone}
          aria-hidden="true"
        >
          #{entry.pullRequest.number}
        </span>
      )))}
      {dispatcher && <span className="session-provenance__badge-mark" aria-hidden="true">↑</span>}
      {delegates.length > 0 && (
        <span className="session-provenance__badge-mark" aria-hidden="true">↓{delegates.length}</span>
      )}
    </span>
  );
}

function CompactProvenance({
  entries,
  dispatcher,
  delegates,
  description,
}: {
  entries: readonly ProvenanceEntry[];
  dispatcher: DispatcherLink<DelegationSession> | null | undefined;
  delegates: readonly SessionDelegateLink[];
  description: string;
}) {
  return (
    <span className="session-provenance session-provenance--compact" title={description} aria-label={description}>
      {entries.map((entry) => (entry.kind === 'automation' ? (
        <span key="automation" className="session-provenance__part">
          <span className="session-provenance__kind" aria-hidden="true">⚡</span>
          <span className="session-provenance__definition">
            {shortDefinitionName(entry.automation.definition_name)}
          </span>
          {entry.automation.pull_request && (
            <span className="session-provenance__target">#{entry.automation.pull_request.number}</span>
          )}
        </span>
      ) : (
        <span key="pull-request" className="session-provenance__part">
          <CompactPullRequest pullRequest={entry.pullRequest} />
        </span>
      )))}
      {dispatcher && (
        <span className="session-provenance__part">
          ↑ {dispatcher.name}{dispatcher.session ? '' : ' · earlier session'}
        </span>
      )}
      {delegates.length > 0 && (
        <span className="session-provenance__part">
          ↓ {delegates.length} {delegates.length === 1 ? 'delegate' : 'delegates'}
        </span>
      )}
    </span>
  );
}

function dispatcherLinePart(
  dispatcher: DispatcherLink<DelegationSession> | null | undefined,
  interactive: boolean,
  onSelectSession: ((sessionId: string) => void) | undefined,
): ReactElement[] {
  if (!dispatcher) return [];
  const earlier = !dispatcher.session;
  const content = (
    <>
      <span aria-hidden="true">↑</span>
      <span>delegated by {dispatcher.name}</span>
      {earlier && <span className="session-provenance__earlier">earlier session</span>}
      {!earlier && <kbd>{formatShortcut('session.orchestrator')}</kbd>}
    </>
  );
  if (!interactive) {
    return [<span key="dispatcher" className="session-provenance__delegation session-provenance__delegation--up">{content}</span>];
  }
  return [(
    <button
      key="dispatcher"
      type="button"
      className="session-provenance__delegation session-provenance__delegation--up"
      disabled={earlier || !onSelectSession}
      title={earlier ? `${dispatcher.name}, earlier session` : `Open ${dispatcher.name}`}
      onPointerDown={(event) => event.stopPropagation()}
      onClick={(event) => {
        event.stopPropagation();
        if (dispatcher.session) onSelectSession?.(dispatcher.session.id);
      }}
    >
      {content}
    </button>
  )];
}

function delegatesLinePart(
  delegates: readonly SessionDelegateLink[],
  interactive: boolean,
  open: boolean,
  onSelectSession: ((sessionId: string) => void) | undefined,
  onClick: (event: MouseEvent<HTMLButtonElement>) => void,
): ReactElement[] {
  if (delegates.length === 0) return [];
  const label = `${delegates.length} ${delegates.length === 1 ? 'delegate' : 'delegates'}`;
  const content = <><span aria-hidden="true">↓</span><span>{label}</span></>;
  if (!interactive) {
    return [<span key="delegates" className="session-provenance__delegation session-provenance__delegation--down">{content}</span>];
  }
  return [(
    <button
      key="delegates"
      type="button"
      className="session-provenance__delegation session-provenance__delegation--down"
      aria-expanded={open}
      disabled={!onSelectSession}
      onPointerDown={(event) => event.stopPropagation()}
      onClick={onClick}
    >
      {content}
    </button>
  )];
}

function CompactPullRequest({ pullRequest }: { pullRequest: SessionPullRequest }) {
  const { label, tone } = describeSessionPullRequest(pullRequest);
  return (
    <>
      <span className="session-provenance__dot" data-tone={tone} aria-hidden="true" />
      <span className="session-provenance__target">#{pullRequest.number}</span>
      <span className="session-provenance__status" data-tone={tone}>{label}</span>
    </>
  );
}

function automationLineParts(
  automation: AutomationProvenanceValue,
  interactive: boolean,
): ReactElement[] {
  const pr = automation.pull_request;
  const target = pr ? `${sessionPullRequestRepositoryName(pr.repository)}#${pr.number}` : null;
  const parts = [
    <span key="automation-kind" className="session-provenance__kind">
      <span aria-hidden="true">⚡</span>
      Automation
    </span>,
    <span key="automation-definition" className="session-provenance__definition">
      {shortDefinitionName(automation.definition_name)}
    </span>,
  ];
  if (target && pr) {
    parts.push(interactive ? (
      <button
        key="automation-target"
        type="button"
        className="session-provenance__target"
        onPointerDown={(event) => event.stopPropagation()}
        onClick={(event) => {
          event.stopPropagation();
          openUrl(pr.url).catch((error) => {
            console.error('[SessionProvenance] Failed to open PR URL:', error);
          });
        }}
      >
        {target} ↗
      </button>
    ) : (
      <span key="automation-target" className="session-provenance__target">{target}</span>
    ));
  }
  if (pr?.title) {
    parts.push(<span key="automation-title" className="session-provenance__title">{pr.title}</span>);
  }
  return parts;
}

function pullRequestLineParts(
  pullRequest: SessionPullRequest,
  interactive: boolean,
  onPointerEnter: (event: PointerEvent<HTMLElement>) => void,
  onPointerLeave: () => void,
  onClick: (event: MouseEvent<HTMLButtonElement>) => void,
): ReactElement[] {
  const { label, tone } = describeSessionPullRequest(pullRequest);
  const target = `${sessionPullRequestRepositoryName(pullRequest.repository)}#${pullRequest.number}`;
  const parts = [
    <span key="pr-kind" className="session-provenance__kind session-provenance__kind--pr">
      <span aria-hidden="true">⎇</span>
      PR
    </span>,
    interactive ? (
      <button
        key="pr-target"
        type="button"
        className="session-provenance__target"
        data-testid="session-provenance-pr"
        aria-label={`Pull request ${target} details`}
        onPointerDown={(event) => event.stopPropagation()}
        onPointerEnter={onPointerEnter}
        onPointerLeave={onPointerLeave}
        onClick={onClick}
      >
        {target} ↗
      </button>
    ) : (
      <span key="pr-target" className="session-provenance__target">{target}</span>
    ),
    <span key="pr-status" className="session-provenance__status" data-tone={tone}>{label}</span>,
  ];
  if (pullRequest.title) {
    parts.push(<span key="pr-title" className="session-provenance__title">{pullRequest.title}</span>);
  }
  return parts;
}
