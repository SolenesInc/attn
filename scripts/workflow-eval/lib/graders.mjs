import { execFileSync, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { openAsks } from './standin.mjs';
import { repository } from './world.mjs';

function seedOf(world, name) {
  const id = world.seeds[name];
  if (!id) throw new Error(`world.sh planted no seed named ${name}`);
  return world.attnJson(['seed', 'show', id]).seed;
}

function bashCommands(transcript) {
  return transcript
    .filter(event => event.kind === 'tool_call' && event.tool_name === 'Bash')
    .map(event => JSON.parse(event.text).command ?? '');
}

function withSeeds(world, text) {
  return text.replace(/\{(\w+)\}/g, (_, name) => {
    if (!world.seeds[name]) throw new Error(`world.sh planted no seed named ${name}`);
    return world.seeds[name];
  });
}

function stubOpenings(world) {
  const root = path.join(world.toolHome, '.codex/sessions');
  if (!fs.existsSync(root)) return [];
  return fs.readdirSync(root, { recursive: true })
    .filter(name => /rollout-.*\.jsonl$/.test(name))
    .map(name => fs.readFileSync(path.join(root, name), 'utf8'));
}

function delegations(world) {
  return stubOpenings(world).map(text => ({
    role: text.match(/Role: (\w+)/)?.[1]?.toLowerCase(),
    seed: text.match(/\bs-[0-9a-z]{6}\b/)?.[0],
    authorized: text.includes('Authorization from the user'),
  }));
}

function checkoutOf(world, ref) {
  const dir = path.join(world.dir, `graded-${ref.replaceAll('/', '-')}`);
  if (!fs.existsSync(dir)) {
    const git = args => spawnSync('git', ['-C', world.repo, ...args], { env: world.env, encoding: 'utf8' });
    git(['fetch', '-q', 'origin']);
    const added = git(['worktree', 'add', '--quiet', '--detach', dir, ref]);
    if (added.status !== 0) throw new Error(`git worktree add ${ref}: ${added.stderr.trim()}`);
  }
  return dir;
}

function firstCall(transcript, pattern) {
  return transcript.findIndex(event => event.kind === 'tool_call' && pattern.test(`${event.tool_name} ${event.text}`));
}

const kinds = {
  check: {
    label: g => `check ${g.seed} ${g.check} ${g.absent ? 'not ' : ''}${g.state}${g.evidence ? ' with evidence' : ''}`,
    grade: (g, { world }) => {
      const check = seedOf(world, g.seed).checks.find(c => c.id === g.check);
      if (g.absent) return check?.state !== g.state;
      return check?.state === g.state && (!g.evidence || check.report?.evidence?.length > 0);
    },
  },
  planted: {
    label: g => `a seed planted${g.title ? ` titled ${g.title}` : ''}`,
    grade: (g, { world }) => {
      const known = new Set(Object.values(world.seeds));
      return world.attnJson(['seed', 'ls', '--flat']).seeds.some(seed =>
        !known.has(seed.id) && (!g.title || new RegExp(g.title, 'i').test(seed.title)));
    },
  },
  any: {
    label: g => `any of: ${g.of.map(graderLabel).join(' | ')}`,
    grade: (g, context) => g.of.some(grader => kinds[grader.kind].grade(grader, context)),
  },
  seed: {
    label: g => `seed ${g.seed} ${g.status}`,
    grade: (g, { world }) => seedOf(world, g.seed).status === g.status,
  },
  command: {
    label: g => `${g.absent ? 'no ' : ''}command ${g.pattern}${g.before ? ` before ${g.before}` : ''}`,
    grade: (g, { world, transcript }) => {
      const commands = bashCommands(transcript).join('\n');
      const first = commands.search(new RegExp(withSeeds(world, g.pattern)));
      if (g.absent) return first < 0;
      if (first < 0 || !g.before) return first >= 0;
      const later = commands.search(new RegExp(withSeeds(world, g.before)));
      return later < 0 || later > first;
    },
  },
  ask: {
    label: g => `ask ${g.state}`,
    grade: (g, { world, session }) => {
      const open = openAsks(world, [session]).length;
      if (g.state === 'none') return open + world.answered.length === 0;
      if (g.state === 'open') return open > 0;
      if (g.state === 'any') return open + world.answered.length > 0;
      throw new Error(`ask state is none, open or any, not ${g.state}`);
    },
  },
  delegation: {
    label: g => `${g.absent ? 'no ' : ''}delegation${g.role ? ` ${g.role}` : ''}${g.seed ? ` on ${g.seed}` : ''}${g.authorization ? ' with authorization' : ''}`,
    grade: (g, { world }) => {
      const seed = g.seed && withSeeds(world, `{${g.seed}}`);
      const matching = delegations(world).filter(d =>
        (!g.role || d.role === g.role) && (!seed || d.seed === seed) && (!g.authorization || d.authorized));
      return g.absent ? matching.length === 0 : matching.length > 0;
    },
  },
  read: {
    label: g => `read ${g.file}${g.before ? ` before ${g.before}` : ''}`,
    grade: (g, { transcript }) => {
      const loaded = g.file === 'SKILL.md' ? '|"skill":"attn-workflow"' : '';
      const read = firstCall(transcript, new RegExp(`attn-workflow[\\s\\S]*${path.basename(g.file).replaceAll('.', '\\.')}${loaded}`));
      if (read < 0 || !g.before) return read >= 0;
      const after = firstCall(transcript, new RegExp(g.before));
      return after < 0 || after > read;
    },
  },
  test: {
    label: g => `test ${g.run}${g.ref ? ` on ${g.ref}` : ''}`,
    grade: (g, { world, testCase }) => {
      const dir = g.ref ? checkoutOf(world, g.ref) : world.repo;
      if (g.from) {
        for (const file of g.from.files) {
          fs.writeFileSync(path.join(dir, file), execFileSync('git', ['-C', repository, 'show', `${g.from.commit}:${file}`]));
        }
      } else {
        fs.cpSync(path.join(testCase.dir, 'hidden'), dir, { recursive: true });
      }
      return spawnSync('bash', ['-c', g.run], { cwd: dir, env: world.env, stdio: 'ignore' }).status === 0;
    },
  },
  landed: {
    label: () => 'origin/main moved past the world\'s start',
    grade: (g, { world }) => {
      const git = args => spawnSync('git', ['-C', world.repo, ...args], { env: world.env, encoding: 'utf8' });
      git(['fetch', '-q', 'origin']);
      return git(['rev-parse', 'origin/main']).stdout.trim() !== world.startCommit;
    },
  },
};

export function checkGraders(graders) {
  for (const grader of graders) {
    if (!kinds[grader.kind]) throw new Error(`no grader kind ${grader.kind}; known: ${Object.keys(kinds).join(', ')}`);
    if (grader.kind === 'any') checkGraders(grader.of);
  }
}

export function graderLabel(grader) {
  return kinds[grader.kind].label(grader);
}

export function grade(graders, context) {
  return graders.map(grader => {
    try {
      return { label: graderLabel(grader), pass: Boolean(kinds[grader.kind].grade(grader, context)) };
    } catch (error) {
      return { label: graderLabel(grader), pass: false, error: error.message };
    }
  });
}
