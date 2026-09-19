import { useMemo } from 'react';
import type { CrewHandoffHistory, CrewHandoffLetter, CrewHandoffLoad } from '../hooks/useCrewHandoffs';
import type { CrewMember } from '../types/generated';
import { crewDisplayName } from '../utils/crewName';
import { MarkdownReader } from './MarkdownReader';
import { seedMarkdownSource } from './MarkdownReader/documentSource';

const letterDateFormat = new Intl.DateTimeFormat(undefined, {
  year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', timeZone: 'UTC', timeZoneName: 'short',
});
const indexDayFormat = new Intl.DateTimeFormat(undefined, { year: 'numeric', month: 'short', day: 'numeric', timeZone: 'UTC' });
const indexTimeFormat = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', timeZone: 'UTC' });

function letterDate(value: Date): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return letterDateFormat.format(date);
}

function HandoffLetterReader({ member, letter, onRetry, onOpenSeed }: {
  member: CrewMember;
  letter?: CrewHandoffLetter;
  onRetry: () => void;
  onOpenSeed: (seedId: string) => void;
}) {
  const source = useMemo(() => seedMarkdownSource(`crew-handoff-${member.id}-${letter?.filename ?? ''}`), [letter?.filename, member.id]);
  if (!letter || letter.state === 'loading') return <div className="crew-document-state">Loading letter…</div>;
  if (letter.state === 'error' || !letter.document) {
    return (
      <div className="crew-document-state is-error" role="alert">
        <span>{letter.error ?? 'The letter could not be read.'}</span>
        <button type="button" data-testid="crew-handoff-letter-retry" onClick={onRetry}>Retry</button>
      </div>
    );
  }
  return <MarkdownReader content={letter.document.content} source={source} allowLocalTargets={false} onOpenSeed={onOpenSeed} />;
}

function HandoffHistory({ member, load, history, onOpenSeed }: {
  member: CrewMember;
  load: CrewHandoffLoad;
  history: CrewHandoffHistory;
  onOpenSeed: (seedId: string) => void;
}) {
  const handoff = load.handoffs.find((candidate) => candidate.filename === load.selected) ?? load.handoffs[0];
  return (
    <div className="crew-handoff-layout">
      <nav className="crew-handoff-index" aria-label={`${crewDisplayName(member.id)} handoffs`}>
        <span>{load.handoffs.length} {load.handoffs.length === 1 ? 'letter' : 'letters'}</span>
        {load.handoffs.map((candidate, index) => (
          <button
            key={candidate.filename}
            type="button"
            data-testid={`crew-handoff-${index}`}
            aria-current={candidate.filename === handoff.filename ? 'page' : undefined}
            onClick={() => history.select(member.id, candidate.filename)}
          >
            <strong>{indexDayFormat.format(new Date(candidate.occurred_at))}</strong>
            <small>{index === 0 ? 'Latest · ' : ''}{indexTimeFormat.format(new Date(candidate.occurred_at))} UTC</small>
          </button>
        ))}
      </nav>
      <article className="crew-handoff-reader" data-testid="crew-handoff-reader">
        <div className="crew-handoff-date"><span>{letterDate(handoff.occurred_at)}</span><code>{handoff.filename}</code></div>
        <HandoffLetterReader
          member={member}
          letter={load.letter?.filename === handoff.filename ? load.letter : undefined}
          onRetry={() => history.loadLetter(member.id, handoff.filename)}
          onOpenSeed={onOpenSeed}
        />
      </article>
    </div>
  );
}

export function CrewHandoffsTab({ member, history, onOpenSeed }: {
  member: CrewMember;
  history: CrewHandoffHistory;
  onOpenSeed: (seedId: string) => void;
}) {
  const load = history.read(member.id);
  if (!load || load.state === 'loading') return <div className="crew-document-state">Loading handoffs…</div>;
  if (load.state === 'error') {
    return (
      <div className="crew-document-state is-error" role="alert">
        <span>{load.error}</span>
        <button type="button" data-testid="crew-handoffs-retry" onClick={() => history.load(member.id, true)}>Retry</button>
      </div>
    );
  }
  return (
    <section className="crew-handoffs">
      <div className="crew-document-heading">
        <h3>Handoffs</h3>
        <div className="crew-handoff-actions">
          {load.handoffs.length > 0 && <span>Read only</span>}
          <button type="button" data-testid="crew-handoffs-refresh" onClick={() => history.load(member.id, true)}>Refresh</button>
        </div>
      </div>
      {load.handoffs.length === 0
        ? <div className="crew-document-empty">No handoffs recorded.</div>
        : <HandoffHistory member={member} load={load} history={history} onOpenSeed={onOpenSeed} />}
    </section>
  );
}
