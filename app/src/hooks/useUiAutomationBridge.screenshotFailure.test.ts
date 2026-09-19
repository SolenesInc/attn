import { beforeEach, describe, expect, it, vi } from 'vitest';
import { toSvg } from 'html-to-image';
import { describeScreenshotFailure, isScreenshotNode } from './useUiAutomationBridge';

vi.mock('html-to-image', () => ({ toPng: vi.fn(), toSvg: vi.fn() }));
vi.mock('@tauri-apps/plugin-opener', () => ({ openUrl: vi.fn(async () => {}) }));

const svgDataUrl = (xml: string) => `data:image/svg+xml;charset=utf-8,${encodeURIComponent(xml)}`;
const options = { cacheBust: true, pixelRatio: 1, backgroundColor: '#111111', skipFonts: true };

describe('describeScreenshotFailure', () => {
  let target: HTMLElement;

  beforeEach(() => {
    vi.mocked(toSvg).mockReset();
    target = document.createElement('div');
    document.body.replaceChildren(target);
  });

  it('names the XML error when the serialized subtree does not parse', async () => {
    vi.mocked(toSvg).mockResolvedValue(
      svgDataUrl('<svg xmlns="http://www.w3.org/2000/svg"><foreignObject><div>a</foreignObject></svg>'),
    );

    const message = await describeScreenshotFailure(target, '.app', options, new Event('error'));

    expect(message).toMatch(/^Screenshot of \.app \(\d+x\d+, 0 canvases, visibility visible\): the serialized SVG \(\d+ chars\) does not parse: /);
    expect(message).toContain('foreignObject');
  });

  it('reports the image load failure when the serialized subtree parses', async () => {
    vi.mocked(toSvg).mockResolvedValue(svgDataUrl('<svg xmlns="http://www.w3.org/2000/svg"/>'));

    const message = await describeScreenshotFailure(target, '.app', options, new Event('error'));

    expect(message).toMatch(/parses but loading it as an image failed: error event$/);
  });

  it('reports the serialization error when html-to-image cannot build the SVG', async () => {
    vi.mocked(toSvg).mockRejectedValue(new Error('font fetch failed'));

    const message = await describeScreenshotFailure(target, '#root', options, new Event('error'));

    expect(message).toMatch(/^Screenshot of #root .*: serializing the subtree failed: font fetch failed; embedded images: none$/);
  });

  it('names every embedded image with its markup and ancestors when serialization fails', async () => {
    vi.mocked(toSvg).mockRejectedValue(new Event('error'));
    target.innerHTML =
      '<div class="cm-editor"><div class="cm-line"><img class="cm-widgetBuffer" aria-hidden="true"></div></div>';

    const message = await describeScreenshotFailure(target, '.app', options, new Event('error'));

    expect(message).toContain('serializing the subtree failed: error event; embedded images: ');
    expect(message).toMatch(
      /img \(no src\) <img class="cm-widgetBuffer" aria-hidden="true"> (0x0|loading) in div\.cm-line < div\.cm-editor < div/,
    );
  });
});

describe('isScreenshotNode', () => {
  it('drops images without a source and keeps everything else', () => {
    const buffer = document.createElement('img');
    buffer.className = 'cm-widgetBuffer';
    const icon = document.createElement('img');
    icon.setAttribute('src', 'data:image/png;base64,AA==');

    expect(isScreenshotNode(buffer)).toBe(false);
    expect(isScreenshotNode(icon)).toBe(true);
    expect(isScreenshotNode(document.createElement('div'))).toBe(true);
  });
});
