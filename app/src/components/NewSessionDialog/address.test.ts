import { describe, expect, it } from 'vitest';
import type { OutpostInfo } from '../../types/generated';
import { parseAddress, type Address } from './address';

const outpost = (id: string, name: string) =>
  ({ id, name, destination: name, state: 'connected', reach: 'reachable', state_since: '' }) as OutpostInfo;
const outposts = [
  outpost('o-box', 'box'),
  outpost('o-ai', 'ai-sandbox'),
  outpost('o-ai2', 'ai-sandbox2'),
  outpost('o-user', 'victor@box'),
  outpost('o-port', 'ssh://box:2222'),
  outpost('o-boxport', 'box:2222'),
];
const ai = { host: 'o-ai', name: 'ai-sandbox', label: 'ai-sandbox' };
const mac = { name: 'This Mac', label: 'home' };

const cases: Array<[string, Address]> = [
  ['', { mode: 'search', text: '', prefix: '' }],
  ['attn', { mode: 'search', text: 'attn', prefix: '' }],
  ['ai', { mode: 'search', text: 'ai', prefix: '' }],
  ['~/projects/', { mode: 'browse', path: '~/projects/', prefix: '', machine: mac }],
  ['/tmp', { mode: 'browse', path: '/tmp', prefix: '', machine: mac }],
  ['ai-sandbox:', { mode: 'search', text: '', prefix: 'ai-sandbox:', machine: ai }],
  ['ai-sandbox:attn', { mode: 'search', text: 'attn', prefix: 'ai-sandbox:', machine: ai }],
  ['ai-sandbox:~/', { mode: 'browse', path: '~/', prefix: 'ai-sandbox:', machine: ai }],
  ['AI-Sandbox:/srv', { mode: 'browse', path: '/srv', prefix: 'AI-Sandbox:', machine: ai }],
  ['ai-sandbox2:~/x', { mode: 'browse', path: '~/x', prefix: 'ai-sandbox2:', machine: { host: 'o-ai2', name: 'ai-sandbox2', label: 'ai-sandbox2' } }],
  ['victor@box:~/', { mode: 'browse', path: '~/', prefix: 'victor@box:', machine: { host: 'o-user', name: 'victor@box', label: 'victor@box' } }],
  ['ssh://box:2222:~/src', { mode: 'browse', path: '~/src', prefix: 'ssh://box:2222:', machine: { host: 'o-port', name: 'ssh://box:2222', label: 'ssh://box:2222' } }],
  ['ssh://box:~/', { mode: 'search', text: 'ssh://box:~/', prefix: '' }],
  ['box:2222:~/x', { mode: 'browse', path: '~/x', prefix: 'box:2222:', machine: { host: 'o-boxport', name: 'box:2222', label: 'box:2222' } }],
  ['box:~/x', { mode: 'browse', path: '~/x', prefix: 'box:', machine: { host: 'o-box', name: 'box', label: 'box' } }],
  ['home:~/', { mode: 'browse', path: '~/', prefix: 'home:', machine: mac }],
  ['home:attn', { mode: 'search', text: 'attn', prefix: 'home:', machine: mac }],
  ['nowhere:~/', { mode: 'search', text: 'nowhere:~/', prefix: '' }],
];

describe('parseAddress', () => {
  it.each(cases)('reads %j', (query, address) => {
    expect(parseAddress(query, outposts)).toEqual(address);
  });

  it('names every machine a shared name could mean and reads each by its id', () => {
    const labs = [outpost('o-lab1', 'lab'), outpost('o-lab2', 'LAB')];
    const first = { host: 'o-lab1', name: 'lab', label: 'o-lab1' };
    const second = { host: 'o-lab2', name: 'LAB', label: 'o-lab2' };
    expect(parseAddress('lab:~/', labs)).toEqual({ mode: 'ambiguous', prefix: 'lab:', machines: [first, second] });
    expect(parseAddress('o-lab2:~/', labs)).toEqual({ mode: 'browse', path: '~/', prefix: 'o-lab2:', machine: second });
  });
});
