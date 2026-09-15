export type AttachHolder = object;

export interface AttachHolds {
  hold(runtimeId: string, holder: AttachHolder): void;
  release(runtimeId: string, holder: AttachHolder): number;
  holderCount(runtimeId: string): number;
}

export function createAttachHolds(): AttachHolds {
  const holdersByRuntime = new Map<string, Set<AttachHolder>>();
  const holderCount = (runtimeId: string) => holdersByRuntime.get(runtimeId)?.size ?? 0;
  return {
    hold(runtimeId, holder) {
      const holders = holdersByRuntime.get(runtimeId) ?? new Set<AttachHolder>();
      holders.add(holder);
      holdersByRuntime.set(runtimeId, holders);
    },
    release(runtimeId, holder) {
      const holders = holdersByRuntime.get(runtimeId);
      holders?.delete(holder);
      if (holders?.size === 0) {
        holdersByRuntime.delete(runtimeId);
      }
      return holderCount(runtimeId);
    },
    holderCount,
  };
}

export const runtimeAttachHolds = createAttachHolds();
