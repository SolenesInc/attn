import { LaunchDesktopKind } from '../../types/generated';
import { LaunchDesktopSelect } from '../LaunchDesktopSelect';
import { useProfilesStore } from '../../store/profiles';
import type { LaunchDesktopSetting } from '../../types/generated';
// The host remounts on a fresh key per target, so mount already means an explicit
// load: edit mode reads once on mount and never re-fetches on definitionId churn.
import { useCallback, useEffect, useMemo, useRef, useState, type ComponentProps, type ReactNode, type RefObject } from 'react';
import {
  useFieldArray,
  useForm,
  type UseFormSetValue,
  type UseFormRegister,
  type UseFormRegisterReturn,
  type FieldArrayWithId,
  type UseFieldArrayAppend,
  type UseFieldArrayRemove,
} from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { AutomationDefinitionSummary } from '../../types/generated';
import {
  AutomationFormValues,
  AutomationTrigger,
  automationFormSchema,
  localTimeZone,
  repositoryEntry,
  specJSONString,
  specToFormValues,
} from './automationFormModel';
import { useHarnessChoices } from '../../hooks/useHarnesses';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { HarnessRouteChip } from '../HarnessRouteChip';
const AUTOMATION_HARNESSES = ['codex', 'claude'];
import { LocationPicker } from '../LocationPicker';
import { writeClipboardText } from '../../utils/clipboardBridge';
import { ScheduleComposer } from './ScheduleComposer';
import { setAutomationFormAutomationHandle } from './automationFormAutomation';
import './AutomationForm.css';

export interface AutomationFormProps {
  definitionId: number | null;
  getDefinition: (definitionId: number) => Promise<{ specYaml: string; specJson: string; definition?: AutomationDefinitionSummary }>;
  applyDefinition: (
    specJson: string,
    expectedId: number,
    expectedRevision: number,
    launchDesktop?: LaunchDesktopSetting,
    profileId?: string,
  ) => Promise<{ definition: AutomationDefinitionSummary }>;
  deleteDefinition: (definitionId: number) => Promise<void>;
  setEnabled: (definitionId: number, enabled: boolean) => Promise<void>;
  onCancel: () => void;
  onSaved: (definition: AutomationDefinitionSummary) => void;
  onDeleted: () => void;
}

type LoadStatus = 'loading' | 'ready' | 'load-error';

function errorCode(err: unknown): string {
  const code = (err as { code?: unknown } | null | undefined)?.code;
  return typeof code === 'string' ? code : '';
}

function messageOf(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback;
}

function makeCreateDefaults(): AutomationFormValues {
  return {
    name: '',
    id: 0,
    trigger: 'manual',
    scheduleCron: '0 9 * * 1-5',
    scheduleTimeZone: localTimeZone(),
    guard: '',
    continuity: 'fresh',
    catchUp: 'latest',
    repositoriesInclude: [],
    repositoriesExclude: [],
    agent: 'codex',
    model: '',
    effort: '',
    executable: '',
    manualLocation: 'directory',
    directoryPath: '',
    repositoryOverrides: [],
    prompt: '',
  };
}

function flattenFieldErrors(errors: Record<string, unknown>, prefix = ''): Record<string, string> {
  const out: Record<string, string> = {};
  for (const key of Object.keys(errors)) {
    const value = errors[key] as Record<string, unknown> | undefined;
    if (!value || typeof value !== 'object') continue;
    const path = prefix ? `${prefix}.${key}` : key;
    if (typeof value.message === 'string') {
      out[path] = value.message;
    }
    const nestedKeys = Object.keys(value).filter(
      (k) => k !== 'message' && k !== 'type' && k !== 'ref' && k !== 'types' && k !== 'root',
    );
    if (nestedKeys.length > 0) {
      const nested: Record<string, unknown> = {};
      for (const nestedKey of nestedKeys) nested[nestedKey] = value[nestedKey];
      Object.assign(out, flattenFieldErrors(nested, path));
    }
  }
  return out;
}

function AutomationNameFields({
  mode,
  loadedId,
  fieldError,
  nameRegister,
}: {
  mode: 'create' | 'edit';
  loadedId: number | null;
  fieldError: (field: keyof AutomationFormValues) => string | undefined;
  nameRegister: ComponentProps<'input'>;
}) {
  return (
    <section className="automation-form__section">
      <span className="automation-form__section-label">Name</span>
      <input
        className={
          fieldError('name') ? 'automation-form__input automation-form__input--invalid' : 'automation-form__input'
        }
        data-testid="automation-form-name"
        placeholder="Automation name"
        name={nameRegister.name}
        ref={nameRegister.ref}
        onBlur={nameRegister.onBlur}
        onChange={nameRegister.onChange}
      />
      {fieldError('name') && (
        <p className="automation-form__field-error" data-testid="automation-form-error-name">
          {fieldError('name')}
        </p>
      )}

      {mode === 'edit' && <p className="automation-form__id-static">ID: {loadedId}</p>}
    </section>
  );
}

interface AutomationFields {
  values: AutomationFormValues;
  fieldError: (field: keyof AutomationFormValues) => string | undefined;
  regField: (field: keyof AutomationFormValues) => Omit<UseFormRegisterReturn, 'onChange'> & {
    onChange: (event: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) => void;
  };
  setValue: UseFormSetValue<AutomationFormValues>;
  register: UseFormRegister<AutomationFormValues>;
}

function AutomationDirectoryField({ fields }: { fields: AutomationFields }) {
  const { regField, fieldError, setValue } = fields;
  const { sendGetRecentLocations, sendBrowseDirectory, sendInspectPath, settings } = useDaemonApi();
  const [browsing, setBrowsing] = useState(false);
  return (
    <div className="automation-form__field">
      <label className="automation-form__label" htmlFor="automation-form-directory-path">
        Folder
      </label>
      <div className="automation-form__with-button">
        <input
          id="automation-form-directory-path"
          className={
            fieldError('directoryPath')
              ? 'automation-form__input automation-form__input--invalid'
              : 'automation-form__input'
          }
          data-testid="automation-form-directory-path"
          placeholder="/absolute/path"
          {...regField('directoryPath')}
        />
        <button type="button" className="automation-form__browse" data-testid="automation-form-browse" onClick={() => setBrowsing(true)}>
          Browse
        </button>
      </div>
      <LocationPicker
        isOpen={browsing}
        folderOnly
        title="Choose a folder"
        onClose={() => setBrowsing(false)}
        onSelect={(path) => setValue('directoryPath', path, { shouldDirty: true, shouldValidate: true })}
        onGetRecentLocations={sendGetRecentLocations}
        onBrowseDirectory={sendBrowseDirectory}
        onInspectPath={sendInspectPath}
        projectsDirectory={settings.projects_directory}
      />
      {fieldError('directoryPath') && (
        <p className="automation-form__field-error" data-testid="automation-form-error-directoryPath">
          {fieldError('directoryPath')}
        </p>
      )}
    </div>
  );
}

function AutomationSegments<T extends string>({ label, value, options, onChange }: {
  label: string;
  value: T;
  options: { value: T; label: string; testId: string }[];
  onChange: (value: T) => void;
}) {
  return (
    <div className="automation-form__segmented" role="group" aria-label={label}>
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          aria-pressed={value === option.value}
          className={value === option.value ? 'automation-form__segment automation-form__segment--selected' : 'automation-form__segment'}
          onClick={() => onChange(option.value)}
          data-testid={option.testId}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}

function advancedChips(values: AutomationFormValues): string[] {
  const chips: string[] = [];
  if (values.trigger === 'scheduled' && values.continuity === 'singleton') chips.push('Same agent');
  if (values.trigger === 'scheduled' && values.catchUp === 'skip') chips.push('Skip missed runs');
  if (values.trigger === 'manual' && values.manualLocation === 'repository_worktree') chips.push('Pull request worktree');
  if (usesWorktree(values) && values.repositoryOverrides.length > 0) {
    chips.push(values.repositoryOverrides.length === 1 ? '1 local clone' : `${values.repositoryOverrides.length} local clones`);
  }
  if (values.executable.trim()) chips.push('Custom executable');
  return chips;
}

function usesWorktree(values: AutomationFormValues): boolean {
  return values.trigger === 'github_review_requested' || (values.trigger === 'manual' && values.manualLocation === 'repository_worktree');
}

function AutomationDefinitionView({ yaml }: { yaml: string }) {
  const [shown, setShown] = useState(false);
  const [copied, setCopied] = useState(false);
  return (
    <div className="automation-form__field">
      <div className="schedule-composer__line">
        <button type="button" className="schedule-composer__quiet" data-testid="automation-form-definition-toggle" onClick={() => setShown(!shown)}>
          {shown ? 'Hide definition' : 'Show definition'}
        </button>
        {shown && (
          <button
            type="button"
            className="schedule-composer__quiet"
            data-testid="automation-form-definition-copy"
            onClick={() => void writeClipboardText(yaml).then(() => setCopied(true))}
          >
            {copied ? 'Copied' : 'Copy'}
          </button>
        )}
      </div>
      {shown && <pre className="automation-form__definition" data-testid="automation-form-definition">{yaml}</pre>}
    </div>
  );
}

function AutomationAdvanced({ fields, overrides, definitionYaml }: {
  fields: AutomationFields;
  overrides: {
    fields: FieldArrayWithId<AutomationFormValues, 'repositoryOverrides'>[];
    append: UseFieldArrayAppend<AutomationFormValues, 'repositoryOverrides'>;
    remove: UseFieldArrayRemove;
  };
  definitionYaml: string;
}) {
  const { values, register, regField, setValue, fieldError } = fields;
  const [open, setOpen] = useState(false);
  const set = { shouldDirty: true, shouldValidate: true };
  const chips = advancedChips(values);
  const hasError = Boolean(fieldError('catchUp') || fieldError('repositoryOverrides'));
  return (
    <details className="automation-form__advanced" open={open || hasError} data-testid="automation-form-advanced" onToggle={(event) => setOpen(event.currentTarget.open)}>
      <summary>
        Advanced
        {chips.map((chip) => <span className="automation-form__advanced-chip" key={chip}>{chip}</span>)}
      </summary>
      <div className="automation-form__advanced-body">
        {values.trigger === 'scheduled' && (
          <>
            <div className="automation-form__field">
              <span className="automation-form__label">Agent</span>
              <AutomationSegments
                label="Agent"
                value={values.continuity}
                onChange={(continuity) => setValue('continuity', continuity, set)}
                options={[
                  { value: 'fresh', label: 'New each run', testId: 'automation-form-continuity-fresh' },
                  { value: 'singleton', label: 'Same agent', testId: 'automation-form-continuity-singleton' },
                ]}
              />
              <p className="automation-form__hint">
                {values.continuity === 'fresh'
                  ? 'Every run starts a new agent.'
                  : 'Every run goes to one agent, and it resumes if closed. Changing the prompt, model or folder starts a replacement.'}
              </p>
            </div>
            <div className="automation-form__field">
              <span className="automation-form__label">Missed runs</span>
              <AutomationSegments
                label="Missed runs"
                value={values.catchUp}
                onChange={(catchUp) => setValue('catchUp', catchUp, set)}
                options={[
                  { value: 'latest', label: 'Run latest', testId: 'automation-form-catchup-latest' },
                  { value: 'skip', label: 'Skip', testId: 'automation-form-catchup-skip' },
                ]}
              />
              <p className="automation-form__hint">
                {values.catchUp === 'skip'
                  ? 'A missed time still runs if attn notices within 5 minutes. Older ones are dropped.'
                  : 'After downtime, one run for the most recent missed time.'}
              </p>
              {fieldError('catchUp') && (
                <p className="automation-form__field-error" data-testid="automation-form-error-catchUp">{fieldError('catchUp')}</p>
              )}
            </div>
          </>
        )}
        {values.trigger === 'manual' && (
          <div className="automation-form__field">
            <span className="automation-form__label">Works in</span>
            <AutomationSegments
              label="Works in"
              value={values.manualLocation}
              onChange={(location) => setValue('manualLocation', location, set)}
              options={[
                { value: 'directory', label: 'Folder', testId: 'automation-form-location-directory' },
                { value: 'repository_worktree', label: 'Pull request worktree', testId: 'automation-form-location-worktree' },
              ]}
            />
            <p className="automation-form__hint">
              {values.manualLocation === 'directory'
                ? 'The agent works in the folder above.'
                : 'Run now asks for a pull request, and the agent works in a fresh worktree at its head.'}
            </p>
          </div>
        )}
        {usesWorktree(values) && (
          <div className="automation-form__overrides">
            <span className="automation-form__label">Local clones</span>
            {overrides.fields.map((field, index) => (
              <div className="automation-form__override-row" key={field.id}>
                <input
                  className="automation-form__input"
                  placeholder="host/owner/repository"
                  data-testid={`automation-form-override-repository-${index}`}
                  {...register(`repositoryOverrides.${index}.repository` as const)}
                />
                <input
                  className="automation-form__input"
                  placeholder="/absolute/path"
                  data-testid={`automation-form-override-path-${index}`}
                  {...register(`repositoryOverrides.${index}.path` as const)}
                />
                <button type="button" onClick={() => overrides.remove(index)} data-testid={`automation-form-overrides-remove-${index}`}>
                  Remove
                </button>
              </div>
            ))}
            <button type="button" onClick={() => overrides.append({ repository: '', path: '' })} data-testid="automation-form-overrides-add">
              Add override
            </button>
          </div>
        )}
        <div className="automation-form__field">
          <label className="automation-form__label" htmlFor="automation-form-executable">Executable override</label>
          <input
            id="automation-form-executable"
            className="automation-form__input"
            placeholder="Default from PATH"
            data-testid="automation-form-executable"
            {...regField('executable')}
          />
        </div>
        {definitionYaml && <AutomationDefinitionView yaml={definitionYaml} />}
      </div>
    </details>
  );
}

function AutomationGitHubFields({
  fields,
  includeInput,
  setIncludeInput,
  excludeInput,
  setExcludeInput,
  addRepository,
  removeRepository,
}: {
  fields: AutomationFields;
  includeInput: string;
  setIncludeInput: (value: string) => void;
  excludeInput: string;
  setExcludeInput: (value: string) => void;
  addRepository: (field: 'repositoriesInclude' | 'repositoriesExclude', value: string) => void;
  removeRepository: (field: 'repositoriesInclude' | 'repositoriesExclude', index: number) => void;
}) {
  const { values } = fields;
  return (
    <div className="automation-form__trigger-section">
      <div className="automation-form__field">
        <label className="automation-form__label" htmlFor="automation-form-repositories-include-input">
          Include repositories
        </label>
        <div className="automation-form__chip-input" data-testid="automation-form-repositories-include">
          {values.repositoriesInclude.map((entry, index) => (
            <span
              className="automation-form__chip"
              key={entry.id}
              data-testid={`automation-form-repositories-include-chip-${index}`}
            >
              {entry.repository}
              <button
                type="button"
                aria-label={`Remove ${entry.repository}`}
                onClick={() => removeRepository('repositoriesInclude', index)}
                data-testid={`automation-form-repositories-include-remove-${index}`}
              >
                ✕
              </button>
            </span>
          ))}
          <input
            id="automation-form-repositories-include-input"
            className="automation-form__chip-input-field"
            value={includeInput}
            onChange={(event) => setIncludeInput(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault();
                addRepository('repositoriesInclude', includeInput);
                setIncludeInput('');
              } else if (event.key === 'Backspace' && includeInput === '' && values.repositoriesInclude.length > 0) {
                removeRepository('repositoriesInclude', values.repositoriesInclude.length - 1);
              }
            }}
            placeholder="host/owner/repository"
            data-testid="automation-form-repositories-include-input"
          />
        </div>
      </div>

      <div className="automation-form__field">
        <label className="automation-form__label" htmlFor="automation-form-repositories-exclude-input">
          Exclude repositories
        </label>
        <div className="automation-form__chip-input" data-testid="automation-form-repositories-exclude">
          {values.repositoriesExclude.map((entry, index) => (
            <span
              className="automation-form__chip"
              key={entry.id}
              data-testid={`automation-form-repositories-exclude-chip-${index}`}
            >
              {entry.repository}
              <button
                type="button"
                aria-label={`Remove ${entry.repository}`}
                onClick={() => removeRepository('repositoriesExclude', index)}
                data-testid={`automation-form-repositories-exclude-remove-${index}`}
              >
                ✕
              </button>
            </span>
          ))}
          <input
            id="automation-form-repositories-exclude-input"
            className="automation-form__chip-input-field"
            value={excludeInput}
            onChange={(event) => setExcludeInput(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault();
                addRepository('repositoriesExclude', excludeInput);
                setExcludeInput('');
              } else if (event.key === 'Backspace' && excludeInput === '' && values.repositoriesExclude.length > 0) {
                removeRepository('repositoriesExclude', values.repositoriesExclude.length - 1);
              }
            }}
            placeholder="host/owner/repository"
            data-testid="automation-form-repositories-exclude-input"
          />
        </div>
      </div>

      <span className="automation-form__fact-chip">One reviewer per PR — later request cycles return to it</span>
      <span className="automation-form__fact-chip">Existing requests are left alone when enabled</span>
      <span className="automation-form__fact-chip">Missed while attn was off: latest request still runs</span>
      <p className="automation-form__invariant">
        Reviews always run in a fresh worktree checked out at the PR&apos;s head commit — your existing clone is never
        touched.
      </p>
    </div>
  );
}

function AutomationTriggerFields({
  fields,
  onTriggerChange,
  onScheduleValidityChange,
  github,
}: {
  fields: AutomationFields;
  onTriggerChange: (trigger: AutomationTrigger) => void;
  onScheduleValidityChange: (valid: boolean) => void;
  github: ReactNode;
}) {
  const { values, regField, setValue } = fields;
  return (
    <section className="automation-form__section">
      <span className="automation-form__section-label">Trigger</span>
      <div className="automation-form__trigger-cards">
        <button
          type="button"
          className={
            values.trigger === 'manual'
              ? 'automation-form__trigger-card automation-form__trigger-card--selected'
              : 'automation-form__trigger-card'
          }
          onClick={() => onTriggerChange('manual')}
          data-testid="automation-form-trigger-manual"
        >
          Manual
        </button>
        <button
          type="button"
          className={
            values.trigger === 'scheduled'
              ? 'automation-form__trigger-card automation-form__trigger-card--selected'
              : 'automation-form__trigger-card'
          }
          onClick={() => onTriggerChange('scheduled')}
          data-testid="automation-form-trigger-scheduled"
        >
          Scheduled
        </button>
        <button
          type="button"
          className={
            values.trigger === 'github_review_requested'
              ? 'automation-form__trigger-card automation-form__trigger-card--selected'
              : 'automation-form__trigger-card'
          }
          onClick={() => onTriggerChange('github_review_requested')}
          data-testid="automation-form-trigger-github"
        >
          PR review requested
        </button>
      </div>

      {values.trigger === 'manual' && (
        <div className="automation-form__trigger-section">
          <span className="automation-form__fact-chip">Fresh worker each run</span>
          {values.manualLocation === 'directory' && <AutomationDirectoryField fields={fields} />}
        </div>
      )}

      {values.trigger === 'scheduled' && (
        <div className="automation-form__trigger-section">
          <ScheduleComposer
            cron={values.scheduleCron}
            timeZone={values.scheduleTimeZone}
            guarded={values.guard.trim() !== ''}
            onCronChange={(cron) => setValue('scheduleCron', cron, { shouldDirty: true, shouldValidate: true })}
            onTimeZoneChange={(timeZone) => setValue('scheduleTimeZone', timeZone, { shouldDirty: true, shouldValidate: true })}
            onValidityChange={onScheduleValidityChange}
          />

          <div className="automation-form__field">
            <label className="automation-form__label" htmlFor="automation-form-guard">
              Only run if
            </label>
            <input
              id="automation-form-guard"
              className="automation-form__input"
              data-testid="automation-form-guard"
              aria-describedby="automation-form-guard-help"
              placeholder="git rev-parse HEAD"
              {...regField('guard')}
            />
            <p className="automation-form__hint" id="automation-form-guard-help">
              Runs in the folder before each scheduled run. The agent starts only when the command prints something new.
            </p>
          </div>

          <AutomationDirectoryField fields={fields} />
        </div>
      )}

      {values.trigger === 'github_review_requested' && github}
    </section>
  );
}

function AutomationLaunchFields({
  fields,
  desktop,
}: {
  fields: AutomationFields;
  desktop: ReactNode;
}) {
  const { values, fieldError, setValue } = fields;
  const { settings } = useDaemonApi();
  const offeredHarnesses = useMemo(() => [...new Set([...AUTOMATION_HARNESSES, values.agent])], [values.agent]);
  const { harnesses, error, retry } = useHarnessChoices(offeredHarnesses, settings);
  const routeHarnesses = values.executable.trim() ? harnesses.map(harness => ({ ...harness, discovery: false })) : harnesses;
  return (
    <section className="automation-form__section">
      <span className="automation-form__section-label">Runs as</span>
      {desktop}
      {error && <div className="settings-warning" role="alert">{error}<button type="button" className="settings-action quiet" onClick={retry}>Retry</button></div>}
      <HarnessRouteChip variant="field" aria-label="Automation model" data-testid="automation-form-route" value={{ harness: values.agent, provider: '', model: values.model, effort: values.effort }} rules={{ harnesses: routeHarnesses }} onChange={route => {
        setValue('agent', route.harness, { shouldDirty: true, shouldValidate: true });
        setValue('model', route.model, { shouldDirty: true, shouldValidate: true });
        setValue('effort', route.effort, { shouldDirty: true, shouldValidate: true });
      }} />
      {values.executable.trim() && <p className="automation-form__invariant">Use model and effort IDs accepted by the executable override.</p>}
      {fieldError('agent') && <p className="automation-form__field-error" data-testid="automation-form-error-agent">{fieldError('agent')}</p>}
      {fieldError('model') && <p className="automation-form__field-error" data-testid="automation-form-error-model">{fieldError('model')}</p>}
      {fieldError('effort') && <p className="automation-form__field-error" data-testid="automation-form-error-effort">{fieldError('effort')}</p>}

      <p className="automation-form__invariant">
        Automation sessions always run unattended with the agent&apos;s automatic approval mode.
      </p>
    </section>
  );
}

function AutomationFormActions({
  mode,
  saving,
  saveBlocked,
  saveError,
  saveErrorCode,
  deleteArmed,
  deleteContainerRef,
  onReload,
  onDelete,
  onCancel,
}: {
  mode: 'create' | 'edit';
  saving: boolean;
  saveBlocked: boolean;
  saveError: string;
  saveErrorCode: string;
  deleteArmed: boolean;
  deleteContainerRef: RefObject<HTMLDivElement | null>;
  onReload: () => void;
  onDelete: () => void;
  onCancel: () => void;
}) {
  return (
    <>
      {saveErrorCode === 'revision_conflict' && (
        <div
          className="automation-form__banner automation-form__banner--stale"
          data-testid="automation-form-stale-banner"
        >
          <p>
            This automation changed elsewhere while you were editing. Reload to pick up the latest revision — your
            unsaved edits will be replaced.
          </p>
          <button type="button" onClick={onReload} data-testid="automation-form-reload">
            Reload
          </button>
        </div>
      )}

      {saveError !== '' && saveErrorCode !== 'revision_conflict' && (
        <p className="automation-form__banner automation-form__banner--error" data-testid="automation-form-save-error">
          {saveError}
        </p>
      )}

      <div className="automation-form__actions">
        {mode === 'edit' ? (
          <div className="automation-form__delete" ref={deleteContainerRef}>
            <button
              type="button"
              className={
                deleteArmed
                  ? 'automation-form__delete-button automation-form__delete-button--armed'
                  : 'automation-form__delete-button'
              }
              onClick={onDelete}
              data-testid="automation-form-delete"
            >
              {deleteArmed ? 'Confirm delete' : 'Delete'}
            </button>
            {deleteArmed && (
              <span className="automation-form__delete-note">Existing seeds and run history are kept.</span>
            )}
          </div>
        ) : (
          <span />
        )}

        <div className="automation-form__actions-primary">
          <button
            type="button"
            className="automation-form__cancel"
            onClick={onCancel}
            data-testid="automation-form-cancel"
          >
            Cancel
          </button>
          <button type="submit" className="automation-form__save" disabled={saving || saveBlocked} data-testid="automation-form-save">
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>
    </>
  );
}

export function AutomationForm({
  definitionId,
  getDefinition,
  applyDefinition,
  deleteDefinition,
  setEnabled: setEnabledAction,
  onCancel,
  onSaved,
  onDeleted,
}: AutomationFormProps) {
  const mode: 'create' | 'edit' = definitionId ? 'edit' : 'create';

  const {
    register,
    handleSubmit,
    watch,
    getValues,
    setValue,
    trigger,
    reset,
    control,
    formState: { errors },
  } = useForm<AutomationFormValues>({
    resolver: zodResolver(automationFormSchema),
    mode: 'onBlur',
    reValidateMode: 'onChange',
    defaultValues: makeCreateDefaults(),
  });

  const {
    fields: overrideFields,
    append: appendOverride,
    remove: removeOverride,
  } = useFieldArray({
    control,
    name: 'repositoryOverrides',
  });

  const [status, setStatus] = useState<LoadStatus>(mode === 'edit' ? 'loading' : 'ready');
  const [loadError, setLoadError] = useState('');
  const [loadedId, setLoadedId] = useState<number | null>(definitionId);
  const [revision, setRevision] = useState(0);
  const [launchDesktop, setLaunchDesktop] = useState<LaunchDesktopSetting>();
  const selectedProfile = useProfilesStore((state) => state.selectedProfileId);
  const [profileId, setProfileId] = useState(selectedProfile ?? '');
  const [enabled, setEnabledState] = useState<boolean | null>(null);

  const [scheduleValid, setScheduleValid] = useState(true);
  const [definitionYaml, setDefinitionYaml] = useState('');
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState('');
  const [saveErrorCode, setSaveErrorCode] = useState('');

  const [deleteArmed, setDeleteArmed] = useState(false);
  const deleteContainerRef = useRef<HTMLDivElement>(null);
  const [includeInput, setIncludeInput] = useState('');
  const [excludeInput, setExcludeInput] = useState('');

  // Mount-only; see the file header for why definitionId is not a dep.
  useEffect(() => {
    if (mode !== 'edit' || !definitionId) return;
    let cancelled = false;
    getDefinition(definitionId)
      .then((result) => {
        if (cancelled) return;
        let parsed: AutomationFormValues;
        try {
          parsed = specToFormValues(result.specJson);
        } catch (error) {
          setLoadError(messageOf(error, 'Failed to parse automation definition'));
          setStatus('load-error');
          return;
        }
        reset(parsed);
        setDefinitionYaml(result.specYaml);
        setLoadedId(result.definition?.id ?? definitionId);
        setRevision(result.definition?.revision ?? 0);
        setLaunchDesktop(result.definition?.launch_desktop);
        setProfileId(result.definition?.profile_id ?? selectedProfile ?? '');
        setEnabledState(result.definition?.enabled ?? null);
        setStatus('ready');
      })
      .catch((error) => {
        if (cancelled) return;
        setLoadError(messageOf(error, 'Failed to load automation definition'));
        setStatus('load-error');
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const doSave = useCallback(
    (values: AutomationFormValues) => {
      setSaving(true);
      setSaveError('');
      setSaveErrorCode('');
      applyDefinition(specJSONString(values), loadedId ?? 0, revision, launchDesktop, profileId)
        .then((result) => {
          setSaving(false);
          setLoadedId(result.definition.id);
          setRevision(result.definition.revision);
          setLaunchDesktop(result.definition.launch_desktop);
          setEnabledState(result.definition.enabled);
          onSaved(result.definition);
        })
        .catch((error: unknown) => {
          setSaving(false);
          const code = errorCode(error);
          const message = messageOf(error, 'Failed to save automation');
          setSaveErrorCode(code);
          setSaveError(message);
        });
    },
    [applyDefinition, loadedId, revision, launchDesktop, profileId, onSaved],
  );

  const onSubmit = handleSubmit(doSave);

  const handleReload = useCallback(() => {
    if (loadedId === null) return;
    getDefinition(loadedId)
      .then((result) => {
        let parsed: AutomationFormValues;
        try {
          parsed = specToFormValues(result.specJson);
        } catch (error) {
          setSaveErrorCode('');
          setSaveError(messageOf(error, 'Failed to parse automation definition'));
          return;
        }
        reset(parsed);
        setDefinitionYaml(result.specYaml);
        setRevision(result.definition?.revision ?? revision);
        setLaunchDesktop(result.definition?.launch_desktop);
        setProfileId(result.definition?.profile_id ?? profileId);
        setEnabledState(result.definition?.enabled ?? enabled);
        setSaveError('');
        setSaveErrorCode('');
      })
      .catch((error) => {
        setSaveErrorCode('');
        setSaveError(messageOf(error, 'Failed to reload automation definition'));
      });
  }, [getDefinition, loadedId, reset, revision, enabled, profileId]);

  const performDelete = useCallback(() => {
    if (loadedId === null) return;
    deleteDefinition(loadedId)
      .then(() => onDeleted())
      .catch((error) => {
        setDeleteArmed(false);
        setSaveErrorCode('');
        setSaveError(messageOf(error, 'Failed to delete automation'));
      });
  }, [loadedId, deleteDefinition, onDeleted]);

  const handleDeleteClick = useCallback(() => {
    if (!deleteArmed) {
      setDeleteArmed(true);
      return;
    }
    performDelete();
  }, [deleteArmed, performDelete]);

  useEffect(() => {
    if (!deleteArmed) return;
    function handlePointerDown(event: MouseEvent) {
      if (deleteContainerRef.current && !deleteContainerRef.current.contains(event.target as Node)) {
        setDeleteArmed(false);
      }
    }
    document.addEventListener('mousedown', handlePointerDown);
    return () => document.removeEventListener('mousedown', handlePointerDown);
  }, [deleteArmed]);

  const handleToggleEnabled = useCallback(() => {
    if (loadedId === null || enabled === null) return;
    const next = !enabled;
    setEnabledAction(loadedId, next)
      .then(() => setEnabledState(next))
      .catch((error) => {
        setSaveErrorCode('');
        setSaveError(messageOf(error, 'Failed to update automation'));
      });
  }, [loadedId, enabled, setEnabledAction]);

  const handleTriggerChange = useCallback(
    (next: AutomationTrigger) => {
      setValue('trigger', next, { shouldDirty: true, shouldValidate: true });
      setSaveError('');
      setSaveErrorCode('');
    },
    [setValue],
  );

  // RHF's reValidateMode only takes effect after the first handleSubmit, so before
  // that an already-errored field would clear only on its next blur.
  const regField = useCallback(
    (field: keyof AutomationFormValues) => {
      const base = register(field);
      return {
        ...base,
        onChange: (event: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) => {
          base.onChange(event);
          if (errors[field]) void trigger(field);
        },
      };
    },
    [register, errors, trigger],
  );

  const nameRegister = regField('name');

  function addRepository(field: 'repositoriesInclude' | 'repositoriesExclude', raw: string) {
    const canonical = raw.trim().toLowerCase();
    if (canonical === '') return;
    setValue(field, [...getValues(field), repositoryEntry(canonical)], { shouldDirty: true, shouldValidate: true });
  }

  function removeRepository(field: 'repositoriesInclude' | 'repositoriesExclude', index: number) {
    setValue(
      field,
      getValues(field).filter((_, i) => i !== index),
      { shouldDirty: true, shouldValidate: true },
    );
  }

  // Re-registered on every state change so the bridge reads current handlers.
  useEffect(() => {
    setAutomationFormAutomationHandle({
      getState: () => ({
        present: true,
        mode,
        definitionId: loadedId,
        revision,
        status,
        loadError,
        values: {
          ...getValues(),
          repositoriesInclude: getValues('repositoriesInclude').map((entry) => entry.repository),
          repositoriesExclude: getValues('repositoriesExclude').map((entry) => entry.repository),
        },
        errors: flattenFieldErrors(errors as Record<string, unknown>),
        saving,
        saveError,
        saveErrorCode,
        enabled,
        deleteArmed,
      }),
      setValues: (partial) => {
        (Object.keys(partial) as (keyof AutomationFormValues)[]).forEach((key) => {
          const value = partial[key];
          if (key === 'repositoriesInclude' || key === 'repositoriesExclude') {
            setValue(key, partial[key]!.map(repositoryEntry), { shouldDirty: true, shouldValidate: true });
          } else {
            setValue(key, value as never, { shouldDirty: true, shouldValidate: true });
          }
        });
      },
      submit: () => {
        onSubmit();
      },
      reload: handleReload,
      armDelete: () => setDeleteArmed(true),
      confirmDelete: performDelete,
    });
    return () => setAutomationFormAutomationHandle(null);
  }, [
    mode,
    loadedId,
    revision,
    status,
    loadError,
    errors,
    saving,
    saveError,
    saveErrorCode,
    enabled,
    deleteArmed,
    getValues,
    setValue,
    onSubmit,
    handleReload,
    performDelete,
  ]);

  if (status === 'load-error') {
    return (
      <div className="automation-form" data-testid="automation-form">
        <div className="automation-form__header">
          <h3 className="automation-form__title">{mode === 'edit' ? 'Edit automation' : 'New automation'}</h3>
          <button
            type="button"
            className="automation-form__close"
            onClick={onCancel}
            aria-label="Close"
            data-testid="automation-form-close"
          >
            ✕
          </button>
        </div>
        <p className="automation-form__load-error" data-testid="automation-form-load-error">
          {loadError}
        </p>
      </div>
    );
  }

  if (status === 'loading') {
    return (
      <div className="automation-form" data-testid="automation-form">
        <div className="automation-form__header">
          <h3 className="automation-form__title">Edit automation</h3>
          <button
            type="button"
            className="automation-form__close"
            onClick={onCancel}
            aria-label="Close"
            data-testid="automation-form-close"
          >
            ✕
          </button>
        </div>
        <p className="automation-form__status">Loading…</p>
      </div>
    );
  }

  const values = watch();

  function fieldError(field: keyof AutomationFormValues): string | undefined {
    const entry = errors[field] as { message?: string } | undefined;
    return entry?.message;
  }

  const fields: AutomationFields = { values, fieldError, regField, setValue, register };
  return (
    <form className="automation-form" data-testid="automation-form" onSubmit={onSubmit}>
      <div className="automation-form__header">
        <h3 className="automation-form__title">{mode === 'edit' ? 'Edit automation' : 'New automation'}</h3>
        {mode === 'edit' && enabled !== null && (
          <button
            type="button"
            role="switch"
            aria-checked={enabled}
            className={enabled ? 'automation-form__enabled automation-form__enabled--on' : 'automation-form__enabled'}
            onClick={handleToggleEnabled}
            data-testid="automation-form-enabled"
          >
            {enabled ? 'Enabled' : 'Disabled'}
          </button>
        )}
        <button
          type="button"
          className="automation-form__close"
          onClick={onCancel}
          aria-label="Close"
          data-testid="automation-form-close"
        >
          ✕
        </button>
      </div>

      <fieldset className="automation-form__body" disabled={saving}>
        {saving && (
          <p className="automation-form__hint" data-testid="automation-form-saving-hint">
            Saving…
          </p>
        )}

        <AutomationNameFields mode={mode} loadedId={loadedId} fieldError={fieldError} nameRegister={nameRegister} />

        <AutomationTriggerFields
          fields={fields}
          onTriggerChange={handleTriggerChange}
          onScheduleValidityChange={setScheduleValid}
          github={
            <AutomationGitHubFields
              fields={fields}
              includeInput={includeInput}
              setIncludeInput={setIncludeInput}
              excludeInput={excludeInput}
              setExcludeInput={setExcludeInput}
              addRepository={addRepository}
              removeRepository={removeRepository}
            />
          }
        />

        <AutomationLaunchFields
          fields={fields}
          desktop={
            <LaunchDesktopSelect
              kind={LaunchDesktopKind.Automation}
              itemId={loadedId === null ? null : String(loadedId)}
              profileId={profileId}
              defaultName={values.name}
              value={launchDesktop}
              onChange={setLaunchDesktop}
              disabled={saving}
            />
          }
        />

        <section className="automation-form__section">
          <span className="automation-form__section-label">Prompt</span>
          <textarea
            className={
              fieldError('prompt')
                ? 'automation-form__textarea automation-form__textarea--invalid'
                : 'automation-form__textarea'
            }
            data-testid="automation-form-prompt"
            {...regField('prompt')}
          />
          <p className="automation-form__hint">
            This is the instruction the agent receives. Trigger details arrive separately as structured context — they
            can never rewrite this prompt.
          </p>
          {fieldError('prompt') && (
            <p className="automation-form__field-error" data-testid="automation-form-error-prompt">
              {fieldError('prompt')}
            </p>
          )}
        </section>

        <AutomationAdvanced
          fields={fields}
          overrides={{ fields: overrideFields, append: appendOverride, remove: removeOverride }}
          definitionYaml={definitionYaml}
        />
      </fieldset>

      <AutomationFormActions
        mode={mode}
        saving={saving}
        saveBlocked={values.trigger === 'scheduled' && !scheduleValid}
        saveError={saveError}
        saveErrorCode={saveErrorCode}
        deleteArmed={deleteArmed}
        deleteContainerRef={deleteContainerRef}
        onReload={handleReload}
        onDelete={handleDeleteClick}
        onCancel={onCancel}
      />
    </form>
  );
}

export function automationFormKey(definitionId: number | null): string {
  return definitionId === null ? '__new__' : String(definitionId);
}
