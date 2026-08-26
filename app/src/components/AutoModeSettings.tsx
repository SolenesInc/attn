import { useEffect, useRef, useState } from 'react';
import type {
  AutoModeConfigInfo,
  AutoModeDenialInfo,
  AutoModeEnvironmentSlot,
  AutoModeProposalInfo,
} from '../hooks/daemonAutoModeEvents';
import type { AutoModePatternList, AutoModePolicy } from '../hooks/useAutoModePolicy';
import { setAutoModeAutomationHandle } from './autoModeAutomation';
import './AutoModeSettings.css';

interface AutoModeSettingsProps {
  policy: AutoModePolicy;
}

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
  const shipped = new Set(config.shipped_hard_deny);
  const runs = config.enabled_default && config.models.length > 0;

  return (
    <section className="settings-block" data-testid="settings-automode">
      <div className="settings-block-body">
        {error && <span className="settings-warning">{error}</span>}

        <div className="automode-section-head">
          <h4>Effective policy</h4>
          <p className="settings-description">
            What a pi session launches with today. Models are edited from{' '}
            <code>attn automode</code>.
          </p>
        </div>

        <div className="automode-config" data-testid="automode-config">
          <div className="automode-field">
            <span className="automode-field-label">New sessions</span>
            <span className="automode-field-value">
              <span
                className={`settings-pill ${runs ? 'good' : ''}`}
                data-testid="automode-new-sessions"
              >
                {runs ? 'Auto mode on' : 'Auto mode off'}
              </span>
            </span>
          </div>
          {renderModels('Models', 'automode-models', config.models)}
        </div>

        <div className="automode-section-head">
          <h4>This machine</h4>
          <p className="settings-description">
            What the classifier's rules look up about this machine. A slot
            nobody filled means nothing is trusted for it, so the rules run at
            their strictest. Grants belong in Allowed, below.
          </p>
        </div>
        <EnvironmentEditor config={config} slots={environmentSlots} policy={policy} />

        <div className="automode-section-head">
          <h4>Allowed</h4>
          <p className="settings-description">
            Narrow patterns that skip the classifier and run. A pattern that
            names nothing is refused — a blanket allow is what the classifier
            exists to replace.
          </p>
        </div>
        <PatternEditor
          list="allow"
          testID="automode-allow"
          values={config.allow}
          shipped={new Set<string>()}
          empty="Nothing skips the classifier."
          placeholder="git status*"
          policy={policy}
        />

        <div className="automode-section-head">
          <h4>Hard denied</h4>
          <p className="settings-description">
            Refused before anything else looks at the call. The built-in entries
            are what stop a session under auto mode from rewriting its own
            policy; they are not stored and cannot be removed.
          </p>
        </div>
        <PatternEditor
          list="hard_deny"
          testID="automode-hard-deny"
          values={config.hard_deny}
          shipped={shipped}
          empty="Nothing is refused before the classifier sees it."
          placeholder="*rm -rf /*"
          policy={policy}
        />

        <div className="automode-section-head">
          <h4>Proposed rules</h4>
          <p className="settings-description">
            An agent can propose a pattern or a model from <code>attn automode</code>;
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
                  <span className={`settings-pill ${proposal.kind === 'allow' ? 'warn' : ''}`}>
                    {proposalKindLabel(proposal)}
                  </span>
                  <code className="automode-value">{proposal.value}</code>
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
          models: now.config.models,
          allow: now.config.allow,
          hardDeny: now.config.hard_deny,
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

interface PatternEditorProps {
  list: AutoModePatternList;
  testID: string;
  values: string[];
  shipped: Set<string>;
  empty: string;
  placeholder: string;
  policy: AutoModePolicy;
}

function PatternEditor({
  list, testID, values, shipped, empty, placeholder, policy,
}: PatternEditorProps) {
  const [draft, setDraft] = useState('');
  const [failure, setFailure] = useState<string | null>(null);
  const busy = policy.editingList !== null;

  const submit = async () => {
    const pattern = draft.trim();
    if (pattern === '') return;
    setFailure(null);
    try {
      await policy.addPattern(list, pattern);
      setDraft('');
    } catch (err) {
      setFailure(err instanceof Error ? err.message : 'Could not add the pattern');
    }
  };

  const remove = async (pattern: string) => {
    setFailure(null);
    try {
      await policy.removePattern(list, pattern);
    } catch (err) {
      setFailure(err instanceof Error ? err.message : 'Could not remove the pattern');
    }
  };

  return (
    <div className="automode-editor" data-testid={testID}>
      {values.length === 0 ? (
        <span className="settings-hint">{empty}</span>
      ) : (
        <ul className="automode-patterns">
          {values.map((value) => {
            const builtIn = shipped.has(value);
            return (
              <li
                key={value}
                className={`automode-pattern-row${builtIn ? ' is-builtin' : ''}`}
                data-testid={`${testID}-entry`}
              >
                <code className="automode-value">{value}</code>
                {builtIn ? (
                  <span className="settings-pill" data-testid={`${testID}-builtin`}>
                    built-in
                  </span>
                ) : (
                  <button
                    type="button"
                    className="settings-action danger"
                    data-testid={`${testID}-remove`}
                    aria-label={`Remove ${value}`}
                    disabled={busy}
                    onClick={() => void remove(value)}
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
          placeholder={placeholder}
          aria-label={list === 'allow' ? 'Allow pattern' : 'Hard deny pattern'}
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
        <span className="settings-warning" data-testid={`${testID}-error`}>
          {failure}
        </span>
      )}
    </div>
  );
}

function renderModels(label: string, testID: string, models: string[]) {
  return (
    <div className="automode-field">
      <span className="automode-field-label">{label}</span>
      <span className="automode-field-value" data-testid={testID}>
        {models.length === 0 ? (
          <span className="settings-hint">
            No model, so auto mode stays off. Name one with <code>attn automode model</code>.
          </span>
        ) : (
          <ul className="automode-patterns">
            {models.map((model, index) => (
              <li key={model}>
                <code className="automode-value automode-mono">{model}</code>
                <span className="settings-hint"> {index === 0 ? 'judges' : 'fallback'}</span>
              </li>
            ))}
          </ul>
        )}
      </span>
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

function proposalKindLabel(proposal: AutoModeProposalInfo): string {
  return proposal.kind === 'model' ? 'models' : proposal.kind;
}

function formatStamp(value: string): string {
  const at = new Date(value);
  if (Number.isNaN(at.getTime())) return value;
  return at.toLocaleString();
}
