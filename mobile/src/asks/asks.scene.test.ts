import { Scene } from 'foldkit'
import { describe, expect, it } from 'vitest'
import { LoadImage, Message, PinchZoom, RenderDiagram, SendAnswer, update, view, type Model } from '../main'
import type { Pairing } from '../pairing'
import type { PhoneAsk, Question } from './ask'

const personal: Pairing = {
  id: 'mac/p-personal',
  cloud: 'https://cloud.attn.codes',
  homeId: 'mac',
  homeNoisePub: 'key',
  homeName: 'Mac',
  profileId: 'p-personal',
  profileName: 'personal',
  pairedAt: Date.parse('2026-11-01T09:00:00Z'),
}
const linux: Pairing = {
  ...personal,
  id: 'box/p-default',
  homeId: 'box',
  homeName: 'Box',
  profileName: 'default',
}

const now = Date.parse('2026-11-02T12:00:00Z')

const online = (...pairings: ReadonlyArray<Pairing>): Model => ({
  screen: 'queue',
  seedTrail: [],
  seedPage: { status: 'loading' },
  seedNotes: {},
  seedMove: null,
  openAsk: null,
  drafts: {},
  answering: {},
  images: {},
  diagrams: {},
  zoom: null,
  openSession: null,
  composers: {},
  terminalStatus: '',
  fullHeight: 800,
  keyboardHeight: 0,
  loaded: true,
  foreground: true,
  homes: pairings.map((pairing) => ({
    pairing,
    api: 4,
    link: 1,
    status: 'online',
    reason: '',
    sessions: [],
    listed: true,
    asks: [],
    garden: [],
  })),
  pairingFlow: 'idle',
  pairingMessage: '',
  typedCode: '',
  pushToken: null,
  pushNote: '',
  pushRefusals: {},
  now,
})

const choiceQuestion: Question = {
  key: 'q1',
  kind: 'one',
  prompt: 'Which store should keep the cache?',
  context: null,
  choices: [
    { label: 'Postgres', description: 'already deployed' },
    { label: 'Redis', description: 'one more service' },
  ],
  recommended: 'Postgres',
}

const ask = (
  id: string,
  minutesAgo: number,
  parts: PhoneAsk['parts'],
  subject = 'Which store?',
): PhoneAsk => ({
  id,
  sessionId: 's1',
  subject,
  parts,
  createdAt: now - minutesAgo * 60_000,
  seedSlug: 'cache-store',
  seedTitle: 'Cache store',
  crewMember: null,
  chief: false,
  reply: null,
  tooLarge: null,
})

const diagram = 'graph TD\n  A-->B'
const storeAsk = ask('a-store', 5, [
  {
    markdown: `Deploys empty the cache.\n\n![the board](shots/board.png)\n\n\`\`\`mermaid\n${diagram}\n\`\`\``,
    question: null,
  },
  { markdown: null, question: choiceQuestion },
])

const entries = (testId: string, html: Parameters<ReturnType<typeof Scene.getAllByTestId>>[0]) =>
  Scene.getAllByTestId(testId)(html).map(Scene.textContent)

const arrive = (pairing: Pairing, asks: ReadonlyArray<PhoneAsk>) =>
  Scene.Subscription.emit(Message.ReceivedAsks({ pairingId: pairing.id, asks }))

const loadStoreVisuals = Scene.Command.resolveAll(
  [
    LoadImage({ pairingId: personal.id, askId: 'a-store', src: 'shots/board.png' }),
    Message.LoadedImage({ key: `${personal.id}\na-store\nshots/board.png`, url: 'blob:board' }),
  ],
  [
    RenderDiagram({ code: diagram }),
    Message.RenderedDiagram({ code: diagram, svg: '<svg data-diagram="1"></svg>', width: 0 }),
  ],
)

describe('asks on the phone', () => {
  it('lists the open asks of every home above the agents, oldest first', () => {
    Scene.scene(
      { update, view },
      Scene.given(online(personal, linux)),
      arrive(linux, [ask('a-new', 1, [{ markdown: null, question: choiceQuestion }], 'Ship it?')]),
      arrive(personal, [
        storeAsk,
        { ...ask('a-done', 30, []), reply: { answers: [], note: 'done', answeredAt: now } },
      ]),
      Scene.Command.resolveAll(
        [
          LoadImage({ pairingId: personal.id, askId: 'a-store', src: 'shots/board.png' }),
          Message.LoadedImage({ key: `${personal.id}\na-store\nshots/board.png`, url: 'blob:board' }),
        ],
        [
          RenderDiagram({ code: diagram }),
          Message.RenderedDiagram({ code: diagram, svg: '<svg></svg>', width: 0 }),
        ],
      ),
      Scene.tap((simulation) => {
        const asks = entries('ask-entry', simulation.html)
        expect(asks).toHaveLength(2)
        expect(asks[0]).toContain('Which store?')
        expect(asks[0]).toContain('cache-store · Mac · personal')
        expect(asks[0]).toContain('5m')
        expect(asks[1]).toContain('Ship it?')
        expect(asks[1]).toContain('Box · default')
      }),
      Scene.expect(Scene.text('2 waiting')).toExist(),
    )
  })

  it('answers a single-choice ask in one tap, with its image and diagram shown, and returns to the queue', () => {
    Scene.scene(
      { update, view },
      Scene.given(online(personal)),
      arrive(personal, [storeAsk]),
      loadStoreVisuals,
      Scene.click(Scene.first(Scene.all.testId('ask-entry'))),
      Scene.expect(Scene.text('Deploys empty the cache.')).toExist(),
      Scene.expect(Scene.role('img', { name: 'the board' })).toHaveAttr('src', 'blob:board'),
      Scene.expect(Scene.text('recommended')).toExist(),
      Scene.tap((simulation) => {
        const svg = Scene.getAllByTestId('ask-screen')(simulation.html)
        expect(JSON.stringify(svg)).toContain('data-diagram')
      }),
      Scene.click(Scene.role('radio', { name: /Redis/ })),
      Scene.Command.expectExact(SendAnswer),
      Scene.Command.resolve(
        SendAnswer({
          pairingId: personal.id,
          askId: 'a-store',
          answers: [{ key: 'q1', picked: ['Redis'] }],
          note: '',
        }),
        Message.SentAnswer({ pairingId: personal.id, askId: 'a-store' }),
      ),
      Scene.expect(Scene.text('Queue')).toExist(),
      Scene.expectAll(Scene.all.testId('ask-entry')).toHaveCount(1),
      arrive(personal, [{ ...storeAsk, reply: { answers: [], note: '', answeredAt: now } }]),
      Scene.expectAll(Scene.all.testId('ask-entry')).toHaveCount(0),
    )
  })

  it('sends a written answer and a note with Send, and keeps them when sending fails', () => {
    const textAsk = ask('a-name', 2, [
      {
        markdown: null,
        question: {
          key: 'q1',
          kind: 'text',
          prompt: 'What should the flag be called?',
          context: null,
          choices: [],
          recommended: 'phone_asks',
        },
      },
    ])
    Scene.scene(
      { update, view },
      Scene.given(online(personal)),
      arrive(personal, [textAsk]),
      Scene.click(Scene.first(Scene.all.testId('ask-entry'))),
      Scene.expect(Scene.testId('ask-send')).toBeDisabled(),
      Scene.click(Scene.role('button', { name: 'Use the suggestion' })),
      Scene.type(Scene.role('textbox', { name: 'Note' }), 'Short is fine.'),
      Scene.click(Scene.testId('ask-send')),
      Scene.Command.resolve(
        SendAnswer({
          pairingId: personal.id,
          askId: 'a-name',
          answers: [{ key: 'q1', picked: [], text: 'phone_asks' }],
          note: 'Short is fine.',
        }),
        Message.FailedAnswer({ pairingId: personal.id, askId: 'a-name', message: 'Mac is not connected' }),
      ),
      Scene.expect(Scene.text('Not sent: Mac is not connected')).toExist(),
      Scene.expect(Scene.role('textbox', { name: 'What should the flag be called?' })).toHaveValue(
        'phone_asks',
      ),
      Scene.expect(Scene.role('textbox', { name: 'Note' })).toHaveValue('Short is fine.'),
    )
  })

  it('shows why an image or a diagram cannot be shown, in its place, and does not retry what the home refused', () => {
    Scene.scene(
      { update, view },
      Scene.given(online(personal)),
      arrive(personal, [storeAsk]),
      Scene.Command.resolveAll(
        [
          LoadImage({ pairingId: personal.id, askId: 'a-store', src: 'shots/board.png' }),
          Message.FailedImage({
            key: `${personal.id}\na-store\nshots/board.png`,
            message: 'ask a-store holds no copy of /work/shots/board.png',
            retry: false,
          }),
        ],
        [
          RenderDiagram({ code: diagram }),
          Message.FailedDiagram({ code: diagram, message: 'Parse error on line 2' }),
        ],
      ),
      Scene.click(Scene.first(Scene.all.testId('ask-entry'))),
      Scene.expect(Scene.text('ask a-store holds no copy of /work/shots/board.png')).toExist(),
      Scene.expect(Scene.text('Diagram failed to render: Parse error on line 2')).toExist(),
      arrive(personal, [storeAsk]),
      Scene.Command.expectNone(),
    )
  })

  it('tries an image again when its home comes back, after the connection dropped mid-load', () => {
    Scene.scene(
      { update, view },
      Scene.given(online(personal)),
      arrive(personal, [storeAsk]),
      Scene.Command.resolveAll(
        [
          LoadImage({ pairingId: personal.id, askId: 'a-store', src: 'shots/board.png' }),
          Message.FailedImage({
            key: `${personal.id}\na-store\nshots/board.png`,
            message: 'Could not load shots/board.png: Mac went offline',
            retry: true,
          }),
        ],
        [
          RenderDiagram({ code: diagram }),
          Message.RenderedDiagram({ code: diagram, svg: '<svg></svg>', width: 0 }),
        ],
      ),
      arrive(personal, [storeAsk]),
      Scene.Command.expectExact(LoadImage),
      Scene.Command.resolve(
        LoadImage({ pairingId: personal.id, askId: 'a-store', src: 'shots/board.png' }),
        Message.LoadedImage({ key: `${personal.id}\na-store\nshots/board.png`, url: 'blob:board' }),
      ),
      Scene.click(Scene.first(Scene.all.testId('ask-entry'))),
      Scene.click(Scene.role('button', { name: 'Open the board full screen' })),
      Scene.Mount.resolve(PinchZoom, Message.AttachedZoom()),
      Scene.expect(Scene.testId('zoom')).toExist(),
    )
  })

  it('loads an image the ask writes as an HTML img tag, as the desktop shows it', () => {
    const htmlAsk = ask('a-html', 1, [
      {
        markdown:
          '<p align="center"><img src="shots/caf&eacute;&#45;board&amp;copy;.png" alt="the wide board" width="600"></p>',
        question: null,
      },
      { markdown: null, question: choiceQuestion },
    ])
    Scene.scene(
      { update, view },
      Scene.given(online(personal)),
      arrive(personal, [htmlAsk]),
      Scene.Command.resolve(
        LoadImage({ pairingId: personal.id, askId: 'a-html', src: 'shots/café-board&copy;.png' }),
        Message.LoadedImage({ key: `${personal.id}\na-html\nshots/café-board&copy;.png`, url: 'blob:wide' }),
      ),
      Scene.click(Scene.first(Scene.all.testId('ask-entry'))),
      Scene.expect(Scene.role('img', { name: 'the wide board' })).toHaveAttr('src', 'blob:wide'),
    )
  })

  it('loads a reference-style image from the definition in its part', () => {
    const referenceAsk = ask('a-ref', 1, [
      { markdown: 'The board:\n\n![the board][shot]\n\n[shot]: shots/board.png', question: null },
      { markdown: null, question: choiceQuestion },
    ])
    Scene.scene(
      { update, view },
      Scene.given(online(personal)),
      arrive(personal, [referenceAsk]),
      Scene.Command.resolve(
        LoadImage({ pairingId: personal.id, askId: 'a-ref', src: 'shots/board.png' }),
        Message.LoadedImage({ key: `${personal.id}\na-ref\nshots/board.png`, url: 'blob:board' }),
      ),
      Scene.click(Scene.role('button', { name: /Which store\?/ })),
      Scene.expect(Scene.role('img', { name: 'the board' })).toHaveAttr('src', 'blob:board'),
    )
  })

  it('keeps an answer in flight when the ask is left and opened again, and shows its failure there', () => {
    const opened = (model: Model) =>
      update(model, Message.ClickedAsk({ pairingId: personal.id, askId: 'a-store' })).model
    let model = update(
      online(personal),
      Message.ReceivedAsks({ pairingId: personal.id, asks: [storeAsk] }),
    ).model
    model = opened(model)
    const tapped = update(model, Message.PickedChoice({ key: 'q1', label: 'Redis' }))
    expect(tapped.commands).toHaveLength(1)
    model = opened(update(tapped.model, Message.ClickedBack()).model)

    const again = update(model, Message.PickedChoice({ key: 'q1', label: 'Postgres' }))
    expect(again.commands ?? []).toHaveLength(0)
    Scene.scene({ update, view }, Scene.given(again.model), Scene.expect(Scene.text('Sending…')).toExist())

    model = update(
      update(again.model, Message.ClickedBack()).model,
      Message.FailedAnswer({ pairingId: personal.id, askId: 'a-store', message: 'Mac went offline' }),
    ).model
    Scene.scene(
      { update, view },
      Scene.given(opened(model)),
      Scene.expect(Scene.text('Not sent: Mac went offline')).toExist(),
    )
  })

  it('says so when the ask on screen is withdrawn', () => {
    Scene.scene(
      { update, view },
      Scene.given(online(personal)),
      arrive(personal, [ask('a-plain', 1, [{ markdown: null, question: choiceQuestion }])]),
      Scene.click(Scene.first(Scene.all.testId('ask-entry'))),
      arrive(personal, []),
      Scene.expect(Scene.text('This ask was withdrawn or closed.')).toExist(),
    )
  })
})
