import { describe as group, expect, it } from 'vitest';
import { describe, parseFriendly, toCron } from './automationSchedule';

const FRIENDLY: [string, string][] = [
  ['*/30 * * * *', 'Every 30 min'],
  ['*/10 7-23 * * *', 'Every 10 min, 07:00–23:59'],
  ['0 3 * * 2', 'Tuesdays at 03:00'],
  ['0 9 * * 1-5', 'Weekdays at 09:00'],
  ['0 0 1 * *', 'Monthly on the 1st at 00:00'],
  ['0 * * * *', 'Hourly at :00'],
  ['0 */2 * * *', 'Every 2 h at :00'],
  ['15 8-18/2 * * 1-5', 'Every 2 h at :15, 08:00–18:59, weekdays'],
  ['0 9 * * 0,6', 'Weekends at 09:00'],
  ['* * * * *', 'Every minute'],
  ['0 9 * * 1,3,5', 'Mon, Wed, Fri at 09:00'],
];

const CUSTOM: [string, string | null][] = [
  ['0 9 * * MON-FRI', null],
  ['15 9-17/2 1-7 * 1', null],
  ['0 9,17 * * *', null],
  ['@daily', 'Daily at 00:00'],
  ['@every 90m', 'Every 90m'],
  ['0 9 13 * 5', null],
  ['0 9 * * 1,2,3,4,5', null],
  ['0  9 * * *', null],
  ['5 4 * 1 *', null],
];

group('friendly schedules', () => {
  it.each(FRIENDLY)('%s writes back exactly and reads as %s', (cron, words) => {
    const schedule = parseFriendly(cron);
    expect(schedule).not.toBeNull();
    expect(toCron(schedule!)).toBe(cron);
    expect(describe(cron)).toBe(words);
  });
});

group('custom crons', () => {
  it.each(CUSTOM)('%s is left as written', (cron, words) => {
    expect(parseFriendly(cron)).toBeNull();
    expect(describe(cron)).toBe(words);
  });
});
