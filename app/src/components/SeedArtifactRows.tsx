import { invoke } from '@tauri-apps/api/core';
import { open, save } from '@tauri-apps/plugin-dialog';
import { useEffect, useState } from 'react';
import type { SeedArtifact, SeedArtifactReference } from '../types/generated';
import { useOptionalDaemonApi } from '../contexts/DaemonApiContext';
import { artifactKey } from './seedArtifacts';
import './SeedArtifactRows.css';

export interface SeedArtifactRowsProps {
  seedId: string;
  artifacts: readonly SeedArtifact[];
  references?: readonly SeedArtifactReference[];
  onOpenMarkdownArtifact?: (path: string) => void;
  checkArtifactPath?: (path: string) => Promise<boolean>;
}

interface Presentation {
  kind: string;
  primary: string;
  secondary: string;
  external: boolean;
  path: string;
  href: string;
}

const PR_URL = /^https?:\/\/(?:www\.)?github\.com\/([^/]+)\/([^/]+)\/(pull|issues)\/(\d+)/;

function present(reference: SeedArtifactReference): Presentation {
  const base: Presentation = { kind: 'artifact', primary: '', secondary: '', external: false, path: '', href: '' };
  const path = reference.path ?? '';
  const dir = path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '';
  const name = path.slice(path.lastIndexOf('/') + 1);

  if (reference.kind === 'markdown_file' || reference.kind === 'repository') {
    return {
      ...base,
      kind: reference.kind === 'repository' ? 'repo file' : 'linked file',
      primary: name || path,
      secondary: [reference.repository, dir].filter(Boolean).join(' / '),
      path,
    };
  }
  if (reference.kind === 'notebook') {
    return { ...base, kind: 'notebook', primary: reference.notebook_document_id ?? '' };
  }
  const url = reference.url ?? '';
  const pr = PR_URL.exec(url);
  if (pr) {
    const [, owner, repo, sort, number] = pr;
    return {
      ...base,
      kind: sort === 'pull' ? 'pull request' : 'issue',
      primary: `#${number}`,
      secondary: `${owner}/${repo}`,
      external: true,
      href: url,
    };
  }
  if (url) {
    let host = url;
    let rest = '';
    try {
      const parsed = new URL(url);
      host = parsed.host.replace(/^www\./, '');
      rest = `${parsed.pathname}${parsed.search}`.replace(/\/$/, '');
    } catch {
    }
    return { ...base, kind: 'link', primary: host, secondary: rest === '/' ? '' : rest, external: true, href: url };
  }
  return { ...base, kind: reference.kind, primary: reference.repository ?? reference.kind };
}

function useMissingPaths(
  references: readonly SeedArtifactReference[],
  check?: (path: string) => Promise<boolean>,
): Set<string> {
  const [missing, setMissing] = useState<Set<string>>(new Set());
  const absolutePaths: string[] = [];
  for (const reference of references) {
    const path = reference.path ?? '';
    if (path.startsWith('/')) absolutePaths.push(path);
  }
  const pathsKey = JSON.stringify(absolutePaths);

  useEffect(() => {
    const absolute = JSON.parse(pathsKey) as string[];
    if (!check || absolute.length === 0) {
      setMissing(new Set());
      return;
    }
    let ignore = false;
    void Promise.all(
      absolute.map((path) => check(path).then((exists) => (exists ? '' : path)).catch(() => '')),
    ).then((results) => {
      if (!ignore) setMissing(new Set(results.filter(Boolean)));
    });
    return () => { ignore = true; };
  }, [check, pathsKey]);

  return missing;
}

function managedKey(artifact: SeedArtifact): string {
  return `managed\0${artifact.filename}`;
}

export function SeedArtifactRows({
  seedId,
  artifacts,
  references = [],
  onOpenMarkdownArtifact,
  checkArtifactPath,
}: SeedArtifactRowsProps) {
  const daemon = useOptionalDaemonApi();
  const missing = useMissingPaths(references, checkArtifactPath);
  const [pending, setPending] = useState('');
  const [error, setError] = useState('');
  if (artifacts.length === 0 && references.length === 0) return null;

  const act = async (key: string, operation: () => Promise<unknown>) => {
    setPending(key);
    setError('');
    try {
      await operation();
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : String(actionError));
    } finally {
      setPending('');
    }
  };

  const openManaged = (artifact: SeedArtifact, reveal: boolean) => act(managedKey(artifact), async () => {
    if (!daemon) throw new Error('The daemon is not connected');
    const target = await daemon.sendSeedArtifactTarget(seedId, artifact.relative_target, 'artifact');
    if (!target.path) throw new Error('The daemon returned no artifact path');
    await invoke('open_safe_seed_artifact_target', { path: target.path, reveal });
  });

  const detachManaged = (artifact: SeedArtifact) => act(managedKey(artifact), async () => {
    if (!daemon) throw new Error('The daemon is not connected');
    const destination = await save({ defaultPath: artifact.filename, title: 'Move artifact out of this seed' });
    if (!destination) return;
    await daemon.sendSeedArtifactTransfer({
      seedId,
      operation: 'detach',
      filename: artifact.filename,
      destinationPath: destination,
    });
  });

  const bringReference = (reference: SeedArtifactReference, operation: 'move' | 'copy') => {
    const key = artifactKey(reference);
    return act(key, async () => {
      if (!daemon) throw new Error('The daemon is not connected');
      let source = reference.path ?? '';
      if (!source.startsWith('/')) {
        const selected = await open({ multiple: false, directory: false, title: 'Choose the linked file to bring into this seed' });
        if (!selected) return;
        source = selected;
      }
      await daemon.sendSeedArtifactTransfer({
        seedId,
        operation,
        sourcePath: source,
        legacyReference: reference,
      });
    });
  };

  return (
    <>
      <ul className="seed-artifacts">
        {artifacts.map((artifact) => {
          const key = managedKey(artifact);
          return (
            <li key={key} className="seed-artifact seed-artifact--managed">
              <span className="seed-artifact__kind">file</span>
              <span className="seed-artifact__primary" title={artifact.filename}>{artifact.filename}</span>
              <span className="seed-artifact__secondary">{artifact.size.toLocaleString()} bytes</span>
              <span className="seed-artifact__actions">
                <button type="button" disabled={pending === key} onClick={() => void openManaged(artifact, false)}>Open</button>
                <button type="button" disabled={pending === key} onClick={() => void openManaged(artifact, true)}>Reveal</button>
                <button type="button" disabled={pending === key} onClick={() => void detachManaged(artifact)}>Move out</button>
              </span>
            </li>
          );
        })}
        {references.map((reference) => {
          const view = present(reference);
          const gone = Boolean(view.path) && missing.has(view.path);
          const key = artifactKey(reference);
          const linkedFile = reference.kind === 'markdown_file';
          return (
            <li key={key} className={`seed-artifact seed-artifact--reference${gone ? ' is-gone' : ''}`} title={view.path || view.href}>
              <span className="seed-artifact__kind">{view.kind}</span>
              {view.href ? (
                <a href={view.href} className="seed-artifact__primary">{view.primary}</a>
              ) : view.path && onOpenMarkdownArtifact && !gone ? (
                <button type="button" className="seed-artifact__primary" onClick={() => onOpenMarkdownArtifact(view.path)}>{view.primary}</button>
              ) : (
                <span className="seed-artifact__primary">{view.primary}</span>
              )}
              {view.secondary && <span className="seed-artifact__secondary">{view.secondary}</span>}
              {gone && <span className="seed-artifact__gone">not on disk</span>}
              {view.external && <span className="seed-artifact__leaves" aria-label="opens outside attn">↗</span>}
              {linkedFile && daemon && (
                <span className="seed-artifact__actions">
                  <button type="button" disabled={pending === key} onClick={() => void bringReference(reference, 'move')}>Move into seed</button>
                  <button type="button" disabled={pending === key} onClick={() => void bringReference(reference, 'copy')}>Copy into seed</button>
                  <button type="button" disabled={pending === key} onClick={() => void act(key, () => daemon.sendSeedArtifactReferenceDetach(seedId, reference))}>Remove link</button>
                </span>
              )}
            </li>
          );
        })}
      </ul>
      {error && <p className="seed-artifacts__error" role="alert">{error}</p>}
    </>
  );
}
