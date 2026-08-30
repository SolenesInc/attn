import { useEffect, useId, type Ref } from 'react';
import type {
  Seed,
  SeedDocument as GeneratedSeedDocument,
} from '../types/generated';
import { Markdown } from './Markdown';
import { MarkdownReader, type MarkdownAnnotationsSendHandle } from './MarkdownReader';
import { seedMarkdownSource } from './MarkdownReader/documentSource';
import { SeedArtifactRows } from './SeedArtifactRows';
import './SeedDocumentView.css';

/** The one read model shared by the panel drill and the docked seed tile. */
export type SeedDocument = GeneratedSeedDocument;

export interface SeedDocumentViewProps {
  document: SeedDocument;
  compact?: boolean;
  annotationsEnabled?: boolean;
  onAnnotationsCountChange?: (count: number) => void;
  annotationsSendRef?: Ref<MarkdownAnnotationsSendHandle | null>;
  onOpenMarkdownArtifact?: (path: string) => void;
  onOpenSeed?: (seedId: string) => void;
  arrival?: 'in' | 'out';
}

function formatTimestamp(iso: string): string {
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString();
}

function holder(seed: Seed): string {
  return seed.tender_member || seed.tender_session || '';
}

function progressWords(seed: Seed): string {
  const progress = seed.plot_progress;
  if (!progress) return '';
  const parts = [`${progress.done}/${progress.total} done`];
  if (progress.growing) parts.push(`${progress.growing} growing`);
  if (progress.ready) parts.push(`${progress.ready} ready`);
  if (progress.blocked) parts.push(`${progress.blocked} blocked`);
  if (progress.dormant) parts.push(`${progress.dormant} parked`);
  return parts.join(' · ');
}

function childSignal(seed: Seed): string {
  if (seed.status === 'growing') return 'growing';
  if (seed.status === 'dormant') return 'parked';
  if (seed.status === 'harvested') return 'done';
  if (seed.status === 'withered') return 'withered';
  return '';
}

export function SeedDocumentView({
  document,
  compact = false,
  annotationsEnabled = false,
  onAnnotationsCountChange,
  annotationsSendRef,
  onOpenMarkdownArtifact,
  onOpenSeed,
  arrival = 'in',
}: SeedDocumentViewProps) {
  const { seed, children, notes, notes_total: notesTotal, artifacts, references } = document;
  const withheld = Math.max(0, notesTotal - notes.length);
  const isPlot = Boolean(seed.plot_progress);
  const tender = holder(seed);
  const progress = progressWords(seed);
  const plotHeadingId = useId();
  const artifactsHeadingId = useId();
  const ledgerHeadingId = useId();

  useEffect(() => {
    if (!seed.body.trim()) onAnnotationsCountChange?.(0);
  }, [onAnnotationsCountChange, seed.body]);

  return (
    <div
      className={`seed-document${compact ? ' seed-document--compact' : ''}`}
      // Which seed this is showing: a workspace can hold several seed tiles, so
      // reading one means naming it.
      data-seed-id={seed.id}
      data-arrival={arrival}
    >
      <div className="seed-document__meta" aria-label="Seed details">
        <span className={`seed-document__state is-${seed.status}`}>
          <span aria-hidden="true" />
          {seed.status}
        </span>
        {tender && <span>tended by {tender}</span>}
        {progress && <span>{progress}</span>}
        <span className="seed-document__id">{seed.id}</span>
      </div>

      {isPlot && (
        <section className="seed-document__plot" aria-labelledby={plotHeadingId}>
          <div className="seed-document__section-head">
            <h3 id={plotHeadingId}>Plot</h3>
            <span>{children.length} {children.length === 1 ? 'seed' : 'seeds'}</span>
          </div>
          {children.length > 0 ? (
            <ul className="seed-document__children" aria-label="Seeds in this plot">
              {children.map((child) => {
                const signal = childSignal(child);
                return (
                  <li key={child.id} className={`is-${child.status}`}>
                    <button
                      type="button"
                      data-seed-target={child.id}
                      onClick={() => onOpenSeed?.(child.id)}
                      disabled={!onOpenSeed}
                    >
                      <span className="seed-document__child-state" aria-label={child.status} />
                      <span className="seed-document__child-title">{child.title}</span>
                      {signal && <span className="seed-document__child-status">{signal}</span>}
                      <span className="seed-document__child-id">{child.id}</span>
                      {onOpenSeed && <span className="seed-document__child-chevron" aria-hidden="true">›</span>}
                    </button>
                  </li>
                );
              })}
            </ul>
          ) : (
            <p className="seed-document__empty-plot">Nothing planted in this plot yet.</p>
          )}
        </section>
      )}

      {seed.reason && <p className="seed-document__reason">{seed.reason}</p>}

      {seed.body.trim() ? (
        <MarkdownReader
          content={seed.body}
          source={seedMarkdownSource(seed.id)}
          allowLocalTargets={false}
          annotationsEnabled={annotationsEnabled}
          onAnnotationsCountChange={onAnnotationsCountChange}
          annotationsSendRef={annotationsSendRef}
          seedArtifacts={artifacts}
        />
      ) : (
        <p className="seed-document__empty-body">No body — the title is the whole seed.</p>
      )}

      {artifacts.length + references.length > 0 && (
        <section className="seed-document__artifacts" aria-labelledby={artifactsHeadingId}>
          <div className="seed-document__section-head">
            <h3 id={artifactsHeadingId}>Artifacts</h3>
            <span>{artifacts.length + references.length}</span>
          </div>
          <SeedArtifactRows
            seedId={seed.id}
            artifacts={artifacts}
            references={references}
            onOpenMarkdownArtifact={onOpenMarkdownArtifact}
          />
        </section>
      )}

      <details key={seed.id} className="seed-document__ledger">
        <summary id={ledgerHeadingId}>
          <span>Log</span>
          <span>{notesTotal}</span>
        </summary>
        <div className="seed-document__ledger-body" aria-labelledby={ledgerHeadingId}>
          {notes.length > 0 ? (
            <ol className="seed-document__notes">
              {notes.map((note) => (
                <li key={note.id} data-kind={note.kind} className={note.kind === 'handoff' ? 'is-handoff' : ''}>
                  <div className="seed-document__note-head">
                    <span>{note.author_member || note.author_session || '—'}</span>
                    {note.kind !== 'note' && <span>{note.kind}</span>}
                    <time dateTime={note.created_at}>{formatTimestamp(note.created_at)}</time>
                  </div>
                  {note.body && <Markdown className="seed-document__note-body" breaks>{note.body}</Markdown>}
                </li>
              ))}
            </ol>
          ) : (
            <p className="seed-document__empty-ledger">Nothing on this seed’s log yet.</p>
          )}

          {withheld > 0 && (
            <p className="seed-document__withheld">{withheld} more {withheld === 1 ? 'entry' : 'entries'} on the log.</p>
          )}
        </div>
      </details>
    </div>
  );
}
