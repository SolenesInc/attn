import { openUrl } from '@tauri-apps/plugin-opener';
import { useCallback, useRef, useState, type MouseEvent, type PointerEvent, type ReactElement } from 'react';
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
import './SessionProvenance.css';

export type SessionProvenanceDensity = 'badge' | 'compact' | 'line' | 'detail';

type ProvenanceEntry =
  | { kind: 'automation'; automation: AutomationProvenanceValue }
  | { kind: 'pull-request'; pullRequest: SessionPullRequest; all: SessionPullRequest[] };

// Hover has to survive the gap between the line and the popover below it.
const HOVER_CLOSE_DELAY_MS = 120;

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

export function SessionProvenance({
  automation,
  pullRequests,
  density = 'line',
  interactive = false,
}: {
  automation?: AutomationProvenanceValue;
  pullRequests?: readonly SessionPullRequest[];
  density?: SessionProvenanceDensity;
  interactive?: boolean;
}) {
  const [popover, setPopover] = useState<{ anchor: PopoverAnchor; focused: boolean } | null>(null);
  const closeTimer = useRef<number | null>(null);

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
  if (entries.length === 0) return null;
  const pullRequestEntry = entries.find((entry) => entry.kind === 'pull-request');

  const anchorFrom = (element: HTMLElement): PopoverAnchor => {
    const rect = element.getBoundingClientRect();
    return { top: rect.bottom + 4, left: rect.left };
  };

  const openOnHover = (event: PointerEvent<HTMLElement>) => {
    if (!interactive) return;
    cancelClose();
    const anchor = anchorFrom(event.currentTarget);
    setPopover((open) => (open ? open : { anchor, focused: false }));
  };

  const openOnClick = (event: MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation();
    if (!interactive) return;
    cancelClose();
    setPopover({ anchor: anchorFrom(event.currentTarget), focused: true });
  };

  const description = entries
    .map((entry) => (entry.kind === 'automation'
      ? automationProvenanceDescription(entry.automation)
      : sessionPullRequestDescription(entry.pullRequest)))
    .join(' · ');

  if (density === 'badge') {
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
      </span>
    );
  }

  if (density === 'compact') {
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
            <SessionPullRequestDot pullRequest={entry.pullRequest} />
            <span className="session-provenance__target">#{entry.pullRequest.number}</span>
            <span
              className="session-provenance__status"
              data-tone={describeSessionPullRequest(entry.pullRequest).tone}
            >
              {describeSessionPullRequest(entry.pullRequest).label}
            </span>
          </span>
        )))}
      </span>
    );
  }

  return (
    <>
      <span className={`session-provenance session-provenance--${density}`} title={description}>
        {entries.flatMap((entry) => (entry.kind === 'automation'
          ? automationLineParts(entry.automation, interactive)
          : pullRequestLineParts(entry.pullRequest, interactive, openOnHover, scheduleClose, openOnClick)))}
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

function SessionPullRequestDot({ pullRequest }: { pullRequest: SessionPullRequest }) {
  return (
    <span
      className="session-provenance__dot"
      data-tone={describeSessionPullRequest(pullRequest).tone}
      aria-hidden="true"
    />
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
