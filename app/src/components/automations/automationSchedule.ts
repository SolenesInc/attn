export type FriendlySchedule =
  | { kind: 'every'; unit: 'min' | 'hour'; n: number; win: [number, number] | null; days: number[]; minute: number }
  | { kind: 'days'; hour: number; minute: number; days: number[] }
  | { kind: 'monthly'; day: number; hour: number; minute: number };

export const ALL_DAYS = [0, 1, 2, 3, 4, 5, 6];
export const WEEKDAYS = [1, 2, 3, 4, 5];

const DAY_ABBREVIATIONS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const DAY_PLURALS = ['Sundays', 'Mondays', 'Tuesdays', 'Wednesdays', 'Thursdays', 'Fridays', 'Saturdays'];
const DESCRIPTOR_WORDS: Record<string, string> = {
  '@yearly': 'Yearly',
  '@annually': 'Yearly',
  '@monthly': 'Monthly',
  '@weekly': 'Weekly',
  '@daily': 'Daily at 00:00',
  '@midnight': 'Daily at 00:00',
  '@hourly': 'Hourly',
};

export function pad(n: number): string {
  return String(n).padStart(2, '0');
}

function isInt(text: string): boolean {
  return /^\d+$/.test(text);
}

function daysToField(days: number[]): string {
  const sorted = [...new Set(days)].sort((a, b) => a - b);
  if (sorted.length === 7) return '*';
  const parts: string[] = [];
  for (let i = 0; i < sorted.length; ) {
    let j = i;
    while (j + 1 < sorted.length && sorted[j + 1] === sorted[j] + 1) j++;
    if (j - i >= 2) parts.push(`${sorted[i]}-${sorted[j]}`);
    else for (let k = i; k <= j; k++) parts.push(String(sorted[k]));
    i = j + 1;
  }
  return parts.join(',');
}

function parseDaysField(field: string): number[] | null {
  if (field === '*') return [...ALL_DAYS];
  const days: number[] = [];
  for (const part of field.split(',')) {
    const range = part.match(/^(\d)-(\d)$/);
    if (range) {
      for (let day = +range[1]; day <= +range[2]; day++) days.push(day);
    } else if (/^\d$/.test(part)) {
      days.push(+part);
    } else {
      return null;
    }
  }
  return days.every((day) => day <= 6) ? days : null;
}

export function toCron(schedule: FriendlySchedule): string {
  if (schedule.kind === 'monthly') return `${schedule.minute} ${schedule.hour} ${schedule.day} * *`;
  const dow = daysToField(schedule.days);
  if (schedule.kind === 'days') return `${schedule.minute} ${schedule.hour} * * ${dow}`;
  if (schedule.unit === 'min') {
    const hours = schedule.win ? `${schedule.win[0]}-${schedule.win[1]}` : '*';
    return `${schedule.n === 1 ? '*' : `*/${schedule.n}`} ${hours} * * ${dow}`;
  }
  const base = schedule.win ? `${schedule.win[0]}-${schedule.win[1]}` : '*';
  return `${schedule.minute} ${schedule.n === 1 ? base : `${base}/${schedule.n}`} * * ${dow}`;
}

function readHours(field: string): { n: number; win: [number, number] | null } | null {
  if (field === '*') return { n: 1, win: null };
  let match = field.match(/^\*\/(\d+)$/);
  if (match) return { n: +match[1], win: null };
  match = field.match(/^(\d+)-(\d+)$/);
  if (match) return { n: 1, win: [+match[1], +match[2]] };
  match = field.match(/^(\d+)-(\d+)\/(\d+)$/);
  if (match) return { n: +match[3], win: [+match[1], +match[2]] };
  return null;
}

function readSchedule([minute, hour, dom, month, dow]: string[]): FriendlySchedule | null {
  if (month !== '*') return null;
  if (dom !== '*') {
    if (dow !== '*' || !isInt(dom) || !isInt(minute) || !isInt(hour)) return null;
    return { kind: 'monthly', day: +dom, hour: +hour, minute: +minute };
  }
  const days = parseDaysField(dow);
  if (!days) return null;
  const step = minute === '*' ? '1' : minute.match(/^\*\/(\d+)$/)?.[1];
  if (step) {
    const hours = readHours(hour);
    if (!hours || hours.n !== 1) return null;
    return { kind: 'every', unit: 'min', n: +step, win: hours.win, days, minute: 0 };
  }
  if (!isInt(minute)) return null;
  if (isInt(hour)) return { kind: 'days', hour: +hour, minute: +minute, days };
  const hours = readHours(hour);
  return hours ? { kind: 'every', unit: 'hour', n: hours.n, win: hours.win, days, minute: +minute } : null;
}

export function parseFriendly(cron: string): FriendlySchedule | null {
  const trimmed = cron.trim();
  const fields = trimmed.split(/\s+/);
  if (fields.length !== 5) return null;
  const schedule = readSchedule(fields);
  return schedule && toCron(schedule) === trimmed ? schedule : null;
}

function dayWords(days: number[]): string {
  if (days.length === 7) return '';
  const field = daysToField(days);
  if (field === '1-5') return 'weekdays';
  if (field === '0,6') return 'weekends';
  return [1, 2, 3, 4, 5, 6, 0]
    .filter((day) => days.includes(day))
    .map((day) => DAY_ABBREVIATIONS[day])
    .join(', ');
}

function ordinal(n: number): string {
  if (n % 100 >= 11 && n % 100 <= 13) return 'th';
  return ({ 1: 'st', 2: 'nd', 3: 'rd' } as Record<number, string>)[n % 10] ?? 'th';
}

export function describe(cron: string): string | null {
  const trimmed = cron.trim();
  if (trimmed.startsWith('@every ')) return `Every ${trimmed.slice('@every '.length)}`;
  if (DESCRIPTOR_WORDS[trimmed]) return DESCRIPTOR_WORDS[trimmed];
  const schedule = parseFriendly(trimmed);
  if (!schedule) return null;
  if (schedule.kind === 'every') {
    const interval =
      schedule.unit === 'min'
        ? schedule.n === 1 ? 'Every minute' : `Every ${schedule.n} min`
        : schedule.n === 1 ? `Hourly at :${pad(schedule.minute)}` : `Every ${schedule.n} h at :${pad(schedule.minute)}`;
    const window = schedule.win ? `${pad(schedule.win[0])}:00–${pad(schedule.win[1])}:59` : '';
    return [interval, window, dayWords(schedule.days)].filter(Boolean).join(', ');
  }
  const at = `${pad(schedule.hour)}:${pad(schedule.minute)}`;
  if (schedule.kind === 'monthly') return `Monthly on the ${schedule.day}${ordinal(schedule.day)} at ${at}`;
  if (schedule.days.length === 7) return `Daily at ${at}`;
  if (schedule.days.length === 1) return `${DAY_PLURALS[schedule.days[0]]} at ${at}`;
  const words = dayWords(schedule.days);
  return `${words[0].toUpperCase()}${words.slice(1)} at ${at}`;
}
