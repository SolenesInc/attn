import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

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

const kinds = {
  check: {
    label: g => `check ${g.seed} ${g.check} ${g.state}${g.evidence ? ' with evidence' : ''}`,
    grade: (g, { world }) => {
      const check = seedOf(world, g.seed).checks.find(c => c.id === g.check);
      return check?.state === g.state && (!g.evidence || check.report?.evidence?.length > 0);
    },
  },
  seed: {
    label: g => `seed ${g.seed} ${g.status}`,
    grade: (g, { world }) => seedOf(world, g.seed).status === g.status,
  },
  command: {
    label: g => `command ${g.pattern}${g.before ? ` before ${g.before}` : ''}`,
    grade: (g, { transcript }) => {
      const commands = bashCommands(transcript).join('\n');
      const first = commands.search(new RegExp(g.pattern));
      if (first < 0 || !g.before) return first >= 0;
      return commands.search(new RegExp(g.before)) > first;
    },
  },
  test: {
    label: g => `test ${g.run}`,
    grade: (g, { world, testCase }) => {
      fs.cpSync(path.join(testCase.dir, 'hidden'), world.repo, { recursive: true });
      return spawnSync('bash', ['-c', g.run], { cwd: world.repo, env: world.env, stdio: 'ignore' }).status === 0;
    },
  },
};

export function checkGraders(graders) {
  for (const grader of graders) {
    if (!kinds[grader.kind]) throw new Error(`no grader kind ${grader.kind}; known: ${Object.keys(kinds).join(', ')}`);
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
