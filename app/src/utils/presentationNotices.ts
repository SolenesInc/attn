import type { Presentation } from '../types/generated';

export function presentationNeedsNotice(presentation: Presentation): boolean {
  return presentation.status === 'open' && !presentation.latest_round_submitted;
}

export function upsertPresentationNotice(notices: Presentation[], updated: Presentation): Presentation[] {
  const withoutExisting = notices.filter((n) => n.id !== updated.id);
  return presentationNeedsNotice(updated) ? [...withoutExisting, updated] : withoutExisting;
}

export function seedPresentationNotices(all: Presentation[]): Presentation[] {
  return all.filter(presentationNeedsNotice);
}

export function latestPresentationBySessionId(notices: Presentation[]): Map<string, Presentation> {
  const bySessionId = new Map<string, Presentation>();
  for (const p of notices) {
    const existing = bySessionId.get(p.session_id);
    if (!existing || p.created_at > existing.created_at) {
      bySessionId.set(p.session_id, p);
    }
  }
  return bySessionId;
}
