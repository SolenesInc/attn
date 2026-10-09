import type { TierInfo } from '../hooks/useHarnessRoute';

export function TierMark({ tier, row = false }: { tier: TierInfo | undefined; row?: boolean }) {
  if (!tier) return null;
  if (!tier.tier) return <span className="route-tier no-tier" title={tier.source === 'alias' ? 'The harness decides its model' : 'Set a tier in Settings → Models'}>{row ? '' : '· '}no tier</span>;
  return row ? null : <span className="route-tier" title={tier.source === 'override' ? `Your tier; attn: ${tier.shipped || 'no tier'}` : 'attn tier'}>· {tier.tier}</span>;
}
