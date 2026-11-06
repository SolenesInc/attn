import { useEffect, useState } from 'react';
import { GardenPanel } from '../../src/components/GardenPanel';
import type { SeedDocument } from '../../src/types/generated';
import type { Seed } from '../../src/hooks/useDaemonSocket';
import type { HarnessProps } from '../types';
import '../../src/App.css';

const now = new Date().toISOString();
const seed: Seed = {
  id: 's-outpost', title: 'Finish the outpost move', body: 'Move the work to the outpost.',
  status: 'parked', state_changed_at: now, state_changed_at_exact: true, step_slug: 'outpost-move', profile_id: 'profile-default',
  planter: { ref: 'user', name: 'the user' }, claimed: false, edges: [], ready: false, template: false, gate: false, vars: [], rev: 1, created_at: now, updated_at: now,
};

function unreachableOn(outpost: string): SeedDocument {
  return {
    seed: {
      ...seed,
      continuation: {
        agent: 'claude', cwd: '/work', repository_root: '/work', branch: 'main', execution_id: 'execution-1', session_live: false, source: 'tender',
        outpost, directory_state: 'unreachable', resume_available: false, handover_placement: 'placement_required',
        resume_reason: `outpost "${outpost}" is unreachable; Resume returns when it is back`,
      },
    },
    children: [], artifacts: [], references: [], notes: [], notes_total: 0,
  };
}

export function GardenSeedResumeHarness({ onReady, setTriggerRerender }: HarnessProps) {
  const params = new URLSearchParams(window.location.search);
  const frame = params.get('frame') === 'dock' ? 'dock' : 'full';
  const outpost = params.get('outpost') ?? 'studio';
  const [resumes, setResumes] = useState(0);
  useEffect(() => { onReady(); setTriggerRerender(() => () => {}); }, [onReady, setTriggerRerender]);
  const place = frame === 'dock'
    ? { top: 0, bottom: 0, right: 0, width: 'min(420px, 42vw)' }
    : { top: 12, bottom: 12, left: 12, right: 12 };
  return (
    <>
      <div style={{ position: 'fixed', ...place }}>
        <GardenPanel
          isOpen
          frame={frame}
          onClose={() => {}}
          seeds={[seed]}
          seedsTotal={1}
          fetchSeedDocument={async () => unreachableOn(outpost)}
          onResumeSeed={async () => setResumes((count) => count + 1)}
        />
      </div>
      <output data-testid="resumes" style={{ position: 'fixed', left: -9999 }}>{resumes}</output>
    </>
  );
}
