import { automationProvenanceDescription, sessionPullRequestDescription } from '../utils/provenanceDescription';
import { openUrl } from '@tauri-apps/plugin-opener';
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type MouseEvent, type PointerEvent, type ReactElement } from 'react';
import type {
  AutomationProvenance as AutomationProvenanceValue,
  SessionPullRequest,
} from '../types/generated';
import {
  describeSessionPullRequest,
  pickSessionPullRequest,
  sessionPullRequestRepositoryName,
  sortSessionPullRequests,
} from '../utils/sessionPullRequest';
import { SessionPullRequestPopover, type PopoverAnchor } from './SessionPullRequestPopover';
import { useDelegationChainControl } from './DelegationChain';
import './SessionProvenance.css';

export type SessionProvenanceDensity = 'compact' | 'line';

export interface SessionProvenancePopoverGroup {
  id: string;
  activeId: string | null;
  onOpen: (id: string) => void;
}

type ProvenanceEntry =
  | { kind: 'automation'; automation: AutomationProvenanceValue }
  | { kind: 'pull-request'; pullRequest: SessionPullRequest; all: SessionPullRequest[] };

const HOVER_CLOSE_DELAY_MS = 120;

function shortDefinitionName(name: string): string {
  return name.replace(/^requested pr review\s*[-—:]\s*/i, '').trim() || name;
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

function provenanceDescription(entries: readonly ProvenanceEntry[]): string {
  return entries.map((entry) => (entry.kind === 'automation'
    ? automationProvenanceDescription(entry.automation)
    : sessionPullRequestDescription(entry.pullRequest))).join(' · ');
}

export function SessionProvenance({
  automation,
  pullRequests,
  density = 'line',
  interactive = false,
  popoverGroup,
}: {
  automation?: AutomationProvenanceValue;
  pullRequests?: readonly SessionPullRequest[];
  density?: SessionProvenanceDensity;
  interactive?: boolean;
  popoverGroup?: SessionProvenancePopoverGroup;
}) {
  const [popover, setPopover] = useState<{ anchor: PopoverAnchor; focused: boolean } | null>(null);
  const delegationChain = useDelegationChainControl();
  const closeTimer = useRef<number | null>(null);
  const popoverGroupId = popoverGroup?.id;
  const activePopoverId = popoverGroup?.activeId;
  const openGroupPopover = popoverGroup?.onOpen;

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

  useLayoutEffect(() => {
    if (!popoverGroupId || activePopoverId === popoverGroupId) return;
    closePopover();
  }, [activePopoverId, closePopover, popoverGroupId]);

  const claimPopover = () => {
    delegationChain?.dismiss('handoff');
    if (popoverGroupId) openGroupPopover?.(popoverGroupId);
  };

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
    if (!interactive || delegationChain?.pinned) return;
    cancelClose();
    claimPopover();
    const anchor = anchorFrom(event.currentTarget);
    setPopover((open) => (open ? open : { anchor, focused: false }));
  };

  const openOnClick = (event: MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation();
    if (!interactive) return;
    cancelClose();
    claimPopover();
    setPopover({ anchor: anchorFrom(event.currentTarget), focused: true });
  };

  const description = provenanceDescription(entries);

  if (entries.length === 0) return null;

  if (density === 'compact') {
    return automation ? <CompactProvenance automation={automation} description={description} /> : null;
  }

  return (
    <>
      <span className={`session-provenance session-provenance--${density}`} title={description}>
        {entries.flatMap((entry) => (entry.kind === 'automation'
          ? automationLineParts(entry.automation, interactive)
          : pullRequestLineParts(entry.pullRequest, openOnHover, scheduleClose, openOnClick)))}
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
    </>
  );
}

function CompactProvenance({
  automation,
  description,
}: {
  automation: AutomationProvenanceValue;
  description: string;
}) {
  return (
    <span className="session-provenance session-provenance--compact" title={description} aria-label={description}>
      <span className="session-provenance__part">
        <span className="session-provenance__kind" aria-hidden="true">⚡</span>
        <span className="session-provenance__definition">
          {shortDefinitionName(automation.definition_name)}
        </span>
        {automation.pull_request && (
          <span className="session-provenance__target">#{automation.pull_request.number}</span>
        )}
      </span>
    </span>
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
    <button
      key="pr-target"
      type="button"
      className="session-provenance__target"
      aria-label={`Pull request ${target} details`}
      onPointerDown={(event) => event.stopPropagation()}
      onPointerEnter={onPointerEnter}
      onPointerLeave={onPointerLeave}
      onClick={onClick}
    >
      {target} ↗
    </button>,
    <span key="pr-status" className="session-provenance__status" data-tone={tone}>{label}</span>,
  ];
  if (pullRequest.title) {
    parts.push(<span key="pr-title" className="session-provenance__title">{pullRequest.title}</span>);
  }
  return parts;
}
