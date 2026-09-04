const states: Record<string, { label: string; meaning: string }> = {
  planted: { label: 'Planted', meaning: 'Open, not currently being tended.' },
  growing: { label: 'Growing', meaning: 'Claimed and being worked on.' },
  dormant: { label: 'Dormant', meaning: 'Paused. Tending it again resumes the work.' },
  harvested: { label: 'Harvested', meaning: 'The work is complete.' },
  withered: { label: 'Withered', meaning: 'Closed without a harvest.' },
};

export function seedStateLabel(status?: string): string {
  return states[status ?? '']?.label ?? 'Unknown';
}

export function seedStateMeaning(status?: string): string {
  return states[status ?? '']?.meaning ?? 'Seed details are not available yet.';
}
