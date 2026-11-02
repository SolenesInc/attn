import { Scene } from 'foldkit'
import { describe, expect, it } from 'vitest'
import {
  KeepFocus,
  Message,
  RegisterPush,
  pushHomeKey,
  SavePairings,
  HideKeyboard,
  SendPrompt,
  TerminalMount,
  subscriptions,
  update,
  view,
  type Model,
} from './main'
import type { Pairing } from './pairing'
import { notificationsOff } from './push'
import type { QueueSession } from './queue'

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
const work: Pairing = { ...personal, id: 'mac/p-work', profileId: 'p-work', profileName: 'work' }
const linux: Pairing = {
  ...personal,
  id: 'box/p-default',
  homeId: 'box',
  homeName: 'Box',
  profileId: 'p-default',
  profileName: 'default',
}

const now = Date.parse('2026-11-02T12:00:00Z')
const minutesAgo = (minutes: number) => now - minutes * 60_000

const session = (
  id: string,
  title: string,
  state: QueueSession['state'],
  waitingSince: number | null = null,
): QueueSession => ({
  id,
  title,
  agent: 'claude',
  state,
  waitingSince,
  host: null,
  offline: false,
  terminalId: null,
})

const paired = (...pairings: ReadonlyArray<Pairing>): Model => ({
  screen: 'queue',
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
    status: 'connecting',
    reason: '',
    sessions: [],
    listed: false,
    asks: [],
  })),
  pairingFlow: 'idle',
  pairingMessage: '',
  typedCode: '',
  pushToken: null,
  pushNote: '',
  pushRefusals: {},
  now,
})

const expectEntries = (expected: ReadonlyArray<ReadonlyArray<string>>) =>
  Scene.tap<Model, Message>((simulation) => {
    const shown = Scene.getAllByTestId('queue-entry')(simulation.html).map(Scene.textContent)
    expect(shown).toHaveLength(expected.length)
    expected.forEach((parts, index) => {
      for (const part of parts) expect(shown[index]).toContain(part)
    })
  })

describe('the queue merges every pairing into one list', () => {
  it('puts waiting agents first, longest waiting first, across homes and profiles', () => {
    Scene.scene(
      { update, view },
      Scene.given(paired(personal, work, linux)),
      Scene.Subscription.emit(
        Message.ReceivedSessions({
          pairingId: personal.id,
          sessions: [
            session('a', 'Refactor parser', 'working'),
            session('b', 'Land the fix', 'waiting', minutesAgo(3)),
            session('c', 'Old idea', 'idle'),
          ],
        }),
      ),
      Scene.Subscription.emit(
        Message.ReceivedSessions({
          pairingId: work.id,
          sessions: [
            session('a', 'Approve deploy', 'needs_approval', minutesAgo(12)),
            session('d', 'Write docs', 'working'),
          ],
        }),
      ),
      Scene.Subscription.emit(
        Message.ReceivedSessions({
          pairingId: linux.id,
          sessions: [session('e', 'Bisect flake', 'waiting', minutesAgo(7))],
        }),
      ),
      expectEntries([
        ['Approve deploy', 'Mac · work', 'needs approval', '12m'],
        ['Bisect flake', 'Box · default', 'waiting', '7m'],
        ['Land the fix', 'Mac · personal', 'waiting', '3m'],
        ['Refactor parser', 'Mac · personal', 'working'],
        ['Write docs', 'Mac · work', 'working'],
        ['Old idea', 'Mac · personal', 'idle'],
      ]),
    )
  })

  it('follows a session that changes state on its home', () => {
    Scene.scene(
      { update, view },
      Scene.given(paired(personal, work)),
      Scene.Subscription.emit(
        Message.ReceivedSessions({
          pairingId: personal.id,
          sessions: [session('a', 'Refactor parser', 'working')],
        }),
      ),
      Scene.Subscription.emit(
        Message.ReceivedSessions({
          pairingId: work.id,
          sessions: [session('b', 'Write docs', 'waiting', minutesAgo(1))],
        }),
      ),
      expectEntries([
        ['Write docs', 'waiting'],
        ['Refactor parser', 'working'],
      ]),
      Scene.Subscription.emit(
        Message.ReceivedSessions({
          pairingId: personal.id,
          sessions: [session('a', 'Refactor parser', 'waiting', minutesAgo(5))],
        }),
      ),
      expectEntries([
        ['Refactor parser', 'Mac · personal', 'waiting', '5m'],
        ['Write docs', 'Mac · work', 'waiting'],
      ]),
    )
  })

  it('shows a home that went offline as offline entries that keep its last sessions, below the live ones', () => {
    Scene.scene(
      { update, view },
      Scene.given(paired(personal, linux)),
      Scene.Subscription.emit(
        Message.ReceivedSessions({
          pairingId: personal.id,
          sessions: [
            session('a', 'Land the fix', 'waiting', minutesAgo(30)),
            session('b', 'Refactor parser', 'working'),
          ],
        }),
      ),
      Scene.Subscription.emit(
        Message.ReceivedSessions({
          pairingId: linux.id,
          sessions: [session('c', 'Bisect flake', 'working')],
        }),
      ),
      Scene.Subscription.emit(Message.WentOffline({ pairingId: personal.id })),
      expectEntries([
        ['Bisect flake', 'Box · default', 'working'],
        ['Mac · personal', 'Mac is offline', 'offline'],
        ['Land the fix', 'Mac · personal', 'offline'],
        ['Refactor parser', 'Mac · personal', 'offline'],
      ]),
      Scene.Subscription.emit(
        Message.ReceivedSessions({
          pairingId: personal.id,
          sessions: [session('b', 'Refactor parser', 'working')],
        }),
      ),
      expectEntries([
        ['Bisect flake', 'working'],
        ['Refactor parser', 'Mac · personal', 'working'],
      ]),
    )
  })

  it('names the home and the address when a home cannot be reached', () => {
    const reason = 'Mac did not answer at wss://cloud.attn.codes/v1/homes/mac/device within 10 s'
    Scene.scene(
      { update, view },
      Scene.given(paired(personal)),
      Scene.Subscription.emit(Message.LostHome({ pairingId: personal.id, reason })),
      expectEntries([['Mac · personal', reason, 'unreachable']]),
    )
  })

  it('says when a home no longer allows this phone, and keeps the other homes live', () => {
    const reason = 'Mac no longer allows this phone; pair it again from Settings → Phones'
    Scene.scene(
      { update, view },
      Scene.given(paired(personal, linux)),
      Scene.Subscription.emit(
        Message.ReceivedSessions({
          pairingId: personal.id,
          sessions: [session('a', 'Land the fix', 'working')],
        }),
      ),
      Scene.Subscription.emit(
        Message.ReceivedSessions({
          pairingId: linux.id,
          sessions: [session('b', 'Bisect flake', 'working')],
        }),
      ),
      Scene.Subscription.emit(Message.ClosedByHome({ pairingId: personal.id, reason })),
      expectEntries([
        ['Bisect flake', 'Box · default', 'working'],
        ['Mac · personal', reason, 'not allowed'],
        ['Land the fix', 'Mac · personal', 'offline'],
      ]),
    )
  })

  it('shows the names the home welcomes the phone with, after a rename on the home', () => {
    Scene.scene(
      { update, view },
      Scene.given(paired(personal, work)),
      Scene.Subscription.emit(
        Message.WelcomedByHome({
          api: 4,
          pairingId: personal.id,
          homeName: 'Mac',
          profileName: 'side projects',
        }),
      ),
      Scene.Command.resolve(SavePairings, Message.SavedPairings()),
      Scene.Subscription.emit(
        Message.ReceivedSessions({
          pairingId: personal.id,
          sessions: [session('a', 'Land the fix', 'working')],
        }),
      ),
      Scene.Subscription.emit(
        Message.ReceivedSessions({ pairingId: work.id, sessions: [session('b', 'Write docs', 'working')] }),
      ),
      expectEntries([
        ['Land the fix', 'Mac · side projects'],
        ['Write docs', 'Mac · work'],
      ]),
    )
  })

  it('forgets a home that is not connected from its queue entry, and keeps its other pairings', () => {
    Scene.scene(
      { update, view },
      Scene.given(paired(personal, work)),
      Scene.Subscription.emit(
        Message.ReceivedSessions({ pairingId: work.id, sessions: [session('b', 'Write docs', 'working')] }),
      ),
      Scene.Subscription.emit(Message.WentOffline({ pairingId: personal.id })),
      expectEntries([
        ['Write docs', 'Mac · work', 'working'],
        ['Mac · personal', 'Mac is offline', 'offline'],
      ]),
      Scene.click(
        Scene.within(Scene.last(Scene.all.testId('queue-entry')), Scene.role('button', { name: 'Forget' })),
      ),
      Scene.Command.expectExact(SavePairings({ pairings: [work] })),
      Scene.Command.resolve(SavePairings, Message.SavedPairings()),
      expectEntries([['Write docs', 'Mac · work', 'working']]),
    )
  })

  it('drops a forgotten pairing from the queue', () => {
    Scene.scene(
      { update, view },
      Scene.given({ ...paired(personal, work), screen: 'pairings' }),
      Scene.Subscription.emit(
        Message.ReceivedSessions({
          pairingId: personal.id,
          sessions: [session('a', 'Land the fix', 'waiting', minutesAgo(2))],
        }),
      ),
      Scene.Subscription.emit(
        Message.ReceivedSessions({ pairingId: work.id, sessions: [session('b', 'Write docs', 'working')] }),
      ),
      Scene.click(
        Scene.within(Scene.first(Scene.all.testId('pairing-row')), Scene.role('button', { name: 'Forget' })),
      ),
      Scene.Command.resolve(SavePairings, Message.SavedPairings()),
      Scene.click(Scene.role('button', { name: 'Queue' })),
      expectEntries([['Write docs', 'Mac · work', 'working']]),
    )
  })
})

describe('an empty queue', () => {
  it('says there are no agents only once the welcomed home has listed its sessions', () => {
    Scene.scene(
      { update, view },
      Scene.given(paired(personal)),
      Scene.Subscription.emit(
        Message.WelcomedByHome({ api: 4, pairingId: personal.id, homeName: 'Mac', profileName: 'personal' }),
      ),
      Scene.expect(Scene.text('No agents yet.')).toBeAbsent(),
      Scene.Subscription.emit(Message.ReceivedSessions({ pairingId: personal.id, sessions: [] })),
      Scene.expect(Scene.text('No agents yet.')).toExist(),
    )
  })
})

describe('pairing again', () => {
  it('connects afresh even when the home and profile are the ones already paired', () => {
    const before = {
      ...paired(personal),
      homes: [
        {
          pairing: personal,
          api: 4,
          link: 1,
          status: 'closed' as const,
          reason: 'Mac ended this pairing',
          sessions: [],
          listed: false,
          asks: [],
        },
      ],
    }
    const { model: after } = update(
      before,
      Message.Paired({ pairing: { ...personal, pairedAt: personal.pairedAt + 60_000 } }),
    )
    const links = (model: Model) => subscriptions.links.modelToDependencies(model)
    expect(links(after)).not.toEqual(links(before))
    expect(links(after).connected).toHaveLength(1)
  })

  it('keeps every link running when only a profile name changes', () => {
    const before = paired(personal, work)
    const { model: after } = update(
      before,
      Message.WelcomedByHome({
        api: 4,
        pairingId: personal.id,
        homeName: 'Mac',
        profileName: 'side projects',
      }),
    )
    expect(subscriptions.links.modelToDependencies(after)).toEqual(
      subscriptions.links.modelToDependencies(before),
    )
  })
})

const withSession = (api: number): Model => ({
  ...paired(personal),
  homes: [
    {
      pairing: personal,
      api,
      link: 1,
      status: 'online',
      reason: '',
      sessions: [
        { ...session('a', 'Land the fix', 'waiting', minutesAgo(2)), terminalId: 't-a' },
        { ...session('b', 'Write docs', 'waiting', minutesAgo(1)), terminalId: 't-b' },
      ],
      listed: true,
      asks: [],
    },
  ],
})

const keepFocus = Scene.Mount.resolveAll(
  [KeepFocus, Message.CompletedCommand()],
  [KeepFocus, Message.CompletedCommand()],
)

const composer = Scene.placeholder('Reply to the agent…')

const openSession = (title: string, terminalId: string, link = 1) =>
  [
    Scene.click(Scene.text(title)),
    keepFocus,
    Scene.Mount.expectHas(TerminalMount({ pairingId: personal.id, terminalId, link })),
    Scene.Mount.resolve(TerminalMount, Message.ChangedTerminalStatus({ text: '' })),
  ] as const

const backToQueue = (terminalId: string) =>
  [
    Scene.click(Scene.role('button', { name: '‹ Queue' })),
    Scene.Mount.expectEnded(
      TerminalMount({ pairingId: personal.id, terminalId, link: 1 }),
      KeepFocus(),
      KeepFocus(),
    ),
    Scene.Command.resolve(HideKeyboard, Message.CompletedCommand()),
  ] as const

describe('answering an agent from the phone', () => {
  it('keeps the words in the composer until the home delivers them', () => {
    Scene.scene(
      { update, view },
      Scene.given(withSession(4)),
      ...openSession('Land the fix', 't-a'),
      Scene.type(composer, 'Yes, land it.'),
      Scene.click(Scene.role('button', { name: 'Send' })),
      Scene.Command.resolve(
        SendPrompt,
        Message.FailedPrompt({
          key: `${personal.id}/a`,
          message: 'session input blocked by pending approval',
        }),
      ),
      Scene.expect(composer).toHaveValue('Yes, land it.'),
      Scene.expect(Scene.text('session input blocked by pending approval')).toExist(),
      Scene.click(Scene.role('button', { name: 'Send' })),
      Scene.Command.resolve(
        SendPrompt,
        Message.DeliveredPrompt({ key: `${personal.id}/a`, text: 'Yes, land it.' }),
      ),
      Scene.expect(composer).toHaveValue(''),
      Scene.expect(Scene.text('session input blocked by pending approval')).not.toExist(),
    )
  })

  it('keeps each session’s draft while the person moves between sessions', () => {
    Scene.scene(
      { update, view },
      Scene.given(withSession(4)),
      ...openSession('Land the fix', 't-a'),
      Scene.type(composer, 'Half a thought'),
      ...backToQueue('t-a'),
      ...openSession('Write docs', 't-b'),
      Scene.expect(composer).toHaveValue(''),
      Scene.type(composer, 'Ship the docs.'),
      ...backToQueue('t-b'),
      ...openSession('Land the fix', 't-a'),
      Scene.expect(composer).toHaveValue('Half a thought'),
    )
  })

  it('keeps words typed while an earlier prompt was being delivered', () => {
    const key = `${personal.id}/b`
    const sending: Model = {
      ...withSession(4),
      screen: 'session',
      openSession: { pairingId: personal.id, sessionId: 'b' },
      composers: { [key]: { text: 'Ship the docs.', sending: true, note: '' } },
    }
    const typed = update(sending, Message.ChangedComposer({ value: 'And the changelog.' })).model
    const delivered = update(typed, Message.DeliveredPrompt({ key, text: 'Ship the docs.' })).model
    expect(delivered.composers[key]).toEqual({ text: 'And the changelog.', sending: false, note: '' })
    const elsewhere = update(
      { ...typed, openSession: { pairingId: personal.id, sessionId: 'a' } },
      Message.DeliveredPrompt({ key, text: 'Ship the docs.' }),
    ).model
    expect(elsewhere.composers[key]?.text).toBe('And the changelog.')
  })

  it('opens the terminal again on the new connection after the app returns to the foreground', () => {
    Scene.scene(
      { update, view },
      Scene.given(withSession(4)),
      ...openSession('Land the fix', 't-a'),
      Scene.Subscription.emit(Message.ChangedForeground({ foreground: false })),
      Scene.Mount.expectEnded(TerminalMount),
      Scene.Subscription.emit(Message.ChangedForeground({ foreground: true })),
      Scene.Subscription.emit(Message.StartedLink({ pairingId: personal.id, link: 2 })),
      Scene.Mount.expectHas(TerminalMount({ pairingId: personal.id, terminalId: 't-a', link: 2 })),
      Scene.Mount.resolve(TerminalMount, Message.ChangedTerminalStatus({ text: '' })),
    )
  })

  it('says the home is not connected and turns the keys and Send off until it is back', () => {
    Scene.scene(
      { update, view },
      Scene.given(withSession(4)),
      ...openSession('Land the fix', 't-a'),
      Scene.type(composer, 'Yes'),
      Scene.Subscription.emit(
        Message.LostHome({ pairingId: personal.id, reason: 'Mac: no answer for 10 s; reconnecting' }),
      ),
      Scene.expect(Scene.testId('home-offline')).toHaveText(
        'Mac: no answer for 10 s; reconnecting. Keys and Send work again when it is back.',
      ),
      Scene.expect(Scene.role('button', { name: '^C' })).toBeDisabled(),
      Scene.expect(Scene.role('button', { name: 'Send' })).toBeDisabled(),
      Scene.Subscription.emit(
        Message.WelcomedByHome({ pairingId: personal.id, api: 4, homeName: 'Mac', profileName: 'personal' }),
      ),
      Scene.expect(Scene.testId('home-offline')).not.toExist(),
      Scene.expect(Scene.role('button', { name: '^C' })).not.toBeDisabled(),
    )
  })

  it('offers Hide to close the keyboard only while the keyboard is open', () => {
    Scene.scene(
      { update, view },
      Scene.given(withSession(4)),
      ...openSession('Land the fix', 't-a'),
      Scene.expect(Scene.role('button', { name: 'Hide' })).not.toExist(),
      Scene.Subscription.emit(Message.ChangedKeyboard({ height: 336 })),
      Scene.click(Scene.role('button', { name: 'Hide' })),
      Scene.Command.expectHas(HideKeyboard),
      Scene.Command.resolve(HideKeyboard, Message.CompletedCommand()),
      Scene.Subscription.emit(Message.ChangedKeyboard({ height: 0 })),
      Scene.expect(Scene.role('button', { name: 'Hide' })).not.toExist(),
    )
  })

  it('says why when the home is too old to open terminals', () => {
    Scene.scene(
      { update, view },
      Scene.given(withSession(3)),
      Scene.click(Scene.text('Land the fix')),
      Scene.Mount.expectNone(),
      Scene.expect(
        Scene.text('attn on Mac is too old to open terminals from a phone. Update it to the latest attn.'),
      ).toExist(),
    )
  })
})

describe('push notifications', () => {
  it('registers every welcomed home, and each again when iOS rotates the token', () => {
    const first = { token: 'aa'.repeat(32), environment: 'production' as const }
    const rotated = { token: 'bb'.repeat(32), environment: 'production' as const }
    const at = (pairing: Pairing, pushToken: typeof first) =>
      RegisterPush({ cloud: pairing.cloud, homeId: pairing.homeId, pushToken })
    const registered = (pairing: Pairing) =>
      Message.RegisteredPush({ homeKey: pushHomeKey(pairing.cloud, pairing.homeId) })
    Scene.scene(
      { update, view },
      Scene.given(paired(personal, work, linux)),
      Scene.Subscription.emit(
        Message.WelcomedByHome({ api: 4, pairingId: personal.id, homeName: 'Mac', profileName: 'personal' }),
      ),
      Scene.Command.expectNone(),
      Scene.Subscription.emit(Message.ReceivedSessions({ pairingId: personal.id, sessions: [] })),
      Scene.Subscription.emit(Message.ReceivedSessions({ pairingId: work.id, sessions: [] })),
      Scene.Subscription.emit(Message.ReceivedPushToken({ pushToken: first })),
      Scene.Command.expectExact(at(personal, first)),
      Scene.Command.resolve(at(personal, first), registered(personal)),
      Scene.Subscription.emit(
        Message.WelcomedByHome({ api: 4, pairingId: linux.id, homeName: 'Box', profileName: 'default' }),
      ),
      Scene.Command.expectExact(at(linux, first)),
      Scene.Command.resolve(at(linux, first), registered(linux)),
      Scene.Subscription.emit(Message.ReceivedSessions({ pairingId: linux.id, sessions: [] })),
      Scene.Subscription.emit(Message.ReceivedPushToken({ pushToken: rotated })),
      Scene.Command.expectExact(at(personal, rotated), at(linux, rotated)),
      Scene.Command.resolve(at(personal, rotated), registered(personal)),
      Scene.Command.resolve(at(linux, rotated), registered(linux)),
    )
  })

  it('names each home that refused the token until it accepts one, and says when notifications are off', () => {
    const pushToken = { token: 'aa'.repeat(32), environment: 'production' as const }
    const refusal = 'Attn Cloud refused this phone’s push token (403): device is not on the allowlist'
    Scene.scene(
      { update, view },
      Scene.given({ ...paired(personal, linux), pushToken }),
      Scene.Subscription.emit(
        Message.WelcomedByHome({ api: 4, pairingId: personal.id, homeName: 'Mac', profileName: 'personal' }),
      ),
      Scene.Command.resolve(
        RegisterPush,
        Message.FailedPush({ homeKey: pushHomeKey(personal.cloud, personal.homeId), message: refusal }),
      ),
      Scene.Subscription.emit(
        Message.WelcomedByHome({ api: 4, pairingId: linux.id, homeName: 'Box', profileName: 'default' }),
      ),
      Scene.Command.resolve(
        RegisterPush,
        Message.RegisteredPush({ homeKey: pushHomeKey(linux.cloud, linux.homeId) }),
      ),
      Scene.expect(Scene.text(`Mac: ${refusal}`)).toExist(),
      Scene.Subscription.emit(
        Message.WelcomedByHome({ api: 4, pairingId: personal.id, homeName: 'Mac', profileName: 'personal' }),
      ),
      Scene.Command.resolve(
        RegisterPush,
        Message.RegisteredPush({ homeKey: pushHomeKey(personal.cloud, personal.homeId) }),
      ),
      Scene.expect(Scene.text(`Mac: ${refusal}`)).not.toExist(),
      Scene.Subscription.emit(Message.RefusedPush({ message: notificationsOff })),
      Scene.expect(Scene.text(notificationsOff)).toExist(),
    )
  })

  it('opens the waiting agent’s session when its notification is tapped', () => {
    Scene.scene(
      { update, view },
      Scene.given(withSession(4)),
      Scene.Subscription.emit(
        Message.TappedNotification({ pairingId: personal.id, sessionId: 'a', askId: '' }),
      ),
      keepFocus,
      Scene.Mount.expectHas(TerminalMount({ pairingId: personal.id, terminalId: 't-a', link: 1 })),
      Scene.Mount.resolve(TerminalMount, Message.ChangedTerminalStatus({ text: '' })),
    )
  })

  it('shows the tapped session connecting, never offline or gone, while the app comes back', () => {
    const started = session('c', 'Ship the release', 'waiting', minutesAgo(1))
    Scene.scene(
      { update, view },
      Scene.given(withSession(4)),
      Scene.Subscription.emit(Message.ChangedForeground({ foreground: false })),
      Scene.Subscription.emit(Message.ChangedForeground({ foreground: true })),
      Scene.Subscription.emit(
        Message.TappedNotification({ pairingId: personal.id, sessionId: 'c', askId: '' }),
      ),
      ...connectingTo('Mac'),
      Scene.Subscription.emit(Message.StartedLink({ pairingId: personal.id, link: 2 })),
      Scene.Subscription.emit(
        Message.WelcomedByHome({ pairingId: personal.id, api: 4, homeName: 'Mac', profileName: 'personal' }),
      ),
      ...connectingTo('Mac'),
      Scene.Subscription.emit(
        Message.ReceivedSessions({ pairingId: personal.id, sessions: [{ ...started, terminalId: 't-c' }] }),
      ),
      keepFocus,
      Scene.Mount.expectHas(TerminalMount({ pairingId: personal.id, terminalId: 't-c', link: 2 })),
      Scene.Mount.resolve(TerminalMount, Message.ChangedTerminalStatus({ text: '' })),
      Scene.expect(Scene.text('Ship the release')).toExist(),
    )
  })

  it('shows a cached session connecting, not offline, until its home is back', () => {
    Scene.scene(
      { update, view },
      Scene.given(withSession(4)),
      Scene.Subscription.emit(Message.ChangedForeground({ foreground: false })),
      Scene.Subscription.emit(Message.ChangedForeground({ foreground: true })),
      Scene.Subscription.emit(
        Message.TappedNotification({ pairingId: personal.id, sessionId: 'a', askId: '' }),
      ),
      keepFocus,
      Scene.expect(Scene.text('Connecting to Mac…')).toExist(),
      Scene.expect(Scene.testId('home-offline')).not.toExist(),
      Scene.expect(Scene.text('connecting… · Mac · personal')).toExist(),
      Scene.Subscription.emit(Message.StartedLink({ pairingId: personal.id, link: 2 })),
      Scene.Mount.expectHas(TerminalMount({ pairingId: personal.id, terminalId: 't-a', link: 2 })),
      Scene.Mount.resolve(TerminalMount, Message.ChangedTerminalStatus({ text: '' })),
      Scene.expect(Scene.testId('home-offline')).not.toExist(),
    )
  })

  it('shows the tapped session connecting when the tap launches the app', () => {
    Scene.scene(
      { update, view },
      Scene.given({ ...paired(), loaded: false }),
      Scene.Subscription.emit(
        Message.TappedNotification({ pairingId: personal.id, sessionId: 'a', askId: '' }),
      ),
      ...connectingTo(''),
      Scene.Subscription.emit(Message.LoadedPairings({ pairings: [personal] })),
      ...connectingTo('Mac'),
      Scene.Subscription.emit(Message.StartedLink({ pairingId: personal.id, link: 1 })),
      Scene.Subscription.emit(
        Message.WelcomedByHome({ pairingId: personal.id, api: 4, homeName: 'Mac', profileName: 'personal' }),
      ),
      Scene.Subscription.emit(
        Message.ReceivedSessions({
          pairingId: personal.id,
          sessions: [{ ...session('a', 'Land the fix', 'waiting', minutesAgo(2)), terminalId: 't-a' }],
        }),
      ),
      keepFocus,
      Scene.Mount.expectHas(TerminalMount({ pairingId: personal.id, terminalId: 't-a', link: 1 })),
      Scene.Mount.resolve(TerminalMount, Message.ChangedTerminalStatus({ text: '' })),
    )
  })
})

const connectingTo = (homeName: string) =>
  [
    Scene.expect(Scene.text(homeName ? `Connecting to ${homeName}…` : 'Connecting…')).toExist(),
    Scene.expect(Scene.text('This session is gone.')).not.toExist(),
    Scene.expect(Scene.testId('home-offline')).not.toExist(),
  ] as const
