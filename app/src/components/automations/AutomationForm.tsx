import { LaunchDesktopKind } from '../../types/generated';
import { LaunchDesktopSelect } from '../LaunchDesktopSelect';
import { useProfilesStore } from '../../store/profiles';
import type { LaunchDesktopSetting } from '../../types/generated';
// The host remounts on a fresh key per target, so mount already means an explicit
// load: edit mode reads once on mount and never re-fetches on definitionId churn.
import { useCallback, useEffect, useRef, useState, type ComponentProps, type ReactNode, type RefObject } from 'react';
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
  repositoryEntry,
  specJSONString,
  specToFormValues,
} from './automationFormModel';
import type { AutomationAgent } from './automationFormModel';
import { useHarnessChoices } from '../../hooks/useHarnesses';
import { useKnownModelName } from '../../hooks/useHarnessRoute';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { HarnessRouteChip } from '../HarnessRouteChip';
const AUTOMATION_HARNESSES = ['codex', 'claude'];
import { compiledSentenceSegments, compiledSentenceText, cronPhrase } from './automationCompiledSentence';
import { setAutomationFormAutomationHandle } from './automationFormAutomation';
import './AutomationForm.css';

export interface AutomationFormProps {
  definitionId: number | null;
  getDefinition: (definitionId: number) => Promise<{ specJson: string; definition?: AutomationDefinitionSummary }>;
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
    scheduleCron: '',
    continuity: 'fresh',
    catchUp: '',
    repositoriesInclude: [],
    repositoriesExclude: [],
    agent: 'codex',
    model: '',
    effort: '',
    executable: '',
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
  const { regField, fieldError } = fields;
  return (
    <div className="automation-form__field">
      <label className="automation-form__label" htmlFor="automation-form-directory-path">
        Directory
      </label>
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
      {fieldError('directoryPath') && (
        <p className="automation-form__field-error" data-testid="automation-form-error-directoryPath">
          {fieldError('directoryPath')}
        </p>
      )}
    </div>
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
  overrides,
}: {
  fields: AutomationFields;
  includeInput: string;
  setIncludeInput: (value: string) => void;
  excludeInput: string;
  setExcludeInput: (value: string) => void;
  addRepository: (field: 'repositoriesInclude' | 'repositoriesExclude', value: string) => void;
  removeRepository: (field: 'repositoriesInclude' | 'repositoriesExclude', index: number) => void;
  overrides: {
    fields: FieldArrayWithId<AutomationFormValues, 'repositoryOverrides'>[];
    append: UseFieldArrayAppend<AutomationFormValues, 'repositoryOverrides'>;
    remove: UseFieldArrayRemove;
  };
}) {
  const { values, register } = fields;
  const { fields: overrideFields, append: appendOverride, remove: removeOverride } = overrides;
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

      <details className="automation-form__advanced">
        <summary>Advanced</summary>
        <div className="automation-form__overrides">
          {overrideFields.map((field, index) => (
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
              <button
                type="button"
                onClick={() => removeOverride(index)}
                data-testid={`automation-form-overrides-remove-${index}`}
              >
                Remove
              </button>
            </div>
          ))}
          <button
            type="button"
            onClick={() => appendOverride({ repository: '', path: '' })}
            data-testid="automation-form-overrides-add"
          >
            Add override
          </button>
        </div>
      </details>
    </div>
  );
}

function AutomationTriggerFields({
  fields,
  onTriggerChange,
  github,
}: {
  fields: AutomationFields;
  onTriggerChange: (trigger: AutomationTrigger) => void;
  github: ReactNode;
}) {
  const { values, regField, fieldError, setValue } = fields;
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
          <AutomationDirectoryField fields={fields} />
        </div>
      )}

      {values.trigger === 'scheduled' && (
        <div className="automation-form__trigger-section">
          <div className="automation-form__field">
            <label className="automation-form__label" htmlFor="automation-form-cron">
              Schedule (cron)
            </label>
            <input
              id="automation-form-cron"
              className={
                fieldError('scheduleCron')
                  ? 'automation-form__input automation-form__input--invalid'
                  : 'automation-form__input'
              }
              data-testid="automation-form-cron"
              placeholder="0 9 * * *"
              {...regField('scheduleCron')}
            />
            <p className="automation-form__cron-phrase" data-testid="automation-form-cron-phrase">
              {cronPhrase(values.scheduleCron) ?? 'not set yet'}
            </p>
            {fieldError('scheduleCron') && (
              <p className="automation-form__field-error" data-testid="automation-form-error-scheduleCron">
                {fieldError('scheduleCron')}
              </p>
            )}
          </div>

          <div className="automation-form__field">
            <span className="automation-form__label">Worker</span>
            <div className="automation-form__segmented">
              <button
                type="button"
                className={
                  values.continuity === 'fresh'
                    ? 'automation-form__segment automation-form__segment--selected'
                    : 'automation-form__segment'
                }
                onClick={() => setValue('continuity', 'fresh', { shouldDirty: true, shouldValidate: true })}
                data-testid="automation-form-continuity-fresh"
              >
                Fresh
              </button>
              <button
                type="button"
                className={
                  values.continuity === 'singleton'
                    ? 'automation-form__segment automation-form__segment--selected'
                    : 'automation-form__segment'
                }
                onClick={() => setValue('continuity', 'singleton', { shouldDirty: true, shouldValidate: true })}
                data-testid="automation-form-continuity-singleton"
              >
                Singleton
              </button>
            </div>
          </div>

          <div className="automation-form__field">
            <span className="automation-form__label">Missed runs</span>
            <div className="automation-form__segmented">
              <button
                type="button"
                className={
                  values.catchUp === 'skip'
                    ? 'automation-form__segment automation-form__segment--selected'
                    : 'automation-form__segment'
                }
                onClick={() => setValue('catchUp', 'skip', { shouldDirty: true, shouldValidate: true })}
                data-testid="automation-form-catchup-skip"
              >
                Skip
              </button>
              <button
                type="button"
                className={
                  values.catchUp === 'latest'
                    ? 'automation-form__segment automation-form__segment--selected'
                    : 'automation-form__segment'
                }
                onClick={() => setValue('catchUp', 'latest', { shouldDirty: true, shouldValidate: true })}
                data-testid="automation-form-catchup-latest"
              >
                Latest
              </button>
            </div>
            {fieldError('catchUp') && (
              <p className="automation-form__field-error" data-testid="automation-form-error-catchUp">
                {fieldError('catchUp')}
              </p>
            )}
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
  const { values, regField, fieldError, setValue } = fields;
  const { settings } = useDaemonApi();
  const { harnesses, error, retry } = useHarnessChoices(AUTOMATION_HARNESSES, settings);
  const routeHarnesses = values.executable.trim() ? harnesses.map(harness => ({ ...harness, discovery: false })) : harnesses;
  return (
    <section className="automation-form__section">
      <span className="automation-form__section-label">Runs as</span>
      {desktop}
      {error && <div className="settings-warning" role="alert">{error}<button type="button" className="settings-action quiet" onClick={retry}>Retry</button></div>}
      <HarnessRouteChip variant="field" aria-label="Automation model" data-testid="automation-form-route" value={{ harness: values.agent, provider: '', model: values.model, effort: values.effort }} rules={{ harnesses: routeHarnesses }} onChange={route => {
        setValue('agent', route.harness as AutomationAgent, { shouldDirty: true, shouldValidate: true });
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

      <details className="automation-form__advanced">
        <summary>Advanced</summary>
        <div className="automation-form__field">
          <label className="automation-form__label" htmlFor="automation-form-executable">
            Executable override
          </label>
          <input
            id="automation-form-executable"
            className="automation-form__input"
            placeholder="Default from PATH"
            data-testid="automation-form-executable"
            {...regField('executable')}
          />
        </div>
      </details>
    </section>
  );
}

function AutomationSentence({ values }: { values: AutomationFormValues }) {
  const modelName = useKnownModelName(values.agent, '', values.model);
  const sentenceSegments = compiledSentenceSegments(values, modelName);
  return (
    <p
      className="automation-form__sentence"
      aria-live="polite"
      aria-label="This automation, in plain words"
      data-testid="automation-form-sentence"
    >
      {sentenceSegments.map((segment, index) => {
        const className =
          segment.emphasis === 'accent'
            ? 'automation-form__sentence-accent'
            : segment.emphasis === 'strong'
              ? 'automation-form__sentence-strong'
              : segment.emphasis === 'mono'
                ? 'automation-form__sentence-mono'
                : undefined;
        return className ? (
          <span className={className} key={index}>
            {segment.text}
          </span>
        ) : (
          <span key={index}>{segment.text}</span>
        );
      })}
    </p>
  );
}

function AutomationFormActions({
  mode,
  saving,
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
          <button type="submit" className="automation-form__save" disabled={saving} data-testid="automation-form-save">
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
        compiledSentence: compiledSentenceText(getValues()),
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
          github={
            <AutomationGitHubFields
              fields={fields}
              includeInput={includeInput}
              setIncludeInput={setIncludeInput}
              excludeInput={excludeInput}
              setExcludeInput={setExcludeInput}
              addRepository={addRepository}
              removeRepository={removeRepository}
              overrides={{ fields: overrideFields, append: appendOverride, remove: removeOverride }}
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
      </fieldset>

      <AutomationSentence values={values} />

      <AutomationFormActions
        mode={mode}
        saving={saving}
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
