import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AutoModeSettings } from './AutoModeSettings';
import type {
  AutoModeConfigInfo,
  AutoModeConfigEdit,
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
  network: { enabled: true, allowed_domains: [], denied_domains: ['localhost:29849'] },
  shipped_denied_domains: ['localhost:29849'],
  legacy_patterns: [],
  ...over,
});

const state = (over: Partial<AutoModeState> = {}): AutoModeState => ({
  config: config(),
  proposals: [],
  denials: [],
  environmentSlots: [],
  ...over,
});

const edited = (): AutoModeConfigEdit => ({ config: config() });

type Writers = {
  addRule: (pattern: string[], decision: string, justification: string) => Promise<AutoModeConfigEdit>;
  removeRule: (pattern: string[]) => Promise<AutoModeConfigEdit>;
  addHost: (host: string, decision: string) => Promise<AutoModeConfigEdit>;
  removeHost: (host: string, decision: string) => Promise<AutoModeConfigEdit>;
  setPolicy: (approvalPolicy: string | null, sandboxMode: string | null) => Promise<AutoModeConfigEdit>;
};

function Harness(props: { getState: () => Promise<AutoModeState> } & Partial<Writers>) {
  const policy = useAutoModePolicy({
    enabled: true,
    promoteProposal: vi.fn().mockResolvedValue({} as AutoModePromotion),
    discardProposal: vi.fn().mockResolvedValue({} as AutoModePromotion),
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

function renderPane(value: AutoModeState, over: Partial<Writers> = {}) {
  const getState = vi.fn().mockResolvedValue(value);
  const writers: Writers = {
    addRule: over.addRule ?? vi.fn().mockResolvedValue(edited()),
    removeRule: over.removeRule ?? vi.fn().mockResolvedValue(edited()),
    addHost: over.addHost ?? vi.fn().mockResolvedValue(edited()),
    removeHost: over.removeHost ?? vi.fn().mockResolvedValue(edited()),
    setPolicy: over.setPolicy ?? vi.fn().mockResolvedValue(edited()),
  };
  render(<Harness getState={getState} {...writers} />);
  return { getState, ...writers };
}

describe('AutoModeSettings rules', () => {
  it('sends the typed words as one token each', async () => {
    const { addRule, getState } = renderPane(state());
    await screen.findByTestId('automode-rules-pattern');

    fireEvent.change(screen.getByTestId('automode-rules-pattern'), {
      target: { value: '  git   status  ' },
    });
    fireEvent.click(screen.getByTestId('automode-rules-add'));

    await waitFor(() => expect(addRule).toHaveBeenCalledWith(['git', 'status'], 'allow', ''));
    await waitFor(() => expect(getState).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByTestId('automode-rules-pattern')).toHaveValue(''));
  });

  it('carries the decision and the reason it refuses', async () => {
    const { addRule } = renderPane(state());
    await screen.findByTestId('automode-rules-pattern');

    fireEvent.change(screen.getByTestId('automode-rules-pattern'), {
      target: { value: 'terraform apply' },
    });
    fireEvent.change(screen.getByTestId('automode-rules-decision'), {
      target: { value: 'forbidden' },
    });
    fireEvent.change(screen.getByTestId('automode-rules-justification'), {
      target: { value: 'it changes real infrastructure' },
    });
    fireEvent.click(screen.getByTestId('automode-rules-add'));

    await waitFor(() => expect(addRule).toHaveBeenCalledWith(
      ['terraform', 'apply'], 'forbidden', 'it changes real infrastructure',
    ));
  });

  it('reads a rule as its words, its decision and why', async () => {
    renderPane(state({
      config: config({
        rules: [shippedRule, rule({
          pattern: [['git'], ['push', 'pull']],
          decision: 'prompt',
          justification: 'it leaves the machine',
        })],
      }),
    }));
    const rules = await screen.findByTestId('automode-rules');

    expect(rules).toHaveTextContent('git {push|pull}');
    expect(rules).toHaveTextContent('prompt');
    expect(rules).toHaveTextContent('it leaves the machine');
  });

  it('removes a rule by the words it matches', async () => {
    const { removeRule, getState } = renderPane(state({
      config: config({ rules: [shippedRule, rule()] }),
    }));
    await screen.findByTestId('automode-rules-remove');

    fireEvent.click(screen.getByTestId('automode-rules-remove'));
    await waitFor(() => expect(removeRule).toHaveBeenCalledWith(['git', 'status']));
    await waitFor(() => expect(getState).toHaveBeenCalledTimes(2));
  });

  // A shipped rule resolves in at read and is never stored, so it must not offer a Remove.
  it('marks a shipped rule as built-in and gives it no remove button', async () => {
    renderPane(state({ config: config({ rules: [shippedRule, rule()] }) }));
    await screen.findByTestId('automode-rules');

    expect(screen.getByTestId('automode-rules-builtin')).toHaveTextContent('built-in');
    expect(screen.getAllByTestId('automode-rules-remove')).toHaveLength(1);
    expect(screen.getByLabelText('Remove git status')).toBeInTheDocument();
  });

  it('shows a refused rule beside its own input and keeps the draft', async () => {
    const addRule = vi.fn().mockRejectedValue(new Error(
      'a forbidden rule needs a justification: it is the text the agent is given',
    ));
    renderPane(state(), { addRule });
    await screen.findByTestId('automode-rules-pattern');

    fireEvent.change(screen.getByTestId('automode-rules-pattern'), { target: { value: 'rm' } });
    fireEvent.click(screen.getByTestId('automode-rules-add'));

    const failure = await screen.findByTestId('automode-rules-error');
    expect(failure).toHaveTextContent('a forbidden rule needs a justification');
    expect(screen.getByTestId('automode-rules-pattern')).toHaveValue('rm');
    expect(screen.queryByTestId('automode-hosts-allow-error')).toBeNull();
  });

  it('reports a refused removal without dropping the rule from the list', async () => {
    const removeRule = vi.fn().mockRejectedValue(new Error('"git status" is not a stored rule'));
    renderPane(state({ config: config({ rules: [rule()] }) }), { removeRule });
    await screen.findByTestId('automode-rules-remove');

    fireEvent.click(screen.getByTestId('automode-rules-remove'));
    await screen.findByTestId('automode-rules-error');
    expect(screen.getByTestId('automode-rules')).toHaveTextContent('git status');
  });
});

describe('AutoModeSettings hosts', () => {
  it('adds a host to the list it was typed into', async () => {
    const { addHost } = renderPane(state());
    await screen.findByTestId('automode-hosts-allow-input');

    fireEvent.change(screen.getByTestId('automode-hosts-allow-input'), {
      target: { value: 'crates.io' },
    });
    fireEvent.click(screen.getByTestId('automode-hosts-allow-add'));
    await waitFor(() => expect(addHost).toHaveBeenCalledWith('crates.io', 'allow'));

    fireEvent.change(screen.getByTestId('automode-hosts-deny-input'), {
      target: { value: 'evil.example' },
    });
    fireEvent.click(screen.getByTestId('automode-hosts-deny-add'));
    await waitFor(() => expect(addHost).toHaveBeenCalledWith('evil.example', 'deny'));
  });

  it('removes a host with the decision it was under', async () => {
    const { removeHost } = renderPane(state({
      config: config({
        network: { enabled: true, allowed_domains: ['crates.io'], denied_domains: ['localhost:29849'] },
      }),
    }));
    await screen.findByTestId('automode-hosts-allow-remove');

    fireEvent.click(screen.getByTestId('automode-hosts-allow-remove'));
    await waitFor(() => expect(removeHost).toHaveBeenCalledWith('crates.io', 'allow'));
  });

  it('marks the daemon\'s own port as built-in and gives it no remove button', async () => {
    renderPane(state({
      config: config({
        network: {
          enabled: true,
          allowed_domains: [],
          denied_domains: ['localhost:29849', 'evil.example'],
        },
      }),
    }));
    await screen.findByTestId('automode-hosts-deny');

    expect(screen.getByTestId('automode-hosts-deny-builtin')).toHaveTextContent('built-in');
    expect(screen.getAllByTestId('automode-hosts-deny-remove')).toHaveLength(1);
    expect(screen.getByLabelText('Remove evil.example')).toBeInTheDocument();
  });

  it('says an empty list is empty rather than leaving a blank row', async () => {
    renderPane(state({
      config: config({ network: { enabled: true, allowed_domains: [], denied_domains: [] } }),
    }));
    await screen.findByTestId('automode-config');

    expect(screen.getByTestId('automode-hosts-allow')).toHaveTextContent('Nothing is reachable');
    expect(screen.getByTestId('automode-hosts-deny')).toHaveTextContent('Nothing is refused outright');
  });
});

describe('AutoModeSettings policy', () => {
  it('sends only the setting that was changed', async () => {
    const { setPolicy, getState } = renderPane(state());
    await screen.findByTestId('automode-approval-policy');

    fireEvent.change(screen.getByTestId('automode-approval-policy'), { target: { value: 'never' } });
    await waitFor(() => expect(setPolicy).toHaveBeenCalledWith('never', null));
    await waitFor(() => expect(getState).toHaveBeenCalledTimes(2));

    fireEvent.change(screen.getByTestId('automode-sandbox-mode'), { target: { value: 'read-only' } });
    await waitFor(() => expect(setPolicy).toHaveBeenCalledWith(null, 'read-only'));
  });

  it('shows a refused policy without moving the picker', async () => {
    const setPolicy = vi.fn().mockRejectedValue(new Error('unknown approval policy "yolo"'));
    renderPane(state(), { setPolicy });
    await screen.findByTestId('automode-approval-policy');

    fireEvent.change(screen.getByTestId('automode-approval-policy'), { target: { value: 'never' } });
    const failure = await screen.findByTestId('automode-policy-error');
    expect(failure).toHaveTextContent('unknown approval policy');
    expect(screen.getByTestId('automode-approval-policy')).toHaveValue('on-request');
  });
});
