import { dirname, relative, resolve } from 'node:path';

export const GITHUB_BLOB = 'https://github.com/SolenesInc/attn/blob/main';

const repoRoot = resolve(process.cwd(), '..');
const NOT_RELATIVE = /^([a-z][a-z0-9+.-]*:|\/|#)/i;
const GUIDE_PAGE = /^docs\/guide\/(?:tutorials|reference)\/([^/]+)\.md$/;

export function siteHref(href, fromFile) {
  if (NOT_RELATIVE.test(href)) return href;
  const [path, anchor] = href.split('#');
  const hash = anchor ? `#${anchor}` : '';
  const target = relative(repoRoot, resolve(dirname(fromFile), path));
  const page = target.match(GUIDE_PAGE);
  if (page) return `/guide/${page[1]}${hash}`;
  if (target === 'docs/guide/README.md') return `/guide/${hash}`;
  return `${GITHUB_BLOB}/${target}${hash}`;
}

export function rewriteGuideLinks() {
  return (tree, file) => {
    const walk = (node) => {
      if (node.type === 'link' || node.type === 'definition') {
        node.url = siteHref(node.url, file.path);
      }
      node.children?.forEach(walk);
    };
    walk(tree);
  };
}
