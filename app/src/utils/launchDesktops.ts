import type { LaunchDesktopItem } from '../types/generated';

export function pendingDestinations(
  items: LaunchDesktopItem[],
  profileId: string,
  ownDestination?: string,
): LaunchDesktopItem[] {
  const seen = new Set<string>();
  return items.filter((item) => {
    const id = item.setting.destination_id;
    if (
      item.profile_id !== profileId ||
      !item.confirmed ||
      !item.setting.pending ||
      !id ||
      id === ownDestination ||
      seen.has(id)
    )
      return false;
    seen.add(id);
    return true;
  });
}
