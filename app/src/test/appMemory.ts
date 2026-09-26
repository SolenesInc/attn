import { clearDelegationModelCatalogs } from '../hooks/useDelegationModelCatalog';
import { _resetEscapeStackForTest } from '../hooks/useEscapeStack';
import { gardenScrollMemory } from '../store/gardenWalk';
import { storeResets } from './storeResets';

export function forgetAppMemory() {
  for (const reset of storeResets) reset();
  gardenScrollMemory.clear();
  clearDelegationModelCatalogs();
  _resetEscapeStackForTest();
}
