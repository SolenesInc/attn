import {
  Convert,
  HomeGatewayAskQuestionKind,
  HomeGatewayCheckState,
  HomeGatewaySeedStatus,
  HomeGatewaySessionState,
  type HomeGatewayAskAnswerResult,
  type HomeGatewayAskEvent,
  type HomeGatewayAskImageChunk,
  type HomeGatewayAskImageError,
  type HomeGatewayAskSeedMentionsResult,
  type HomeGatewayAsks,
  type HomeGatewayCrewStartResult,
  type HomeGatewayGarden,
  type HomeGatewayGardenSeeds,
  type HomeGatewayPong,
  type HomeGatewayRefused,
  type HomeGatewaySeedDetail,
  type HomeGatewaySeedChanged,
  type HomeGatewaySeedError,
  type HomeGatewaySeedResult,
  type HomeGatewaySessions,
  type HomeGatewayWelcome,
  type SubmitResult,
  type TerminalEnded,
  type TerminalError,
  type TerminalResized,
  type TerminalScreen,
} from './generated'

export type HomeGatewayEvent =
  | HomeGatewayWelcome
  | HomeGatewayRefused
  | HomeGatewaySessions
  | HomeGatewayPong
  | HomeGatewayAskEvent
  | HomeGatewayAsks
  | HomeGatewayAskAnswerResult
  | HomeGatewayAskImageChunk
  | HomeGatewayAskImageError
  | HomeGatewayAskSeedMentionsResult
  | TerminalScreen
  | TerminalResized
  | TerminalError
  | TerminalEnded
  | SubmitResult
  | HomeGatewayGardenSeeds
  | HomeGatewayGarden
  | HomeGatewaySeedDetail
  | HomeGatewaySeedChanged
  | HomeGatewaySeedError
  | HomeGatewaySeedResult
  | HomeGatewayCrewStartResult

const sessionStates = new Set<string>(Object.values(HomeGatewaySessionState))

const questionKinds = new Set<string>(Object.values(HomeGatewayAskQuestionKind))

const seedStatuses = new Set<string>(Object.values(HomeGatewaySeedStatus))

const checkStates = new Set<string>(Object.values(HomeGatewayCheckState))

function known(members: Set<string>, unknown: string, value: unknown): unknown {
  return typeof value === 'string' && !members.has(value) ? unknown : value
}

function knownSessionState(state: unknown): unknown {
  return known(sessionStates, HomeGatewaySessionState.Unknown, state)
}

function withKnownQuestionKinds(ask: Record<string, unknown>): unknown {
  if (!Array.isArray(ask?.parts)) return ask
  const parts = ask.parts.map((part: Record<string, unknown>) => {
    const question = part?.question as Record<string, unknown> | undefined
    return question
      ? {
          ...part,
          question: {
            ...question,
            kind: known(questionKinds, HomeGatewayAskQuestionKind.Unknown, question.kind),
          },
        }
      : part
  })
  return { ...ask, parts }
}

function withKnownStatus(seed: Record<string, unknown>): unknown {
  return seed ? { ...seed, status: known(seedStatuses, HomeGatewaySeedStatus.Unknown, seed.status) } : seed
}

function withKnownStatuses(seeds: unknown): unknown {
  return Array.isArray(seeds) ? seeds.map(withKnownStatus) : seeds
}

function withKnownCheckStates(checks: unknown): unknown {
  return Array.isArray(checks)
    ? checks.map((check: Record<string, unknown>) => ({
        ...check,
        state: known(checkStates, HomeGatewayCheckState.Unknown, check?.state),
      }))
    : checks
}

// A home on a newer api may send enum members and events this build does not know: an unknown member decodes as
// unknown, and an unknown event decodes as null so the caller skips it.
export function decodeHomeGatewayEvent(frame: string): HomeGatewayEvent | null {
  const message = JSON.parse(frame)
  switch (message?.event) {
    case 'welcome':
      return Convert.toHomeGatewayWelcome(frame)
    case 'refused':
      return Convert.toHomeGatewayRefused(frame)
    case 'pong':
      return Convert.toHomeGatewayPong(frame)
    case 'terminal_screen':
      return Convert.toTerminalScreen(frame)
    case 'terminal_resized':
      return Convert.toTerminalResized(frame)
    case 'terminal_error':
      return Convert.toTerminalError(frame)
    case 'terminal_ended':
      return Convert.toTerminalEnded(frame)
    case 'submit_result':
      return Convert.toSubmitResult(frame)
    case 'sessions': {
      const sessions = Array.isArray(message.sessions)
        ? message.sessions.map((session: Record<string, unknown>) => ({
            ...session,
            state: knownSessionState(session?.state),
          }))
        : message.sessions
      return Convert.toHomeGatewaySessions(JSON.stringify({ ...message, sessions }))
    }
    case 'ask':
      return Convert.toHomeGatewayAskEvent(
        JSON.stringify({ ...message, ask: withKnownQuestionKinds(message.ask) }),
      )
    case 'asks':
      return Convert.toHomeGatewayAsks(frame)
    case 'ask_answer_result':
      return Convert.toHomeGatewayAskAnswerResult(frame)
    case 'ask_seed_mentions_result':
      return Convert.toHomeGatewayAskSeedMentionsResult(frame)
    case 'ask_image_chunk':
      return Convert.toHomeGatewayAskImageChunk(frame)
    case 'ask_image_error':
      return Convert.toHomeGatewayAskImageError(frame)
    case 'garden_seeds':
      return Convert.toHomeGatewayGardenSeeds(
        JSON.stringify({ ...message, seeds: withKnownStatuses(message.seeds) }),
      )
    case 'garden':
      return Convert.toHomeGatewayGarden(frame)
    case 'seed_detail':
      return Convert.toHomeGatewaySeedDetail(
        JSON.stringify({
          ...message,
          seed: withKnownStatus(message.seed),
          children: withKnownStatuses(message.children),
          checks: withKnownCheckStates(message.checks),
        }),
      )
    case 'seed_changed':
      return Convert.toHomeGatewaySeedChanged(frame)
    case 'seed_error':
      return Convert.toHomeGatewaySeedError(frame)
    case 'seed_result':
      return Convert.toHomeGatewaySeedResult(frame)
    case 'crew_start_result':
      return Convert.toHomeGatewayCrewStartResult(frame)
    default:
      return null
  }
}
