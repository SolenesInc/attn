import { isMacLikePlatform } from '../shortcuts/platform';

export function tileWindowsSupported(): boolean {
  return isMacLikePlatform();
}
