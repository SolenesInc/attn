import { useEffect, useState } from 'react';
import { useOptionalDaemonApi } from '../contexts/DaemonApiContext';
import type { Seed, SeedDocument } from '../hooks/useDaemonSocket';
import { terminalSeedBodyExcerpt } from '../utils/terminalSeedPreviewText';
import { SeedStateIcon } from './SeedStateIcon';
import { seedStateLabel, seedStateMeaning } from './seedStatePresentation';

function age(iso: string): string {
  const time = Date.parse(iso);
  if (!Number.isFinite(time)) return '';
  const minutes = Math.max(0, Math.floor((Date.now() - time) / 60_000));
  if (minutes < 1) return 'just now';
  if (minutes < 60) return `${minutes}m ago`;
  if (minutes < 1440) return `${Math.floor(minutes / 60)}h ago`;
  return `${Math.floor(minutes / 1440)}d ago`;
}

function useSeedContextDocument(seedId: string, seed?: Seed) {
  const fetchDocument = useOptionalDaemonApi()?.sendSeedDocumentGet;
  const [document, setDocument] = useState<SeedDocument | null>(null);
  const [failed, setFailed] = useState(false);
  const revision = seed?.rev;

  useEffect(() => {
    if (!fetchDocument) return;
    let cancelled = false;
    setFailed(false);
    void fetchDocument(seedId).then((result) => {
      if (cancelled) return;
      if (result.seed.id !== seedId || result.seed.rev < (revision ?? 0)) {
        setFailed(true);
        return;
      }
      setDocument(result);
    }, () => {
      if (!cancelled) setFailed(true);
    });
    return () => { cancelled = true; };
  // Notes arrive through Garden snapshots without advancing the seed revision.
  }, [fetchDocument, seedId, revision, seed]);

  return { document, unavailable: failed || !fetchDocument };
}

function latestSeedNote(document: SeedDocument | null) {
  let latest: SeedDocument['notes'][number] | undefined;
  for (const note of document?.notes ?? []) {
    if (note.kind === 'attach' || note.kind === 'detach' || !note.body.trim()) continue;
    if (!latest || Date.parse(note.created_at) > Date.parse(latest.created_at)) latest = note;
  }
  return latest;
}

function SeedContextState({ seed }: { seed?: Seed }) {
  const timestamp = seed?.state_changed_at;
  const elapsed = timestamp ? age(timestamp) : '';
  return (
    <div className="pane-seed-context-state">
      <SeedStateIcon status={seed?.status} size={44} />
      <div>
        <strong>{seedStateLabel(seed?.status)}</strong>
        <span>{seedStateMeaning(seed?.status)}</span>
      </div>
      {elapsed ? (
        <time dateTime={timestamp} title={timestamp ? new Date(timestamp).toLocaleString() : undefined}>
          {seed?.state_changed_at_exact ? '' : '~'}{elapsed}
        </time>
      ) : null}
    </div>
  );
}

function SeedContextOutcome({ seed, hasNote }: { seed?: Seed; hasNote: boolean }) {
  const reason = seed?.reason?.trim();
  const summary = seed ? terminalSeedBodyExcerpt(seed.body, 240) : '';
  if (!reason && (hasNote || !summary)) return null;
  const caption = reason ? (seed?.status === 'harvested' ? 'Outcome' : 'Reason') : 'About this seed';
  return (
    <section className="pane-seed-context-outcome">
      <div className="pane-seed-context-caption">{caption}</div>
      <p>{reason ? terminalSeedBodyExcerpt(reason, 360) : summary}</p>
    </section>
  );
}

export function PaneSeedContext({ seed, seedId, reportsHere }: {
  seed?: Seed;
  seedId: string;
  reportsHere: boolean;
}) {
  const { document, unavailable } = useSeedContextDocument(seedId, seed);
  const current = document?.seed ?? seed;
  const latest = latestSeedNote(document);
  const excerpt = latest ? terminalSeedBodyExcerpt(latest.body, 360) : '';

  return (
    <div className="pane-seed-context" data-seed-state={current?.status ?? 'unknown'}>
      <SeedContextState seed={current} />
      <section className="pane-seed-context-note" aria-label="Latest note">
        <div className="pane-seed-context-caption">
          <span>{latest?.kind === 'handoff' ? 'Latest handoff' : 'Latest note'}</span>
          {latest ? <time dateTime={latest.created_at}>{age(latest.created_at)}</time> : null}
        </div>
        <p>{excerpt || (unavailable ? 'Latest note unavailable.' : !document ? 'Loading latest note…' : 'No notes yet.')}</p>
      </section>
      <SeedContextOutcome seed={current} hasNote={Boolean(latest)} />
      {reportsHere ? <p className="pane-seed-context-relation">This agent reports to this seed.</p> : null}
    </div>
  );
}
