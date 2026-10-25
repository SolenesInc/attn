import { openUrl } from '@tauri-apps/plugin-opener';
import { useEffect, useRef, useState, type RefObject } from 'react';
import { useOptionalDaemonApi } from '../contexts/DaemonApiContext';
import { useEscapeStack } from '../hooks/useEscapeStack';
import type { SeedCheckVerb } from '../hooks/useDaemonSocket';
import type { Seed, SeedArtifact, SeedArtifactReference } from '../types/generated';
import { GardenCompose } from './GardenCompose';
import { isSafeLocalMarkdownTarget } from './MarkdownReader/markdownLinks';
import { openSeedArtifact } from './SeedArtifactRows';
import { artifactLabel } from './seedArtifacts';
import {
  checkAuthor,
  checkState,
  checkTally,
  evidenceLabel,
  tallyOfChecks,
  tallySentence,
  tallyWords,
  type CheckState,
  type SeedCheck,
  type SeedCheckEvidence,
  type SeedCheckTally,
  type SeedSharedCheck,
} from './seedCheckPresentation';
import './SeedChecks.css';

const BOX: Record<CheckState, string> = { open: '', reported: '?', confirmed: '✓', rejected: '✕' };

export interface SeedChecksProps {
  seed: Seed;
  artifacts: readonly SeedArtifact[];
  references: readonly SeedArtifactReference[];
  seedById: (id: string) => Seed | undefined;
  sessionLabels?: ReadonlyMap<string, string>;
  onOpenMarkdownArtifact?: (path: string) => void;
  onOpenSeedCheck?: (seedId: string, checkId: string) => void;
  focusCheckId?: string;
  onCheckFocused?: () => void;
  sectionRef?: RefObject<HTMLElement | null>;
  className?: string;
}

export function hasChecks(seed: Seed): boolean {
  return (seed.checks?.length ?? 0) + (seed.shared_checks?.length ?? 0) > 0;
}

export function CheckTallyLink({ tally, target }: { tally: SeedCheckTally; target: RefObject<HTMLElement | null> }) {
  return (
    <button
      type="button"
      className="seed-checks-tally"
      aria-label={`Checks: ${tallySentence(tally)}`}
      onClick={() => {
        const section = target.current;
        if (!section) return;
        section.scrollIntoView({ block: 'start' });
        section.querySelector<HTMLElement>('.seed-checks__heading')?.focus({ preventScroll: true });
      }}
    >
      {tallyWords(tally)}
    </button>
  );
}

function shortCommit(commit: string): string {
  return commit.slice(0, 7);
}

function sharedTally(shared: SeedSharedCheck): SeedCheckTally {
  const tally: SeedCheckTally = { total: shared.seeds.length, reported: 0, confirmed: 0, rejected: 0 };
  for (const entry of shared.seeds) {
    const state = checkState(entry.state);
    if (state !== 'open') tally[state] += 1;
  }
  return tally;
}

export function SeedChecks({
  seed,
  artifacts,
  references,
  seedById,
  sessionLabels,
  onOpenMarkdownArtifact,
  onOpenSeedCheck,
  focusCheckId,
  onCheckFocused,
  sectionRef,
  className = '',
}: SeedChecksProps) {
  const daemon = useOptionalDaemonApi();
  const [pending, setPending] = useState('');
  const [failure, setFailure] = useState<{ checkId: string; message: string } | null>(null);
  const [rejecting, setRejecting] = useState<string | null>(null);
  const [removedShown, setRemovedShown] = useState(false);
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set());
  const rejectInput = useRef<HTMLInputElement | null>(null);
  const listRef = useRef<HTMLDivElement | null>(null);

  const composedFor = useRef<string | null>(null);

  useEscapeStack(() => {
    setRejecting(null);
    setFailure(null);
  }, rejecting !== null);

  useEffect(() => {
    if (rejecting) {
      composedFor.current = rejecting;
      rejectInput.current?.focus();
      return;
    }
    const returnTo = composedFor.current;
    composedFor.current = null;
    if (returnTo) listRef.current?.querySelector<HTMLElement>(`[data-check-row="${returnTo}"] .seed-check__box`)?.focus();
  }, [rejecting]);

  useEffect(() => {
    if (!focusCheckId) return;
    const row = listRef.current?.querySelector<HTMLElement>(`[data-check-row="${focusCheckId}"]`);
    if (row) {
      row.scrollIntoView({ block: 'center' });
      row.querySelector<HTMLElement>('button')?.focus({ preventScroll: true });
    }
    onCheckFocused?.();
  }, [focusCheckId, onCheckFocused]);

  const checks = seed.checks ?? [];
  const shared = seed.shared_checks ?? [];
  if (checks.length + shared.length === 0) return null;

  const move = async (checkId: string, verb: SeedCheckVerb, summary?: string) => {
    if (!daemon || pending) return;
    setPending(checkId);
    setFailure(null);
    try {
      await daemon.sendSeedCheck(seed.id, checkId, verb, summary);
      if (verb === 'reject') setRejecting(null);
    } catch (error) {
      setFailure({ checkId, message: error instanceof Error ? error.message : String(error) });
    } finally {
      setPending('');
    }
  };

  const commitReject = () => {
    if (!rejecting) return;
    const reason = (rejectInput.current?.value ?? '').trim();
    if (!reason) {
      setFailure({ checkId: rejecting, message: `rejecting ${rejecting} needs a reason: say what would prove it` });
      rejectInput.current?.focus();
      return;
    }
    void move(rejecting, 'reject', reason);
  };

  const openEvidence = (evidence: SeedCheckEvidence, checkId: string) => {
    const fail = (error: unknown) => setFailure({ checkId, message: error instanceof Error ? error.message : String(error) });
    if (evidence.url) {
      void openUrl(evidence.url).catch(fail);
      return;
    }
    const managed = artifacts.find((artifact) => artifact.filename === evidence.artifact);
    if (managed) {
      void openSeedArtifact(daemon, seed.id, managed, !isSafeLocalMarkdownTarget(managed.filename)).catch(fail);
      return;
    }
    const reference = references.find((candidate) => artifactLabel(candidate) === evidence.artifact);
    if (reference?.url) void openUrl(reference.url).catch(fail);
    else if (reference?.kind === 'markdown_file' && reference.path) onOpenMarkdownArtifact?.(reference.path);
  };

  const evidenceOpens = (evidence: SeedCheckEvidence): boolean => {
    if (evidence.url) return true;
    if (artifacts.some((artifact) => artifact.filename === evidence.artifact)) return Boolean(daemon);
    const reference = references.find((candidate) => artifactLabel(candidate) === evidence.artifact);
    return Boolean(reference?.url || (reference?.kind === 'markdown_file' && reference.path && onOpenMarkdownArtifact));
  };

  const live = checks.filter((check) => !check.removed);
  const removed = checks.filter((check) => check.removed);
  const liveShared = shared.filter((check) => !check.removed);
  const removedShared = shared.filter((check) => check.removed);
  const removedCount = removed.length + removedShared.length;
  const tally = tallyOfChecks(checks);

  const renderCheck = (check: SeedCheck) => {
    const state = checkState(check.state);
    const readOnly = check.removed || !daemon;
    const busy = pending === check.id;
    const report = check.report;
    const verdict = check.verdict;
    const boxVerb: SeedCheckVerb = state === 'open' || state === 'reported' ? 'confirm' : 'uncheck';
    const plot = check.from ? seedById(check.from) : undefined;
    return (
      <li key={check.id} className={`seed-check is-${state}${check.removed ? ' is-removed' : ''}`} data-check-row={check.id}>
        {readOnly ? (
          <span className="seed-check__box" aria-label={state}>{BOX[state]}</span>
        ) : (
          <button
            type="button"
            className="seed-check__box"
            aria-label={`${boxVerb === 'confirm' ? 'Confirm' : 'Uncheck'} ${check.id}`}
            title={`${state}: ${boxVerb === 'confirm' ? 'confirm it' : 'clear the verdict'}`}
            disabled={busy}
            onClick={() => void move(check.id, boxVerb)}
          >
            {BOX[state]}
          </button>
        )}
        <div className="seed-check__main">
          <div className="seed-check__line">
            <span className="seed-check__id">{check.id}</span>
            <span className="seed-check__text">{check.text}</span>
            {check.from && (
              <span className="seed-check__from" title={plot?.title}>from {check.from}</span>
            )}
            {check.removed && <span className="seed-check__from">removed</span>}
          </div>
          {report && (
            <div className="seed-check__detail">
              <span className="seed-check__label">report</span>
              <span className="seed-check__value">
                {report.summary}
                {report.evidence.map((evidence, at) => (
                  <span key={at} className="seed-check__meta">
                    {' · '}
                    {evidenceOpens(evidence) ? (
                      <button type="button" className="seed-check__evidence" onClick={() => openEvidence(evidence, check.id)}>
                        {evidenceLabel(evidence)}
                      </button>
                    ) : (
                      <span className="seed-check__evidence">{evidenceLabel(evidence)}</span>
                    )}
                  </span>
                ))}
                {report.commit && <span className="seed-check__meta" title={report.commit}> · @{shortCommit(report.commit)}</span>}
                <span className="seed-check__meta"> · {checkAuthor(report, sessionLabels)}</span>
              </span>
            </div>
          )}
          {verdict && (
            <div className={`seed-check__verdict is-${verdict.confirmed ? 'confirmed' : 'rejected'}`}>
              {verdict.confirmed ? 'confirmed' : 'rejected'} by {checkAuthor(verdict, sessionLabels)}
              {verdict.reason && <>: {verdict.reason}</>}
              {!report && <span className="seed-check__meta"> · no report</span>}
            </div>
          )}
          {rejecting === check.id && (
            <GardenCompose
              verb="Reject"
              target={check.id}
              prompt="what would prove it"
              required
              busy={busy}
              inputRef={rejectInput}
              onCommit={commitReject}
              onCancel={() => {
                setRejecting(null);
                setFailure(null);
              }}
            />
          )}
          {failure?.checkId === check.id && <p className="seed-check__error" role="alert">{failure.message}</p>}
        </div>
        {!readOnly && rejecting !== check.id && (
          <div className="seed-check__actions">
            {state === 'rejected' && (
              <button type="button" disabled={busy} onClick={() => void move(check.id, 'uncheck')}>Uncheck</button>
            )}
            {report && (
              <button type="button" disabled={busy} aria-label={`Reopen ${check.id}`} onClick={() => void move(check.id, 'reopen')}>Reopen</button>
            )}
            {(state === 'open' || state === 'reported') && (
              <button type="button" disabled={busy} aria-label={`Reject ${check.id}`} onClick={() => setRejecting(check.id)}>Reject…</button>
            )}
          </div>
        )}
      </li>
    );
  };

  const renderShared = (check: SeedSharedCheck) => {
    const open = expanded.has(check.id);
    const rollup = sharedTally(check);
    const toggle = () => setExpanded((cur) => {
      const next = new Set(cur);
      if (next.has(check.id)) next.delete(check.id);
      else next.add(check.id);
      return next;
    });
    return (
      <li key={check.id} className={`seed-check seed-check--shared${check.removed ? ' is-removed' : ''}`} data-check-row={check.id}>
        <button type="button" className="seed-check__toggle" aria-expanded={open} onClick={toggle}>
          <span className="seed-check__caret" aria-hidden="true">{open ? '▾' : '▸'}</span>
          <span className="seed-check__id">{check.id}</span>
          <span className="seed-check__text">{check.text}</span>
          <span className="seed-check__rollup">{tallySentence(rollup)}</span>
        </button>
        {open && (
          <ul className="seed-check__seeds" aria-label={`Seeds that prove ${check.id}`}>
            {check.seeds.map((entry) => {
              const state = checkState(entry.state);
              const inheritor = seedById(entry.seed_id);
              return (
                <li key={entry.seed_id} className={`is-${state}`}>
                  <button
                    type="button"
                    disabled={!onOpenSeedCheck}
                    onClick={() => onOpenSeedCheck?.(entry.seed_id, check.id)}
                  >
                    <span className="seed-check__box" aria-label={state}>{BOX[state]}</span>
                    <span className="seed-check__seed-title">{inheritor?.title ?? entry.seed_id}</span>
                    <span className="seed-check__seed-state">{inheritor?.status ?? entry.seed_id}</span>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </li>
    );
  };

  return (
    <section className={`seed-checks ${className}`} ref={sectionRef} aria-labelledby={`seed-checks-${seed.id}`}>
      <div className="seed-checks__head">
        <h3 className="seed-checks__heading" id={`seed-checks-${seed.id}`} tabIndex={-1}>Checks</h3>
        {tally && <span className="seed-checks__tally">{tallyWords(tally)}</span>}
      </div>
      <div ref={listRef}>
        {live.length > 0 && <ul className="seed-checks__list">{live.map(renderCheck)}</ul>}
        {liveShared.length > 0 && (
          <>
            <h4 className="seed-checks__subhead">Shared by every child</h4>
            <ul className="seed-checks__list">{liveShared.map(renderShared)}</ul>
          </>
        )}
        {removedCount > 0 && (
          <>
            <button
              type="button"
              className="seed-checks__removed-toggle"
              aria-expanded={removedShown}
              onClick={() => setRemovedShown((shown) => !shown)}
            >
              <span aria-hidden="true">{removedShown ? '⌄' : '›'}</span>
              {removedCount} removed
            </button>
            {removedShown && (
              <ul className="seed-checks__list is-removed">
                {removed.map(renderCheck)}
                {removedShared.map(renderShared)}
              </ul>
            )}
          </>
        )}
      </div>
    </section>
  );
}

export function CheckTally({ seed }: { seed: Seed }) {
  const tally = checkTally(seed);
  if (!tally) return null;
  return <span className="seed-checks-tally-compact" title={`checks: ${tallySentence(tally)}`}>{tallyWords(tally)}</span>;
}
