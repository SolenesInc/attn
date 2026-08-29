import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AutoModeSettings } from './AutoModeSettings';
import type {
  AutoModeConfigInfo,
  AutoModePatternEdit,
  AutoModeProposalInfo,
  AutoModePromotion,
  AutoModeState,
} from '../hooks/daemonAutoModeEvents';
import { useAutoModePolicy } from '../hooks/useAutoModePolicy';

const config = (over: Partial<AutoModeConfigInfo> = {}): AutoModeConfigInfo => ({
  enabled_default: true,
  environment: { slots: [], notes: [] },
  allow: [],
  hard_deny: ['*attn automode env*'],
  shipped_hard_deny: ['*attn automode env*'],
  models: ['opencode-go/glm-5.3', 'opencode-go/qwen3.8-max'],
  ...over,
});

const proposal = (over: Partial<AutoModeProposalInfo> = {}): AutoModeProposalInfo => ({
  id: 7,
  kind: 'allow',
  target: '',
  value: 'git push origin*',
  proposed_by: 'session-a',
  state: 'pending',
  created_at: '2026-08-16T10:00:00Z',
  resolved_at: '',
  ...over,
});

const state = (over: Partial<AutoModeState> = {}): AutoModeState => ({
  config: config(),
  proposals: [],
  denials: [],
  environmentSlots: [
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
  ],
  ...over,
});

function Harness(props: {
  getState: () => Promise<AutoModeState>;
  promoteProposal: (id: number) => Promise<AutoModePromotion>;
  discardProposal: (id: number) => Promise<AutoModePromotion>;
  addPattern?: (list: string, pattern: string) => Promise<AutoModePatternEdit>;
  removePattern?: (list: string, pattern: string) => Promise<AutoModePatternEdit>;
  setEnvironmentSlot?: (slot: string, values: string[]) => Promise<AutoModePatternEdit>;
}) {
  const policy = useAutoModePolicy({
    enabled: true,
    addPattern: vi.fn().mockResolvedValue(edited()),
    removePattern: vi.fn().mockResolvedValue(edited()),
    setEnvironmentSlot: vi.fn().mockResolvedValue(edited()),
    setModels: vi.fn(async () => ({ config: config() })),
    loadModels: vi.fn(async () => ({ providers: [], problem: null })),
    ...props,
  });
  return <AutoModeSettings policy={policy} />;
}

const edited = (): AutoModePatternEdit => ({ config: config() });

const resolved = (): AutoModePromotion => ({ proposal: proposal(), config: config() });

function renderPane(value: AutoModeState, over: Partial<{
  promoteProposal: (id: number) => Promise<AutoModePromotion>;
  discardProposal: (id: number) => Promise<AutoModePromotion>;
  addPattern: (list: string, pattern: string) => Promise<AutoModePatternEdit>;
  removePattern: (list: string, pattern: string) => Promise<AutoModePatternEdit>;
  setEnvironmentSlot: (slot: string, values: string[]) => Promise<AutoModePatternEdit>;
}> = {}) {
  const getState = vi.fn().mockResolvedValue(value);
  const promoteProposal = over.promoteProposal ?? vi.fn().mockResolvedValue(resolved());
  const discardProposal = over.discardProposal ?? vi.fn().mockResolvedValue(resolved());
  const addPattern = over.addPattern ?? vi.fn().mockResolvedValue(edited());
  const removePattern = over.removePattern ?? vi.fn().mockResolvedValue(edited());
  const setEnvironmentSlot = over.setEnvironmentSlot ?? vi.fn().mockResolvedValue(edited());
  render(
    <Harness
      getState={getState}
      promoteProposal={promoteProposal}
      discardProposal={discardProposal}
      addPattern={addPattern}
      removePattern={removePattern}
      setEnvironmentSlot={setEnvironmentSlot}
    />,
  );
  return { getState, promoteProposal, discardProposal, addPattern, removePattern, setEnvironmentSlot };
}

describe('AutoModeSettings', () => {
  it('reads auto mode once on open and does not loop', async () => {
    const { getState } = renderPane(state());
    await waitFor(() => screen.getByTestId('automode-config'));

    expect(getState).toHaveBeenCalledTimes(1);
    await new Promise((done) => setTimeout(done, 50));
    expect(getState).toHaveBeenCalledTimes(1);
  });

  it('explains an empty proposal list rather than showing nothing', async () => {
    renderPane(state());
    const empty = await screen.findByTestId('automode-no-proposals');
    expect(empty).toHaveTextContent('No proposals are waiting');
    expect(screen.queryByTestId('automode-proposals')).toBeNull();
  });

  it('shows what each proposal asks for and who asked', async () => {
    renderPane(state({
      proposals: [
        proposal(),
        proposal({ id: 8, kind: 'model', target: 'models', value: 'opencode-go/other', proposed_by: '' }),
      ],
    }));
    await screen.findByTestId('automode-proposals');

    const allow = screen.getByTestId('automode-proposal-7');
    expect(allow).toHaveTextContent('allow');
    expect(allow).toHaveTextContent('git push origin*');
    expect(allow).toHaveTextContent('session-a');

    const model = screen.getByTestId('automode-proposal-8');
    expect(model).toHaveTextContent('models');
    expect(model).toHaveTextContent('unattributed');
  });

  it('shows the models in order, marking the one that judges', async () => {
    renderPane(state({
      config: config({ models: ['opencode-go/glm-5.3', 'vendor/backup'] }),
    }));
    const models = await screen.findByTestId('automode-models');
    const rows = within(models).getAllByTestId('automode-models-entry');
    expect(rows[0]).toHaveTextContent('opencode-go/glm-5.3');
    expect(rows[0]).toHaveTextContent('judges');
    expect(rows[1]).toHaveTextContent('vendor/backup');
    expect(rows[1]).toHaveTextContent('fallback');
    expect(within(rows[0]).queryByTestId('automode-models-primary')).toBeNull();
  });

  it('says auto mode stays off while no model can judge a call', async () => {
    renderPane(state({ config: config({ models: [], enabled_default: true }) }));
    expect(await screen.findByTestId('automode-new-sessions')).toHaveTextContent('Auto mode off');
    expect(screen.getByTestId('automode-models')).toHaveTextContent('No model, so auto mode stays off');
  });

  it('promotes a proposal and re-reads the result', async () => {
    const promoteProposal = vi.fn().mockResolvedValue(resolved());
    const { getState } = renderPane(state({ proposals: [proposal()] }), { promoteProposal });
    await screen.findByTestId('automode-proposals');

    fireEvent.click(screen.getByTestId('automode-promote-7'));
    await waitFor(() => expect(promoteProposal).toHaveBeenCalledWith(7));
    await waitFor(() => expect(getState).toHaveBeenCalledTimes(2));
  });

  it('discards a proposal and re-reads the result', async () => {
    const discardProposal = vi.fn().mockResolvedValue(resolved());
    const { getState } = renderPane(state({ proposals: [proposal()] }), { discardProposal });
    await screen.findByTestId('automode-proposals');

    fireEvent.click(screen.getByTestId('automode-discard-7'));
    await waitFor(() => expect(discardProposal).toHaveBeenCalledWith(7));
    await waitFor(() => expect(getState).toHaveBeenCalledTimes(2));
  });

  it('shows the failure when a promotion is refused and keeps the list', async () => {
    const promoteProposal = vi.fn().mockRejectedValue(new Error('auto mode proposal 7 is already promoted'));
    renderPane(state({ proposals: [proposal()] }), { promoteProposal });
    await screen.findByTestId('automode-proposals');

    fireEvent.click(screen.getByTestId('automode-promote-7'));
    await waitFor(() => screen.getByText('auto mode proposal 7 is already promoted'));
    expect(screen.getByTestId('automode-proposal-7')).toBeInTheDocument();
  });

  it('shows the effective policy a session would launch with', async () => {
    renderPane(state({
      config: config({
        enabled_default: false,
        allow: ['git push origin*'],
        hard_deny: ['*attn automode env*', 'rm -rf /*'],
        environment: { slots: [{ id: 'domains', values: ['grafana.acme.corp'] }], notes: [] },
      }),
    }));
    const shown = await screen.findByTestId('automode-config');

    expect(shown).toHaveTextContent('Auto mode off');
    const models = screen.getByTestId('automode-models');
    expect(models).toHaveTextContent('opencode-go/glm-5.3');
    expect(models).toHaveTextContent('opencode-go/qwen3.8-max');
    // The shipped denies are resolved in daemon-side, so they show up without anyone promoting them.
    expect(screen.getByTestId('automode-hard-deny')).toHaveTextContent('*attn automode env*');
    expect(screen.getByTestId('automode-allow')).toHaveTextContent('git push origin*');
    expect(screen.getByTestId('automode-slot-domains')).toHaveTextContent('grafana.acme.corp');
  });

  it('writes the slot the entry was typed into', async () => {
    const setEnvironmentSlot = vi.fn().mockResolvedValue(edited());
    renderPane(state(), { setEnvironmentSlot });
    fireEvent.click(await screen.findByTestId('automode-slot-edit-domains'));
    const input = screen.getByTestId('automode-slot-input-domains');

    fireEvent.change(input, { target: { value: 'grafana.acme.corp' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(setEnvironmentSlot).toHaveBeenCalledWith('domains', ['grafana.acme.corp']));
  });

  it('shows an unfilled slot as what the rules assume', async () => {
    renderPane(state());
    expect(await screen.findByTestId('automode-slot-domains')).toHaveTextContent('None configured');
  });

  it('says so when a list is empty rather than leaving a blank row', async () => {
    renderPane(state({ config: config({ hard_deny: [] }) }));
    await screen.findByTestId('automode-config');

    expect(screen.getByTestId('automode-allow')).toHaveTextContent('Nothing skips the classifier');
    expect(screen.getByTestId('automode-hard-deny')).toHaveTextContent('Nothing is refused');
  });

  it('adds an allow pattern and re-reads the list', async () => {
    const { addPattern, getState } = renderPane(state());
    await screen.findByTestId('automode-allow-input');

    fireEvent.change(screen.getByTestId('automode-allow-input'), {
      target: { value: 'git status*' },
    });
    fireEvent.click(screen.getByTestId('automode-allow-add'));

    await waitFor(() => expect(addPattern).toHaveBeenCalledWith('allow', 'git status*'));
    await waitFor(() => expect(getState).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByTestId('automode-allow-input')).toHaveValue(''));
  });

  it('adds a hard deny from its own list, naming that list', async () => {
    const { addPattern } = renderPane(state());
    await screen.findByTestId('automode-hard-deny-input');

    fireEvent.change(screen.getByTestId('automode-hard-deny-input'), {
      target: { value: '*terraform apply*' },
    });
    fireEvent.click(screen.getByTestId('automode-hard-deny-add'));

    await waitFor(() => expect(addPattern).toHaveBeenCalledWith('hard_deny', '*terraform apply*'));
  });

  it('removes a hand-added pattern and re-reads the list', async () => {
    const { removePattern, getState } = renderPane(
      state({ config: config({ allow: ['git push origin*'] }) }),
    );
    await screen.findByTestId('automode-allow-remove');

    fireEvent.click(screen.getByTestId('automode-allow-remove'));
    await waitFor(() => expect(removePattern).toHaveBeenCalledWith('allow', 'git push origin*'));
    await waitFor(() => expect(getState).toHaveBeenCalledTimes(2));
  });

  // A shipped hard deny is resolved in at read and never stored, so it must not offer a Remove.
  it('marks a shipped hard deny as built-in and gives it no remove button', async () => {
    renderPane(state({
      config: config({
        hard_deny: ['*attn automode env*', '*terraform apply*'],
        shipped_hard_deny: ['*attn automode env*'],
      }),
    }));
    await screen.findByTestId('automode-hard-deny');

    expect(screen.getByTestId('automode-hard-deny-builtin')).toHaveTextContent('built-in');
    expect(screen.getAllByTestId('automode-hard-deny-remove')).toHaveLength(1);
    expect(screen.getByLabelText('Remove *terraform apply*')).toBeInTheDocument();
  });

  it('shows a refused pattern next to its own input and keeps the draft', async () => {
    const addPattern = vi.fn().mockRejectedValue(new Error(
      'broad allow pattern "*" is refused: an allow entry must name something',
    ));
    renderPane(state(), { addPattern });
    await screen.findByTestId('automode-allow-input');

    fireEvent.change(screen.getByTestId('automode-allow-input'), { target: { value: '*' } });
    fireEvent.click(screen.getByTestId('automode-allow-add'));

    const failure = await screen.findByTestId('automode-allow-error');
    expect(failure).toHaveTextContent('an allow entry must name something');
    expect(screen.getByTestId('automode-allow-input')).toHaveValue('*');
    expect(screen.queryByTestId('automode-hard-deny-error')).toBeNull();
  });

  it('reports a refused removal without dropping the entry from the list', async () => {
    const removePattern = vi.fn().mockRejectedValue(new Error('"x" is not in the allow list'));
    renderPane(state({ config: config({ allow: ['x'] }) }), { removePattern });
    await screen.findByTestId('automode-allow-remove');

    fireEvent.click(screen.getByTestId('automode-allow-remove'));
    await screen.findByTestId('automode-allow-error');
    expect(screen.getByTestId('automode-allow')).toHaveTextContent('x');
  });

  it('promotes a proposal while the same list is directly editable', async () => {
    const promoteProposal = vi.fn().mockResolvedValue(resolved());
    const { addPattern } = renderPane(
      state({ proposals: [proposal()] }),
      { promoteProposal },
    );
    await screen.findByTestId('automode-proposals');

    fireEvent.click(screen.getByTestId('automode-promote-7'));
    await waitFor(() => expect(promoteProposal).toHaveBeenCalledWith(7));
    expect(addPattern).not.toHaveBeenCalled();

    fireEvent.change(screen.getByTestId('automode-allow-input'), { target: { value: 'ls*' } });
    fireEvent.click(screen.getByTestId('automode-allow-add'));
    await waitFor(() => expect(addPattern).toHaveBeenCalledWith('allow', 'ls*'));
  });

  it('lists recent denials and what decided them', async () => {
    renderPane(state({
      denials: [{
        id: 3,
        session_id: 'session-a',
        tool: 'bash',
        signature: 'curl https://example.com',
        reason: 'reaches the network',
        rule: 'classifier-2a',
        created_at: '2026-08-18T09:00:00Z',
      }],
    }));
    const ledger = await screen.findByTestId('automode-denials');
    expect(ledger).toHaveTextContent('curl https://example.com');
    expect(ledger).toHaveTextContent('classifier-2a');
  });

  it('says the ledger is empty rather than showing a bare heading', async () => {
    renderPane(state());
    await screen.findByTestId('automode-no-denials');
  });

  it('offers a retry when auto mode cannot be read at all', async () => {
    const getState = vi.fn().mockRejectedValue(new Error('no database'));
    render(
      <Harness
        getState={getState}
        promoteProposal={vi.fn()}
        discardProposal={vi.fn()}
      />,
    );

    await waitFor(() => screen.getByText('no database'));
    getState.mockResolvedValue(state());
    fireEvent.click(screen.getByText('Try again'));
    await waitFor(() => screen.getByTestId('automode-config'));
  });
});
