import { useState } from 'react';
import { useHarnessRoute, tierLabel, type HarnessRoute, type HarnessRouteRules } from '../hooks/useHarnessRoute';
import { TierMark } from './TierMark';
import { HarnessRoutePopover } from './HarnessRoutePopover';
import './HarnessRoute.css';

export interface HarnessRouteChipProps {
  value: HarnessRoute;
  rules: HarnessRouteRules;
  onChange?: (value: HarnessRoute) => void;
  variant?: 'chip' | 'field';
  'aria-label': string;
  'data-testid'?: string;
  onOpen?: (anchor: Element) => void;
  open?: boolean;
  disabled?: boolean;
}
export function HarnessRouteChip({ value, rules, onChange, variant = 'chip', 'aria-label': label, 'data-testid': testID, onOpen, open, disabled }: HarnessRouteChipProps) {
  const view = useHarnessRoute(value, rules, variant === 'field' ? 'on-mount' : 'never');
  const [anchor, setAnchor] = useState<Element | null>(null);
  const expanded = open ?? Boolean(anchor);
  const field = variant === 'field';
  const hasEffort = view.effortMode !== 'hidden' && (view.effortMode !== 'unsupported' || value.effort);
  const toggle = (element: Element) => { if (onOpen) onOpen(element); else setAnchor(expanded ? null : element); };
  return <div className={`route-wrap ${field ? 'is-field' : ''}`}>
    <button type="button" className={`${field ? 'route-field' : 'route-chip delegation-model'} ${!view.effectiveHarness ? 'unset' : ''} ${rules.fixedHarness ? 'fixed-harness' : ''}`} aria-label={label} data-testid={testID} aria-haspopup="dialog" aria-expanded={expanded} disabled={disabled} onClick={event => toggle(event.currentTarget)} onKeyDown={event => { if (!expanded && event.key === 'ArrowDown') { event.preventDefault(); toggle(event.currentTarget); } }}>
      {(field || !rules.fixedHarness) && <span className="route-part route-harness"><span className="route-label">{field ? 'Harness' : ''}</span><span>{view.harnessLabel}</span></span>}
      <span className="route-part route-model"><span className="route-label">{field ? 'Model' : ''}</span><span className={view.missing ? 'route-missing' : ''}>{view.effectiveHarness ? view.modelLabel : 'Choose a model'} <TierMark tier={view.tier} /></span></span>
      {hasEffort && <span className="route-part route-effort"><span className="route-label">{field ? 'Effort' : ''}</span><span className={view.effortMissing ? 'route-missing' : ''}>{value.effort || view.defaultEffort || 'default'}</span></span>}
      <span className="route-caret" aria-hidden="true">⌄</span>
    </button>
    {field && view.aboveTier && rules.tier && <span className="route-recommended">{tierLabel(rules.tier)} recommended</span>}
    {anchor && onChange && <HarnessRoutePopover value={value} rules={rules} anchor={anchor} onChange={onChange} onClose={() => setAnchor(null)} />}
  </div>;
}
