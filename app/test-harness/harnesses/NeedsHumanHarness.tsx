/** PROTOTYPE (seed s-j3j5bk): needs-human questions in the garden panel.
 *  Fake data, client-only. /test-harness/?component=NeedsHuman */
import { useCallback, useEffect, useMemo, useState } from 'react';
import { GardenBoard } from '../../src/components/GardenBoard';
import { GardenPanel } from '../../src/components/GardenPanel';
import { NeedsYouRail } from '../../src/components/GardenQuestions';
import { asking, type SeedQuestion } from '../../src/components/seedQuestions';
import { SeedDocumentView, type SeedDocument } from '../../src/components/SeedDocumentView';
import type { Seed } from '../../src/hooks/useDaemonSocket';
import { useGardenWalk } from '../../src/store/gardenWalk';
import type { HarnessProps } from '../types';
import '../../src/App.css';
import './NeedsHumanHarness.css';

type Note = SeedDocument['notes'][number];

const now = Date.now();
const ago = (minutes: number) => new Date(now - minutes * 60_000).toISOString();

const blank: Seed = {
  id: '', title: '', body: '', status: 'planted', state_changed_at: ago(4000), state_changed_at_exact: true,
  step_slug: '', planter_session: '', planter_member: 'Trellis', tender_session: '', tender_member: '',
  edges: [], ready: true, template: false, gate: false, vars: [], rev: 1,
  created_at: ago(4000), updated_at: ago(400),
};

const partOf = (plot: string) => [{ kind: 'part-of', to: plot }];

function ask(seed: string, by: string, minutes: number, text: string): SeedQuestion {
  return { id: `q-${seed}`, seed_id: seed, asked_by: by, asked_at: ago(minutes), text, status: 'open' };
}

function garden(): Seed[] {
  const plot: Seed = {
    ...blank,
    id: 's-dssvxq',
    title: 'Finish the garden',
    body: 'The promises made when the garden shipped and never built: search, ripples, priming, needs-human, gates, packets.',
    status: 'planted',
    plot_progress: { done: 1, total: 9, ready: 3, growing: 4, blocked: 1, dormant: 0, withered: 0 },
  };
  const kids: Seed[] = [
    {
      ...blank,
      id: 's-fjw5bf',
      title: 'Decide gates: build the turn-opening gate or delete the field',
      body: 'The `gate` flag has been in the schema since the garden shipped and nothing has ever set it.',
      edges: partOf(plot.id),
      status: 'growing',
      tender_member: 'chief',
      updated_at: ago(2600),
      question: ask(
        's-fjw5bf',
        'chief',
        2620,
        'Nothing has set `gate` since it landed — no verb writes it, no surface reads it. Delete the field and its column, or build the turn-opening gate this quarter?\n\nDeleting costs a migration. Keeping it costs nothing today and reads as a feature to every agent that finds it.',
      ),
    },
    {
      ...blank,
      id: 's-m4cc8y',
      title: 'seed-search: find seeds by keyword from the CLI',
      body: 'A garden you cannot search is a garden you plant twice.',
      edges: partOf(plot.id),
      status: 'growing',
      tender_member: 'sprout',
      updated_at: ago(90),
      question: ask(
        's-m4cc8y',
        'sprout',
        95,
        'Search covers titles and bodies. Should it read the log too? Half the context lives in notes, but then every result needs a snippet from a note and closed seeds will flood the list.',
      ),
    },
    {
      ...blank,
      id: 's-nxz9vg',
      title: 'Wake priming lists the seeds a member holds',
      body: 'A member waking up should know what it is holding before it is told what to do.',
      edges: partOf(plot.id),
      status: 'growing',
      tender_member: 'bramble',
      updated_at: ago(20),
      question: ask(
        's-nxz9vg',
        'bramble',
        22,
        'Priming can name every seed the member holds, or only the ones with a fresh handoff. Ten seeds is a wall of text at wake; three is a lie by omission. Which way?',
      ),
    },
    {
      ...blank,
      id: 's-72wzjh',
      title: 'Decide packets: build sow and cutting or delete the template fields',
      body: 'Packets are templates for plots. The schema fields exist; nobody sets them.',
      edges: partOf(plot.id),
      status: 'planted',
      updated_at: ago(300),
      question: {
        ...ask('s-72wzjh', 'chief', 1400, 'Same call as gates, for `template` and `vars`. Build or delete?'),
        status: 'withdrawn',
        resolution: 'folding this into the gates question, one seed is enough',
        resolved_at: ago(30),
      },
    },
    {
      ...blank,
      id: 's-q8ey0a',
      title: 'harvest-ripples: closing a seed says what it unblocked',
      body: 'Harvest and wither print the seeds they made ready, and ring the tender of each.',
      edges: partOf(plot.id),
      status: 'growing',
      tender_member: 'trellis',
      updated_at: ago(140),
    },
    {
      ...blank,
      id: 's-j3j5bk',
      title: 'Prototype: needs-human questions in the garden panel',
      body: 'What you are looking at.',
      edges: partOf(plot.id),
      status: 'growing',
      tender_session: 'proto-session',
      updated_at: ago(5),
    },
    {
      ...blank,
      id: 's-rm13zn',
      title: 'Board: dropping a seed on Growing offers dispatch an agent',
      body: '',
      edges: partOf(plot.id),
      updated_at: ago(900),
    },
    {
      ...blank,
      id: 's-g9yxwv',
      title: 'Artifact presence comes from the daemon',
      body: 'The app stats files itself today; over SSH it guesses.',
      edges: partOf(plot.id),
      ready: false,
      updated_at: ago(1200),
    },
    {
      ...blank,
      id: 's-2td2c1',
      title: 'needs-human: pending decisions are a visible queue',
      body: 'Make a raised question first-class: ask, answer, dismiss, withdraw.',
      edges: [...partOf(plot.id), { kind: 'blocks', to: 's-nope' }],
      ready: false,
      updated_at: ago(1000),
    },
  ];
  const outside: Seed = {
    ...blank,
    id: 's-z7a6sm',
    title: 'Promises bigger than the garden',
    body: 'Uplink, server, laurels, the day story, the front door.',
    status: 'dormant',
    updated_at: ago(5000),
  };
  return [plot, ...kids, outside];
}

const seedNotes: Record<string, Note[]> = {};
function notesFor(seed: Seed): Note[] {
  if (!seedNotes[seed.id]) {
    seedNotes[seed.id] = seed.tender_member
      ? [{
        id: `n-${seed.id}`, seed_id: seed.id, kind: 'note', created_at: ago(200),
        author_member: seed.tender_member, author_session: '',
        body: 'Picked this up. The shape is clear except for the one call I cannot make alone.',
      }]
      : [];
  }
  return seedNotes[seed.id];
}

type Surface = 'list' | 'board' | 'tile';
type Placement = 'banner' | 'rail';

export function NeedsHumanHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const [seeds, setSeeds] = useState<Seed[]>(garden);
  const [placement, setPlacement] = useState<Placement>('banner');
  const [surface, setSurface] = useState<Surface>('list');
  const [tileSeed, setTileSeed] = useState('s-m4cc8y');
  const setTrail = useGardenWalk((walk) => walk.setTrail);

  useEffect(() => {
    onReady();
    setTriggerRerender(() => () => {});
    setTrail(['s-dssvxq']);
  }, [onReady, setTriggerRerender, setTrail]);

  const settle = useCallback((questionId: string, status: SeedQuestion['status'], resolution?: string) => {
    setSeeds((current) => {
      // The note is written here, not in the mapper: StrictMode runs the
      // updater twice and the log would carry the answer twice.
      const target = current.find((seed) => (seed.question as SeedQuestion | undefined)?.id === questionId);
      const asked = target?.question as SeedQuestion | undefined;
      if (target && asked && status === 'answered' && resolution) {
        const notes = notesFor(target);
        if (!notes.some((note) => note.id === `n-answer-${questionId}`)) {
          notes.unshift({
            id: `n-answer-${questionId}`, seed_id: target.id, kind: 'answer', created_at: new Date().toISOString(),
            author_member: 'victor', author_session: '',
            body: `**${asked.text.split('\n')[0]}**\n\n${resolution}`,
          });
        }
      }
      return current.map((seed) => {
      const question = seed.question as SeedQuestion | undefined;
      if (!question || question.id !== questionId) return seed;
      return {
        ...seed,
        rev: seed.rev + 1,
        updated_at: new Date().toISOString(),
        question: { ...question, status, resolution, resolved_at: new Date().toISOString() },
      };
      });
    });
  }, []);

  const clear = useCallback((questionId: string) => {
    setSeeds((current) => current.map((seed) => {
      const question = seed.question as SeedQuestion | undefined;
      if (!question || question.id !== questionId) return seed;
      const { question: _gone, ...rest } = seed;
      return { ...rest, rev: seed.rev + 1 } as Seed;
    }));
  }, []);

  const onAnswer = useCallback((id: string, text: string) => settle(id, 'answered', text), [settle]);
  const onDismiss = useCallback((id: string) => settle(id, 'dismissed', 'not deciding this now'), [settle]);

  const withdrawLive = useCallback(() => {
    setSeeds((current) => current.map((seed) => {
      const question = seed.question as SeedQuestion | undefined;
      if (seed.id !== 's-nxz9vg' || !question || question.status !== 'open') return seed;
      return {
        ...seed,
        rev: seed.rev + 1,
        question: {
          ...question,
          status: 'withdrawn',
          resolution: 'found the answer in the arc plan',
          resolved_at: new Date().toISOString(),
        },
      };
    }));
  }, []);

  const rows = useMemo(() => asking(seeds, ['open', 'withdrawn']), [seeds]);
  const byID = useMemo(() => new Map(seeds.map((seed) => [seed.id, seed])), [seeds]);

  const fetchSeedDocument = useCallback(async (seedId: string): Promise<SeedDocument> => {
    const seed = byID.get(seedId);
    if (!seed) throw new Error(`no seed ${seedId}`);
    const notes = notesFor(seed);
    return {
      seed,
      children: seeds.filter((child) => (child.edges ?? []).some((edge) => edge.kind === 'part-of' && edge.to === seedId)),
      artifacts: [], references: [], notes, notes_total: notes.length, tender_holds: Boolean(seed.tender_member),
    } as SeedDocument;
  }, [byID, seeds]);

  const [tileDoc, setTileDoc] = useState<SeedDocument | null>(null);
  useEffect(() => {
    if (surface !== 'tile') return;
    let ignore = false;
    void fetchSeedDocument(tileSeed).then((doc) => {
      if (!ignore) setTileDoc(doc);
    });
    return () => { ignore = true; };
  }, [surface, tileSeed, fetchSeedDocument]);

  const rail = placement === 'rail' ? (
    <NeedsYouRail
      rows={rows}
      onAnswer={onAnswer}
      onDismiss={onDismiss}
      onClear={clear}
      onOpenSeed={(id) => setTrail(['s-dssvxq', id])}
    />
  ) : null;

  const live = rows.filter((row) => row.question.status === 'open').length;

  return (
    <div className="proto">
      <div className="proto__controls">
        <span className="proto__label">needs-human · s-j3j5bk</span>
        <Group name="placement" value={placement} on={setPlacement} options={[['banner', 'A · band in the panel'], ['rail', 'B · rail beside it']]} />
        <Group name="surface" value={surface} on={setSurface} options={[['list', 'list'], ['board', 'board'], ['tile', 'tile']]} />
        <span className="proto__spacer" />
        <button type="button" onClick={withdrawLive}>bramble withdraws</button>
        <button type="button" onClick={() => { setSeeds(garden()); for (const key of Object.keys(seedNotes)) delete seedNotes[key]; }}>
          reset
        </button>
        <span className="proto__count">{live} waiting</span>
      </div>

      <div className="proto__stage">
        {surface === 'tile' ? (
          <div className="proto__tile-stage">
            <div className="proto__tile-picker">
              {seeds.filter((seed) => seed.question).map((seed) => (
                <button key={seed.id} type="button" aria-pressed={tileSeed === seed.id} onClick={() => setTileSeed(seed.id)}>
                  {seed.id}
                </button>
              ))}
            </div>
            <div className="proto__tile">
              {tileDoc && (
                <SeedDocumentView
                  document={tileDoc}
                  compact
                  onAnswerQuestion={onAnswer}
                  onDismissQuestion={onDismiss}
                />
              )}
            </div>
          </div>
        ) : surface === 'board' ? (
          <GardenBoard
            seeds={seeds}
            seedsTotal={seeds.length}
            liveSessions={new Set(['proto-session'])}
            loaded
            onTransition={async () => {}}
            onNote={async () => {}}
            onClose={() => {}}
            onEscapeFloor={() => {}}
          />
        ) : (
          <GardenPanel
            isOpen
            seeds={seeds}
            seedsTotal={seeds.length}
            onClose={() => {}}
            frame="full"
            fetchSeedDocument={fetchSeedDocument}
            questionPlacement={placement === 'banner' ? 'banner' : 'none'}
            onAnswerQuestion={onAnswer}
            onDismissQuestion={onDismiss}
            onClearQuestion={clear}
          />
        )}
        {surface !== 'tile' && rail}
      </div>
    </div>
  );
}

function Group<T extends string>({
  name, value, on, options,
}: { name: string; value: T; on: (next: T) => void; options: Array<[T, string]> }) {
  return (
    <div className="proto__group" role="group" aria-label={name}>
      {options.map(([key, label]) => (
        <button key={key} type="button" aria-pressed={value === key} onClick={() => on(key)}>
          {label}
        </button>
      ))}
    </div>
  );
}
