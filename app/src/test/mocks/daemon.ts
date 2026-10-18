import type { DaemonApi } from '../../contexts/DaemonApiContext';

export function createMockDaemonApi(methods: Partial<DaemonApi>): DaemonApi {
  const api: Partial<DaemonApi> = {
    isConnected: false,
    connectionError: null,
    connectionGeneration: 0,
    hasReceivedInitialState: false,
    settings: {},
    rateLimit: null,
    warnings: [],
    gitOperations: {},
    ...methods,
  };
  return new Proxy(api, {
    get(target, property) {
      if (!(property in target)) {
        Object.defineProperty(target, property, {
          value: () => { throw new Error(`Unexpected daemon API call: ${String(property)}`); },
        });
      }
      return target[property as keyof DaemonApi];
    },
  }) as DaemonApi;
}
