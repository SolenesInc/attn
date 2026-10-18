import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { gesture } from './test/renderApp';
import type { CommandName, EventMessage } from './test/protocol';
import type { Reply, ScriptedDaemon } from './test/scriptedDaemon';
import { openSection } from './test/settings';

type AutoModeState = EventMessage<'automode_state_result'>;
type Config = AutoModeState['config'];
type Rule = Config['rules'][number];
type Proposal = AutoModeState['proposals'][number];
type Slot = AutoModeState['environment_slots'][number];

const rule = (over: Partial<Rule> = {}): Rule => ({
  pattern: [['git'], ['status']],
  decision: 'allow',
  sandbox: 'bypass',
  justification: '',
  match: [],
  not_match: [],
  ...over,
});

const shippedRule = rule({
  pattern: [['attn'], ['automode'], ['env']],
  decision: 'forbidden',
  sandbox: 'inherit',
  justification: 'the environment is what the reviewer reads',
});

const config = (over: Partial<Config> = {}): Config => ({
  enabled_default: true,
  approval_policy: 'on-request',
  sandbox_mode: 'workspace-write',
  environment: { slots: [], notes: [] },
  rules: [shippedRule],
  shipped_rules: [shippedRule],
  network: { enabled: true, allowed_domains: [], denied_domains: ['localhost:29849'], allow_local_binding: false },
  shipped_denied_domains: ['localhost:29849'],
  legacy_patterns: [],
  presets: [],
  ...over,
});

const proposal = (over: Partial<Proposal> = {}): Proposal => ({
  id: 7,
  kind: 'rule',
  target: '',
  value: '{"pattern":["git","push"],"decision":"allow"}',
  summary: 'allow, bypass sandbox: git push',
  proposed_by: { ref: 'session:session-a', name: 'session-a' },
  state: 'pending',
  created_at: '2026-08-16T10:00:00Z',
  resolved_at: '',
  ...over,
});

const domainsSlot: Slot = {
  id: 'domains',
  label: 'Trusted internal domains',
  kind: 'list',
  choices: [],
  detail: 'Hosts the agent may send data to.',
  unset: 'None configured',
  detected: false,
  read_by: ['Data Exfiltration'],
};

const visibilitySlot: Slot = {
  id: 'repo_visibility',
  label: 'Repository visibility',
  kind: 'choice',
  choices: ['private', 'public'],
  detail: 'Whether that repository is private or public.',
  unset: 'assume private unless the transcript shows otherwise',
  detected: true,
  read_by: ['Data Exfiltration'],
};

interface Scene {
  config?: Partial<Config>;
  proposals?: Proposal[];
  denials?: AutoModeState['denials'];
  slots?: Slot[];
}

const state = (scene: Scene = {}) => ({
  config: config(scene.config),
  proposals: scene.proposals ?? [],
  denials: scene.denials ?? [],
  environment_slots: scene.slots ?? [domainsSlot],
});

const stateResult = (scene: Scene = {}): Reply => ({ event: 'automode_state_result', success: true, ...state(scene) });

const configEdits = [
  'automode_rule_add',
  'automode_rule_remove',
  'automode_legacy_dismiss',
  'automode_host_add',
  'automode_host_remove',
  'automode_policy_set',
] as const satisfies readonly CommandName[];

type Refusals = Partial<Record<(typeof configEdits)[number] | 'automode_promote' | 'automode_env_slot', string>>;

function serveAutoMode(daemon: ScriptedDaemon, scene: Scene, refusals: Refusals = {}) {
  daemon.on('automode_get', () => stateResult(scene));
  for (const cmd of configEdits) {
    daemon.on(cmd, () => (refusals[cmd]
      ? { event: 'automode_config_result', success: false, error: refusals[cmd] }
      : { event: 'automode_config_result', success: true, config: config(scene.config) }));
  }
  daemon.on('automode_env_slot', () => (refusals.automode_env_slot
    ? { event: 'automode_env_set_result', success: false, error: refusals.automode_env_slot }
    : { event: 'automode_env_set_result', success: true, config: config(scene.config) }));
  daemon.on('automode_promote', () => (refusals.automode_promote
    ? { event: 'automode_promote_result', success: false, error: refusals.automode_promote }
    : { event: 'automode_promote_result', success: true, proposal: proposal({ state: 'promoted' }), config: config(scene.config) }));
  daemon.on('automode_discard', () => ({ event: 'automode_discard_result', success: true, proposal: proposal({ state: 'discarded' }) }));
}

function openAutoMode(scene: Scene = {}, refusals: Refusals = {}) {
  return openSection('autoMode', {}, (daemon) => serveAutoMode(daemon, scene, refusals));
}

const reads = (daemon: ScriptedDaemon) => daemon.sentOf('automode_get').length;
const change = (daemon: ScriptedDaemon, testID: string, value: string) =>
  gesture(daemon, () => fireEvent.change(screen.getByTestId(testID), { target: { value } }));
const click = (daemon: ScriptedDaemon, element: HTMLElement) => gesture(daemon, () => fireEvent.click(element));

describe('App auto mode', () => {
  describe('reading', () => {
    it('reads auto mode once when settings opens and not again on its own', async () => {
      const daemon = await openAutoMode();

      expect(screen.getByTestId('automode-config')).toBeInTheDocument();
      expect(reads(daemon)).toBe(1);
    });

    it('shows the effective policy a session would launch with, shipped entries included', async () => {
      await openAutoMode({
        config: {
          enabled_default: false,
          approval_policy: 'never',
          sandbox_mode: 'read-only',
          rules: [shippedRule, rule({ pattern: [['git'], ['push']], decision: 'prompt' })],
          network: { enabled: true, allowed_domains: ['crates.io'], denied_domains: ['localhost:29849', 'evil.example'], allow_local_binding: false },
          environment: { slots: [{ id: 'domains', values: ['grafana.acme.corp'] }], notes: [] },
        },
      });

      expect(screen.getByTestId('automode-config')).toHaveTextContent('Auto mode off');
      expect(screen.getByTestId('automode-approval-policy')).toHaveValue('never');
      expect(screen.getByTestId('automode-sandbox-mode')).toHaveValue('read-only');
      expect(screen.getByTestId('automode-rules')).toHaveTextContent('attn automode env');
      expect(screen.getByTestId('automode-rules')).toHaveTextContent('git push');
      expect(screen.getByTestId('automode-hosts-allow')).toHaveTextContent('crates.io');
      expect(screen.getByTestId('automode-hosts-deny')).toHaveTextContent('evil.example');
      expect(screen.getByTestId('automode-slot-domains')).toHaveTextContent('grafana.acme.corp');
    });

    it('lists recent denials and what decided them, or says the ledger is empty', async () => {
      await openAutoMode({
        denials: [{
          id: 3,
          session_id: 'session-a',
          tool: 'bash',
          signature: 'curl https://example.com',
          reason: 'reaches the network',
          rule: 'guardian',
          created_at: '2026-08-18T09:00:00Z',
        }],
      });

      expect(screen.getByTestId('automode-denials')).toHaveTextContent('curl https://example.com');
      expect(screen.getByTestId('automode-denials')).toHaveTextContent('guardian');
    });

    it('says when nothing is waiting and nothing was denied', async () => {
      await openAutoMode();

      expect(screen.getByTestId('automode-no-proposals')).toHaveTextContent('No proposals are waiting');
      expect(screen.queryByTestId('automode-proposals')).toBeNull();
      expect(screen.getByTestId('automode-no-denials')).toBeInTheDocument();
    });

    it('offers a retry when auto mode cannot be read at all', async () => {
      let readable = false;
      const daemon = await openSection('autoMode', {}, (scripted) => {
        serveAutoMode(scripted, {});
        scripted.on('automode_get', () => (readable
          ? stateResult()
          : { event: 'automode_state_result', success: false, error: 'no database', ...state() }));
      });
      expect(screen.getByText('no database')).toBeInTheDocument();

      readable = true;
      await click(daemon, screen.getByText('Try again'));

      expect(screen.getByTestId('automode-config')).toBeInTheDocument();
    });
  });

  describe('proposals', () => {
    it('reads each proposal as the line the daemon summarised, with who proposed it', async () => {
      await openAutoMode({
        proposals: [
          proposal(),
          proposal({ id: 8, kind: 'host', value: '{"host":"crates.io","decision":"allow"}', summary: 'allow crates.io', proposed_by: undefined }),
        ],
      });

      const first = screen.getByTestId('automode-proposal-7');
      expect(first).toHaveTextContent('rule');
      expect(first).toHaveTextContent('allow, bypass sandbox: git push');
      expect(first).not.toHaveTextContent('{"pattern"');
      expect(first).toHaveTextContent('session-a');
      expect(screen.getByTestId('automode-proposal-8')).toHaveTextContent('allow crates.io');
      expect(screen.getByTestId('automode-proposal-8')).toHaveTextContent('unattributed');
    });

    it('promotes and discards a proposal, reading auto mode again after each', async () => {
      const daemon = await openAutoMode({ proposals: [proposal(), proposal({ id: 8 })] });

      await click(daemon, screen.getByTestId('automode-promote-7'));
      expect(daemon.sentOf('automode_promote').map(({ id }) => id)).toEqual([7]);
      expect(reads(daemon)).toBe(2);

      await click(daemon, screen.getByTestId('automode-discard-8'));
      expect(daemon.sentOf('automode_discard').map(({ id }) => id)).toEqual([8]);
      expect(reads(daemon)).toBe(3);
    });

    it('shows a refused promotion and keeps the proposal listed', async () => {
      const daemon = await openAutoMode({ proposals: [proposal()] }, { automode_promote: 'auto mode proposal 7 is already promoted' });

      await click(daemon, screen.getByTestId('automode-promote-7'));

      expect(screen.getByText('auto mode proposal 7 is already promoted')).toBeInTheDocument();
      expect(screen.getByTestId('automode-proposal-7')).toBeInTheDocument();
    });
  });

  describe('rules', () => {
    it('sends the typed words as one token each, reads again, and clears the draft', async () => {
      const daemon = await openAutoMode();

      await change(daemon, 'automode-rules-pattern', '  git   status  ');
      await click(daemon, screen.getByTestId('automode-rules-add'));

      expect(daemon.sentOf('automode_rule_add').map(({ pattern, decision, sandbox, justification }) => [pattern, decision, sandbox, justification]))
        .toEqual([[['git', 'status'], 'allow', 'bypass', '']]);
      expect(reads(daemon)).toBe(2);
      expect(screen.getByTestId('automode-rules-pattern')).toHaveValue('');
    });

    it('authors the review decision, the sandbox and the reason independently', async () => {
      const daemon = await openAutoMode();

      await change(daemon, 'automode-rules-pattern', 'go test');
      await change(daemon, 'automode-rules-decision', 'prompt');
      expect(screen.getByTestId('automode-rules-sandbox')).toHaveValue('inherit');
      await change(daemon, 'automode-rules-sandbox', 'bypass');
      await click(daemon, screen.getByTestId('automode-rules-add'));

      await change(daemon, 'automode-rules-pattern', 'terraform apply');
      await change(daemon, 'automode-rules-decision', 'forbidden');
      await change(daemon, 'automode-rules-justification', 'it changes real infrastructure');
      await click(daemon, screen.getByTestId('automode-rules-add'));

      expect(daemon.sentOf('automode_rule_add').map(({ pattern, decision, sandbox, justification }) => [pattern, decision, sandbox, justification])).toEqual([
        [['go', 'test'], 'prompt', 'bypass', ''],
        [['terraform', 'apply'], 'forbidden', 'inherit', 'it changes real infrastructure'],
      ]);
    });

    it('reads a rule as its words, its decision and why', async () => {
      await openAutoMode({
        config: { rules: [shippedRule, rule({ pattern: [['git'], ['push', 'pull']], decision: 'prompt', justification: 'it leaves the machine' })] },
      });

      const rules = screen.getByTestId('automode-rules');
      expect(rules).toHaveTextContent('git {push|pull}');
      expect(rules).toHaveTextContent('prompt');
      expect(rules).toHaveTextContent('it leaves the machine');
    });

    it('removes a rule by its whole pattern, alternatives and all', async () => {
      const daemon = await openAutoMode({
        config: { rules: [shippedRule, rule({ pattern: [['git'], ['push']] }), rule({ pattern: [['git'], ['push', 'pull']], decision: 'prompt' })] },
      });

      await click(daemon, screen.getByLabelText('Remove git {push|pull}'));

      expect(daemon.sentOf('automode_rule_remove').map(({ pattern }) => pattern)).toEqual([[['git'], ['push', 'pull']]]);
      expect(reads(daemon)).toBe(2);
    });

    it('marks a shipped rule built-in and offers no way to remove it', async () => {
      await openAutoMode({ config: { rules: [shippedRule, rule()] } });

      expect(screen.getByTestId('automode-rules-builtin')).toHaveTextContent('built-in');
      expect(screen.getAllByTestId('automode-rules-remove')).toHaveLength(1);
      expect(screen.getByLabelText('Remove git status')).toBeInTheDocument();
    });

    it('shows a refused rule beside its own input and keeps the draft', async () => {
      const daemon = await openAutoMode({}, { automode_rule_add: 'a forbidden rule needs a justification: it is the text the agent is given' });

      await change(daemon, 'automode-rules-pattern', 'rm');
      await click(daemon, screen.getByTestId('automode-rules-add'));

      expect(screen.getByTestId('automode-rules-error')).toHaveTextContent('a forbidden rule needs a justification');
      expect(screen.getByTestId('automode-rules-pattern')).toHaveValue('rm');
      expect(screen.queryByTestId('automode-hosts-allow-error')).toBeNull();
    });

    it('reports a refused removal without dropping the rule', async () => {
      const daemon = await openAutoMode({ config: { rules: [rule()] } }, { automode_rule_remove: '"git status" is not a stored rule' });

      await click(daemon, screen.getByTestId('automode-rules-remove'));

      expect(screen.getByTestId('automode-rules-error')).toHaveTextContent('is not a stored rule');
      expect(screen.getByTestId('automode-rules')).toHaveTextContent('git status');
    });
  });

  describe('patterns the migration could not convert', () => {
    it('names each one and says what to do with it', async () => {
      await openAutoMode({ config: { legacy_patterns: ['git status*', '*curl*'] } });

      expect(screen.getAllByTestId('automode-legacy-entry')).toHaveLength(2);
      expect(screen.getByTestId('automode-legacy')).toHaveTextContent('*curl*');
      expect(screen.getByText(/Rewrite each one as/)).toBeInTheDocument();
    });

    it('hides the section when nothing was left behind', async () => {
      await openAutoMode();

      expect(screen.queryByTestId('automode-legacy')).toBeNull();
    });

    it('dismisses one and reads auto mode again', async () => {
      const daemon = await openAutoMode({ config: { legacy_patterns: ['git status*', '*curl*'] } });

      await click(daemon, screen.getByLabelText('Dismiss *curl*'));

      expect(daemon.sentOf('automode_legacy_dismiss').map(({ pattern }) => pattern)).toEqual(['*curl*']);
      expect(reads(daemon)).toBe(2);
    });

    it('shows a refused dismissal beside the list', async () => {
      const daemon = await openAutoMode({ config: { legacy_patterns: ['*curl*'] } }, { automode_legacy_dismiss: '"*curl*" is not on the list' });

      await click(daemon, screen.getByTestId('automode-legacy-dismiss'));

      expect(screen.getByTestId('automode-legacy')).toHaveTextContent('is not on the list');
    });
  });

  describe('hosts', () => {
    it('adds a host to the list it was typed into', async () => {
      const daemon = await openAutoMode();

      await change(daemon, 'automode-hosts-allow-input', 'crates.io');
      await click(daemon, screen.getByTestId('automode-hosts-allow-add'));
      await change(daemon, 'automode-hosts-deny-input', 'evil.example');
      await click(daemon, screen.getByTestId('automode-hosts-deny-add'));

      expect(daemon.sentOf('automode_host_add').map(({ host, decision }) => [host, decision])).toEqual([
        ['crates.io', 'allow'],
        ['evil.example', 'deny'],
      ]);
    });

    it('removes a host under the decision it was listed with', async () => {
      const daemon = await openAutoMode({
        config: { network: { enabled: true, allowed_domains: ['crates.io'], denied_domains: ['localhost:29849'], allow_local_binding: false } },
      });

      await click(daemon, screen.getByTestId('automode-hosts-allow-remove'));

      expect(daemon.sentOf('automode_host_remove').map(({ host, decision }) => [host, decision])).toEqual([['crates.io', 'allow']]);
    });

    it('marks the daemon’s own port built-in and offers no way to remove it', async () => {
      await openAutoMode({
        config: { network: { enabled: true, allowed_domains: [], denied_domains: ['localhost:29849', 'evil.example'], allow_local_binding: false } },
      });

      expect(screen.getByTestId('automode-hosts-deny-builtin')).toHaveTextContent('built-in');
      expect(screen.getAllByTestId('automode-hosts-deny-remove')).toHaveLength(1);
      expect(screen.getByLabelText('Remove evil.example')).toBeInTheDocument();
    });

    it('says an empty list is empty rather than leaving a blank row', async () => {
      await openAutoMode({ config: { network: { enabled: true, allowed_domains: [], denied_domains: [], allow_local_binding: false } } });

      expect(screen.getByTestId('automode-hosts-allow')).toHaveTextContent('Nothing is reachable');
      expect(screen.getByTestId('automode-hosts-deny')).toHaveTextContent('Nothing is refused outright');
    });
  });

  describe('policy', () => {
    it('sends only the setting that changed', async () => {
      const daemon = await openAutoMode();
      expect(screen.getByTestId('automode-allow-local-binding')).not.toBeChecked();

      await change(daemon, 'automode-approval-policy', 'never');
      await change(daemon, 'automode-sandbox-mode', 'read-only');
      await click(daemon, screen.getByTestId('automode-allow-local-binding'));

      expect(daemon.sentOf('automode_policy_set').map(({ cmd: _cmd, request_id: _id, ...edit }) => edit)).toEqual([
        { approval_policy: 'never' },
        { sandbox_mode: 'read-only' },
        { allow_local_binding: true },
      ]);
      expect(reads(daemon)).toBe(4);
    });

    it('shows a refused policy without moving the picker', async () => {
      const daemon = await openAutoMode({}, { automode_policy_set: 'unknown approval policy "yolo"' });

      await change(daemon, 'automode-approval-policy', 'never');

      expect(screen.getByTestId('automode-policy-error')).toHaveTextContent('unknown approval policy');
      expect(screen.getByTestId('automode-approval-policy')).toHaveValue('on-request');
    });
  });

  describe('what the reviewer knows about this machine', () => {
    const withSlots = { slots: [domainsSlot, visibilitySlot], config: { environment: { slots: [{ id: 'domains', values: ['grafana.acme.corp'] }], notes: [] } } };

    it('shows an unfilled slot as what the rules assume, and which ones a session detects for itself', async () => {
      await openAutoMode(withSlots);

      expect(screen.getByTestId('automode-slot-repo_visibility')).toHaveTextContent('assume private unless the transcript shows otherwise');
      expect(screen.getByTestId('automode-slot-detected-repo_visibility')).toHaveTextContent('detected per session');
      expect(screen.queryByTestId('automode-slot-detected-domains')).toBeNull();
    });

    it('writes the whole slot when an entry is typed or removed', async () => {
      const daemon = await openAutoMode(withSlots);

      await click(daemon, screen.getByTestId('automode-slot-edit-domains'));
      await change(daemon, 'automode-slot-input-domains', 'docs.acme.corp');
      await gesture(daemon, () => fireEvent.keyDown(screen.getByTestId('automode-slot-input-domains'), { key: 'Enter' }));
      await click(daemon, screen.getByLabelText('Remove grafana.acme.corp from Trusted internal domains'));

      expect(daemon.sentOf('automode_env_slot').map(({ slot, values }) => [slot, values])).toEqual([
        ['domains', ['grafana.acme.corp', 'docs.acme.corp']],
        ['domains', []],
      ]);
    });

    it('offers a choice slot its choices rather than free text', async () => {
      const daemon = await openAutoMode(withSlots);

      await click(daemon, screen.getByTestId('automode-slot-edit-repo_visibility'));
      await click(daemon, screen.getByTestId('automode-slot-choice-repo_visibility-public'));

      expect(daemon.sentOf('automode_env_slot').map(({ slot, values }) => [slot, values])).toEqual([['repo_visibility', ['public']]]);
    });
  });

  describe('a change made elsewhere', () => {
    const pushed = (values: string[]): Reply => ({
      event: 'automode_state_changed',
      ...state({ config: { environment: { slots: [{ id: 'domains', values }], notes: [] } } }),
    });
    const opened = { config: { environment: { slots: [{ id: 'domains', values: ['read-when-it-opened.corp'] }], notes: [] } } };

    it('adopts what the daemon pushes without reading again', async () => {
      const daemon = await openAutoMode(opened);

      daemon.emit(pushed(['from-the-cli.corp']));
      await daemon.idle();

      expect(screen.getByTestId('automode-slot-domains')).toHaveTextContent('from-the-cli.corp');
      expect(reads(daemon)).toBe(1);
    });

    it('leaves a half-typed entry alone', async () => {
      const daemon = await openAutoMode(opened);
      await click(daemon, screen.getByTestId('automode-slot-edit-domains'));
      await change(daemon, 'automode-slot-input-domains', 'still-typing.corp');

      daemon.emit(pushed(['from-the-cli.corp']));
      await daemon.idle();

      expect(screen.getByTestId('automode-slot-domains')).toHaveTextContent('from-the-cli.corp');
      expect(screen.getByTestId('automode-slot-input-domains')).toHaveValue('still-typing.corp');
    });
  });
});
