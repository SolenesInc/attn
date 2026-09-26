import { act, fireEvent, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { invoke } from '@tauri-apps/api/core';
import { describe, expect, it, vi } from 'vitest';
import { openMarkdownTiles, openTiles } from './test/appFixtures';
import { daemonSeed, seedDocument, type DaemonSeedDocument } from './test/daemonFixtures';
import type { EventMessage } from './test/protocol';
import { gesture } from './test/renderApp';

const DOC = '/tmp/doc.md';

async function openMarkdown(content: string, { tiles = ['tile-a'], endpointId }: { tiles?: string[]; endpointId?: string } = {}) {
  const remote = endpointId ? { endpoint_id: endpointId } : {};
  const view = await openMarkdownTiles(content, {
    path: DOC,
    tileIds: tiles,
    workspace: remote,
    session: remote,
    initialState: endpointId ? { endpoints: [{ id: endpointId, name: 'gpu-box', ssh_target: 'user@gpu-box', status: 'connected', enabled: true }] } : {},
  });
  return { ...view, reader: within(view.tile()) };
}

type TargetResult = NonNullable<EventMessage<'seed_artifact_target_result'>['result']>;
type SeedArtifact = DaemonSeedDocument['artifacts'][number];

const SEED_ID = 's-7k3f9m';
const REPORT_PATH = '/notebook/seeds/s-7k3f9m/report.pdf';

function seedTarget(target: string, purpose: string): TargetResult {
  return purpose === 'image'
    ? { relative_target: target, mime_type: 'image/png', data_base64: 'aW1hZ2U=' }
    : { relative_target: target, path: REPORT_PATH };
}

function artifact(filename: string, relativeTarget = filename, modifiedAt = '2026-08-29T20:00:00Z'): SeedArtifact {
  return { filename, relative_target: relativeTarget, size: 5, modified_at: modifiedAt };
}

async function openSeed(
  body: string,
  { document = () => ({}), resolve = seedTarget }: { document?: () => Partial<DaemonSeedDocument>; resolve?: (target: string, purpose: string) => TargetResult } = {},
) {
  const seed = daemonSeed(SEED_ID, { body });
  const view = await openTiles([{ tile_id: 'tile-seed', tile_kind: 'seed', tile_params: SEED_ID }], {
    initialState: { seeds: [seed] },
    script: (daemon) => {
      daemon.on('seed_document_get', () => ({ event: 'seed_document_get_result', success: true, document: seedDocument(seed, document()) }));
      daemon.on('seed_artifact_target', ({ seed_id, relative_target, purpose }) => (
        seed_id === SEED_ID
          ? { event: 'seed_artifact_target_result', success: true, result: resolve(relative_target, purpose) }
          : { event: 'seed_artifact_target_result', success: false, error: `unknown seed ${seed_id}` }
      ));
    },
  });
  const targets = () => view.daemon.sentOf('seed_artifact_target').map((command) => [command.relative_target, command.purpose]);
  return { ...view, seed, reader: within(view.tile()), targets };
}

async function pressEnter(control: HTMLElement) {
  control.focus();
  await Promise.all([userEvent.setup({ delay: null }).keyboard('{Enter}'), vi.advanceTimersByTimeAsync(10)]);
}

function tileBodyScrolls() {
  return vi.spyOn(HTMLElement.prototype, 'scrollTo').mockImplementation(() => {});
}

describe('App markdown rendering', () => {
  it('renders a document’s heading and GFM table, and keeps a single newline inside its paragraph', async () => {
    const { reader } = await openMarkdown('# Plan\n\n| step | owner |\n| - | - |\n| parse | ana |\n\nfirst line\nsecond line\n');

    expect(reader.getByRole('heading', { name: 'Plan' })).toBeInTheDocument();
    expect(within(reader.getByRole('table')).getAllByRole('cell').map((cell) => cell.textContent)).toEqual(['parse', 'ana']);
    const paragraph = reader.getByText(/first line/);
    expect(paragraph.querySelector('br')).toBeNull();
    expect(paragraph).toHaveTextContent('first line second line');
  });

  it('keeps a single newline as a line break in a seed’s log note', async () => {
    const { reader } = await openSeed('', {
      document: () => ({
        notes: [{ id: 'n1', seed_id: SEED_ID, kind: 'note', author_member: 'ana', author_session: '', body: 'first line\nsecond line', created_at: '2026-01-01T00:00:00Z' }],
        notes_total: 1,
      }),
    });

    const note = reader.getByText(/first line/);
    expect(note.querySelector('br')).toBeInTheDocument();
  });

  it('links a seed’s web targets and leaves paths that assume a local directory as text', async () => {
    const { reader } = await openSeed('[local](docs/setup.md) [site](https://example.test/docs)');

    expect(reader.queryByRole('link', { name: 'local' })).toBeNull();
    expect(reader.getByText('local')).toBeInTheDocument();
    expect(reader.getByRole('link', { name: 'site' })).toHaveAttribute('href', 'https://example.test/docs');
  });

  it('shows fenced code in any language as its text, and copies the fence body', async () => {
    const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue();
    const { reader } = await openMarkdown('```ts\nconst answer = 42;\n```\n\n```nonsense-lang\nplain body\n```\n');

    const [typescript, plain] = Array.from(reader.getAllByRole('button', { name: 'Copy code' }), (button) => button.parentElement!.querySelector('pre code')!);
    expect(typescript.textContent).toBe('const answer = 42;');
    expect(plain.textContent).toBe('plain body');

    fireEvent.click(reader.getAllByRole('button', { name: 'Copy code' })[0]);
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(writeText).toHaveBeenCalledWith('const answer = 42;');
    expect(reader.getByRole('button', { name: 'Copied!' })).toBeInTheDocument();
    await act(() => vi.advanceTimersByTimeAsync(1999));
    expect(reader.getByRole('button', { name: 'Copied!' })).toBeInTheDocument();
    await act(() => vi.advanceTimersByTimeAsync(1));
    expect(reader.queryByRole('button', { name: 'Copied!' })).toBeNull();
  });

  it('shows a diagram that fails to render as its source, never as prose', async () => {
    const source = 'flowchart LR\n  E -->|[| C';
    const { daemon, reader, tile } = await openMarkdown(`# Flow\n\n\`\`\`mermaid\n${source}\n\`\`\`\n`);
    await act(() => vi.dynamicImportSettled());
    await daemon.idle();

    expect(reader.getByText('Diagram failed to render')).toBeInTheDocument();
    const shownSource = Array.from(tile().querySelectorAll('pre')).find((pre) => pre.textContent === source);
    expect(shownSource).toBeDefined();
    const prose = Array.from(tile().querySelectorAll('.md-reader-card :not(pre):not(pre *)'))
      .flatMap((element) => Array.from(element.childNodes).filter((node) => node.nodeType === Node.TEXT_NODE).map((node) => node.textContent));
    expect(prose.join('\n')).not.toMatch(/\|[^\n|]*\|/);
  });

  it('scrolls the tile a fragment link lives in to its heading, even when another tile shows the same document', async () => {
    const scrolls = tileBodyScrolls();
    const { tile } = await openMarkdown('[Jump](#setup)\n\n## Setup\n', { tiles: ['tile-a', 'tile-b'] });

    fireEvent.click(within(tile('tile-b')).getByRole('link', { name: 'Jump' }));

    expect(scrolls.mock.contexts).toEqual([tile('tile-b').querySelector('.workspace-dock-tile-body')]);
    expect(scrolls).toHaveBeenCalledWith(expect.objectContaining({ behavior: 'smooth' }));
  });

  it('shows a local image through the asset protocol in a lightbox, and blocks remote and escaping images', async () => {
    const { reader } = await openMarkdown('![the diagram](docs/pic%20name.png)\n\n![remote](https://example.test/pixel.png)\n\n![script](../evil.sh)\n');

    const image = reader.getByRole('img', { name: 'the diagram' });
    expect(image).toHaveAttribute('src', 'asset://localhost//tmp/docs/pic name.png');
    expect(reader.getByText('[blocked image: remote]')).toBeInTheDocument();
    expect(reader.getByText('[blocked image: script]')).toBeInTheDocument();

    fireEvent.click(image);
    const lightbox = () => document.body.querySelector<HTMLElement>('.md-lightbox');
    expect(within(lightbox()!).getByRole('img')).toHaveAttribute('src', 'asset://localhost//tmp/docs/pic name.png');
    expect(lightbox()).toHaveTextContent('the diagram');
    fireEvent.click(within(lightbox()!).getByRole('img'));
    expect(lightbox()).toBeInTheDocument();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(lightbox()).toBeNull();

    fireEvent.click(image);
    fireEvent.click(lightbox()!);
    expect(lightbox()).toBeNull();
  });

  it('blocks a relative image in a remote workspace’s document', async () => {
    const { reader } = await openMarkdown('![diagram](docs/pic.png)\n', { endpointId: 'ep-remote' });

    expect(reader.queryByRole('img')).toBeNull();
    expect(reader.getByText('[blocked image: diagram]')).toBeInTheDocument();
  });

  it('keeps what the user opened when the daemon re-sends the same content, and re-renders changed content', async () => {
    const content = '<details>\n<summary>More</summary>\n\nBody.\n\n</details>\n';
    const { show, tile, reader } = await openMarkdown(content);
    const details = tile().querySelector('details')!;
    details.open = true;

    await show(content);
    expect(tile().querySelector('details')!.open).toBe(true);

    await show(`${content}\nNew paragraph.\n`);
    expect(reader.getByText('New paragraph.')).toBeInTheDocument();
  });
});

describe('App markdown rendering seed targets', () => {
  it('resolves direct seed links and images through the owning daemon', async () => {
    const { daemon, reader, targets } = await openSeed('[report](report.pdf)\n\n![cover](cover%20art.png)', {
      document: () => ({ artifacts: [artifact('cover art.png', 'cover%20art.png')] }),
    });

    expect(reader.getByRole('img', { name: 'cover' })).toHaveAttribute('src', 'data:image/png;base64,aW1hZ2U=');
    await pressEnter(reader.getByRole('button', { name: 'cover' }));
    expect(document.body.querySelector('.md-lightbox')).toBeInTheDocument();
    fireEvent.keyDown(window, { key: 'Escape' });
    await gesture(daemon, () => fireEvent.click(reader.getByRole('button', { name: 'report' })));
    expect(invoke).toHaveBeenCalledWith('open_safe_seed_artifact_target', { path: REPORT_PATH });
    expect(targets()).toEqual(expect.arrayContaining([['cover%20art.png', 'image'], ['report.pdf', 'link']]));
  });

  it('keeps a linked seed image and its target as sibling keyboard controls', async () => {
    const { daemon, tile, reader } = await openSeed('[![cover](cover.png)](report.pdf)', { document: () => ({ artifacts: [artifact('cover.png')] }) });

    const imageButton = reader.getByRole('button', { name: 'cover' });
    const linkButton = reader.getByRole('button', { name: 'Open report.pdf' });
    expect(tile().querySelector('button button')).toBeNull();

    await pressEnter(imageButton);
    expect(document.body.querySelector('.md-lightbox')).toBeInTheDocument();
    fireEvent.keyDown(window, { key: 'Escape' });

    await pressEnter(linkButton);
    await daemon.idle();
    expect(invoke).toHaveBeenCalledWith('open_safe_seed_artifact_target', { path: REPORT_PATH });
  });

  it('rejects nested, escaped, and active-content seed targets', async () => {
    const { reader, targets } = await openSeed('[up](../secret.pdf) [nested](docs/report.pdf) [html](page.html) [encoded](%2e%2e%2fsecret.pdf) ![svg](art.svg)');

    expect(reader.getByText(/blocked image: svg/)).toBeInTheDocument();
    for (const name of ['up', 'nested', 'html', 'encoded']) {
      expect(reader.queryByRole('link', { name })).toBeNull();
      expect(reader.queryByRole('button', { name })).toBeNull();
    }
    expect(targets()).toEqual([]);
  });

  it('refreshes a seed image the garden changed without remounting the Markdown tree', async () => {
    const images = ['b25l', 'dHdv'];
    let modifiedAt = '2026-08-29T20:00:00Z';
    const { daemon, seed, tile, reader } = await openSeed('<details><summary>Receipt</summary>\n\n![cover](cover.png)\n\n</details>', {
      document: () => ({ artifacts: [artifact('cover.png', 'cover.png', modifiedAt)] }),
      resolve: () => ({ relative_target: 'cover.png', mime_type: 'image/png', data_base64: images.shift() }),
    });
    expect(reader.getByRole('img', { name: 'cover' })).toHaveAttribute('src', 'data:image/png;base64,b25l');
    const details = tile().querySelector('details')!;
    details.open = true;

    modifiedAt = '2026-08-29T20:01:00Z';
    await gesture(daemon, () => daemon.emit({ event: 'garden_seeds_updated', seeds: [{ ...seed, rev: 2 }], total: 1 }));

    expect(reader.getByRole('img', { name: 'cover' })).toHaveAttribute('src', 'data:image/png;base64,dHdv');
    expect(details.open).toBe(true);
  });
});
