import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AutoModeSettings } from './AutoModeSettings';
import type {
  AutoModeConfigInfo,
  AutoModeConfigEdit,
  AutoModeProposalInfo,
  AutoModePromotion,
  AutoModeRuleInfo,
  AutoModeState,
} from '../hooks/daemonAutoModeEvents';
import { useAutoModePolicy } from '../hooks/useAutoModePolicy';

const rule = (over: Partial<AutoModeRuleInfo> = {}): AutoModeRuleInfo => ({
  pattern: [['git'], ['status']],
  decision: 'allow',
  justification: '',
  match: [],
  not_match: [],
  ...over,
});

const shippedRule = rule({
  pattern: [['attn'], ['automode'], ['env']],
  decision: 'forbidden',
  justification: 'the environment is what the reviewer reads',
});

const config = (over: Partial<AutoModeConfigInfo> = {}): AutoModeConfigInfo => ({
  enabled_default: true,
  approval_policy: 'on-request',
  sandbox_mode: 'workspace-write',
  environment: { slots: [], notes: [] },
  rules: [shippedRule],
  shipped_rules: [shippedRule],
  network: { enabled: true, allowed_domains: [], denied_domains: ['localhost:29849'], allow_local_binding: false },
  shipped_denied_domains: ['localhost:29849'],
  legacy_patterns: [],
  ...over,
});

const proposal = (over: Partial<AutoModeProposalInfo> = {}): AutoModeProposalInfo => ({
  id: 7,
  kind: 'rule',
  target: '',
  value: '{"pattern":["git","push"],"decision":"allow"}',
  summary: 'allow git push',
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

const edited = (): AutoModeConfigEdit => ({ config: config() });

const resolved = (): AutoModePromotion => ({ proposal: proposal(), config: config() });

function Harness(props: {
  getState: () => Promise<AutoModeState>;
  promoteProposal: (id: number) => Promise<AutoModePromotion>;
  discardProposal: (id: number) => Promise<AutoModePromotion>;
  setEnvironmentSlot?: (slot: string, values: string[]) => Promise<AutoModeConfigEdit>;
}) {
  const policy = useAutoModePolicy({
    enabled: true,
    addRule: vi.fn().mockResolvedValue(edited()),
    removeRule: vi.fn().mockResolvedValue(edited()),
    addHost: vi.fn().mockResolvedValue(edited()),
    removeHost: vi.fn().mockResolvedValue(edited()),
    setPolicy: vi.fn().mockResolvedValue(edited()),
    setEnvironmentSlot: vi.fn().mockResolvedValue(edited()),
    ...props,
  });
  return <AutoModeSettings policy={policy} />;
}

function renderPane(value: AutoModeState, over: Partial<{
  promoteProposal: (id: number) => Promise<AutoModePromotion>;
  discardProposal: (id: number) => Promise<AutoModePromotion>;
  setEnvironmentSlot: (slot: string, values: string[]) => Promise<AutoModeConfigEdit>;
}> = {}) {
  const getState = vi.fn().mockResolvedValue(value);
  const promoteProposal = over.promoteProposal ?? vi.fn().mockResolvedValue(resolved());
  const discardProposal = over.discardProposal ?? vi.fn().mockResolvedValue(resolved());
  const setEnvironmentSlot = over.setEnvironmentSlot ?? vi.fn().mockResolvedValue(edited());
  render(
    <Harness
      getState={getState}
      promoteProposal={promoteProposal}
      discardProposal={discardProposal}
      setEnvironmentSlot={setEnvironmentSlot}
    />,
  );
  return { getState, promoteProposal, discardProposal, setEnvironmentSlot };
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

  it('reads a proposal as the line the daemon summarised, not its JSON', async () => {
    renderPane(state({
      proposals: [
        proposal(),
        proposal({
          id: 8,
          kind: 'host',
          value: '{"host":"crates.io","decision":"allow"}',
          summary: 'allow crates.io',
          proposed_by: '',
        }),
      ],
    }));
    await screen.findByTestId('automode-proposals');

    const first = screen.getByTestId('automode-proposal-7');
    expect(first).toHaveTextContent('rule');
    expect(first).toHaveTextContent('allow git push');
    expect(first).not.toHaveTextContent('{"pattern"');
    expect(first).toHaveTextContent('session-a');

    const host = screen.getByTestId('automode-proposal-8');
    expect(host).toHaveTextContent('allow crates.io');
    expect(host).toHaveTextContent('unattributed');
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
        approval_policy: 'never',
        sandbox_mode: 'read-only',
        rules: [shippedRule, rule({ pattern: [['git'], ['push']], decision: 'prompt' })],
        network: {
          enabled: true,
          allowed_domains: ['crates.io'],
          denied_domains: ['localhost:29849', 'evil.example'],
          allow_local_binding: false,
        },
        environment: { slots: [{ id: 'domains', values: ['grafana.acme.corp'] }], notes: [] },
      }),
    }));
    const shown = await screen.findByTestId('automode-config');

    expect(shown).toHaveTextContent('Auto mode off');
    expect(screen.getByTestId('automode-approval-policy')).toHaveValue('never');
    expect(screen.getByTestId('automode-sandbox-mode')).toHaveValue('read-only');
    // The shipped entries resolve daemon-side, so they show without anyone promoting them.
    expect(screen.getByTestId('automode-rules')).toHaveTextContent('attn automode env');
    expect(screen.getByTestId('automode-rules')).toHaveTextContent('git push');
    expect(screen.getByTestId('automode-hosts-allow')).toHaveTextContent('crates.io');
    expect(screen.getByTestId('automode-hosts-deny')).toHaveTextContent('evil.example');
    expect(screen.getByTestId('automode-slot-domains')).toHaveTextContent('grafana.acme.corp');
  });

  it('names a glob the migration could not convert and says what to do with it', async () => {
    renderPane(state({ config: config({ legacy_patterns: ['*curl*'] }) }));
    const legacy = await screen.findByTestId('automode-legacy');
    expect(legacy).toHaveTextContent('*curl*');
    expect(screen.getByText(/Rewrite each one as/)).toBeInTheDocument();
  });

  it('hides the not-converted section when nothing was left behind', async () => {
    renderPane(state());
    await screen.findByTestId('automode-config');
    expect(screen.queryByTestId('automode-legacy')).toBeNull();
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

  it('lists recent denials and what decided them', async () => {
    renderPane(state({
      denials: [{
        id: 3,
        session_id: 'session-a',
        tool: 'bash',
        signature: 'curl https://example.com',
        reason: 'reaches the network',
        rule: 'guardian',
        created_at: '2026-08-18T09:00:00Z',
      }],
    }));
    const ledger = await screen.findByTestId('automode-denials');
    expect(ledger).toHaveTextContent('curl https://example.com');
    expect(ledger).toHaveTextContent('guardian');
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
