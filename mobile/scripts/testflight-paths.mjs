import { execFileSync } from 'node:child_process'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { build } from 'vite'

const mobileDir = fileURLToPath(new URL('..', import.meta.url))
const repoRoot = path.resolve(mobileDir, '..')

const moduleIds = []
await build({
  root: mobileDir,
  logLevel: 'silent',
  build: { write: false },
  plugins: [
    {
      name: 'collect-module-ids',
      generateBundle() {
        moduleIds.push(...this.getModuleIds())
      },
    },
  ],
})

const tracked = new Set(
  execFileSync('git', ['ls-files', 'app/src'], { cwd: repoRoot, encoding: 'utf8' }).split('\n'),
)

const paths = new Set()
for (const id of moduleIds) {
  const relative = path.relative(repoRoot, id.replace(/^\0/, '').split('?')[0])
  if (tracked.has(relative)) paths.add(relative)
}

console.log([...paths].sort().join('\n'))
