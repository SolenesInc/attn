import type { HomeGatewayAsk, HomeGatewayAskAnswer } from '../homeGateway/generated'
import { Schema } from 'effect'

export const QuestionKind = Schema.Literals(['one', 'any', 'text', 'unknown'])

export const Question = Schema.Struct({
  key: Schema.String,
  kind: QuestionKind,
  prompt: Schema.String,
  context: Schema.NullOr(Schema.String),
  choices: Schema.Array(Schema.Struct({ label: Schema.String, description: Schema.NullOr(Schema.String) })),
  recommended: Schema.NullOr(Schema.String),
})
export type Question = typeof Question.Type

export const Part = Schema.Struct({
  markdown: Schema.NullOr(Schema.String),
  question: Schema.NullOr(Question),
})
export type Part = typeof Part.Type

export const Answer = Schema.Struct({
  key: Schema.String,
  picked: Schema.Array(Schema.String),
  other: Schema.String,
  text: Schema.String,
})
export type Answer = typeof Answer.Type

export const PhoneAsk = Schema.Struct({
  id: Schema.String,
  sessionId: Schema.String,
  subject: Schema.String,
  parts: Schema.Array(Part),
  createdAt: Schema.Finite,
  seedSlug: Schema.NullOr(Schema.String),
  seedTitle: Schema.NullOr(Schema.String),
  crewMember: Schema.NullOr(Schema.String),
  chief: Schema.Boolean,
  reply: Schema.NullOr(
    Schema.Struct({ answers: Schema.Array(Answer), note: Schema.String, answeredAt: Schema.Finite }),
  ),
  tooLarge: Schema.NullOr(Schema.Struct({ bytes: Schema.Finite, limit: Schema.Finite })),
})
export type PhoneAsk = typeof PhoneAsk.Type

const fromHomeGatewayAnswer = (answer: HomeGatewayAskAnswer): Answer => ({
  key: answer.key,
  picked: answer.picked,
  other: answer.other ?? '',
  text: answer.text ?? '',
})

export const fromHomeGatewayAsk = (ask: HomeGatewayAsk): PhoneAsk => ({
  id: ask.id,
  sessionId: ask.session_id,
  subject: ask.subject,
  parts: ask.parts.map((part) => ({
    markdown: part.markdown ?? null,
    question: part.question
      ? {
          key: part.question.key,
          kind: part.question.kind,
          prompt: part.question.prompt,
          context: part.question.context ?? null,
          choices: part.question.choices.map((choice) => ({
            label: choice.label,
            description: choice.description ?? null,
          })),
          recommended: part.question.recommended ?? null,
        }
      : null,
  })),
  createdAt: ask.created_at.getTime(),
  seedSlug: ask.seed_slug ?? null,
  seedTitle: ask.seed_title ?? null,
  crewMember: ask.crew_member ?? null,
  chief: ask.chief ?? false,
  reply: ask.reply
    ? {
        answers: ask.reply.answers.map(fromHomeGatewayAnswer),
        note: ask.reply.note ?? '',
        answeredAt: ask.reply.answered_at.getTime(),
      }
    : null,
  tooLarge:
    ask.too_large_bytes !== undefined
      ? { bytes: ask.too_large_bytes, limit: ask.max_frame_bytes ?? 0 }
      : null,
})

export const questionsOf = (ask: PhoneAsk): ReadonlyArray<Question> =>
  ask.parts.flatMap((part) => (part.question ? [part.question] : []))

// An ask with one single-choice question and nothing else to fill in is answered by tapping a choice.
export const answersInOneTap = (ask: PhoneAsk): boolean => {
  const questions = questionsOf(ask)
  return questions.length === 1 && questions[0]!.kind === 'one'
}

export const Draft = Schema.Struct({
  answers: Schema.Record(Schema.String, Answer),
  note: Schema.String,
})
export type Draft = typeof Draft.Type

export const emptyDraft: Draft = { answers: {}, note: '' }

export const emptyAnswer = (key: string): Answer => ({ key, picked: [], other: '', text: '' })

export const recommendedAnswers = (ask: PhoneAsk): ReadonlyArray<Answer> =>
  questionsOf(ask).flatMap((question) => {
    const recommended = question.recommended
    if (!recommended) return []
    if (question.kind === 'text') return [{ ...emptyAnswer(question.key), text: recommended }]
    if (question.kind === 'one' || question.kind === 'any')
      return [{ ...emptyAnswer(question.key), picked: [recommended] }]
    return []
  })

export const draftAnswers = (ask: PhoneAsk, draft: Draft): ReadonlyArray<HomeGatewayAskAnswer> =>
  questionsOf(ask).flatMap((question) => {
    const answer = draft.answers[question.key]
    if (!answer) return []
    const other = answer.other.trim()
    const text = answer.text.trim()
    if (answer.picked.length === 0 && !other && !text) return []
    return [
      {
        key: question.key,
        picked: [...answer.picked],
        ...(other ? { other } : {}),
        ...(text ? { text } : {}),
      },
    ]
  })

export const draftSendable = (ask: PhoneAsk, draft: Draft): boolean =>
  draftAnswers(ask, draft).length > 0 || draft.note.trim() !== ''

export const askKey = (pairingId: string, askId: string) => `${pairingId}/${askId}`

export const askFrom = (ask: PhoneAsk, sessionTitle: string | undefined): string => {
  if (ask.seedSlug || ask.seedTitle) return ask.seedSlug ?? ask.seedTitle ?? ''
  if (ask.crewMember) return ask.crewMember
  if (ask.chief) return 'chief'
  return sessionTitle ?? 'an agent'
}
