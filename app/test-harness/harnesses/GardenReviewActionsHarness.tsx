import { useEffect, useState } from 'react';
import { GardenReviewPanel } from '../../src/components/GardenReviewPanel';
import type { GardenReview, SeedDocument } from '../../src/types/generated';
import type { Seed } from '../../src/hooks/useDaemonSocket';
import type { HarnessProps } from '../types';
import '../../src/App.css';

const now = new Date().toISOString();
const seed: Seed = {
  id: 's-review', title: 'Finish the outpost move', body: 'Move the work to the outpost.',
  status: 'parked', state_changed_at: now, state_changed_at_exact: true, step_slug: 'outpost-move', profile_id: 'profile-default',
  planter: { ref: 'user', name: 'the user' }, claimed: false, edges: [], ready: false, template: false, gate: false, vars: [], rev: 1, created_at: now, updated_at: now,
};

function reviewOn(outpost: string): GardenReview {
  return {
    run: { id: 'run-1', profile_id: 'profile-default', status: 'running', candidate_ids: [seed.id], captured_at: now, recipe: { agent: 'claude', model: 'sonnet' } },
    items: [{
      id: 'item-1', run_id: 'run-1', seed_id: seed.id, seed_rev: 1, title: seed.title, body: seed.body ?? '',
      status: 'ready', resolution: 'unresolved', evidence: [], evidence_version: 'v1', recommendation: 'resume', outpost,
      actions: ['resume', 'handover', 'send_to_chief', 'keep_growing', 'park', 'harvest', 'wither'],
    }],
  };
}

export function GardenReviewActionsHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const params = new URLSearchParams(window.location.search);
  const frame = params.get('frame') === 'dock' ? 'dock' : 'full';
  const hasChief = params.has('chief');
  const [chosen, setChosen] = useState<string[]>([]);
  useEffect(() => { onReady(); setTriggerRerender(() => () => {}); }, [onReady, setTriggerRerender]);
  const record = (action: string) => async () => { setChosen((current) => [...current, action]); return seed; };
  const place = frame === 'dock'
    ? { top: 0, bottom: 0, right: 0, width: 'min(420px, 42vw)' }
    : { top: 12, bottom: 12, left: 12, right: 12 };
  return (
    <>
      <div style={{ position: 'fixed', ...place }}>
        <GardenReviewPanel
          review={reviewOn(params.get('outpost') ?? 'ai-sandbox')}
          seeds={[seed]}
          frame={frame}
          onExit={() => {}}
          onClose={() => {}}
          fetchSeedDocument={async (): Promise<SeedDocument> => ({ seed, children: [], artifacts: [], references: [], notes: [], notes_total: 0 })}
          onMoveSeed={record('move')}
          onResumeSeed={record('resume')}
          onKeepSeed={record('keep_growing')}
          onHandoverSeed={record('handover')}
          onSendSeedToChief={hasChief ? record('send_to_chief') : undefined}
          onRetry={async () => {}}
          onDraft={async () => ''}
          onRefresh={async () => {}}
        />
      </div>
      <output data-testid="chosen" style={{ position: 'fixed', left: -9999 }}>{chosen.join(',')}</output>
    </>
  );
}
