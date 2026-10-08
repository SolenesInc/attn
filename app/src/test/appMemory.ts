import { clearHarnessModelCatalogs } from '../hooks/useHarnessModelCatalog';
import { _resetEscapeStackForTest } from '../hooks/useEscapeStack';
import { gardenScrollMemory } from '../store/gardenWalk';
import { storeResets } from './storeResets';

export function forgetAppMemory() {
  for (const reset of storeResets) reset();
  gardenScrollMemory.clear();
  clearHarnessModelCatalogs();
  _resetEscapeStackForTest();
}
