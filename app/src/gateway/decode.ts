import {
  Convert,
  GatewayAskQuestionKind,
  GatewaySessionState,
  type GatewayAskAnswerResult,
  type GatewayAskEvent,
  type GatewayAskImageChunk,
  type GatewayAskImageError,
  type GatewayAsks,
  type GatewayPong,
  type GatewayRefused,
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
  | SubmitResult;

const sessionStates = new Set<string>(Object.values(GatewaySessionState));

const questionKinds = new Set<string>(Object.values(GatewayAskQuestionKind));

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
    default:
      return null;
  }
}
