import { Scene } from 'foldkit'
import { describe, expect, it } from 'vitest'
import { Message, SavePairings, subscriptions, update, view, type Model } from './main'
import type { Pairing } from './pairing'
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
})

const paired = (...pairings: ReadonlyArray<Pairing>): Model => ({
  screen: 'queue',
  loaded: true,
  foreground: true,
  homes: pairings.map((pairing) => ({ pairing, status: 'connecting', reason: '', sessions: [] })),
  pairingFlow: 'idle',
  pairingMessage: '',
  typedCode: '',
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
        Message.WelcomedByHome({ pairingId: personal.id, homeName: 'Mac', profileName: 'side projects' }),
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

describe('pairing again', () => {
  it('connects afresh even when the home and profile are the ones already paired', () => {
    const before = {
      ...paired(personal),
      homes: [
        { pairing: personal, status: 'closed' as const, reason: 'Mac ended this pairing', sessions: [] },
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
      Message.WelcomedByHome({ pairingId: personal.id, homeName: 'Mac', profileName: 'side projects' }),
    )
    expect(subscriptions.links.modelToDependencies(after)).toEqual(
      subscriptions.links.modelToDependencies(before),
    )
  })
})
