// Unknown extensions must fail closed: opening an opaque file as text could
// transform its bytes on a later autosave.
const TEXT_EXTENSIONS = new Set([
  'txt', 'text', 'html', 'htm', 'css', 'scss', 'sass', 'less',
  'js', 'mjs', 'cjs', 'ts', 'tsx', 'jsx',
  'json', 'jsonc', 'jsonl', 'yaml', 'yml', 'toml', 'xml', 'csv', 'tsv',
  'ini', 'cfg', 'conf', 'env', 'log',
  'sh', 'bash', 'zsh', 'fish', 'ps1',
  'go', 'rs', 'py', 'rb', 'php', 'java', 'kt', 'kts', 'c', 'cc', 'cpp', 'cxx', 'h', 'hpp', 'cs', 'swift', 'scala', 'sql',
  'md', 'markdown',
]);

const TEXT_FILENAMES = new Set(['readme', 'license', 'makefile', 'dockerfile', 'brewfile']);

export type FileKind = 'markdown' | 'text' | 'binary';

export function extensionOf(path: string): string {
  const name = path.slice(path.lastIndexOf('/') + 1);
  const dot = name.lastIndexOf('.');
  if (dot <= 0) return '';
  return name.slice(dot + 1).toLowerCase();
}

export function fileKind(path: string): FileKind {
  const ext = extensionOf(path);
  if (ext === 'md' || ext === 'markdown') return 'markdown';
  const name = path.slice(path.lastIndexOf('/') + 1).toLowerCase();
  return TEXT_EXTENSIONS.has(ext) || TEXT_FILENAMES.has(name) ? 'text' : 'binary';
}

export function isMarkdownPath(path: string): boolean {
  return fileKind(path) === 'markdown';
}

export function isBinaryPath(path: string): boolean {
  return fileKind(path) === 'binary';
}
