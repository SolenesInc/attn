import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AutoModeSettings } from './AutoModeSettings';
import { getAutoModeAutomationHandle } from './autoModeAutomation';
import { handleAutoModeDaemonEvent } from '../hooks/daemonAutoModeEvents';
import type {
  AutoModeConfigInfo,
  AutoModeConfigEdit,
  AutoModePromotion,
  AutoModeState,
} from '../hooks/daemonAutoModeEvents';
import { useAutoModePolicy } from '../hooks/useAutoModePolicy';
import { useAutoModePushStore } from '../store/autoMode';

const config = (over: Partial<AutoModeConfigInfo> = {}): AutoModeConfigInfo => ({
  enabled_default: true,
  approval_policy: 'on-request',
  sandbox_mode: 'workspace-write',
  environment: { slots: [{ id: 'domains', values: ['read-when-it-opened.corp'] }], notes: [] },
  rules: [],
  shipped_rules: [],
  network: { enabled: true, allowed_domains: [], denied_domains: [], allow_local_binding: false },
  shipped_denied_domains: [],
  legacy_patterns: [],
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

function Harness({ getState }: { getState: () => Promise<AutoModeState> }) {
  const policy = useAutoModePolicy({
    enabled: true,
    getState,
    promoteProposal: vi.fn().mockResolvedValue({} as AutoModePromotion),
    discardProposal: vi.fn().mockResolvedValue({} as AutoModePromotion),
    addRule: vi.fn().mockResolvedValue({ config: config() } as AutoModeConfigEdit),
    removeRule: vi.fn().mockResolvedValue({ config: config() } as AutoModeConfigEdit),
    addHost: vi.fn().mockResolvedValue({ config: config() } as AutoModeConfigEdit),
    removeHost: vi.fn().mockResolvedValue({ config: config() } as AutoModeConfigEdit),
    setPolicy: vi.fn().mockResolvedValue({ config: config() } as AutoModeConfigEdit),
    setEnvironmentSlot: vi.fn().mockResolvedValue({ config: config() } as AutoModeConfigEdit),
  });
  return <AutoModeSettings policy={policy} />;
}

beforeEach(() => {
  useAutoModePushStore.setState({ pushed: null, version: 0 });
});

const push = (over: Partial<AutoModeConfigInfo>) =>
  handleAutoModeDaemonEvent(
    {
      event: 'automode_state_changed',
      config: config(over),
      proposals: [],
      denials: [],
      environment_slots: state().environmentSlots,
    },
    new Map(),
  );

describe('AutoModeSettings and a config change from elsewhere', () => {
  const slotValues = () =>
    getAutoModeAutomationHandle()?.getState().environment.slots.find((s) => s.id === 'domains')?.values;

  it('adopts a slot another surface wrote while it was open', async () => {
    const getState = vi.fn().mockResolvedValue(state());
    render(<Harness getState={getState} />);
    await waitFor(() => screen.getByTestId('automode-config'));
    expect(slotValues()).toEqual(['read-when-it-opened.corp']);

    push({ environment: { slots: [{ id: 'domains', values: ['from-the-cli.corp'] }], notes: [] } });

    await waitFor(() => expect(slotValues()).toEqual(['from-the-cli.corp']));
    expect(getState).toHaveBeenCalledTimes(1);
  });

  it('leaves a half-typed entry alone', async () => {
    render(<Harness getState={vi.fn().mockResolvedValue(state())} />);
    await waitFor(() => screen.getByTestId('automode-config'));
    fireEvent.click(screen.getByTestId('automode-slot-edit-domains'));
    const input = await screen.findByTestId('automode-slot-input-domains');
    fireEvent.change(input, { target: { value: 'still-typing.corp' } });

    push({ environment: { slots: [{ id: 'domains', values: ['from-the-cli.corp'] }], notes: [] } });

    await waitFor(() => expect(slotValues()).toEqual(['from-the-cli.corp']));
    expect(screen.getByTestId('automode-slot-input-domains')).toHaveValue('still-typing.corp');
  });

  it('ignores a push that predates it', async () => {
    push({ environment: { slots: [{ id: 'domains', values: ['stale.corp'] }], notes: [] } });

    render(<Harness getState={vi.fn().mockResolvedValue(state())} />);
    await waitFor(() => screen.getByTestId('automode-config'));
    expect(slotValues()).toEqual(['read-when-it-opened.corp']);
  });
});
