import { useEffect, useMemo, useState } from 'react';
import { useDaemonApi } from '../../contexts/DaemonApiContext';
import { localTimeZone } from './automationFormModel';
import { ALL_DAYS, WEEKDAYS, pad, parseFriendly, toCron, type FriendlySchedule } from './automationSchedule';

type ScheduleTab = FriendlySchedule['kind'] | 'cron';

const TABS: [ScheduleTab, string][] = [
  ['every', 'Every'],
  ['days', 'On days'],
  ['monthly', 'Monthly'],
  ['cron', 'Cron'],
];
const MINUTE_STEPS = [1, 2, 5, 10, 15, 20, 30];
const HOUR_STEPS = [1, 2, 3, 4, 6, 8, 12];
const DAY_BUTTONS: [number, string, string][] = [
  [1, 'M', 'Monday'],
  [2, 'T', 'Tuesday'],
  [3, 'W', 'Wednesday'],
  [4, 'T', 'Thursday'],
  [5, 'F', 'Friday'],
  [6, 'S', 'Saturday'],
  [0, 'S', 'Sunday'],
];
const SCHEDULER_TICK_MS = 60_000;

function timeZoneNames(): string[] {
  return ['UTC', ...Intl.supportedValuesOf('timeZone').filter((zone) => zone !== 'UTC')];
}

function displayableZone(timeZone: string, fallback: string): string {
  try {
    new Intl.DateTimeFormat('en-GB', { timeZone });
    return timeZone;
  } catch {
    return fallback;
  }
}

function longDate(at: Date, timeZone: string): string {
  return new Intl.DateTimeFormat('en-GB', {
    timeZone, weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  }).format(at).replace(/, (\d+ \w+),/, ' $1,');
}

function clock(at: Date, timeZone: string): string {
  return new Intl.DateTimeFormat('en-GB', { timeZone, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(at);
}

function until(at: Date): string {
  const minutes = Math.max(0, Math.round((at.getTime() - Date.now()) / 60_000));
  if (minutes < 60) return `in ${minutes}m`;
  const hours = Math.round(minutes / 60);
  return hours < 48 ? `in ${hours}h` : `in ${Math.round(hours / 24)}d`;
}

function DayButtons({ days, onChange }: { days: number[]; onChange: (days: number[]) => void }) {
  return (
    <div className="schedule-composer__line">
      <span className="schedule-composer__days" role="group" aria-label="Days">
        {DAY_BUTTONS.map(([day, letter, name]) => {
          const on = days.includes(day);
          return (
            <button
              key={day}
              type="button"
              className={on ? 'schedule-composer__day schedule-composer__day--on' : 'schedule-composer__day'}
              aria-pressed={on}
              aria-label={name}
              data-testid={`automation-form-schedule-day-${day}`}
              onClick={() => {
                if (on && days.length === 1) return;
                onChange(on ? days.filter((other) => other !== day) : [...days, day]);
              }}
            >
              {letter}
            </button>
          );
        })}
      </span>
      <button type="button" className="schedule-composer__quiet" onClick={() => onChange([...ALL_DAYS])}>Every day</button>
      <button type="button" className="schedule-composer__quiet" onClick={() => onChange([...WEEKDAYS])}>Weekdays</button>
    </div>
  );
}

function TimeField({ hour, minute, onChange }: { hour: number; minute: number; onChange: (hour: number, minute: number) => void }) {
  return (
    <input
      className="automation-form__input schedule-composer__compact"
      type="time"
      aria-label="Time"
      data-testid="automation-form-schedule-at"
      value={`${pad(hour)}:${pad(minute)}`}
      onChange={(event) => {
        if (!event.target.value) return;
        const [nextHour, nextMinute] = event.target.value.split(':').map(Number);
        onChange(nextHour, nextMinute);
      }}
    />
  );
}

function HourSelect({ value, suffix, label, onChange }: { value: number; suffix: string; label: string; onChange: (hour: number) => void }) {
  return (
    <select className="automation-form__input schedule-composer__compact" aria-label={label} value={value} onChange={(event) => onChange(+event.target.value)}>
      {Array.from({ length: 24 }, (_, hour) => <option key={hour} value={hour}>{pad(hour)}{suffix}</option>)}
    </select>
  );
}

function EveryControls({ schedule, onChange }: { schedule: Extract<FriendlySchedule, { kind: 'every' }>; onChange: (schedule: FriendlySchedule) => void }) {
  const steps = schedule.unit === 'min' ? MINUTE_STEPS : HOUR_STEPS;
  const offered = steps.includes(schedule.n) ? steps : [...steps, schedule.n].sort((a, b) => a - b);
  const window = schedule.win;
  return (
    <>
      <div className="schedule-composer__line">
        <span>Every</span>
        <select
          className="automation-form__input schedule-composer__compact"
          aria-label="Interval"
          data-testid="automation-form-schedule-every-n"
          value={schedule.n}
          onChange={(event) => onChange({ ...schedule, n: +event.target.value })}
        >
          {offered.map((n) => <option key={n} value={n}>{n}</option>)}
        </select>
        <select
          className="automation-form__input schedule-composer__compact"
          aria-label="Unit"
          data-testid="automation-form-schedule-every-unit"
          value={schedule.unit}
          onChange={(event) => {
            const unit = event.target.value as 'min' | 'hour';
            onChange({ ...schedule, unit, n: unit === 'min' ? 30 : 1, minute: 0 });
          }}
        >
          <option value="min">minutes</option>
          <option value="hour">hours</option>
        </select>
        {schedule.unit === 'hour' && (
          <>
            <span>at minute</span>
            <input
              className="automation-form__input schedule-composer__compact"
              type="number"
              min={0}
              max={59}
              aria-label="Minute"
              value={schedule.minute}
              onChange={(event) => onChange({ ...schedule, minute: Math.min(59, Math.max(0, +event.target.value || 0)) })}
            />
          </>
        )}
      </div>
      <div className="schedule-composer__line">
        <label className="schedule-composer__check">
          <input
            type="checkbox"
            data-testid="automation-form-schedule-window"
            checked={window !== null}
            onChange={(event) => onChange({ ...schedule, win: event.target.checked ? [9, 17] : null })}
          />
          Only between
        </label>
        {window && (
          <>
            <HourSelect label="From hour" suffix=":00" value={window[0]} onChange={(from) => onChange({ ...schedule, win: [from, Math.max(from, window[1])] })} />
            <span>and</span>
            <HourSelect label="To hour" suffix=":59" value={window[1]} onChange={(to) => onChange({ ...schedule, win: [Math.min(window[0], to), to] })} />
          </>
        )}
      </div>
      <DayButtons days={schedule.days} onChange={(days) => onChange({ ...schedule, days })} />
    </>
  );
}

function FriendlyControls({ schedule, onChange }: { schedule: FriendlySchedule; onChange: (schedule: FriendlySchedule) => void }) {
  if (schedule.kind === 'every') return <EveryControls schedule={schedule} onChange={onChange} />;
  if (schedule.kind === 'days') {
    return (
      <>
        <DayButtons days={schedule.days} onChange={(days) => onChange({ ...schedule, days })} />
        <div className="schedule-composer__line">
          <span>at</span>
          <TimeField hour={schedule.hour} minute={schedule.minute} onChange={(hour, minute) => onChange({ ...schedule, hour, minute })} />
        </div>
      </>
    );
  }
  return (
    <>
      <div className="schedule-composer__line">
        <span>On day</span>
        <select
          className="automation-form__input schedule-composer__compact"
          aria-label="Day of month"
          data-testid="automation-form-schedule-month-day"
          value={schedule.day}
          onChange={(event) => onChange({ ...schedule, day: +event.target.value })}
        >
          {Array.from({ length: 31 }, (_, index) => <option key={index + 1} value={index + 1}>{index + 1}</option>)}
        </select>
        <span>at</span>
        <TimeField hour={schedule.hour} minute={schedule.minute} onChange={(hour, minute) => onChange({ ...schedule, hour, minute })} />
      </div>
      {schedule.day > 28 && <span className="automation-form__hint">Months without this day are skipped.</span>}
    </>
  );
}

function scheduleForTab(tab: FriendlySchedule['kind'], current: FriendlySchedule | null): FriendlySchedule {
  const days = current && 'days' in current ? current.days : [...ALL_DAYS];
  const hour = current && current.kind !== 'every' ? current.hour : 9;
  const minute = current && current.kind !== 'every' ? current.minute : 0;
  if (tab === 'every') return { kind: 'every', unit: 'min', n: 30, win: null, days, minute: 0 };
  if (tab === 'days') return { kind: 'days', hour, minute, days };
  return { kind: 'monthly', day: current?.kind === 'monthly' ? current.day : 1, hour, minute };
}

export interface ScheduleComposerProps {
  cron: string;
  timeZone: string;
  guarded: boolean;
  onCronChange: (cron: string) => void;
  onTimeZoneChange: (timeZone: string) => void;
  onValidityChange: (valid: boolean) => void;
}

export function ScheduleComposer({ cron, timeZone, guarded, onCronChange, onTimeZoneChange, onValidityChange }: ScheduleComposerProps) {
  const { previewAutomationSchedule } = useDaemonApi();
  const [cronTabChosen, setCronTabChosen] = useState(false);
  const [replaced, setReplaced] = useState<string | null>(null);
  const [preview, setPreview] = useState<{ times: string[]; error: string }>({ times: [], error: '' });
  const zones = useMemo(timeZoneNames, []);
  const viewerZone = useMemo(localTimeZone, []);

  const schedule = parseFriendly(cron);
  const tab: ScheduleTab = cronTabChosen || !schedule ? 'cron' : schedule.kind;

  useEffect(() => {
    let latest = true;
    previewAutomationSchedule(cron, timeZone)
      .then((result) => {
        if (!latest) return;
        setPreview({ times: result.times ?? [], error: result.error ?? '' });
        onValidityChange(!result.error);
      })
      .catch((error: unknown) => {
        if (!latest) return;
        setPreview({ times: [], error: error instanceof Error ? error.message : 'Could not preview this schedule.' });
        onValidityChange(true);
      });
    return () => {
      latest = false;
    };
  }, [cron, timeZone, previewAutomationSchedule, onValidityChange]);

  function chooseTab(next: ScheduleTab) {
    if (next === 'cron') {
      setCronTabChosen(true);
      return;
    }
    setCronTabChosen(false);
    if (schedule?.kind === next) return;
    if (!schedule && cron.trim() !== '') setReplaced(cron);
    onCronChange(toCron(scheduleForTab(next, schedule)));
  }

  const times = preview.times.map((time) => new Date(time));
  const shownZone = displayableZone(timeZone, viewerZone);
  const runsFasterThanTheScheduler = times.length > 1 && times[1].getTime() - times[0].getTime() < SCHEDULER_TICK_MS;

  return (
    <div className="schedule-composer" data-testid="automation-form-schedule">
      <div className="automation-form__segmented" role="group" aria-label="Schedule type">
        {TABS.map(([value, label]) => (
          <button
            key={value}
            type="button"
            aria-pressed={tab === value}
            className={tab === value ? 'automation-form__segment automation-form__segment--selected' : 'automation-form__segment'}
            data-testid={`automation-form-schedule-tab-${value}`}
            onClick={() => chooseTab(value)}
          >
            {label}
          </button>
        ))}
      </div>

      {tab !== 'cron' && schedule && <FriendlyControls schedule={schedule} onChange={(next) => onCronChange(toCron(next))} />}
      {tab === 'cron' && !schedule && cron.trim() !== '' && (
        <span className="automation-form__hint">This schedule is kept as written. The controls can&apos;t express it.</span>
      )}
      {replaced !== null && (
        <span className="automation-form__hint" data-testid="automation-form-schedule-replaced">
          Replaced <code>{replaced}</code>.{' '}
          <button
            type="button"
            className="schedule-composer__quiet"
            data-testid="automation-form-schedule-undo"
            onClick={() => {
              onCronChange(replaced);
              setReplaced(null);
            }}
          >
            Undo
          </button>
        </span>
      )}

      <div className="automation-form__field">
        <label className="automation-form__label" htmlFor="automation-form-cron">Cron</label>
        <input
          id="automation-form-cron"
          className={preview.error ? 'automation-form__input automation-form__input--invalid schedule-composer__cron' : 'automation-form__input schedule-composer__cron'}
          data-testid="automation-form-cron"
          spellCheck={false}
          autoComplete="off"
          value={cron}
          onChange={(event) => {
            setReplaced(null);
            onCronChange(event.target.value);
          }}
        />
      </div>

      {preview.error && <p className="automation-form__field-error" data-testid="automation-form-schedule-error">{preview.error}</p>}
      {runsFasterThanTheScheduler && <span className="automation-form__hint">attn checks schedules once a minute, so this runs at most once a minute.</span>}

      <div className="schedule-composer__next" data-testid="automation-form-next-runs">
        <div className="schedule-composer__next-head">
          <span className="automation-form__label">{guarded ? 'Next checks' : 'Next runs'}</span>
          <label className="schedule-composer__zone">
            Time zone
            <input
              className="automation-form__input schedule-composer__compact"
              list="automation-form-time-zones"
              data-testid="automation-form-time-zone"
              spellCheck={false}
              autoComplete="off"
              value={timeZone}
              onChange={(event) => onTimeZoneChange(event.target.value)}
            />
            <datalist id="automation-form-time-zones">{zones.map((zone) => <option key={zone} value={zone} />)}</datalist>
          </label>
        </div>
        {times.length > 0 && (
          <ol className="schedule-composer__runs">
            {times.map((at) => (
              <li key={at.getTime()}>
                <span>
                  {longDate(at, shownZone)}
                  {shownZone !== viewerZone && <span className="automation-form__hint"> ({clock(at, viewerZone)} here)</span>}
                </span>
                <span className="automation-form__hint">{until(at)}</span>
              </li>
            ))}
          </ol>
        )}
      </div>
    </div>
  );
}
