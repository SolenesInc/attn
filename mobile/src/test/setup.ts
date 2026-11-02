import { setup } from 'foldkit/test/vitest'
import { vi } from 'vitest'

vi.mock('../appleCrypto', () => import('./nobleCrypto'))

setup()
