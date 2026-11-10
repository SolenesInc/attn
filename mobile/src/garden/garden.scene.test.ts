import { Scene } from 'foldkit'
import { describe, expect, it } from 'vitest'
import { AddNote, GetSeed, Message, MoveSeed, update, view, type Model } from '../main'
import type { Pairing } from '../pairing'
import type { GardenSeed, SeedDetail } from './seed'

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

const now = Date.parse('2026-11-02T12:00:00Z')

const online = (api = 6): Model => ({
  screen: 'queue',
  seedTrail: [],
  seedPage: { status: 'loading' },
  seedNotes: {},
  seedMove: null,
  openAsk: null,
  drafts: {},
  answering: {},
  crewStarts: {},
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
  homes: [
    {
      pairing: personal,
      api,
      link: 1,
      status: 'online',
      reason: '',
      sessions: [],
      crewBetweenSessions: [],
      listed: true,
      asks: [],
      garden: [],
    },
  ],
  pairingFlow: 'idle',
  pairingMessage: '',
  typedCode: '',
  pushToken: null,
  pushNote: '',
  pushRefusals: {},
  now,
})

const seed = (id: string, title: string, fields: Partial<GardenSeed> = {}): GardenSeed => ({
  id,
  slug: title.toLowerCase().replaceAll(' ', '-'),
  title,
  status: 'planted',
  tender: null,
  claimed: false,
  ready: true,
  plotId: null,
  waitsFor: [],
  progress: null,
  checks: null,
  stateChangedAt: now - 60_000,
  reason: null,
  ...fields,
})

const plot = seed('s-plot', 'Ship the phone', {
  ready: false,
  status: 'growing',
  progress: { total: 3, done: 1, withered: 0, growing: 1, parked: 0, ready: 0, blocked: 1 },
  checks: { confirmed: 1, total: 2 },
})
const reading = seed('s-read', 'Read seeds', {
  status: 'growing',
  ready: false,
  claimed: true,
  tender: 'phone-builder',
  plotId: 's-plot',
  checks: { confirmed: 0, total: 1 },
})
const moving = seed('s-move', 'Move seeds', { ready: false, plotId: 's-plot', waitsFor: ['s-read'] })
const loose = seed('s-loose', 'Tidy the docs')
const shelved = seed('s-shelf', 'Try a widget', { status: 'parked', ready: false })
const finished = seed('s-done', 'Spike', { status: 'harvested', ready: false, reason: 'Proved it' })

const plotDetail: SeedDetail = {
  seed: plot,
  body: '# Plan\n\nThe phone reads the **Garden**.',
  bodyTooLarge: null,
  checks: [{ id: 'C1', text: 'Wire tests pass', state: 'confirmed', from: null }],
  children: [moving, reading, seed('s-done', 'Spike', { status: 'harvested', reason: 'Proved it' })],
  log: [
    {
      id: 'n1',
      kind: 'handoff',
      body: 'Start with reading.',
      author: 'orchestrator',
      createdAt: now - 120_000,
    },
  ],
  logTotal: 1,
}

const readingDetail: SeedDetail = {
  seed: reading,
  body: 'List and read.',
  bodyTooLarge: null,
  checks: [{ id: 'C2', text: 'Reads a plot', state: 'open', from: 's-plot' }],
  children: [],
  log: [],
  logTotal: 0,
}

const garden = Scene.Subscription.emit(
  Message.ReceivedGarden({
    pairingId: personal.id,
    seeds: [finished, shelved, loose, moving, reading, plot],
  }),
)

const sectionTitles = (html: Parameters<ReturnType<typeof Scene.getAllByTestId>>[0]) =>
  Scene.getAllByTestId('seed-entry')(html).map(Scene.textContent)

describe('the Garden on the phone', () => {
  it('lists plots with their progress, then what grows, is ready, waits and is parked', () => {
    Scene.scene(
      { update, view },
      Scene.given(online()),
      Scene.click(Scene.role('button', { name: 'Garden' })),
      garden,
      Scene.expect(Scene.text('Plots · 1')).toExist(),
      Scene.expect(Scene.text('Growing · 1')).toExist(),
      Scene.expect(Scene.text('Ready · 1')).toExist(),
      Scene.expect(Scene.text('Blocked · 1')).toExist(),
      Scene.expect(Scene.text('Parked · 1')).toExist(),
      Scene.expect(Scene.text('Closed today · 1')).toExist(),
      Scene.tap((simulation) => {
        const rows = sectionTitles(simulation.html)
        expect(rows[0]).toContain('Ship the phone')
        expect(rows[0]).toContain('1/3 done · 1 growing · 1 blocked')
        expect(rows[0]).toContain('✓1/2')
        expect(rows[1]).toContain('Read seeds')
        expect(rows[1]).toContain('phone-builder · Ship the phone')
        expect(rows[2]).toContain('Tidy the docs')
        expect(rows[3]).toContain('Move seeds')
        expect(rows[3]).toContain('waits for 1')
        expect(rows[4]).toContain('Try a widget')
        expect(rows[5]).toContain('Spike')
        expect(rows[5]).toContain('Proved it')
      }),
    )
  })

  it('reads a plot, opens its child, adds a note, and parks the child with a reason', () => {
    Scene.scene(
      { update, view },
      Scene.given(online()),
      Scene.click(Scene.role('button', { name: 'Garden' })),
      garden,
      Scene.click(Scene.first(Scene.all.testId('seed-entry'))),
      Scene.Command.resolve(
        GetSeed({ pairingId: personal.id, seedId: 's-plot' }),
        Message.LoadedSeed({ pairingId: personal.id, seedId: 's-plot', detail: plotDetail }),
      ),
      Scene.expect(Scene.role('heading', { name: 'Plan' })).toExist(),
      Scene.expect(Scene.text('Garden')).toExist(),
      Scene.expect(Scene.text('Seeds · 3')).toExist(),
      Scene.expect(Scene.text('Proved it')).toExist(),
      Scene.expect(Scene.text('Start with reading.')).toExist(),
      Scene.click(Scene.role('button', { name: /Read seeds/ })),
      Scene.Command.resolve(
        GetSeed({ pairingId: personal.id, seedId: 's-read' }),
        Message.LoadedSeed({ pairingId: personal.id, seedId: 's-read', detail: readingDetail }),
      ),
      Scene.expect(Scene.role('button', { name: '‹ Ship the phone' })).toExist(),
      Scene.expect(Scene.text('growing · tended by phone-builder · Mac · personal')).toExist(),

      Scene.type(Scene.role('textbox', { name: 'Note' }), 'Checked from the phone.'),
      Scene.click(Scene.testId('note-send')),
      Scene.Command.resolve(
        AddNote({
          key: 'mac/p-personal/s-read',
          pairingId: personal.id,
          seedId: 's-read',
          body: 'Checked from the phone.',
        }),
        Message.AddedNote({ key: 'mac/p-personal/s-read', text: 'Checked from the phone.' }),
      ),
      Scene.Command.resolve(
        GetSeed({ pairingId: personal.id, seedId: 's-read' }),
        Message.LoadedSeed({
          pairingId: personal.id,
          seedId: 's-read',
          detail: {
            ...readingDetail,
            log: [
              { id: 'n2', kind: 'note', body: 'Checked from the phone.', author: 'the user', createdAt: now },
            ],
            logTotal: 1,
          },
        }),
      ),
      Scene.expect(Scene.role('textbox', { name: 'Note' })).toHaveValue(''),
      Scene.expectAll(Scene.all.testId('log-entry')).toHaveCount(1),

      Scene.click(Scene.role('button', { name: 'Park' })),
      Scene.expect(Scene.text('phone-builder tends this seed; park moves it anyway.')).toExist(),
      Scene.expect(Scene.testId('move-confirm')).toBeDisabled(),
      Scene.type(Scene.role('textbox', { name: 'What are you leaving it at?' }), 'Waiting on the build.'),
      Scene.click(Scene.testId('move-confirm')),
      Scene.Command.resolve(
        MoveSeed({
          key: 'mac/p-personal/s-read',
          pairingId: personal.id,
          seedId: 's-read',
          verb: 'park',
          text: 'Waiting on the build.',
        }),
        Message.MovedSeed({ key: 'mac/p-personal/s-read' }),
      ),
      Scene.Command.resolve(
        GetSeed({ pairingId: personal.id, seedId: 's-read' }),
        Message.LoadedSeed({
          pairingId: personal.id,
          seedId: 's-read',
          detail: { ...readingDetail, seed: { ...reading, status: 'parked', claimed: false, tender: null } },
        }),
      ),
      Scene.expect(Scene.text('parked · Mac · personal')).toExist(),
      Scene.expect(Scene.role('button', { name: 'Replant' })).toExist(),

      Scene.click(Scene.role('button', { name: '‹ Ship the phone' })),
      Scene.Command.resolve(
        GetSeed({ pairingId: personal.id, seedId: 's-plot' }),
        Message.LoadedSeed({ pairingId: personal.id, seedId: 's-plot', detail: plotDetail }),
      ),
      Scene.click(Scene.role('button', { name: '‹ Garden' })),
      Scene.expect(Scene.text('Plots · 1')).toExist(),
    )
  })

  it('keeps a move’s reason and shows why the home refused it', () => {
    Scene.scene(
      { update, view },
      Scene.given(online()),
      Scene.click(Scene.role('button', { name: 'Garden' })),
      garden,
      Scene.click(Scene.role('button', { name: /Tidy the docs/ })),
      Scene.Command.resolve(
        GetSeed({ pairingId: personal.id, seedId: 's-loose' }),
        Message.LoadedSeed({
          pairingId: personal.id,
          seedId: 's-loose',
          detail: { ...readingDetail, seed: loose },
        }),
      ),
      Scene.click(Scene.role('button', { name: 'Harvest' })),
      Scene.type(Scene.role('textbox', { name: 'What got done?' }), 'Docs are tidy.'),
      Scene.click(Scene.testId('move-confirm')),
      Scene.Command.resolve(
        MoveSeed({
          key: 'mac/p-personal/s-loose',
          pairingId: personal.id,
          seedId: 's-loose',
          verb: 'harvest',
          text: 'Docs are tidy.',
        }),
        Message.FailedMove({ key: 'mac/p-personal/s-loose', message: 's-loose is part of an open plot' }),
      ),
      Scene.expect(Scene.text('Not moved: s-loose is part of an open plot')).toExist(),
      Scene.expect(Scene.role('textbox', { name: 'What got done?' })).toHaveValue('Docs are tidy.'),
    )
  })

  it('rereads the open seed when the home says it changed', () => {
    let model = update(online(), Message.ClickedGardenTab()).model
    model = update(model, Message.ReceivedGarden({ pairingId: personal.id, seeds: [reading] })).model
    model = update(model, Message.ClickedSeed({ pairingId: personal.id, seedId: 's-read' })).model
    model = update(
      model,
      Message.LoadedSeed({ pairingId: personal.id, seedId: 's-read', detail: readingDetail }),
    ).model
    const same = update(model, Message.ReceivedGarden({ pairingId: personal.id, seeds: [reading] }))
    expect(same.commands ?? []).toHaveLength(0)
    const moved = update(
      model,
      Message.ReceivedGarden({ pairingId: personal.id, seeds: [{ ...reading, status: 'parked' }] }),
    )
    expect(moved.commands).toHaveLength(1)
    const gone = update(model, Message.ReceivedGarden({ pairingId: personal.id, seeds: [] }))
    expect(gone.commands).toHaveLength(1)
  })

  it('does not call a Garden it has not received empty', () => {
    Scene.scene(
      { update, view },
      Scene.given({ ...online(), loaded: false, homes: [] }),
      Scene.Subscription.emit(Message.LoadedPairings({ pairings: [personal] })),
      Scene.click(Scene.role('button', { name: 'Garden' })),
      Scene.expect(Scene.text('Connecting to Mac · personal…')).toExist(),
      Scene.expect(Scene.text('Nothing is planted.')).not.toExist(),
      Scene.Subscription.emit(Message.WentOffline({ pairingId: personal.id })),
      Scene.expect(Scene.text('Mac · personal is offline.')).toExist(),
      Scene.expect(Scene.text('Nothing is planted.')).not.toExist(),
      Scene.Subscription.emit(
        Message.WelcomedByHome({ pairingId: personal.id, api: 6, homeName: 'Mac', profileName: 'personal' }),
      ),
      Scene.expect(Scene.text('Loading the Garden from Mac · personal…')).toExist(),
      Scene.Subscription.emit(Message.ReceivedGarden({ pairingId: personal.id, seeds: [] })),
      Scene.expect(Scene.text('Loading the Garden from Mac · personal…')).not.toExist(),
      Scene.expect(Scene.text('Nothing is planted.')).toExist(),
    )
  })

  it('marks the seeds of a home that went away until it sends its Garden again', () => {
    const work: Pairing = {
      ...personal,
      id: 'box/p-work',
      homeId: 'box',
      homeName: 'Box',
      profileId: 'p-work',
      profileName: 'work',
    }
    const both = online()
    Scene.scene(
      { update, view },
      Scene.given({ ...both, homes: [...both.homes, { ...both.homes[0]!, pairing: work }] }),
      Scene.click(Scene.role('button', { name: 'Garden' })),
      garden,
      Scene.Subscription.emit(
        Message.ReceivedGarden({ pairingId: work.id, seeds: [seed('s-box', 'Review box')] }),
      ),
      Scene.Subscription.emit(Message.WentOffline({ pairingId: personal.id })),
      Scene.expect(
        Scene.text('Mac · personal is offline. Its seeds are as of the last connection.'),
      ).toExist(),
      Scene.tap((simulation) => {
        const rows = sectionTitles(simulation.html)
        expect(rows.find((row) => row.includes('Read seeds'))).toContain('Mac · personal · offline')
        expect(rows.find((row) => row.includes('Review box'))).toContain('Box · work')
        expect(rows.find((row) => row.includes('Review box'))).not.toContain('offline')
      }),
      Scene.Subscription.emit(
        Message.WelcomedByHome({ pairingId: personal.id, api: 6, homeName: 'Mac', profileName: 'personal' }),
      ),
      Scene.Subscription.emit(
        Message.ReceivedGarden({ pairingId: personal.id, seeds: [{ ...reading, status: 'parked' }] }),
      ),
      Scene.expect(
        Scene.text('Mac · personal is offline. Its seeds are as of the last connection.'),
      ).not.toExist(),
      Scene.tap((simulation) => {
        const rows = sectionTitles(simulation.html)
        expect(rows).toHaveLength(2)
        expect(rows.join('\n')).not.toContain('offline')
        expect(rows.find((row) => row.includes('Read seeds'))).toContain('parked')
      }),
    )
  })

  it('shows an agent’s new handoff and edited plan on an open seed, live and after a reconnect', () => {
    const handoff = {
      id: 'n3',
      kind: 'handoff',
      body: 'Handing off: read the new plan.',
      author: 'phone-builder',
      createdAt: now,
    }
    Scene.scene(
      { update, view },
      Scene.given(online()),
      Scene.click(Scene.role('button', { name: 'Garden' })),
      garden,
      Scene.click(Scene.role('button', { name: /Read seeds/ })),
      Scene.Command.resolve(
        GetSeed({ pairingId: personal.id, seedId: 's-read' }),
        Message.LoadedSeed({ pairingId: personal.id, seedId: 's-read', detail: readingDetail }),
      ),
      Scene.expect(Scene.text('List and read.')).toExist(),

      Scene.Subscription.emit(Message.ChangedSeed({ pairingId: personal.id, seedId: 's-read' })),
      Scene.Command.resolve(
        GetSeed({ pairingId: personal.id, seedId: 's-read' }),
        Message.LoadedSeed({
          pairingId: personal.id,
          seedId: 's-read',
          detail: { ...readingDetail, log: [handoff], logTotal: 1 },
        }),
      ),
      Scene.expect(Scene.text('Handing off: read the new plan.')).toExist(),

      Scene.Subscription.emit(Message.ChangedForeground({ foreground: false })),
      Scene.Subscription.emit(Message.ChangedForeground({ foreground: true })),
      Scene.Subscription.emit(
        Message.WelcomedByHome({ pairingId: personal.id, api: 7, homeName: 'Mac', profileName: 'personal' }),
      ),
      Scene.Command.resolve(
        GetSeed({ pairingId: personal.id, seedId: 's-read' }),
        Message.LoadedSeed({
          pairingId: personal.id,
          seedId: 's-read',
          detail: { ...readingDetail, body: 'Read seeds, then plots.', log: [handoff], logTotal: 1 },
        }),
      ),
      Scene.expect(Scene.text('Read seeds, then plots.')).toExist(),
      Scene.expect(Scene.text('List and read.')).toBeAbsent(),
    )
  })

  it('leaves a seed the phone does not show alone when it changes', () => {
    let model = update(online(), Message.ClickedGardenTab()).model
    model = update(model, Message.ReceivedGarden({ pairingId: personal.id, seeds: [reading, loose] })).model
    model = update(model, Message.ClickedSeed({ pairingId: personal.id, seedId: 's-read' })).model
    expect(
      update(model, Message.ChangedSeed({ pairingId: personal.id, seedId: 's-loose' })).commands ?? [],
    ).toHaveLength(0)
    const listing = update(model, Message.ClickedGardenTab()).model
    expect(
      update(listing, Message.ChangedSeed({ pairingId: personal.id, seedId: 's-read' })).commands ?? [],
    ).toHaveLength(0)
  })

  it('says which home is too old to show the Garden', () => {
    Scene.scene(
      { update, view },
      Scene.given(online(5)),
      Scene.click(Scene.role('button', { name: 'Garden' })),
      Scene.expect(
        Scene.text('attn on Mac · personal is too old to show the Garden. Update it to the latest attn.'),
      ).toExist(),
    )
  })
})
