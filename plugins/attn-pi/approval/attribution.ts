import { getAgentDir, SettingsManager } from "@earendil-works/pi-coding-agent";
import { mergeProviderAttributionHeaders } from "../node_modules/@earendil-works/pi-coding-agent/dist/core/provider-attribution.js";
import type { ModelLike } from "./provider";

export function createAttributionHeaders(settings: SettingsManager) {
  return (
    model: ModelLike,
    sessionId: string,
    requestHeaders?: Record<string, string | null> | undefined,
  ): Record<string, string | null> | undefined =>
    mergeProviderAttributionHeaders(
      { ...model, baseUrl: model.baseUrl ?? "" } as Parameters<typeof mergeProviderAttributionHeaders>[0],
      settings,
      sessionId,
      requestHeaders,
    );
}

export function guardianAttributionSettings(cwd: string = process.cwd()): SettingsManager {
  return SettingsManager.create(cwd, getAgentDir());
}
