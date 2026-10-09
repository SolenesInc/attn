import { clearHarnesses } from '../hooks/useHarnesses';
import { clearHarnessModelCatalogs } from '../hooks/useHarnessRoute';
import { _resetEscapeStackForTest } from '../hooks/useEscapeStack';
import { gardenScrollMemory } from '../store/gardenWalk';
import { storeResets } from './storeResets';

export function forgetAppMemory() {
  for (const reset of storeResets) reset();
  gardenScrollMemory.clear();
  clearHarnessModelCatalogs();
  clearHarnesses();
  _resetEscapeStackForTest();
}
