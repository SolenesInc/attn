import { useCallback, useEffect, useMemo, useState } from 'react';
import type {
  GardenReview,
  GardenReviewItem,
  Seed,
  SeedDocument,
  SeedHandoverOptions,
  SeedSendToChiefOptions,
  SeedReviewActionContext,
} from '../hooks/useDaemonSocket';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { gardenPathToSeed } from '../store/gardenWalk';
import { SeedDocumentView } from './SeedDocumentView';
import './GardenReviewPanel.css';

type LifecycleAction = 'wither' | 'park';
type ComposerKind = LifecycleAction | 'handover' | 'chief';

interface ComposerState {
  itemId: string;
  kind: ComposerKind;
  text: string;
  busy: boolean;
  drafting: boolean;
  error: string;
  pendingDraft: string;
}

export interface GardenReviewPanelProps {
  review: GardenReview;
  seeds: Seed[];
  frame: 'dock' | 'full';
  onExit: () => void;
  onClose: () => void;
  onToggleFrame?: () => void;
  onEscapeFloor: () => void;
  fetchSeedDocument: (seedId: string) => Promise<SeedDocument>;
  onMoveSeed: (
    seedId: string,
    verb: string,
    reason?: string,
    force?: boolean,
    comment?: string,
    review?: SeedReviewActionContext,
  ) => Promise<Seed>;
  onResumeSeed: (seedId: string, review: SeedReviewActionContext) => Promise<unknown>;
  onKeepSeed: (seedId: string, review: SeedReviewActionContext) => Promise<unknown>;
  onHandoverSeed: (options: Omit<SeedHandoverOptions, 'sourceSessionId'>) => Promise<unknown>;
  onSendSeedToChief?: (options: Omit<SeedSendToChiefOptions, 'sourceSessionId'>) => Promise<unknown>;
  onRetry: (reviewId: string, seedId: string) => Promise<unknown>;
  onDraft: (seedId: string, review: SeedReviewActionContext) => Promise<string>;
  onRefresh: (reviewId: string) => Promise<unknown>;
}

const actionLabels: Record<string, string> = {
  resume: 'Resume',
  handover: 'Handover',
  chief: 'Send to Chief',
  send_to_chief: 'Send to Chief',
  keep_growing: 'Keep growing',
  park: 'Park',
  harvest: 'Harvest',
  wither: 'Wither',
};

const REVIEW_HARVEST_REASON = "The seed's stated outcome and required verification are complete.";
const LIFECYCLE_REASON_MAX_CHARS = 400;

function advisorGuidance(item: GardenReviewItem, action: string): string {
  if (item.recommendation !== action) return '';
  return item.explanation?.trim() ?? '';
}

function lifecycleReason(text: string): string {
  const characters = Array.from(text.trim());
  if (characters.length <= LIFECYCLE_REASON_MAX_CHARS) return characters.join('');
  return `${characters.slice(0, LIFECYCLE_REASON_MAX_CHARS - 1).join('')}…`;
}

function reviewContext(review: GardenReview, item: GardenReviewItem): SeedReviewActionContext {
  return { reviewId: review.run.id, evidenceVersion: item.evidence_version };
}

function statusWords(item: GardenReviewItem): string {
  if (item.resolution === 'resolved') {
    return item.resolved_action === 'keep_growing' ? 'kept growing' : 'resolved';
  }
  if (item.resolution === 'no_longer_applicable') return 'no longer needs review';
  if (item.status === 'queued' && item.advisor_state === 'retrying') return 'advisor retrying';
  if (item.status === 'queued') return 'waiting for advisor';
  if (item.status === 'running') return 'advisor is reviewing';
  if (item.status === 'failed') return 'advisor failed';
  if (item.status === 'invalidated') return 'seed changed';
  return 'ready';
}

function ageWords(iso: string | undefined, now: number): string {
  if (!iso) return '';
  const at = Date.parse(iso);
  if (Number.isNaN(at)) return '';
  const seconds = Math.max(0, Math.round((now - at) / 1000));
  if (seconds < 10) return 'just now';
  if (seconds < 60) return `${seconds}s ago`;
  return `${Math.round(seconds / 60)}m ago`;
}

function untilWords(iso: string | undefined, now: number): string {
  if (!iso) return '';
  const at = Date.parse(iso);
  if (Number.isNaN(at)) return '';
  const seconds = Math.max(0, Math.ceil((at - now) / 1000));
  if (seconds < 10) return 'in a few seconds';
  if (seconds < 60) return `in ${seconds}s`;
  return `in ${Math.ceil(seconds / 60)}m`;
}

function advisorFailureWords(error: string | undefined): string {
  if (!error) return 'The advisor stopped before it produced advice.';
  if (error.includes('invalid advice') || error.includes('does not match its schema')) {
    return 'The advisor stopped because its answers did not match the expected format.';
  }
  if (error.includes('deadline exceeded') || error.includes('took too long')) {
    return 'The advisor stopped because its attempts took too long.';
  }
  return error;
}

function AdvisorProgress({ review, item }: { review: GardenReview; item: GardenReviewItem }) {
  const active = item.advisor_state !== 'failed'
    && (item.status === 'queued' || item.status === 'running');
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!active) return;
    const timer = window.setInterval(() => setNow(Date.now()), 10_000);
    return () => window.clearInterval(timer);
  }, [active]);

  const advised = review.items.filter((candidate) => candidate.status === 'ready'
    && candidate.resolution === 'unresolved').length;
  const attempt = item.advisor_attempt ?? 0;
  const maxAttempts = item.advisor_max_attempts ?? 3;
  let detail = 'Waiting for an advisor slot.';
  if (item.advisor_state === 'retrying') {
    detail = `Attempt ${attempt} of ${maxAttempts} did not produce usable advice. Retrying ${untilWords(item.advisor_retry_at, now)}.`;
  } else if (item.advisor_state === 'failed') {
    detail = `The advisor stopped after ${attempt} of ${maxAttempts} attempts.`;
  } else if (item.status === 'running') {
    detail = `Attempt ${Math.max(1, attempt)} of ${maxAttempts} started ${ageWords(item.started_at ?? item.advisor_updated_at, now)}.`;
  } else if (attempt > 0) {
    detail = `Preparing attempt ${Math.min(attempt + 1, maxAttempts)} of ${maxAttempts}.`;
  }

  return (
    <section className="garden-review__waiting" aria-live="polite">
      <div className="garden-review__progress-head">
        <div>
          <h3>Advisor progress</h3>
          <p>Advice ready for {advised} of {review.items.length} seeds.</p>
        </div>
        <span>{review.run.recipe.agent} · {review.run.recipe.model}</span>
      </div>
      <div className="garden-review__progress-track" aria-label={`Advice ready for ${advised} of ${review.items.length} seeds`}>
        {review.items.map((candidate) => (
          <span
            key={candidate.id}
            className={`is-${candidate.status}${candidate.advisor_state ? ` is-advisor-${candidate.advisor_state}` : ''}`}
            title={`${candidate.title}: ${statusWords(candidate)}`}
          />
        ))}
      </div>
      <p className="garden-review__progress-detail">{detail}</p>
      {item.advisor_error && <p className="garden-review__progress-error">{item.advisor_error}</p>}
      <p>You can choose an action now. Advice will appear here if it arrives first.</p>
    </section>
  );
}

function FrameGlyph({ direction }: { direction: 'out' | 'in' }) {
  return (
    <svg width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden="true">
      <path
        d={direction === 'out'
          ? 'M4.5 1.5H1.5V4.5M7.5 10.5H10.5V7.5'
          : 'M1.5 4.5H4.5V1.5M10.5 7.5H7.5V10.5'}
        stroke="currentColor"
        strokeWidth="1.25"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function emptyComposer(item: GardenReviewItem, kind: ComposerKind): ComposerState {
  const guidance = advisorGuidance(item, kind);
  return {
    itemId: item.id,
    kind,
    text: kind === 'wither' ? lifecycleReason(guidance) : guidance,
    busy: false,
    drafting: false,
    error: '',
    pendingDraft: '',
  };
}

export function GardenReviewPanel({
  review,
  seeds,
  frame,
  onExit,
  onClose,
  onToggleFrame,
  onEscapeFloor,
  fetchSeedDocument,
  onMoveSeed,
  onResumeSeed,
  onKeepSeed,
  onHandoverSeed,
  onSendSeedToChief,
  onRetry,
  onDraft,
  onRefresh,
}: GardenReviewPanelProps) {
  const unresolved = useMemo(
    () => review.items.filter((item) => item.resolution === 'unresolved'),
    [review.items],
  );
  const [selectedId, setSelectedId] = useState(() => unresolved[0]?.id ?? review.items[0]?.id ?? '');
  const [composer, setComposer] = useState<ComposerState | null>(null);
  const [actionError, setActionError] = useState('');
  const [retrying, setRetrying] = useState(false);
  const [browsedSeedId, setBrowsedSeedId] = useState('');
  const [browsedDocument, setBrowsedDocument] = useState<SeedDocument | null>(null);
  const [seedReadError, setSeedReadError] = useState('');
  const [seedLoading, setSeedLoading] = useState(false);
  const selected = review.items.find((item) => item.id === selectedId) ?? unresolved[0] ?? review.items[0];
  const visibleActions = selected?.actions.filter(
    (action) => action !== 'send_to_chief' || Boolean(onSendSeedToChief),
  ) ?? [];
  const complete = unresolved.length === 0;
  const atReviewSeed = Boolean(selected && browsedSeedId === selected.seed_id);

  useEffect(() => {
    if (!selected) return;
    setBrowsedSeedId(selected.seed_id);
    setBrowsedDocument(null);
    setSeedReadError('');
  }, [selected?.id, selected?.seed_id]);

  useEffect(() => {
    if (!browsedSeedId) return;
    let ignore = false;
    setSeedLoading(true);
    setSeedReadError('');
    fetchSeedDocument(browsedSeedId)
      .then((document) => {
        if (ignore) return;
        setBrowsedDocument(document);
        setSeedLoading(false);
      })
      .catch((error) => {
        if (ignore) return;
        setBrowsedDocument(null);
        setSeedLoading(false);
        setSeedReadError(error instanceof Error ? error.message : 'Could not read this seed');
      });
    return () => {
      ignore = true;
    };
  }, [browsedSeedId, fetchSeedDocument, seeds]);

  useEffect(() => {
    if (complete) return;
    const current = review.items.find((item) => item.id === selectedId);
    if (!current || current.resolution !== 'unresolved') {
      setSelectedId(unresolved[0].id);
      setComposer(null);
    }
  }, [complete, review.items, selectedId, unresolved]);

  useEffect(() => {
    setActionError('');
    setComposer((current) => (current?.itemId === selectedId ? current : null));
  }, [selectedId]);

  useEscapeStack(onEscapeFloor, true);
  useEscapeStack(onExit, true);
  useEscapeStack(() => setComposer(null), composer !== null);

  const refresh = useCallback(async () => {
    await onRefresh(review.run.id);
  }, [onRefresh, review.run.id]);

  const runResume = useCallback(async (item: GardenReviewItem) => {
    setActionError('');
    try {
      await onResumeSeed(item.seed_id, reviewContext(review, item));
      await refresh();
    } catch (error) {
      setActionError(error instanceof Error ? error.message : 'Resume failed');
      await refresh().catch(() => {});
    }
  }, [onResumeSeed, refresh, review]);

  const runKeep = useCallback(async (item: GardenReviewItem) => {
    setActionError('');
    try {
      await onKeepSeed(item.seed_id, reviewContext(review, item));
      await refresh();
    } catch (error) {
      setActionError(error instanceof Error ? error.message : 'Keeping the seed growing failed');
      await refresh().catch(() => {});
    }
  }, [onKeepSeed, refresh, review]);

  const runHarvest = useCallback(async (item: GardenReviewItem) => {
    setActionError('');
    try {
      const reason = lifecycleReason(advisorGuidance(item, 'harvest')) || REVIEW_HARVEST_REASON;
      await onMoveSeed(
        item.seed_id,
        'harvest',
        reason,
        undefined,
        undefined,
        reviewContext(review, item),
      );
      await refresh();
    } catch (error) {
      setActionError(error instanceof Error ? error.message : 'Harvest failed');
      await refresh().catch(() => {});
    }
  }, [onMoveSeed, refresh, review]);

  const submitLifecycle = useCallback(async (item: GardenReviewItem, state: ComposerState) => {
    const text = state.text.trim();
    setComposer({ ...state, busy: true, error: '' });
    try {
      await onMoveSeed(
        item.seed_id,
        state.kind,
        state.kind === 'park' ? undefined : text || undefined,
        undefined,
        state.kind === 'park' ? text || undefined : undefined,
        reviewContext(review, item),
      );
      setComposer(null);
      await refresh();
    } catch (error) {
      setComposer((current) => current && current.itemId === item.id ? {
        ...current,
        busy: false,
        error: error instanceof Error ? error.message : `${actionLabels[state.kind]} failed`,
      } : current);
      await refresh().catch(() => {});
    }
  }, [onMoveSeed, refresh, review]);

  const submitHandover = useCallback(async (item: GardenReviewItem, state: ComposerState) => {
    setComposer({ ...state, busy: true, error: '' });
    try {
      const document = await fetchSeedDocument(item.seed_id);
      const continuation = document.seed.continuation;
      if (continuation?.handover_placement === 'placement_required') {
        setComposer({ ...state, busy: false, error: 'Send this seed to Chief to choose a different working context.' });
        return;
      }
      await onHandoverSeed({
        seedId: item.seed_id,
        expectedRev: document.seed.rev,
        expectedTenderSession: document.seed.tender_session || '',
        expectedTenderMember: document.seed.tender_member || '',
        handoff: state.text,
        review: reviewContext(review, item),
      });
      setComposer(null);
      await refresh();
    } catch (error) {
      setComposer((current) => current && current.itemId === item.id ? {
        ...current,
        busy: false,
        error: error instanceof Error ? error.message : 'Handover failed',
      } : current);
      await refresh().catch(() => {});
    }
  }, [fetchSeedDocument, onHandoverSeed, refresh, review]);

  const submitChief = useCallback(async (item: GardenReviewItem, state: ComposerState) => {
    if (!onSendSeedToChief) return;
    setComposer({ ...state, busy: true, error: '' });
    try {
      const document = await fetchSeedDocument(item.seed_id);
      await onSendSeedToChief({
        seedId: item.seed_id,
        expectedRev: document.seed.rev,
        expectedTenderSession: document.seed.tender_session || '',
        expectedTenderMember: document.seed.tender_member || '',
        guidance: state.text,
        review: reviewContext(review, item),
      });
      setComposer(null);
      await refresh();
    } catch (error) {
      setComposer((current) => current && current.itemId === item.id ? {
        ...current,
        busy: false,
        error: error instanceof Error ? error.message : 'Sending the seed to Chief failed',
      } : current);
      await refresh().catch(() => {});
    }
  }, [fetchSeedDocument, onSendSeedToChief, refresh, review]);

  const requestDraft = useCallback(async (item: GardenReviewItem) => {
    const base = composer?.text ?? '';
    setComposer((current) => current ? { ...current, drafting: true, error: '', pendingDraft: '' } : current);
    try {
      const draft = await onDraft(item.seed_id, reviewContext(review, item));
      setComposer((current) => {
        if (!current || current.itemId !== item.id || current.kind !== 'handover') return current;
        if (current.text === base) return { ...current, text: draft, drafting: false };
        return { ...current, drafting: false, pendingDraft: draft };
      });
    } catch (error) {
      setComposer((current) => current && current.itemId === item.id ? {
        ...current,
        drafting: false,
        error: error instanceof Error ? error.message : 'Drafting the handoff failed',
      } : current);
    }
  }, [composer?.text, onDraft, review]);

  const retry = useCallback(async (item: GardenReviewItem) => {
    setRetrying(true);
    setActionError('');
    try {
      await onRetry(review.run.id, item.seed_id);
    } catch (error) {
      setActionError(error instanceof Error ? error.message : 'Retry failed');
    } finally {
      setRetrying(false);
    }
  }, [onRetry, review.run.id]);

  const ordinaryEvidence = selected?.evidence.filter((entry) =>
    !entry.label.startsWith('Related seed ') && !entry.label.startsWith('Seed log ')) ?? [];
  const browsedSeed = browsedDocument?.seed ?? seeds.find((seed) => seed.id === browsedSeedId);
  const path = gardenPathToSeed(seeds, browsedSeedId)
    .map((id) => seeds.find((seed) => seed.id === id))
    .filter((seed): seed is Seed => Boolean(seed));
  const openSeed = (seedId: string) => {
    setBrowsedSeedId(seedId);
    setBrowsedDocument(null);
    setComposer(null);
    setActionError('');
  };
  const returnToReviewSeed = () => selected && openSeed(selected.seed_id);
  const actionable = Boolean(selected && selected.resolution === 'unresolved'
    && selected.status !== 'invalidated');
  const advised = review.items.filter((item) => item.status === 'ready'
    && item.resolution === 'unresolved').length;

  return (
    <div className={`garden-review is-${frame}`} data-testid="garden-review">
      <header className="garden-review__chrome">
        <button type="button" className="garden-review__back" onClick={onExit}>‹ Garden</button>
        <div className="garden-review__title">
          <strong>Review garden</strong>
          <span>{complete ? 'complete' : `${unresolved.length} left · ${advised}/${review.items.length} advised`}</span>
        </div>
        {onToggleFrame && (
          <button
            type="button"
            className="garden-review__frame"
            onClick={onToggleFrame}
            aria-label={frame === 'full' ? 'Return the garden to the dock' : 'Expand the garden'}
          >
            <FrameGlyph direction={frame === 'full' ? 'in' : 'out'} />
          </button>
        )}
        <button type="button" className="garden-review__close" onClick={onClose} aria-label="Close">×</button>
      </header>

      <nav className="garden-review__rail" aria-label="Seeds in this review">
        {review.items.map((item, index) => (
          <button
            type="button"
            key={item.id}
            className={`${item.id === selected?.id ? 'is-current' : ''}${item.resolution !== 'unresolved' ? ' is-resolved' : ''}`}
            onClick={() => setSelectedId(item.id)}
            aria-current={item.id === selected?.id ? 'step' : undefined}
          >
            <span className="garden-review__rail-number">{index + 1}</span>
            <span className="garden-review__rail-copy">
              <strong>{item.title}</strong>
              <span>{statusWords(item)}</span>
            </span>
          </button>
        ))}
      </nav>

      {complete ? (
        <main className="garden-review__complete">
          <span aria-hidden="true">✓</span>
          <h2>Garden review complete</h2>
          <p>Every captured seed has been dealt with. New candidates will appear in the next review.</p>
          <button type="button" onClick={onExit}>Back to the garden</button>
        </main>
      ) : selected ? (
        <main className="garden-review__reader">
          <article className="garden-review__seed">
            <nav className="garden-review__seed-nav" aria-label="Plot path">
              {!atReviewSeed && (
                <button type="button" className="garden-review__return" onClick={returnToReviewSeed}>
                  ‹ Review seed
                </button>
              )}
              {path.length > 0 && (
                <ol>
                  {path.map((seed, index) => (
                    <li key={seed.id}>
                      {index < path.length - 1 ? (
                        <button type="button" onClick={() => openSeed(seed.id)}>{seed.title}</button>
                      ) : <span aria-current="page">{seed.title}</span>}
                    </li>
                  ))}
                </ol>
              )}
            </nav>
            <div className="garden-review__seed-head">
              <div>
                <span className="garden-review__eyebrow">{browsedSeed?.id ?? browsedSeedId}</span>
                <h2>{browsedSeed?.title ?? selected.title}</h2>
              </div>
              <span className={`garden-review__status is-${atReviewSeed ? selected.status : browsedSeed?.status ?? 'queued'}`}>
                {atReviewSeed ? statusWords(selected) : browsedSeed?.status}
              </span>
            </div>

            {seedLoading && !browsedDocument && <p className="garden-review__empty">Reading the seed and its log…</p>}
            {seedReadError && <p className="garden-review__error" role="alert">{seedReadError}</p>}
            {browsedDocument && (
              <SeedDocumentView
                key={browsedDocument.seed.id}
                document={browsedDocument}
                compact
                ledgerInitiallyOpen
                onOpenSeed={openSeed}
              />
            )}
          </article>

          <aside className="garden-review__decision">
            {!atReviewSeed ? (
              <section className="garden-review__context">
                <span>Plot context</span>
                <h3>You are looking around the plot</h3>
                <p>The review decision still belongs to <strong>{selected.title}</strong>.</p>
                <button type="button" onClick={returnToReviewSeed}>Return to review seed</button>
              </section>
            ) : selected.status === 'ready' && selected.resolution === 'unresolved' ? (
              <>
                {selected.recommendation && (
                  <section className="garden-review__advice">
                    <span>Advisor suggests</span>
                    <strong>{actionLabels[selected.recommendation] ?? selected.recommendation}</strong>
                    {selected.explanation && <p>{selected.explanation}</p>}
                    {selected.cited_evidence && selected.cited_evidence.length > 0 && (
                      <ul>
                        {selected.cited_evidence.map((entry) => <li key={entry}>{entry}</li>)}
                      </ul>
                    )}
                  </section>
                )}
              </>
            ) : selected.status === 'failed' || selected.status === 'invalidated' ? (
              <section className="garden-review__problem">
                <h3>{selected.status === 'failed' ? 'The advisor could not review this seed' : 'This seed changed'}</h3>
                <p>{selected.status === 'failed'
                  ? selected.advisor_error || advisorFailureWords(selected.error)
                  : selected.error || 'Refresh the evidence and try again.'}</p>
                {!retrying && <button type="button" onClick={() => void retry(selected)}>Try again</button>}
                {retrying && <span aria-live="polite">Refreshing…</span>}
              </section>
            ) : (
              <AdvisorProgress review={review} item={selected} />
            )}
            {atReviewSeed && (
              <section className="garden-review__evidence">
                <h3>What we know</h3>
                <dl>
                  {ordinaryEvidence.map((entry) => (
                    <div key={entry.label}>
                      <dt>{entry.label}</dt>
                      <dd>{entry.text}</dd>
                    </div>
                  ))}
                </dl>
              </section>
            )}
            {actionError && <p className="garden-review__error" role="alert">{actionError}</p>}
            {atReviewSeed && actionable && (
              <section className="garden-review__actions">
                <h3>What should happen?</h3>
                {composer?.itemId === selected.id ? (
                  <form
                    className="garden-review__composer"
                    onSubmit={(event) => {
                      event.preventDefault();
                      if (composer.busy) return;
                      if (composer.kind === 'handover') void submitHandover(selected, composer);
                      else if (composer.kind === 'chief') void submitChief(selected, composer);
                      else void submitLifecycle(selected, composer);
                    }}
                  >
                    <div className="garden-review__composer-head">
                      <label htmlFor={`garden-review-compose-${selected.id}`}>
                        {composer.kind === 'handover'
                          ? 'What should the new agent know?'
                          : composer.kind === 'chief'
                            ? 'What should Chief know?'
                          : composer.kind === 'park'
                            ? 'Why are you parking this?'
                            : 'Why should this be withered?'}
                        <span>optional</span>
                      </label>
                      {composer.kind === 'handover' && !composer.busy && !composer.drafting && (
                        <button type="button" className="garden-review__draft" onClick={() => void requestDraft(selected)}>
                          Draft
                        </button>
                      )}
                      {composer.drafting && <span aria-live="polite">Drafting…</span>}
                    </div>
                    <textarea
                      id={`garden-review-compose-${selected.id}`}
                      autoFocus
                      value={composer.text}
                      onChange={(event) => setComposer({ ...composer, text: event.target.value, pendingDraft: '' })}
                      onKeyDown={(event) => {
                        if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
                          event.preventDefault();
                          event.currentTarget.form?.requestSubmit();
                        }
                      }}
                    />
                    {composer.pendingDraft && (
                      <p className="garden-review__draft-ready">
                        Draft ready. Your edits were kept.
                        <button type="button" onClick={() => setComposer({ ...composer, text: composer.pendingDraft, pendingDraft: '' })}>
                          Use draft
                        </button>
                      </p>
                    )}
                    {composer.error && <p className="garden-review__error" role="alert">{composer.error}</p>}
                    <div className="garden-review__composer-actions">
                      {composer.busy ? (
                        <span aria-live="polite">{composer.kind === 'handover'
                          ? 'Handing over…'
                          : composer.kind === 'chief'
                            ? 'Sending to Chief…'
                            : `${actionLabels[composer.kind]}ing…`}</span>
                      ) : (
                        <>
                          <button type="button" onClick={() => setComposer(null)}>Cancel</button>
                          <button type="submit">{actionLabels[composer.kind]}</button>
                        </>
                      )}
                    </div>
                  </form>
                ) : (
                  <div className="garden-review__action-row" aria-label="Choose what happens to this seed">
                    {visibleActions.map((action) => (
                      <button
                        type="button"
                        key={action}
                        className={action === selected.recommendation ? 'is-recommended' : ''}
                        onClick={() => {
                          if (action === 'resume') void runResume(selected);
                          else if (action === 'keep_growing') void runKeep(selected);
                          else if (action === 'harvest') void runHarvest(selected);
                          else if (action === 'send_to_chief' && onSendSeedToChief) {
                            setComposer(emptyComposer(selected, 'chief'));
                          }
                          else if (action === 'handover' || action === 'park' || action === 'wither') {
                            setComposer(emptyComposer(selected, action));
                          }
                        }}
                      >
                        {actionLabels[action] ?? action}
                      </button>
                    ))}
                  </div>
                )}
              </section>
            )}
          </aside>
        </main>
      ) : null}

    </div>
  );
}
