import type { SeedStatus, Verb } from './seed'

type Column = 'ready' | 'growing' | 'parked' | 'closed'

const legalVerbs = (status: SeedStatus, target: Column): Verb[] => {
  const open = status === 'planted' || status === 'growing' || status === 'parked'
  switch (target) {
    case 'closed':
      return open ? ['harvest', 'wither'] : []
    case 'parked':
      return status === 'planted' || status === 'growing' ? ['park'] : []
    case 'ready':
      return status === 'planted' ? [] : ['replant']
    case 'growing':
      return []
  }
}

export const verbsFor = (seed: { readonly status: SeedStatus }): Verb[] =>
  (['growing', 'parked', 'closed', 'ready'] as const).flatMap((column) => legalVerbs(seed.status, column))
