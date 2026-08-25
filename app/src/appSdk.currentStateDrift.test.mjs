import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

// The SDK hand-copies the wire's current-state shapes; nothing else catches
// drift, because check-types and check-sdk both stay green through it.

const sdkSource = readFileSync(resolve(process.cwd(), '../sdk/attn-app/src/currentState.ts'), 'utf8');
const wireSource = readFileSync(resolve(process.cwd(), 'src/types/generated.ts'), 'utf8');

// Plain JS, per the include rule in vite.config.ts: this reads source off disk
// and the app tsconfig carries no node types.
const SHAPES = [
  ['Session', 'Session'],
  ['EndpointCapabilities', 'EndpointCapabilities'],
  ['EndpointInfo', 'EndpointInfo'],
  ['WorkspacePane', 'WorkspaceLayoutPane'],
  ['WorkspaceLayout', 'WorkspaceLayout'],
  ['Workspace', 'Workspace'],
  ['PR', 'PR'],
  ['RepoState', 'RepoState'],
  ['AuthorState', 'AuthorState'],
  // No TicketRow pair: the wire carries none, and the SDK's currentState.tickets
  // is served by a daemon-local row.
  ['SeedEdge', 'SeedEdge'],
  ['SeedPlotProgress', 'SeedPlotProgress'],
  ['SeedVar', 'SeedVar'],
  ['Seed', 'Seed'],
  ['CrewMember', 'CrewMember'],
  ['AppViewInfo', 'AppViewInfo'],
  ['AppRegistryEntry', 'AppRegistryEntry'],
];

function fieldsOf(source, name) {
  const header = `export interface ${name} {`;
  const start = source.indexOf(header);
  if (start < 0) throw new Error(`no interface ${name} in the source`);
  const end = source.indexOf('\n}', start);
  const body = source.slice(start + header.length, end);
  const fields = [];
  for (const line of body.split('\n')) {
    if (line.includes('[property: string]')) continue;
    const match = /^\s*(?:readonly\s+)?([A-Za-z_][A-Za-z0-9_]*)(\??):/.exec(line);
    if (match) fields.push(`${match[1]}${match[2]}`);
  }
  if (fields.length === 0) throw new Error(`interface ${name} parsed to no fields`);
  return fields.sort();
}

describe('the SDK current-state types', () => {
  it.each(SHAPES)('%s carries exactly what the wire sends', (sdkName, wireName) => {
    expect(fieldsOf(sdkSource, sdkName)).toEqual(fieldsOf(wireSource, wireName));
  });
});
