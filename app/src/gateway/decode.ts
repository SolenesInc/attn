import {
  Convert,
  GatewaySessionState,
  type GatewayPong,
  type GatewayRefused,
  type GatewaySessions,
  type GatewayWelcome,
} from '../types/gatewayGenerated';

export type GatewayEvent = GatewayWelcome | GatewayRefused | GatewaySessions | GatewayPong;

const sessionStates = new Set<string>(Object.values(GatewaySessionState));

function knownSessionState(state: unknown): unknown {
  return typeof state === 'string' && !sessionStates.has(state) ? GatewaySessionState.Unknown : state;
}

// A home on a newer api may send enum members and events this build does not know: an unknown member decodes as
// unknown, and an unknown event decodes as null so the caller skips it.
export function decodeGatewayEvent(frame: string): GatewayEvent | null {
  const message = JSON.parse(frame);
  switch (message?.event) {
    case 'welcome':
      return Convert.toGatewayWelcome(frame);
    case 'refused':
      return Convert.toGatewayRefused(frame);
    case 'pong':
      return Convert.toGatewayPong(frame);
    case 'sessions': {
      const sessions = Array.isArray(message.sessions)
        ? message.sessions.map((session: Record<string, unknown>) => ({ ...session, state: knownSessionState(session?.state) }))
        : message.sessions;
      return Convert.toGatewaySessions(JSON.stringify({ ...message, sessions }));
    }
    default:
      return null;
  }
}
