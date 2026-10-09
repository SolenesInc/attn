import { harnessLabel } from '../harnessLabel';
import { knownModelName } from '../../hooks/useHarnessRoute';
import { AutomationFormValues } from './automationFormModel';


export interface SentenceSegment {
  text: string;
  emphasis?: 'accent' | 'strong' | 'mono';
}

const KNOWN_CRON_PHRASES: Record<string, string> = {
  '* * * * *': 'every minute',
  '*/5 * * * *': 'every 5 minutes',
  '0 * * * *': 'every hour, on the hour',
  '0 9 * * *': 'every day at 09:00',
  '0 9 * * 1-5': 'weekdays at 09:00',
  '0 0 * * 0': 'Sundays at midnight',
};

function isFiveFieldCron(cron: string): boolean {
  const fields = cron.trim().split(/\s+/).filter(Boolean);
  return fields.length === 5;
}

export function cronPhrase(cron: string): string | null {
  const trimmed = cron.trim();
  if (!isFiveFieldCron(trimmed)) return null;
  return KNOWN_CRON_PHRASES[trimmed] ?? 'on this schedule';
}

function capitalize(text: string): string {
  return text.length === 0 ? text : text[0].toUpperCase() + text.slice(1);
}

function agentSegments(values: AutomationFormValues, name: string): SentenceSegment[] {
  const segments: SentenceSegment[] = [{ text: harnessLabel(values.agent), emphasis: 'strong' }];
  const model = name || values.model.trim();
  const effort = values.effort.trim();
  const details = [model, effort ? `${effort} effort` : ''].filter(Boolean).join(' · ');
  if (details) segments.push({ text: ` (${details})` });
  return segments;
}

function repositoryCountPhrase(count: number): string {
  return `${count} selected repositor${count === 1 ? 'y' : 'ies'}`;
}

export function compiledSentenceSegments(values: AutomationFormValues, modelName = knownModelName(values.agent, '', values.model)): SentenceSegment[] {
  if (values.executable.trim()) modelName = '';
  switch (values.trigger) {
    case 'manual': {
      return [
        { text: 'When you press ' },
        { text: 'Run now', emphasis: 'accent' },
        { text: ' → ' },
        ...agentSegments(values, modelName),
        { text: ' works in ' },
        { text: values.directoryPath || '…', emphasis: 'mono' },
        { text: ' — a fresh worker each run, unattended.' },
      ];
    }
    case 'scheduled': {
      const phrase = cronPhrase(values.scheduleCron) ?? "on a schedule you haven't set yet";
      const continuityText =
        values.continuity === 'singleton'
          ? 'one ongoing worker picks it up each time'
          : 'a fresh worker each run';
      const catchUpText =
        values.catchUp === 'latest'
          ? 'the latest missed run still fires after downtime'
          : values.catchUp === 'skip'
            ? 'missed runs are skipped'
            : "missed-run behavior not chosen yet";
      return [
        { text: capitalize(phrase), emphasis: 'accent' },
        { text: ' (local time) → ' },
        ...agentSegments(values, modelName),
        { text: ' works in ' },
        { text: values.directoryPath || '…', emphasis: 'mono' },
        { text: ` — ${continuityText}, ${catchUpText}, unattended.` },
      ];
    }
    case 'github_review_requested': {
      const include = values.repositoriesInclude;
      const exclude = values.repositoriesExclude;
      const scopeText = include.length > 0 ? repositoryCountPhrase(include.length) : 'any repository you can access';
      const excludeText = exclude.length > 0 ? ` (excluding ${exclude.length})` : '';
      return [
        { text: 'When ' },
        { text: 'a PR requests your review', emphasis: 'accent' },
        { text: ` on ${scopeText}${excludeText} → ` },
        ...agentSegments(values, modelName),
        { text: ' reviews it in a ' },
        { text: 'fresh worktree at the PR head', emphasis: 'strong' },
        { text: ' — one reviewer per PR, later cycles return to it, unattended.' },
      ];
    }
  }
}

export function compiledSentenceText(values: AutomationFormValues): string {
  return compiledSentenceSegments(values)
    .map((segment) => segment.text)
    .join('');
}
