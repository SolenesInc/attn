import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

const GIT_ENV = {
  GIT_AUTHOR_NAME: 'Attn Harness',
  GIT_AUTHOR_EMAIL: 'harness@attn.test',
  GIT_COMMITTER_NAME: 'Attn Harness',
  GIT_COMMITTER_EMAIL: 'harness@attn.test',
};

function git(cwd, ...args) {
  return execFileSync('git', args, {
    cwd,
    env: { ...process.env, ...GIT_ENV },
    stdio: ['ignore', 'pipe', 'pipe'],
  }).toString();
}

function write(repoDir, relPath, contents) {
  const full = path.join(repoDir, relPath);
  fs.mkdirSync(path.dirname(full), { recursive: true });
  fs.writeFileSync(full, contents, 'utf8');
}

export function buildPresentFixtureRepo(rootDir) {
  const repoDir = path.join(rootDir, 'present-fixture');
  fs.mkdirSync(repoDir, { recursive: true });
  git(repoDir, 'init', '-q');

  write(repoDir, 'greeting.ts', [
    'export function greet(name: string): string {',
    '  return `Hello, ${name}!`;',
    '}',
    '',
  ].join('\n'));

  write(repoDir, 'notes.md', [
    '# Notes',
    '',
    'A throwaway project used to exercise the Present window.',
    '',
  ].join('\n'));

  git(repoDir, 'add', '-A');
  git(repoDir, 'commit', '-q', '-m', 'base');
  const baseSha = git(repoDir, 'rev-parse', 'HEAD').trim();

  write(repoDir, 'greeting.ts', [
    'export function greet(name: string): string {',
    '  // reworked to include a friendlier sign-off',
    '  return `Hello, ${name}! Welcome aboard.`;',
    '}',
    '',
  ].join('\n'));

  git(repoDir, 'add', '-A');
  git(repoDir, 'commit', '-q', '-m', 'head: tweak greeting');
  const headSha = git(repoDir, 'rev-parse', 'HEAD').trim();

  const manifestPath = path.join(repoDir, '.present.yml');
  const manifestYaml = [
    'version: 1',
    'kind: changes',
    'title: Present flow smoke',
    'frame:',
    `  repo: ${repoDir}`,
    `  base: ${baseSha}`,
    `  head: ${headSha}`,
    'summary: |',
    '  Smoke presentation for the packaged-app present-flow scenario.',
    'files:',
    '  - path: greeting.ts',
    '    note: Reworked the greeting.',
    '',
  ].join('\n');
  fs.writeFileSync(manifestPath, manifestYaml, 'utf8');

  return { repoDir, manifestPath, baseSha, headSha, notedPath: 'greeting.ts' };
}
