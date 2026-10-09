import { useDaemonApi } from '../contexts/DaemonApiContext';
import { LaunchDesktopKind } from '../types/generated';
import type { CrewLaunchEdit, CrewLaunchSelection, useCrewLaunchAutosave } from '../hooks/useCrewLaunchAutosave';
import type { CrewRestartAttempt } from '../hooks/useCrewRestart';
import type { DaemonSession } from '../hooks/useDaemonSocket';
import { routeFromStored, storedRouteModel, knownHarnessModel } from '../hooks/useHarnessRoute';
import { HarnessRouteChip } from './HarnessRouteChip';
import { HarnessRouteBadge } from './HarnessRouteBadge';
import type { HarnessModelCatalog } from '../hooks/daemonDelegationEvents';
import type { CrewMember, Harness } from '../types/generated';
import {
  launchSaveCopy,
  restartBusy,
  restartNotice,
} from './crewLaunchPresentation';

import { LaunchDesktopSelect } from './LaunchDesktopSelect';

type LaunchAutosave = ReturnType<typeof useCrewLaunchAutosave>;

function RestartState({ member, attempt, isConnected, onResend, onReview }: {
  member: CrewMember;
  attempt?: CrewRestartAttempt;
  isConnected: boolean;
  onResend: () => void;
  onReview: () => void;
}) {
  const notice = restartNotice(member, attempt);
  if (!notice) return null;
  return (
    <div className={`crew-restart-state is-${notice.tone}`} role="status">
      <span>{notice.text}</span>
      {notice.action && (
        <button
          type="button"
          disabled={notice.action.kind === 'resend' && (!isConnected || Boolean(attempt?.sending))}
          onClick={notice.action.kind === 'resend' ? onResend : onReview}
        >
          {notice.action.label}
        </button>
      )}
    </div>
  );
}

function RunningNow({ member, running }: { member: CrewMember; running?: DaemonSession }) {
  if (!member.binding_session) return null;
  return (
    <section className="crew-running" aria-label="Running now">
      <span className="crew-kicker">Running now</span>
      <HarnessRouteBadge unknown value={{ harness: running?.agent || '', provider: '', model: '', effort: '' }} label="Running route" />
      <code>{member.binding_session.slice(0, 8)}</code>
    </section>
  );
}

function LaunchFields({ member, selection, harnesses, effectiveAgent, catalogLoading, update }: {
  member: CrewMember; selection: CrewLaunchSelection; harnesses: Harness[]; harness?: Harness; effectiveAgent: string; catalogLoading: boolean; update: (next: Partial<CrewLaunchSelection>) => void;
}) {
  const { settings } = useDaemonApi();
  const value = { ...routeFromStored(effectiveAgent, selection.model, selection.effort), harness: selection.agent };
  const inherited = routeFromStored(effectiveAgent, member.model ? settings[`default_model_${effectiveAgent}`] || '' : member.resolved_model || '', member.effort ? settings[`default_effort_${effectiveAgent}`] || '' : member.resolved_effort || '');
  return <div className="crew-launch-fields">
    <LaunchDesktopSelect kind={LaunchDesktopKind.Crew} itemId={member.id} profileId={member.profile_id ?? ''} defaultName={member.name || member.id} value={selection.launchDesktop} onChange={launchDesktop => update({ launchDesktop })} disabled={catalogLoading} />
    <HarnessRouteChip variant="field" aria-label="Crew launch model" data-testid="crew-route" value={value} rules={{ harnesses, requireAvailable: true, allowNone: true, noneLabel: 'Crew default', inherited }} disabled={catalogLoading} onChange={route => {
      const model = storedRouteModel(route);
      if (route.harness !== selection.agent) { update({ agent: route.harness, model, effort: route.effort }); return; }
      const patch: Partial<CrewLaunchSelection> = {};
      if (model !== selection.model) {
        patch.model = model;
        const picked = knownHarnessModel(effectiveAgent, route.provider, route.model);
        if (route.effort === '' && (!picked || picked.effort_support === 'unsupported')) patch.effort = '';
      }
      if (route.effort !== selection.effort) patch.effort = route.effort;
      if (Object.keys(patch).length) update(patch);
    }} />
  </div>;
}

function LaunchCard({ member, edit, harnesses, harness, effectiveAgent, catalogLoading, catalogError, onRetryCatalog, autosave }: {
  member: CrewMember;
  edit: CrewLaunchEdit;
  harnesses: Harness[];
  harness?: Harness;
  effectiveAgent: string;
  catalogLoading: boolean;
  catalogError: string;
  onRetryCatalog: () => void;
  autosave: LaunchAutosave;
}) {
  const warning = catalogError || (effectiveAgent && !harness?.available ? 'This harness is unavailable on this daemon.' : '');
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
        update={(next) => autosave.update(member.id, next)}
      />

      {warning && (
        <div className="crew-capability-warning" role="alert">
          <span>{warning}</span>
          {catalogError && <button type="button" onClick={onRetryCatalog}>Retry harness discovery</button>}
        </div>
      )}
      <div className="crew-acknowledged" data-testid="crew-acknowledged">
        <span>Acknowledged next wake</span>
        <HarnessRouteBadge value={routeFromStored(edit.acknowledged.resolved_agent || '', edit.acknowledged.resolved_model || '', edit.acknowledged.resolved_effort || '')} label="Acknowledged next wake" />
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
  harnesses: Harness[];
  catalogLoading: boolean;
  catalogError: string;
  onRetryCatalog: () => void;
  autosave: LaunchAutosave;
  loadModels: (harness: string, refresh?: boolean) => Promise<HarnessModelCatalog>;
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
      />
      <RestartSection member={member} edit={edit} restart={restart} isConnected={isConnected} onRestart={onRestart} />
      <RestartState
        member={member}
        attempt={restart}
        isConnected={isConnected}
        onResend={onResendRestart}
        onReview={onReviewRestart}
      />
    </>
  );
}
