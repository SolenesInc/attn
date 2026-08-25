import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AutoModeSettings } from './AutoModeSettings';
import { getAutoModeAutomationHandle } from './autoModeAutomation';
import type {
  AutoModeConfigInfo,
  AutoModeEnvironmentSlot,
  AutoModePatternEdit,
  AutoModePromotion,
  AutoModeState,
} from '../hooks/daemonAutoModeEvents';
import { useAutoModePolicy } from '../hooks/useAutoModePolicy';

const config = (over: Partial<AutoModeConfigInfo> = {}): AutoModeConfigInfo => ({
  enabled_default: true,
  environment: { slots: [{ id: 'domains', values: ['grafana.acme.corp'] }], notes: [] },
  allow: ['git status*'],
  hard_deny: ['*attn automode env*'],
  shipped_hard_deny: ['*attn automode env*'],
  models: ['opencode-go/glm-5.3'],
  ...over,
});

const SLOTS: AutoModeEnvironmentSlot[] = [
  {
    id: 'domains',
    label: 'Trusted internal domains',
    kind: 'list',
    choices: [],
    detail: 'Hosts the agent may send data to.',
    unset: 'None configured',
    detected: false,
    read_by: ['Data Exfiltration'],
  },
  {
    id: 'repo_visibility',
    label: 'Repository visibility',
    kind: 'choice',
    choices: ['private', 'public'],
    detail: 'Whether that repository is private or public.',
    unset: 'assume private unless the transcript shows otherwise',
    detected: true,
    read_by: ['Data Exfiltration'],
  },
];

const state = (over: Partial<AutoModeState> = {}): AutoModeState => ({
  config: config(),
  proposals: [],
  denials: [],
  environmentSlots: SLOTS,
  ...over,
});

const edited = (over: Partial<AutoModeConfigInfo> = {}): AutoModePatternEdit => ({
  config: config(over),
});

function Harness({
  setEnvironmentSlot,
}: {
  setEnvironmentSlot: (slot: string, values: string[]) => Promise<AutoModePatternEdit>;
}) {
  const policy = useAutoModePolicy({
    enabled: true,
    getState: vi.fn().mockResolvedValue(state()),
    promoteProposal: vi.fn().mockResolvedValue({} as AutoModePromotion),
    discardProposal: vi.fn().mockResolvedValue({} as AutoModePromotion),
    addPattern: vi.fn().mockResolvedValue(edited()),
    removePattern: vi.fn().mockResolvedValue(edited()),
    setEnvironmentSlot,
  });
  return <AutoModeSettings policy={policy} />;
}

describe('AutoModeSettings automation handle', () => {
  it('reports what each slot holds and how many are filled', async () => {
    render(<Harness setEnvironmentSlot={vi.fn().mockResolvedValue(edited())} />);
    await waitFor(() => screen.getByTestId('automode-config'));

    const reported = getAutoModeAutomationHandle()?.getState();
    expect(reported?.present).toBe(true);
    expect(reported?.models).toEqual(['opencode-go/glm-5.3']);
    expect(reported?.environment.slots).toEqual([{ id: 'domains', values: ['grafana.acme.corp'] }]);
    expect(reported?.environment.filled).toBe(1);
    expect(reported?.environment.total).toBe(SLOTS.length);
  });

  it('unregisters when the pane goes away', async () => {
    const view = render(<Harness setEnvironmentSlot={vi.fn().mockResolvedValue(edited())} />);
    await waitFor(() => screen.getByTestId('automode-config'));
    expect(getAutoModeAutomationHandle()).not.toBeNull();

    view.unmount();
    expect(getAutoModeAutomationHandle()).toBeNull();
  });

  // A slot nobody filled has to say what the rules fall back to; rendering
  // nothing reads as "this does not matter", when it is why a call gets blocked.
  it('shows an unfilled slot as what the rules assume instead of blank', async () => {
    render(<Harness setEnvironmentSlot={vi.fn().mockResolvedValue(edited())} />);
    await waitFor(() => screen.getByTestId('automode-slot-repo_visibility'));

    expect(screen.getByTestId('automode-slot-repo_visibility').textContent).toContain(
      'assume private unless the transcript shows otherwise',
    );
  });

  // A detected slot fills itself from the session's repository. Left looking
  // like every other empty row, it reads as one more thing the user forgot.
  it('says which empty slots a session fills for itself', async () => {
    render(<Harness setEnvironmentSlot={vi.fn().mockResolvedValue(edited())} />);
    await waitFor(() => screen.getByTestId('automode-slot-repo_visibility'));

    expect(screen.getByTestId('automode-slot-detected-repo_visibility').textContent).toContain(
      'detected per session',
    );
    expect(screen.queryByTestId('automode-slot-detected-domains')).toBeNull();
  });

  it('writes one slot when an entry is typed', async () => {
    const setEnvironmentSlot = vi.fn().mockResolvedValue(edited());
    render(<Harness setEnvironmentSlot={setEnvironmentSlot} />);
    await waitFor(() => screen.getByTestId('automode-slot-edit-domains'));

    fireEvent.click(screen.getByTestId('automode-slot-edit-domains'));
    const input = await screen.findByTestId('automode-slot-input-domains');
    fireEvent.change(input, { target: { value: 'docs.acme.corp' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(setEnvironmentSlot).toHaveBeenCalledWith('domains', [
        'grafana.acme.corp',
        'docs.acme.corp',
      ]);
    });
  });

  it('writes the whole slot when an entry is removed', async () => {
    const setEnvironmentSlot = vi.fn().mockResolvedValue(edited());
    render(<Harness setEnvironmentSlot={setEnvironmentSlot} />);
    await waitFor(() => screen.getByTestId('automode-slot-domains'));

    fireEvent.click(screen.getByLabelText('Remove grafana.acme.corp from Trusted internal domains'));
    await waitFor(() => expect(setEnvironmentSlot).toHaveBeenCalledWith('domains', []));
  });

  it('offers a choice slot its choices rather than free text', async () => {
    const setEnvironmentSlot = vi.fn().mockResolvedValue(edited());
    render(<Harness setEnvironmentSlot={setEnvironmentSlot} />);
    await waitFor(() => screen.getByTestId('automode-slot-edit-repo_visibility'));

    fireEvent.click(screen.getByTestId('automode-slot-edit-repo_visibility'));
    fireEvent.click(await screen.findByTestId('automode-slot-choice-repo_visibility-public'));

    await waitFor(() => {
      expect(setEnvironmentSlot).toHaveBeenCalledWith('repo_visibility', ['public']);
    });
  });
});
