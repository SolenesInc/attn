import { useHarnesses } from '../hooks/useHarnesses';
import { useHarnessRoute, type HarnessRoute, type HarnessRouteRules } from '../hooks/useHarnessRoute';
import { TierMark } from './TierMark';
import './HarnessRoute.css';

export function HarnessRouteBadge({ value, rules, label, unknown = false }: { value: HarnessRoute; rules?: HarnessRouteRules; label?: string; unknown?: boolean }) {
  const { harnesses } = useHarnesses(false);
  const view = useHarnessRoute(value, rules || { harnesses }, 'never');
  return <span className="route-badge" aria-label={label}>
    <span className="route-harness">{value.harness ? view.harnessName : unknown ? 'Not reported' : view.harnessName}</span>
    <span className={view.missing ? 'route-missing' : ''}>{unknown && !value.model ? 'Model not reported' : view.modelLabel}</span>
    <TierMark tier={view.tier} />
    {(value.effort || unknown) && <span className={`route-effort ${view.effortMissing ? 'route-missing' : ''}`}>{value.effort || 'Effort not reported'}</span>}
  </span>;
}
