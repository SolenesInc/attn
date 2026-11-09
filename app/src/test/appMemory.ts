import { clearHarnesses } from '../hooks/useHarnesses';
import { clearHarnessModelCatalogs } from '../hooks/useHarnessRoute';
import { _resetEscapeStackForTest } from '../hooks/useEscapeStack';
import { setShortcutOverrides } from '../shortcuts/resolver';
import { gardenScrollMemory } from '../store/gardenWalk';
import { storeResets } from './storeResets';
import { _resetTileWindowsForTest } from '../tileWindow/pool';

export function forgetAppMemory() {
  for (const reset of storeResets) reset();
  gardenScrollMemory.clear();
  clearHarnessModelCatalogs();
  clearHarnesses();
  _resetEscapeStackForTest();
  setShortcutOverrides({});
  _resetTileWindowsForTest();
}
