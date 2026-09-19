import { useEffect, useRef, useState, type InputHTMLAttributes } from 'react';
import type { CrewLaunchEdit, CrewLaunchSelection, useCrewLaunchAutosave } from '../hooks/useCrewLaunchAutosave';
import type { CrewRestartAttempt } from '../hooks/useCrewRestart';
import type { DaemonSession } from '../hooks/useDaemonSocket';
import { useDelegationModelCatalog } from '../hooks/useDelegationModelCatalog';
import type { DelegationModelCatalog } from '../hooks/daemonDelegationEvents';
import type { CrewMember, DelegationHarness } from '../types/generated';
import {
  currentModel,
  launchSaveCopy,
  modelIdentity,
  modelLabel,
  nextWakeLabel,
  restartBusy,
  restartNotice,
} from './crewLaunchPresentation';

type LaunchAutosave = ReturnType<typeof useCrewLaunchAutosave>;

function CommitOnBlurInput({ value, onCommit, onKeyDown, ...rest }: Omit<InputHTMLAttributes<HTMLInputElement>, 'value' | 'onChange'> & {
  value: string;
  onCommit: (value: string) => void;
}) {
  const [draft, setDraft] = useState(value);
  const [editing, setEditing] = useState(false);
  const shown = editing ? draft : value;
  const latest = useRef({ draft, editing, value, onCommit });
  useEffect(() => {
    latest.current = { draft, editing, value, onCommit };
  });
  const commit = () => {
    setEditing(false);
    if (draft !== value) onCommit(draft);
  };
  useEffect(() => () => {
    const { draft: unsent, editing: open, value: saved, onCommit: send } = latest.current;
    if (open && unsent !== saved) send(unsent);
  }, []);
  return (
    <input
      {...rest}
      value={shown}
      onChange={(event) => { setEditing(true); setDraft(event.target.value); }}
      onBlur={commit}
      onKeyDown={(event) => {
        onKeyDown?.(event);
        if (event.key === 'Enter') { event.preventDefault(); commit(); }
      }}
    />
  );
}

function RestartState({ member, attempt, onResend, onReview }: {
  member: CrewMember;
  attempt?: CrewRestartAttempt;
  onResend: () => void;
  onReview: () => void;
}) {
  const notice = restartNotice(member, attempt);
  if (!notice) return null;
  return (
    <div className={`crew-restart-state is-${notice.tone}`} role="status">
      <span>{notice.text}</span>
      {notice.action && (
        <button type="button" onClick={notice.action.kind === 'resend' ? onResend : onReview}>{notice.action.label}</button>
      )}
    </div>
  );
}

function RunningNow({ member, running }: { member: CrewMember; running?: DaemonSession }) {
  if (!member.binding_session) return null;
  return (
    <section className="crew-running" aria-label="Running now">
      <span className="crew-kicker">Running now</span>
      <div className="crew-runtime-values">
        <span><small>Harness</small>{running?.agent || 'Not reported'}</span>
        <span><small>Model</small>Not reported</span>
        <span><small>Effort</small>Not reported</span>
      </div>
      <code>{member.binding_session.slice(0, 8)}</code>
    </section>
  );
}

function LaunchFields({ member, selection, harnesses, harness, effectiveAgent, catalogLoading, models, update }: {
  member: CrewMember;
  selection: CrewLaunchSelection;
  harnesses: DelegationHarness[];
  harness?: DelegationHarness;
  effectiveAgent: string;
  catalogLoading: boolean;
  models: ReturnType<typeof useDelegationModelCatalog>;
  update: (next: Partial<CrewLaunchSelection>) => void;
}) {
  const [manualModel, setManualModel] = useState(false);
  const catalog = models.catalog;
  const selectedModel = currentModel(catalog?.models, selection.model);
  const chooseModel = (value: string) => {
    if (value === '__custom') {
      setManualModel(true);
      return;
    }
    setManualModel(false);
    const nextModel = currentModel(catalog?.models, value);
    const clearsEffort = nextModel?.effort_support === 'unsupported'
      || Boolean(nextModel?.effort_levels?.length && selection.effort && !nextModel.effort_levels.includes(selection.effort));
    update({ model: value, ...(clearsEffort ? { effort: '' } : {}) });
  };
  return (
    <div className="crew-launch-fields">
      <label>
        <span>Harness</span>
        <select
          data-testid="crew-harness"
          value={selection.agent}
          disabled={catalogLoading}
          onChange={(event) => {
            setManualModel(false);
            update({ agent: event.target.value, model: '', effort: '' });
          }}
        >
          <option value="">Crew default</option>
          {harnesses.map((candidate) => (
            <option key={candidate.id} value={candidate.id} disabled={!candidate.available && candidate.id !== selection.agent}>
              {candidate.name}{candidate.available ? '' : ' (unavailable)'}
            </option>
          ))}
          {selection.agent && !harness && <option value={selection.agent}>{selection.agent} (unavailable)</option>}
        </select>
      </label>

      <label>
        <span>Model</span>
        <select
          data-testid="crew-model"
          value={manualModel ? '__custom' : selection.model}
          disabled={!effectiveAgent || harness?.model_pin === false}
          onChange={(event) => chooseModel(event.target.value)}
        >
          <option value="">Harness default</option>
          {catalog?.models.map((candidate) => (
            <option key={`${candidate.provider}/${candidate.id}`} value={modelIdentity(candidate)} disabled={candidate.access === 'unsupported'}>
              {modelLabel(candidate)}{candidate.access === 'unsupported' ? ' (unsupported)' : ''}
            </option>
          ))}
          {selection.model && !selectedModel && !manualModel && <option value={selection.model}>{selection.model} (custom)</option>}
          <option value="__custom">Enter a model ID…</option>
        </select>
      </label>

      {manualModel && (
        <label className="crew-custom-model">
          <span>Exact model ID</span>
          <CommitOnBlurInput
            data-testid="crew-custom-model"
            autoFocus
            value={selection.model}
            placeholder="Model ID from the harness"
            onCommit={(model) => update({ model, effort: '' })}
          />
        </label>
      )}

      <label>
        <span>Reasoning effort</span>
        <CommitOnBlurInput
          data-testid="crew-effort"
          list={`crew-efforts-${member.id}`}
          value={selection.effort}
          placeholder="Harness default"
          disabled={!effectiveAgent || harness?.effort_pin === false || selectedModel?.effort_support === 'unsupported'}
          onCommit={(effort) => update({ effort })}
        />
        <datalist id={`crew-efforts-${member.id}`}>
          {selectedModel?.effort_levels?.map((level) => <option key={level} value={level} />)}
        </datalist>
      </label>
    </div>
  );
}

function LaunchCard({ member, edit, harnesses, harness, effectiveAgent, catalogLoading, catalogError, onRetryCatalog, autosave, models }: {
  member: CrewMember;
  edit: CrewLaunchEdit;
  harnesses: DelegationHarness[];
  harness?: DelegationHarness;
  effectiveAgent: string;
  catalogLoading: boolean;
  catalogError: string;
  onRetryCatalog: () => void;
  autosave: LaunchAutosave;
  models: ReturnType<typeof useDelegationModelCatalog>;
}) {
  const discoveryLabel = models.loading ? 'Discovering models…' : models.catalog ? 'Refresh models' : 'Discover models';
  const warning = catalogError || models.error || (effectiveAgent && !harness?.available ? 'This harness is unavailable on this daemon.' : '');
  return (
    <section className="crew-launch-card">
      <div className="crew-launch-title">
        <div><span className="crew-kicker">Next wake</span><h3>Launch settings</h3></div>
        <div className={`crew-save-state is-${edit.state}`} role="status" aria-live="polite">
          {launchSaveCopy(edit.state)}
          {edit.state === 'error' && <button type="button" onClick={() => autosave.retry(member.id)}>Retry</button>}
        </div>
      </div>

      <LaunchFields
        member={member}
        selection={edit.draft}
        harnesses={harnesses}
        harness={harness}
        effectiveAgent={effectiveAgent}
        catalogLoading={catalogLoading}
        models={models}
        update={(next) => autosave.update(member.id, next)}
      />

      {warning && (
        <div className="crew-capability-warning" role="alert">
          <span>{warning}</span>
          {catalogError && <button type="button" onClick={onRetryCatalog}>Retry harness discovery</button>}
        </div>
      )}
      {effectiveAgent && harness?.discovery && (
        <button type="button" className="crew-discover" disabled={models.loading} onClick={() => models.discover(true)}>
          {discoveryLabel}
        </button>
      )}
      <div className="crew-acknowledged" data-testid="crew-acknowledged">
        <span>Acknowledged next wake</span>
        <strong>{nextWakeLabel(edit)}</strong>
      </div>
      {edit.error && <div className="crew-save-error">{edit.error}</div>}
    </section>
  );
}

function RestartSection({ member, edit, restart, isConnected, onRestart }: {
  member: CrewMember;
  edit: CrewLaunchEdit;
  restart?: CrewRestartAttempt;
  isConnected: boolean;
  onRestart: () => void;
}) {
  const busy = restartBusy(member, restart);
  const savesAcknowledged = edit.state === 'saved';
  const restartLabel = busy ? 'Restart in progress…' : member.binding_session ? 'Handoff and restart' : 'Wake';
  return (
    <section className="crew-restart">
      <div>
        <h3>{member.binding_session ? 'Handoff and restart' : 'Wake member'}</h3>
      </div>
      <button
        type="button"
        data-testid="crew-restart"
        disabled={!savesAcknowledged || busy || !isConnected}
        title={!savesAcknowledged ? 'Wait for launch settings to be saved' : undefined}
        onClick={onRestart}
      >
        {restartLabel}
      </button>
    </section>
  );
}

export interface CrewLaunchTabProps {
  member: CrewMember;
  edit: CrewLaunchEdit;
  running?: DaemonSession;
  harnesses: DelegationHarness[];
  catalogLoading: boolean;
  catalogError: string;
  onRetryCatalog: () => void;
  autosave: LaunchAutosave;
  loadModels: (harness: string) => Promise<DelegationModelCatalog>;
  isConnected: boolean;
  restart?: CrewRestartAttempt;
  onRestart: () => void;
  onResendRestart: () => void;
  onReviewRestart: () => void;
}

export function CrewLaunchTab({
  member,
  edit,
  running,
  harnesses,
  catalogLoading,
  catalogError,
  onRetryCatalog,
  autosave,
  loadModels,
  isConnected,
  restart,
  onRestart,
  onResendRestart,
  onReviewRestart,
}: CrewLaunchTabProps) {
  const selection = edit.draft;
  const clearingAgent = selection.agent === '' && Boolean(edit.acknowledged.agent);
  const effectiveAgent = clearingAgent ? '' : selection.agent || member.resolved_agent || '';
  const harness = harnesses.find((candidate) => candidate.id === effectiveAgent);
  const models = useDelegationModelCatalog(harness, loadModels);

  return (
    <>
      <RunningNow member={member} running={running} />
      <LaunchCard
        member={member}
        edit={edit}
        harnesses={harnesses}
        harness={harness}
        effectiveAgent={effectiveAgent}
        catalogLoading={catalogLoading}
        catalogError={catalogError}
        onRetryCatalog={onRetryCatalog}
        autosave={autosave}
        models={models}
      />
      <RestartSection member={member} edit={edit} restart={restart} isConnected={isConnected} onRestart={onRestart} />
      <RestartState member={member} attempt={restart} onResend={onResendRestart} onReview={onReviewRestart} />
    </>
  );
}
