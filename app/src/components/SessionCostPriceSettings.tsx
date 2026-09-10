import { useAutosaveSetting, useSettingsAutosave, type SaveSetting } from './SettingsAutosave';
import { useEffect, useMemo, useRef, useState } from 'react';

export const SESSION_COST_PRICE_PREFIX = 'session_cost.price.';

const RATE_FIELDS = [
  { key: 'input_usd_per_mtok', label: 'Input' },
  { key: 'output_usd_per_mtok', label: 'Output' },
  { key: 'cache_read_usd_per_mtok', label: 'Cache read' },
  { key: 'cache_write_5m_usd_per_mtok', label: 'Cache write · 5m' },
  { key: 'cache_write_1h_usd_per_mtok', label: 'Cache write · 1h' },
] as const;

type RateField = (typeof RATE_FIELDS)[number]['key'];
type PriceDraft = Record<RateField, string>;
type PriceCard = Record<RateField, number>;

const blankDraft = (): PriceDraft => ({
  input_usd_per_mtok: '',
  output_usd_per_mtok: '',
  cache_read_usd_per_mtok: '',
  cache_write_5m_usd_per_mtok: '',
  cache_write_1h_usd_per_mtok: '',
});

function parsePriceCard(raw: string): PriceCard | null {
  try {
    const value = JSON.parse(raw) as Partial<Record<RateField, unknown>>;
    if (
      typeof value !== 'object'
      || value === null
      || Array.isArray(value)
      || Object.keys(value).some((key) => !RATE_FIELDS.some((field) => field.key === key))
    ) return null;
    const card = {} as PriceCard;
    for (const { key } of RATE_FIELDS) {
      const rate = value?.[key];
      if (typeof rate !== 'number' || !Number.isFinite(rate) || rate < 0) return null;
      card[key] = rate;
    }
    return card;
  } catch {
    return null;
  }
}

function draftFromRaw(raw: string): PriceDraft {
  const card = parsePriceCard(raw);
  if (!card) return blankDraft();
  return Object.fromEntries(RATE_FIELDS.map(({ key }) => [key, String(card[key])])) as PriceDraft;
}

function priceCardFromDraft(draft: PriceDraft): PriceCard | null {
  const card = {} as PriceCard;
  for (const { key } of RATE_FIELDS) {
    if (String(draft[key]).trim() === '') return null;
    const rate = Number(draft[key]);
    if (!Number.isFinite(rate) || rate < 0) return null;
    card[key] = rate;
  }
  return card;
}

function serializePriceCard(card: PriceCard): string {
  return JSON.stringify(Object.fromEntries(RATE_FIELDS.map(({ key }) => [key, card[key]])));
}

interface RateInputsProps {
  draft: PriceDraft;
  idPrefix: string;
  onChange: (key: RateField, value: string) => void;
  onCommit: () => void;
}

function RateInputs({ draft, idPrefix, onChange, onCommit }: RateInputsProps) {
  return (
    <div className="settings-price-grid">
      {RATE_FIELDS.map(({ key, label }) => {
        const inputId = `${idPrefix}-${key}`;
        return (
          <div className="settings-field" key={key}>
            <label className="settings-label" htmlFor={inputId}>{label} · USD/MTok</label>
            <input
              id={inputId}
              data-testid={inputId}
              type="number"
              min="0"
              step="any"
              inputMode="decimal"
              className="settings-input settings-price-input"
              value={draft[key]}
              onChange={(event) => onChange(key, event.target.value)}
              onBlur={onCommit}
              onKeyDown={(event) => { if (event.key === 'Enter') onCommit(); }}
              placeholder="0"
            />
          </div>
        );
      })}
    </div>
  );
}

interface ExistingPriceOverrideProps {
  modelId: string;
  raw: string;
  onSetSetting: SaveSetting;
}

function ExistingPriceOverride({ modelId, raw, onSetSetting }: ExistingPriceOverrideProps) {
  const parsed = useMemo(() => parsePriceCard(raw), [raw]);
  const settingKey = `${SESSION_COST_PRICE_PREFIX}${modelId}`;
  const field = useAutosaveSetting(settingKey, JSON.stringify(draftFromRaw(raw)), onSetSetting, (value) => {
    if (value === '') return '';
    const card = priceCardFromDraft(JSON.parse(value) as PriceDraft);
    if (!card) throw new Error(`Complete every rate for ${modelId} with a non-negative number; use 0 for unused rates.`);
    return serializePriceCard(card);
  });
  const draft = field.value ? JSON.parse(field.value) as PriceDraft : draftFromRaw(raw);
  const idPrefix = `settings-price-${modelId}`;

  return (
    <div className="settings-price-card">
      <div className="settings-price-card-head">
        <code className="settings-price-model">{modelId}</code>
        <div className="settings-price-actions">
          <button
            type="button"
            className="settings-action danger"
            data-testid={`${idPrefix}-remove`}
            onClick={() => void field.apply('')}
          >
            Remove
          </button>
        </div>
      </div>
      {!parsed && (
        <div className="settings-warning">
          This saved override is invalid. Complete every rate to repair it, or remove it.
        </div>
      )}
      <RateInputs
        draft={draft}
        idPrefix={idPrefix}
        onChange={(key, value) => field.set(JSON.stringify({ ...draft, [key]: value }))}
        onCommit={field.onBlur}
      />
    </div>
  );
}

interface SessionCostPriceSettingsProps {
  settings: Record<string, string>;
  onSetSetting: SaveSetting;
}

export function SessionCostPriceSettings({ settings, onSetSetting }: SessionCostPriceSettingsProps) {
  const overrides = useMemo(() => {
    const result: Array<{ modelId: string; raw: string }> = [];
    for (const [key, raw] of Object.entries(settings)) {
      if (!key.startsWith(SESSION_COST_PRICE_PREFIX) || raw.trim() === '') continue;
      const modelId = key.slice(SESSION_COST_PRICE_PREFIX.length);
      if (modelId.trim() !== '') result.push({ modelId, raw });
    }
    return result.sort((left, right) => left.modelId.localeCompare(right.modelId));
  }, [settings]);
  const existingModelIds = useMemo(() => new Set(overrides.map(({ modelId }) => modelId)), [overrides]);
  const autosave = useSettingsAutosave();
  const [adding, setAdding] = useState(false);
  const [addError, setAddError] = useState<string | null>(null);
  const submitted = useRef<{ key: string; value: string } | null>(null);
  const [modelId, setModelId] = useState('');
  const [draft, setDraft] = useState<PriceDraft>(blankDraft);
  const card = priceCardFromDraft(draft);
  const normalizedModelId = modelId.trim();
  const canAdd = Boolean(card && normalizedModelId && !existingModelIds.has(normalizedModelId));

  useEffect(() => {
    const pending = submitted.current;
    if (!pending || settings[pending.key] !== pending.value) return;
    if (pending.key === `${SESSION_COST_PRICE_PREFIX}${normalizedModelId}` && card && serializePriceCard(card) === pending.value) {
      setModelId('');
      setDraft(blankDraft());
      submitted.current = null;
    }
  }, [settings, normalizedModelId, card]);

  const addOverride = async () => {
    if (!card || !canAdd || adding) return;
    const key = `${SESSION_COST_PRICE_PREFIX}${normalizedModelId}`;
    const value = serializePriceCard(card);
    submitted.current = { key, value };
    setAdding(true);
    setAddError(null);
    try {
      if (autosave) {
        autosave.ensure(key, settings[key] || '');
        autosave.set(key, value);
        if (!await autosave.commit(key)) return;
      } else {
        await onSetSetting(key, value);
      }
      setModelId('');
      setDraft(blankDraft());
      submitted.current = null;
    } catch (error) {
      setAddError(error instanceof Error ? error.message : String(error));
    } finally {
      setAdding(false);
    }
  };

  return (
    <section className="settings-block" data-testid="settings-session-cost-prices">
      <div className="settings-block-intro">
        <div className="settings-kicker">Agents</div>
        <h3>Model pricing</h3>
        <p className="settings-description">
          Add exact model IDs that are missing from the built-in price table, or correct stale
          rates. Every field is USD per million tokens; cache reads and writes keep their own rates.
        </p>
      </div>
      <div className="settings-block-body">
        {overrides.length > 0 && (
          <div className="settings-price-list">
            {overrides.map(({ modelId: overrideModelId, raw }) => (
              <ExistingPriceOverride
                key={overrideModelId}
                modelId={overrideModelId}
                raw={raw}
                onSetSetting={onSetSetting}
              />
            ))}
          </div>
        )}

        <fieldset className="settings-price-card settings-price-card--new" disabled={adding}>
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-price-new-model">Exact model ID</label>
            <input
              id="settings-price-new-model"
              data-testid="settings-price-new-model"
              type="text"
              className="settings-input"
              value={modelId}
              onChange={(event) => setModelId(event.target.value)}
              onBlur={addOverride}
              onKeyDown={(event) => { if (event.key === 'Enter') addOverride(); }}
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              placeholder="provider-model-id"
            />
          </div>
          <RateInputs
            draft={draft}
            idPrefix="settings-price-new"
            onChange={(key, value) => setDraft((current) => ({ ...current, [key]: value }))}
            onCommit={addOverride}
          />
          {normalizedModelId && existingModelIds.has(normalizedModelId) && (
            <div className="settings-warning">That model already has an override above.</div>
          )}
          {addError && <div className="settings-warning" role="alert">{addError} <button type="button" className="settings-action" onClick={() => void addOverride()}>Retry</button></div>}
          {normalizedModelId && !card && <p className="settings-hint">Complete every rate with a non-negative number.</p>}
          <p className="settings-hint">The override is added automatically when the model ID and every rate are filled. Use 0 for unused rates.</p>
        </fieldset>
      </div>
    </section>
  );
}
