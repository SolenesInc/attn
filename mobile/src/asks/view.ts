import type { Html, HtmlBuilder } from 'foldkit/html'
import type { Pairing } from '../pairing'
import {
  answersInOneTap,
  askFrom,
  draftSendable,
  emptyAnswer,
  recommendedAnswers,
  type Answer,
  type Draft,
  type PhoneAsk,
  type Question,
} from './ask'
import { renderMarkdown, type MarkdownContext } from './markdown'

export type AskRow = {
  readonly key: string
  readonly ask: PhoneAsk
  readonly pairing: Pairing
  readonly from: string
  readonly live: boolean
}

export type AskActions<Message> = MarkdownContext<Message> & {
  readonly openAsk: (pairingId: string, askId: string) => Message
  readonly back: Message
  readonly pick: (key: string, label: string) => Message
  readonly changeOther: (key: string, value: string) => Message
  readonly changeText: (key: string, value: string) => Message
  readonly changeNote: (value: string) => Message
  readonly acceptRecommended: Message
  readonly send: Message
}

export type OpenAsk = {
  readonly row: AskRow | undefined
  readonly draft: Draft
  readonly sending: boolean
  readonly error: string
}

const where = (pairing: Pairing) => `${pairing.homeName} · ${pairing.profileName}`

export const askRow = <Message>(
  row: AskRow,
  age: string,
  actions: AskActions<Message>,
  h: HtmlBuilder<Message>,
): Html =>
  h.li(
    [h.Key(row.key), h.Class('ask-item')],
    [
      h.button(
        [
          h.Type('button'),
          h.Class(`row ask${row.live ? '' : ' offline'}`),
          h.DataAttribute('testid', 'ask-entry'),
          h.OnClick(actions.openAsk(row.pairing.id, row.ask.id)),
        ],
        [
          h.span(
            [h.Class('row-main')],
            [
              h.span([h.Class('row-title')], [row.ask.subject]),
              h.span([h.Class('row-where')], [`${row.from} · ${where(row.pairing)}`]),
            ],
          ),
          h.span(
            [h.Class('row-side')],
            [h.span([h.Class('age')], [age]), h.span([h.Class('chevron'), h.AriaHidden(true)], ['›'])],
          ),
        ],
      ),
    ],
  )

const choiceList = <Message>(
  question: Question,
  answer: Answer,
  oneTap: boolean,
  sending: boolean,
  actions: AskActions<Message>,
  h: HtmlBuilder<Message>,
): Html =>
  h.div(
    [
      h.Class('choices'),
      h.Role(question.kind === 'one' ? 'radiogroup' : 'group'),
      h.AriaLabel(question.prompt),
    ],
    [
      ...question.choices.map((choice) => {
        const picked = answer.picked.includes(choice.label)
        return h.button(
          [
            h.Key(choice.label),
            h.Type('button'),
            h.Class(
              `choice${picked ? ' picked' : ''}${question.recommended === choice.label ? ' recommended' : ''}`,
            ),
            h.Role(question.kind === 'one' ? 'radio' : 'checkbox'),
            h.AriaChecked(picked),
            h.Disabled(sending),
            h.DataAttribute('testid', 'ask-choice'),
            h.OnClick(actions.pick(question.key, choice.label)),
          ],
          [
            h.span([h.Class(`mark ${question.kind === 'one' ? 'radio' : 'check'}`), h.AriaHidden(true)], []),
            h.span(
              [h.Class('choice-text')],
              [
                h.span(
                  [h.Class('choice-label')],
                  [
                    choice.label,
                    question.recommended === choice.label
                      ? h.span([h.Class('recommended-badge')], ['recommended'])
                      : h.empty,
                  ],
                ),
                choice.description ? h.span([h.Class('choice-description')], [choice.description]) : h.empty,
              ],
            ),
            oneTap && picked && sending ? h.span([h.Class('sending')], ['Sending…']) : h.empty,
          ],
        )
      }),
      h.input([
        h.Class('other'),
        h.Type('text'),
        h.Placeholder('Other…'),
        h.AriaLabel(`Other answer to ${question.prompt}`),
        h.Value(answer.other),
        h.Disabled(sending),
        h.EnterKeyHint(oneTap ? 'send' : 'done'),
        h.OnInput((value) => actions.changeOther(question.key, value)),
      ]),
    ],
  )

const writtenAnswer = <Message>(
  question: Question,
  answer: Answer,
  sending: boolean,
  actions: AskActions<Message>,
  h: HtmlBuilder<Message>,
): Html =>
  h.div(
    [h.Class('written')],
    [
      h.textarea([
        h.AriaLabel(question.prompt),
        h.Value(answer.text),
        h.Rows(3),
        h.Disabled(sending),
        h.Placeholder(question.recommended ? `Suggested: ${question.recommended}` : 'Your answer…'),
        h.OnInput((value) => actions.changeText(question.key, value)),
      ]),
      question.recommended && answer.text.trim() === ''
        ? h.button(
            [
              h.Type('button'),
              h.Class('quiet'),
              h.OnClick(actions.changeText(question.key, question.recommended)),
            ],
            ['Use the suggestion'],
          )
        : h.empty,
    ],
  )

const repliedAnswer = <Message>(
  question: Question,
  answer: Answer | undefined,
  h: HtmlBuilder<Message>,
): Html => {
  const shown = [...(answer?.picked ?? []), answer?.other ?? '', answer?.text ?? '']
    .filter(Boolean)
    .join(' · ')
  return h.p([h.Class(`replied${shown ? '' : ' none'}`)], [shown || 'Not answered'])
}

const questionCard = <Message>(
  ask: PhoneAsk,
  question: Question,
  index: number,
  open: OpenAsk,
  actions: AskActions<Message>,
  h: HtmlBuilder<Message>,
): Html => {
  const answer = open.draft.answers[question.key] ?? emptyAnswer(question.key)
  const replied = ask.reply?.answers.find((reply) => reply.key === question.key)
  return h.fieldset(
    [h.Class('question'), h.DataAttribute('testid', `ask-question-${question.key}`)],
    [
      h.legend([], [h.span([h.Class('index')], [String(index)]), question.prompt]),
      question.context ? renderMarkdown(question.context, actions, h) : h.empty,
      ask.reply
        ? repliedAnswer(question, replied, h)
        : question.kind === 'text'
          ? writtenAnswer(question, answer, open.sending, actions, h)
          : question.kind === 'unknown'
            ? h.p(
                [h.Class('muted')],
                ['This phone app does not know this kind of question yet; answer it on your computer.'],
              )
            : choiceList(question, answer, answersInOneTap(ask), open.sending, actions, h),
    ],
  )
}

export const askScreen = <Message>(
  open: OpenAsk,
  now: number,
  age: (since: number, now: number) => string,
  actions: AskActions<Message>,
  h: HtmlBuilder<Message>,
): Html => {
  const row = open.row
  if (!row) {
    return h.div(
      [h.Class('ask-screen empty')],
      [
        h.p([], ['This ask was withdrawn or closed.']),
        h.button([h.Class('primary'), h.OnClick(actions.back)], ['Back to the queue']),
      ],
    )
  }
  const { ask } = row
  let index = 0
  const body = ask.tooLarge
    ? [
        h.p(
          [h.Class('error')],
          [
            `This ask holds ${Math.ceil(ask.tooLarge.bytes / 1024)} KiB, more than the ${Math.floor(ask.tooLarge.limit / 1024)} KiB one phone message carries. Answer it on your computer.`,
          ],
        ),
      ]
    : ask.parts.map((part) =>
        part.question
          ? questionCard(ask, part.question, ++index, open, actions, h)
          : renderMarkdown(part.markdown ?? '', actions, h),
      )
  const sendable = !ask.reply && !ask.tooLarge && draftSendable(ask, open.draft) && !open.sending && row.live
  return h.article(
    [h.Class('ask-screen'), h.DataAttribute('testid', 'ask-screen')],
    [
      h.h2([h.Class('subject')], [ask.subject]),
      h.p([h.Class('ask-from')], [`${row.from} · ${where(row.pairing)} · ${age(ask.createdAt, now)}`]),
      ...body,
      ask.reply
        ? h.div(
            [h.Class('answered')],
            [
              ask.reply.note ? h.p([h.Class('replied')], [ask.reply.note]) : h.empty,
              h.p([h.Class('muted')], [`Answered ${age(ask.reply.answeredAt, now)} ago`]),
            ],
          )
        : ask.tooLarge
          ? h.empty
          : h.div(
              [h.Class('answer-bar')],
              [
                h.textarea([
                  h.Class('note'),
                  h.AriaLabel('Note'),
                  h.Placeholder('Note (optional)'),
                  h.Rows(2),
                  h.Value(open.draft.note),
                  h.Disabled(open.sending),
                  h.OnInput(actions.changeNote),
                ]),
                open.error ? h.p([h.Class('error')], [open.error]) : h.empty,
                row.live ? h.empty : h.p([h.Class('muted')], [`${row.pairing.homeName} is not connected.`]),
                !open.sending && recommendedAnswers(ask).length > 1
                  ? h.button(
                      [
                        h.Class('quiet'),
                        h.Type('button'),
                        h.DataAttribute('testid', 'ask-accept-recommended'),
                        h.OnClick(actions.acceptRecommended),
                      ],
                      ['Accept all recommended'],
                    )
                  : h.empty,
                h.button(
                  [
                    h.Class('primary'),
                    h.Type('button'),
                    h.Disabled(!sendable),
                    h.DataAttribute('testid', 'ask-send'),
                    h.OnClick(actions.send),
                  ],
                  [open.sending ? 'Sending…' : 'Send'],
                ),
              ],
            ),
    ],
  )
}

export { askFrom }
