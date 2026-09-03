import { useEffect, useState } from 'react';
import { useOptionalDaemonApi } from '../contexts/DaemonApiContext';
import type { Seed, SeedDocument } from '../hooks/useDaemonSocket';
import { terminalSeedBodyExcerpt } from '../utils/terminalSeedPreviewText';
import { SeedStateIcon, seedStateLabel, seedStateMeaning } from './SeedStateIcon';

function age(iso: string): string {
  const time = Date.parse(iso);
  if (!Number.isFinite(time)) return '';
  const minutes = Math.max(0, Math.floor((Date.now() - time) / 60_000));
  if (minutes < 1) return 'just now';
  if (minutes < 60) return `${minutes}m ago`;
  if (minutes < 1440) return `${Math.floor(minutes / 60)}h ago`;
  return `${Math.floor(minutes / 1440)}d ago`;
}

export function PaneSeedContext({ seed, seedId, reportsHere }: {
  seed?: Seed;
  seedId: string;
  reportsHere: boolean;
}) {
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

  const current = document?.seed ?? seed;
  const latest = document?.notes
    .filter((note) => note.kind !== 'attach' && note.kind !== 'detach' && note.body.trim())
    .reduce<SeedDocument['notes'][number] | undefined>((last, note) => (
      !last || Date.parse(note.created_at) > Date.parse(last.created_at) ? note : last
    ), undefined);
  const reason = current?.reason?.trim();
  const timestamp = current?.state_changed_at;
  const elapsed = timestamp ? age(timestamp) : '';
  const excerpt = latest ? terminalSeedBodyExcerpt(latest.body, 360) : '';
  const summary = current ? terminalSeedBodyExcerpt(current.body, 240) : '';

  return (
    <div className="pane-seed-context" data-seed-state={current?.status ?? 'unknown'}>
      <div className="pane-seed-context-state">
        <SeedStateIcon status={current?.status} size={44} />
        <div>
          <strong>{seedStateLabel(current?.status)}</strong>
          <span>{seedStateMeaning(current?.status)}</span>
        </div>
        {elapsed ? (
          <time dateTime={timestamp} title={timestamp ? new Date(timestamp).toLocaleString() : undefined}>
            {current?.state_changed_at_exact ? '' : '~'}{elapsed}
          </time>
        ) : null}
      </div>
      <section className="pane-seed-context-note" aria-label="Latest note">
        <div className="pane-seed-context-caption">
          <span>{latest?.kind === 'handoff' ? 'Latest handoff' : 'Latest note'}</span>
          {latest ? <time dateTime={latest.created_at}>{age(latest.created_at)}</time> : null}
        </div>
        <p>{excerpt || (failed || !fetchDocument ? 'Latest note unavailable.' : !document ? 'Loading latest note…' : 'No notes yet.')}</p>
      </section>
      {reason || (!latest && summary) ? (
        <section className="pane-seed-context-outcome">
          <div className="pane-seed-context-caption">
            {reason ? (current?.status === 'harvested' ? 'Outcome' : 'Reason') : 'About this seed'}
          </div>
          <p>{reason ? terminalSeedBodyExcerpt(reason, 360) : summary}</p>
        </section>
      ) : null}
      {reportsHere ? <p className="pane-seed-context-relation">This agent reports to this seed.</p> : null}
    </div>
  );
}
