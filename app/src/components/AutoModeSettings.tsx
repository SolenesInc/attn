import { useEffect, useRef, useState } from 'react';
import type {
  AutoModeConfigInfo,
  AutoModeDenialInfo,
  AutoModeEnvironmentSlot,
  AutoModePolicyEdit,
  AutoModeRuleInfo,
} from '../hooks/daemonAutoModeEvents';
import type { AutoModePolicy } from '../hooks/useAutoModePolicy';
import { setAutoModeAutomationHandle } from './autoModeAutomation';
import './AutoModeSettings.css';

interface AutoModeSettingsProps {
  policy: AutoModePolicy;
}

const APPROVAL_POLICIES = ['untrusted', 'on-request', 'never'];
const SANDBOX_MODES = ['read-only', 'workspace-write', 'danger-full-access'];
const DECISIONS = ['allow', 'prompt', 'forbidden'];

export const ruleLine = (rule: AutoModeRuleInfo): string =>
  rule.pattern
    .map((alternatives) => (alternatives.length === 1 ? alternatives[0] : `{${alternatives.join('|')}}`))
    .join(' ');

export function AutoModeSettings({ policy }: AutoModeSettingsProps) {
  const { state, error, loading, resolvingID, refresh, promote, discard } = policy;

  if (error && !state) {
    return (
      <section className="settings-block">
        <div className="settings-block-body">
          <div className="automode-state">
            <span className="settings-warning">{error}</span>
            <button type="button" className="settings-action" onClick={() => void refresh()}>
              Try again
            </button>
          </div>
        </div>
      </section>
    );
  }

  if (!state) {
    return (
      <section className="settings-block">
        <div className="settings-block-body">
          <div className="automode-state" data-testid="automode-loading">
            Reading auto mode…
          </div>
        </div>
      </section>
    );
  }

  const { config, proposals, denials, environmentSlots } = state;

  return (
    <section className="settings-block" data-testid="settings-automode">
      <div className="settings-block-body">
        {error && <span className="settings-warning">{error}</span>}

        <div className="automode-section-head">
          <h4>Effective policy</h4>
          <p className="settings-description">
            What a pi session launches with today.
          </p>
        </div>

        <PolicyEditor config={config} policy={policy} />

        <div className="automode-section-head">
          <h4>This machine</h4>
          <p className="settings-description">
            What the rules look up about this machine. A slot nobody filled
            means nothing is trusted for it, so the rules run at their
            strictest. Grants belong in Rules and Network, below.
          </p>
        </div>
        <EnvironmentEditor config={config} slots={environmentSlots} policy={policy} />

        <div className="automode-section-head">
          <h4>Rules</h4>
          <p className="settings-description">
            A rule matches a command by its leading words, one word per box:
            <code> git push</code> answers every command that starts with it.
            The built-in entries are what stop a session under auto mode from
            rewriting its own policy; they are not stored and cannot be removed.
          </p>
        </div>
        <RuleEditor config={config} policy={policy} />

        <div className="automode-section-head">
          <h4>Network</h4>
          <p className="settings-description">
            Which hosts a session may reach. The built-in denials cover attn's
            own port, which is how a session would otherwise reach the app.
          </p>
        </div>
        <HostEditor config={config} policy={policy} />

        {config.legacy_patterns.length > 0 && (
          <>
            <div className="automode-section-head">
              <h4>Not converted</h4>
              <p className="settings-description">
                These came from the old glob lists and carried a wildcard no rule
                can express, so nothing reads them any more. Rewrite each one as
                a rule above, then dismiss it to take it off this list.
              </p>
            </div>
            <LegacyList patterns={config.legacy_patterns} policy={policy} />
          </>
        )}

        <div className="automode-section-head">
          <h4>Proposed rules</h4>
          <p className="settings-description">
            An agent can propose a rule or a host from <code>attn automode</code>;
            nothing it proposes changes what a session runs under until it is
            promoted here.
          </p>
        </div>

        {proposals.length === 0 ? (
          <div className="settings-empty" data-testid="automode-no-proposals">
            No proposals are waiting. Anything an agent proposes shows up here.
          </div>
        ) : (
          <div className="automode-proposals" data-testid="automode-proposals">
            {proposals.map((proposal) => (
              <div
                key={proposal.id}
                className="automode-proposal"
                data-testid={`automode-proposal-${proposal.id}`}
              >
                <span className="automode-proposal-subject">
                  <span className="settings-pill">{proposal.kind}</span>
                  <code className="automode-value">{proposal.summary || proposal.value}</code>
                </span>
                <span className="automode-proposal-origin">
                  {proposal.proposed_by || 'unattributed'}
                  {proposal.created_at && ` · ${formatStamp(proposal.created_at)}`}
                </span>
                <span className="automode-proposal-actions">
                  <button
                    type="button"
                    className="settings-action"
                    data-testid={`automode-promote-${proposal.id}`}
                    disabled={resolvingID !== null}
                    onClick={() => void promote(proposal.id)}
                  >
                    Promote
                  </button>
                  <button
                    type="button"
                    className="settings-action danger"
                    data-testid={`automode-discard-${proposal.id}`}
                    disabled={resolvingID !== null}
                    onClick={() => void discard(proposal.id)}
                  >
                    Discard
                  </button>
                </span>
              </div>
            ))}
          </div>
        )}

        <div className="automode-section-head">
          <h4>Recent denials</h4>
          <p className="settings-description">
            What auto mode refused, newest first, and which layer decided.
          </p>
        </div>
        {renderDenials(denials)}

        <div className="automode-footer">
          <button
            type="button"
            className="settings-action"
            data-testid="automode-refresh"
            disabled={loading}
            onClick={() => void refresh()}
          >
            {loading ? 'Reading…' : 'Refresh'}
          </button>
          <span className="settings-hint">
            An edit or a promotion reaches the next session that launches; a
            running one keeps the policy it started with.
          </span>
        </div>
      </div>
    </section>
  );
}

interface PolicyEditorProps {
  config: AutoModeConfigInfo;
  policy: AutoModePolicy;
}

function PolicyEditor({ config, policy }: PolicyEditorProps) {
  const [failure, setFailure] = useState<string | null>(null);
  const busy = policy.editing === 'policy';

  const write = async (change: AutoModePolicyEdit) => {
    setFailure(null);
    try {
      await policy.setPolicy(change);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : 'Could not save the policy');
    }
  };

  return (
    <div className="automode-config" data-testid="automode-config">
      <div className="automode-field">
        <span className="automode-field-label">New sessions</span>
        <span className="automode-field-value">
          <span
            className={`settings-pill ${config.enabled_default ? 'good' : ''}`}
            data-testid="automode-new-sessions"
          >
            {config.enabled_default ? 'Auto mode on' : 'Auto mode off'}
          </span>
        </span>
      </div>
      <div className="automode-field">
        <span className="automode-field-label">Approval policy</span>
        <span className="automode-field-value">
          <select
            className="settings-input"
            data-testid="automode-approval-policy"
            aria-label="Approval policy"
            value={config.approval_policy}
            disabled={busy}
            onChange={(event) => void write({ approvalPolicy: event.target.value })}
          >
            {APPROVAL_POLICIES.map((choice) => (
              <option key={choice} value={choice}>{choice}</option>
            ))}
          </select>
        </span>
      </div>
      <div className="automode-field">
        <span className="automode-field-label">Sandbox</span>
        <span className="automode-field-value">
          <select
            className="settings-input"
            data-testid="automode-sandbox-mode"
            aria-label="Sandbox mode"
            value={config.sandbox_mode}
            disabled={busy}
            onChange={(event) => void write({ sandboxMode: event.target.value })}
          >
            {SANDBOX_MODES.map((choice) => (
              <option key={choice} value={choice}>{choice}</option>
            ))}
          </select>
        </span>
      </div>
      {failure && (
        <span className="settings-warning" data-testid="automode-policy-error">{failure}</span>
      )}
    </div>
  );
}

interface RuleEditorProps {
  config: AutoModeConfigInfo;
  policy: AutoModePolicy;
}

function RuleEditor({ config, policy }: RuleEditorProps) {
  const [pattern, setPattern] = useState('');
  const [decision, setDecision] = useState('allow');
  const [justification, setJustification] = useState('');
  const [failure, setFailure] = useState<string | null>(null);
  const busy = policy.editing !== null;

  const shipped = new Set(config.shipped_rules.map(ruleLine));

  const submit = async () => {
    const tokens = pattern.trim().split(/\s+/).filter((token) => token !== '');
    if (tokens.length === 0) return;
    setFailure(null);
    try {
      await policy.addRule({ pattern: tokens, decision, justification: justification.trim() });
      setPattern('');
      setJustification('');
    } catch (err) {
      setFailure(err instanceof Error ? err.message : 'Could not add the rule');
    }
  };

  const remove = async (rule: AutoModeRuleInfo) => {
    setFailure(null);
    try {
      await policy.removeRule(rule.pattern);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : 'Could not remove the rule');
    }
  };

  return (
    <div className="automode-editor" data-testid="automode-rules">
      {config.rules.length === 0 ? (
        <span className="settings-hint">No rule decides anything yet.</span>
      ) : (
        <ul className="automode-patterns">
          {config.rules.map((rule) => {
            const line = ruleLine(rule);
            const builtIn = shipped.has(line);
            return (
              <li
                key={line}
                className={`automode-pattern-row${builtIn ? ' is-builtin' : ''}`}
                data-testid="automode-rules-entry"
              >
                <span className="automode-rule-subject">
                  <span className={`settings-pill ${rule.decision === 'allow' ? 'good' : 'warn'}`}>
                    {rule.decision}
                  </span>
                  <code className="automode-value">{line}</code>
                  {rule.justification && (
                    <span className="settings-hint automode-rule-why">{rule.justification}</span>
                  )}
                </span>
                {builtIn ? (
                  <span className="settings-pill" data-testid="automode-rules-builtin">built-in</span>
                ) : (
                  <button
                    type="button"
                    className="settings-action danger"
                    data-testid="automode-rules-remove"
                    aria-label={`Remove ${line}`}
                    disabled={busy}
                    onClick={() => void remove(rule)}
                  >
                    Remove
                  </button>
                )}
              </li>
            );
          })}
        </ul>
      )}
      <div className="automode-rule-add">
        <input
          type="text"
          className="settings-input"
          data-testid="automode-rules-pattern"
          value={pattern}
          placeholder="git push"
          aria-label="Command words the rule matches"
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          disabled={busy}
          onChange={(event) => setPattern(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') void submit();
          }}
        />
        <select
          className="settings-input"
          data-testid="automode-rules-decision"
          aria-label="What the rule decides"
          value={decision}
          disabled={busy}
          onChange={(event) => setDecision(event.target.value)}
        >
          {DECISIONS.map((choice) => (
            <option key={choice} value={choice}>{choice}</option>
          ))}
        </select>
        <input
          type="text"
          className="settings-input"
          data-testid="automode-rules-justification"
          value={justification}
          placeholder={decision === 'forbidden' ? 'why it is refused' : 'why (optional)'}
          aria-label="Why the rule decides that"
          disabled={busy}
          onChange={(event) => setJustification(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') void submit();
          }}
        />
        <button
          type="button"
          className="settings-action"
          data-testid="automode-rules-add"
          disabled={busy || pattern.trim() === ''}
          onClick={() => void submit()}
        >
          Add
        </button>
      </div>
      {failure && (
        <span className="settings-warning" data-testid="automode-rules-error">{failure}</span>
      )}
    </div>
  );
}

interface HostEditorProps {
  config: AutoModeConfigInfo;
  policy: AutoModePolicy;
}

function HostEditor({ config, policy }: HostEditorProps) {
  return (
    <>
      <LocalBindingToggle config={config} policy={policy} />
      <HostList
        decision="allow"
        testID="automode-hosts-allow"
        hosts={config.network.allowed_domains}
        shipped={new Set<string>()}
        empty="Nothing is reachable beyond what pi allows on its own."
        policy={policy}
      />
      <HostList
        decision="deny"
        testID="automode-hosts-deny"
        hosts={config.network.denied_domains}
        shipped={new Set(config.shipped_denied_domains)}
        empty="Nothing is refused outright."
        policy={policy}
      />
    </>
  );
}

function LocalBindingToggle({ config, policy }: HostEditorProps) {
  const [failure, setFailure] = useState<string | null>(null);
  const busy = policy.editing === 'policy';

  const write = async (allowLocalBinding: boolean) => {
    setFailure(null);
    try {
      await policy.setPolicy({ allowLocalBinding });
    } catch (err) {
      setFailure(err instanceof Error ? err.message : 'Could not save the network policy');
    }
  };

  return (
    <div className="automode-editor">
      <label className="automode-toggle">
        <input
          type="checkbox"
          data-testid="automode-allow-local-binding"
          checked={config.network.allow_local_binding}
          disabled={busy}
          onChange={(event) => void write(event.target.checked)}
        />
        <span>
          Reach localhost and private networks
          <span className="settings-description">
            Let sandboxed commands reach localhost and private networks through the proxy.
          </span>
        </span>
      </label>
      {failure && (
        <span className="settings-warning" data-testid="automode-network-error">{failure}</span>
      )}
    </div>
  );
}

interface LegacyListProps {
  patterns: string[];
  policy: AutoModePolicy;
}

function LegacyList({ patterns, policy }: LegacyListProps) {
  const [failure, setFailure] = useState<string | null>(null);
  const busy = policy.editing !== null;

  const dismiss = async (pattern: string) => {
    setFailure(null);
    try {
      await policy.dismissLegacy(pattern);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : 'Could not dismiss the pattern');
    }
  };

  return (
    <div className="automode-editor" data-testid="automode-legacy">
      <ul className="automode-patterns">
        {patterns.map((pattern) => (
          <li key={pattern} className="automode-pattern-row" data-testid="automode-legacy-entry">
            <span className="automode-rule-subject">
              <span className="settings-pill warn">not converted</span>
              <code className="automode-value">{pattern}</code>
            </span>
            <button
              type="button"
              className="settings-action"
              data-testid="automode-legacy-dismiss"
              aria-label={`Dismiss ${pattern}`}
              disabled={busy}
              onClick={() => void dismiss(pattern)}
            >
              Dismiss
            </button>
          </li>
        ))}
      </ul>
      {failure && <span className="settings-warning">{failure}</span>}
    </div>
  );
}

interface HostListProps {
  decision: string;
  testID: string;
  hosts: string[];
  shipped: Set<string>;
  empty: string;
  policy: AutoModePolicy;
}

function HostList({ decision, testID, hosts, shipped, empty, policy }: HostListProps) {
  const [draft, setDraft] = useState('');
  const [failure, setFailure] = useState<string | null>(null);
  const busy = policy.editing !== null;

  const submit = async () => {
    const host = draft.trim();
    if (host === '') return;
    setFailure(null);
    try {
      await policy.addHost(host, decision);
      setDraft('');
    } catch (err) {
      setFailure(err instanceof Error ? err.message : 'Could not add the host');
    }
  };

  const remove = async (host: string) => {
    setFailure(null);
    try {
      await policy.removeHost(host, decision);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : 'Could not remove the host');
    }
  };

  return (
    <div className="automode-editor" data-testid={testID}>
      <span className="settings-hint">{decision === 'allow' ? 'Allowed hosts' : 'Denied hosts'}</span>
      {hosts.length === 0 ? (
        <span className="settings-hint">{empty}</span>
      ) : (
        <ul className="automode-patterns">
          {hosts.map((host) => {
            const builtIn = shipped.has(host);
            return (
              <li
                key={host}
                className={`automode-pattern-row${builtIn ? ' is-builtin' : ''}`}
                data-testid={`${testID}-entry`}
              >
                <code className="automode-value">{host}</code>
                {builtIn ? (
                  <span className="settings-pill" data-testid={`${testID}-builtin`}>built-in</span>
                ) : (
                  <button
                    type="button"
                    className="settings-action danger"
                    data-testid={`${testID}-remove`}
                    aria-label={`Remove ${host}`}
                    disabled={busy}
                    onClick={() => void remove(host)}
                  >
                    Remove
                  </button>
                )}
              </li>
            );
          })}
        </ul>
      )}
      <div className="automode-pattern-add">
        <input
          type="text"
          className="settings-input"
          data-testid={`${testID}-input`}
          value={draft}
          placeholder="crates.io"
          aria-label={decision === 'allow' ? 'Allowed host' : 'Denied host'}
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          disabled={busy}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') void submit();
          }}
        />
        <button
          type="button"
          className="settings-action"
          data-testid={`${testID}-add`}
          disabled={busy || draft.trim() === ''}
          onClick={() => void submit()}
        >
          Add
        </button>
      </div>
      {failure && (
        <span className="settings-warning" data-testid={`${testID}-error`}>{failure}</span>
      )}
    </div>
  );
}

interface EnvironmentEditorProps {
  config: AutoModeConfigInfo;
  slots: AutoModeEnvironmentSlot[];
  policy: AutoModePolicy;
}

const slotValues = (config: AutoModeConfigInfo, id: string): string[] =>
  config.environment.slots.find((slot) => slot.id === id)?.values ?? [];

function EnvironmentEditor({ config, slots, policy }: EnvironmentEditorProps) {
  const [editing, setEditing] = useState<string | null>(null);
  const [entry, setEntry] = useState('');
  const [failure, setFailure] = useState<string | null>(null);
  const input = useRef<HTMLInputElement | null>(null);

  const filled = slots.filter((slot) => slotValues(config, slot.id).length > 0).length;
  const busy = policy.savingEnvironment;

  // The handle registers once and reads this ref, so it must hold committed
  // state: a discarded render must not leak into it.
  const latest = useRef({ config, slots, policy, filled, editing, failure, busy });
  useEffect(() => {
    latest.current = { config, slots, policy, filled, editing, failure, busy };
  });

  useEffect(() => {
    setAutoModeAutomationHandle({
      getState: () => {
        const now = latest.current;
        return {
          present: true,
          enabledDefault: now.config.enabled_default,
          approvalPolicy: now.config.approval_policy,
          sandboxMode: now.config.sandbox_mode,
          rules: now.config.rules.map(ruleLine),
          allowedHosts: now.config.network.allowed_domains,
          deniedHosts: now.config.network.denied_domains,
          allowLocalBinding: now.config.network.allow_local_binding,
          legacyPatterns: now.config.legacy_patterns,
          proposals: now.policy.state?.proposals.length ?? 0,
          environment: {
            slots: now.config.environment.slots,
            notes: now.config.environment.notes,
            filled: now.filled,
            total: now.slots.length,
            editing: now.editing ?? '',
            saving: now.busy,
            error: now.failure ?? '',
          },
        };
      },
      setEnvironmentSlot: (id: string, values: string[]) =>
        latest.current.policy.setEnvironmentSlot(id, values),
    });
    return () => setAutoModeAutomationHandle(null);
  }, []);

  const write = async (id: string, values: string[]) => {
    setFailure(null);
    try {
      await policy.setEnvironmentSlot(id, values);
      setEditing(null);
      setEntry('');
    } catch (err) {
      setFailure(err instanceof Error ? err.message : 'Could not save it');
    }
  };

  const add = (slot: AutoModeEnvironmentSlot) => {
    const value = entry.trim();
    if (value === '') return;
    void write(slot.id, [...slotValues(config, slot.id), value]);
  };

  return (
    <div className="automode-editor" data-testid="automode-environment">
      <div className="automode-slot-summary" data-testid="automode-environment-filled">
        {filled === 0
          ? `Nothing named yet. A session still trusts the repository it starts in; everything else is external.`
          : `${filled} of ${slots.length} filled.`}
      </div>
      <ul className="automode-slots">
        {slots.map((slot) => {
          const values = slotValues(config, slot.id);
          const open = editing === slot.id;
          return (
            <li className="automode-slot" key={slot.id} data-testid={`automode-slot-${slot.id}`}>
              <div className="automode-slot-row">
                <span className="automode-slot-label" title={slot.detail}>{slot.label}</span>
                <span className="automode-slot-values">
                  {values.length > 0 ? (
                    values.map((value) => (
                      <span className="automode-slot-chip" key={value}>
                        <code>{value}</code>
                        <button
                          type="button"
                          className="automode-slot-remove"
                          aria-label={`Remove ${value} from ${slot.label}`}
                          disabled={busy}
                          onClick={() => void write(slot.id, values.filter((v) => v !== value))}
                        >
                          ×
                        </button>
                      </span>
                    ))
                  ) : (
                    <span className="automode-slot-unset">
                      {slot.unset}
                      {slot.detected && (
                        <span className="automode-slot-detected" data-testid={`automode-slot-detected-${slot.id}`}>
                          detected per session
                        </span>
                      )}
                    </span>
                  )}
                </span>
                <button
                  type="button"
                  className="settings-action"
                  data-testid={`automode-slot-edit-${slot.id}`}
                  aria-expanded={open}
                  disabled={busy}
                  onClick={() => {
                    setFailure(null);
                    setEntry('');
                    setEditing(open ? null : slot.id);
                    if (!open) requestAnimationFrame(() => input.current?.focus());
                  }}
                >
                  {open ? 'Done' : 'Add'}
                </button>
              </div>
              {open && (
                <div className="automode-slot-editor">
                  {slot.kind === 'choice' ? (
                    <span className="automode-slot-choices">
                      {slot.choices.map((choice) => (
                        <button
                          type="button"
                          key={choice}
                          className="settings-action"
                          data-testid={`automode-slot-choice-${slot.id}-${choice}`}
                          disabled={busy}
                          onClick={() => void write(slot.id, [choice])}
                        >
                          {choice}
                        </button>
                      ))}
                    </span>
                  ) : (
                    <input
                      ref={input}
                      className="settings-input"
                      data-testid={`automode-slot-input-${slot.id}`}
                      aria-label={`Add an entry to ${slot.label}`}
                      spellCheck={false}
                      value={entry}
                      disabled={busy}
                      onChange={(event) => setEntry(event.target.value)}
                      onKeyDown={(event) => {
                        if (event.key === 'Enter') {
                          event.preventDefault();
                          add(slot);
                        }
                        if (event.key === 'Escape') setEditing(null);
                      }}
                    />
                  )}
                  <span className="settings-hint">{slot.detail}</span>
                </div>
              )}
            </li>
          );
        })}
      </ul>
      {failure && (
        <span className="settings-warning" data-testid="automode-environment-error">
          {failure}
        </span>
      )}
    </div>
  );
}

function renderDenials(denials: AutoModeDenialInfo[]) {
  if (denials.length === 0) {
    return (
      <div className="settings-empty" data-testid="automode-no-denials">
        Auto mode has refused nothing yet.
      </div>
    );
  }
  return (
    <div className="automode-denials" data-testid="automode-denials">
      {denials.map((denial) => (
        <div key={denial.id} className="automode-denial">
          <code className="automode-value">{denial.signature || denial.tool}</code>
          <span className="automode-denial-rule">
            <span className="settings-pill">{denial.rule || 'unattributed'}</span>
          </span>
          <span className="automode-proposal-origin">
            {denial.created_at ? formatStamp(denial.created_at) : ''}
          </span>
        </div>
      ))}
    </div>
  );
}

function formatStamp(value: string): string {
  const at = new Date(value);
  if (Number.isNaN(at.getTime())) return value;
  return at.toLocaleString();
}
