import {
  Convert,
  GatewayAskQuestionKind,
  GatewayCheckState,
  GatewaySeedStatus,
  GatewaySessionState,
  type GatewayAskAnswerResult,
  type GatewayAskEvent,
  type GatewayAskImageChunk,
  type GatewayAskImageError,
  type GatewayAsks,
  type GatewayGarden,
  type GatewayGardenSeeds,
  type GatewayPong,
  type GatewayRefused,
  type GatewaySeedDetail,
  type GatewaySeedError,
  type GatewaySeedResult,
  type GatewaySessions,
  type GatewayWelcome,
  type SubmitResult,
  type TerminalEnded,
  type TerminalError,
  type TerminalResized,
  type TerminalScreen,
} from '../types/gatewayGenerated';

export type GatewayEvent =
  | GatewayWelcome
  | GatewayRefused
  | GatewaySessions
  | GatewayPong
  | GatewayAskEvent
  | GatewayAsks
  | GatewayAskAnswerResult
  | GatewayAskImageChunk
  | GatewayAskImageError
  | TerminalScreen
  | TerminalResized
  | TerminalError
  | TerminalEnded
  | SubmitResult
  | GatewayGardenSeeds
  | GatewayGarden
  | GatewaySeedDetail
  | GatewaySeedError
  | GatewaySeedResult;

const sessionStates = new Set<string>(Object.values(GatewaySessionState));

const questionKinds = new Set<string>(Object.values(GatewayAskQuestionKind));

const seedStatuses = new Set<string>(Object.values(GatewaySeedStatus));

const checkStates = new Set<string>(Object.values(GatewayCheckState));

function known(members: Set<string>, unknown: string, value: unknown): unknown {
  return typeof value === 'string' && !members.has(value) ? unknown : value;
}

function knownSessionState(state: unknown): unknown {
  return known(sessionStates, GatewaySessionState.Unknown, state);
}

function withKnownQuestionKinds(ask: Record<string, unknown>): unknown {
  if (!Array.isArray(ask?.parts)) return ask;
  const parts = ask.parts.map((part: Record<string, unknown>) => {
    const question = part?.question as Record<string, unknown> | undefined;
    return question
      ? { ...part, question: { ...question, kind: known(questionKinds, GatewayAskQuestionKind.Unknown, question.kind) } }
      : part;
  });
  return { ...ask, parts };
}

function withKnownStatus(seed: Record<string, unknown>): unknown {
  return seed ? { ...seed, status: known(seedStatuses, GatewaySeedStatus.Unknown, seed.status) } : seed;
}

function withKnownStatuses(seeds: unknown): unknown {
  return Array.isArray(seeds) ? seeds.map(withKnownStatus) : seeds;
}

function withKnownCheckStates(checks: unknown): unknown {
  return Array.isArray(checks)
    ? checks.map((check: Record<string, unknown>) => ({ ...check, state: known(checkStates, GatewayCheckState.Unknown, check?.state) }))
    : checks;
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
    case 'terminal_screen':
      return Convert.toTerminalScreen(frame);
    case 'terminal_resized':
      return Convert.toTerminalResized(frame);
    case 'terminal_error':
      return Convert.toTerminalError(frame);
    case 'terminal_ended':
      return Convert.toTerminalEnded(frame);
    case 'submit_result':
      return Convert.toSubmitResult(frame);
    case 'sessions': {
      const sessions = Array.isArray(message.sessions)
        ? message.sessions.map((session: Record<string, unknown>) => ({ ...session, state: knownSessionState(session?.state) }))
        : message.sessions;
      return Convert.toGatewaySessions(JSON.stringify({ ...message, sessions }));
    }
    case 'ask':
      return Convert.toGatewayAskEvent(JSON.stringify({ ...message, ask: withKnownQuestionKinds(message.ask) }));
    case 'asks':
      return Convert.toGatewayAsks(frame);
    case 'ask_answer_result':
      return Convert.toGatewayAskAnswerResult(frame);
    case 'ask_image_chunk':
      return Convert.toGatewayAskImageChunk(frame);
    case 'ask_image_error':
      return Convert.toGatewayAskImageError(frame);
    case 'garden_seeds':
      return Convert.toGatewayGardenSeeds(JSON.stringify({ ...message, seeds: withKnownStatuses(message.seeds) }));
    case 'garden':
      return Convert.toGatewayGarden(frame);
    case 'seed_detail':
      return Convert.toGatewaySeedDetail(JSON.stringify({
        ...message,
        seed: withKnownStatus(message.seed),
        children: withKnownStatuses(message.children),
        checks: withKnownCheckStates(message.checks),
      }));
    case 'seed_error':
      return Convert.toGatewaySeedError(frame);
    case 'seed_result':
      return Convert.toGatewaySeedResult(frame);
    default:
      return null;
  }
}
