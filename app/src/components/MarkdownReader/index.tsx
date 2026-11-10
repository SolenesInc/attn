import { Children, createContext, isValidElement, memo, useCallback, useContext, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react';
import type { HTMLAttributes, ReactNode, Ref, RefObject } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import { convertFileSrc, invoke } from '@tauri-apps/api/core';
import rehypeRaw from 'rehype-raw';
import rehypeSanitize from 'rehype-sanitize';
import remarkFrontmatter from 'remark-frontmatter';
import remarkGfm from 'remark-gfm';
import type { PluggableList } from 'unified';
import '@fontsource-variable/inter';
import { CodeRenderer, ReaderPresentation } from '../Markdown';
import { CodeBlock } from './CodeBlock';
import { extractFrontmatter, type FrontmatterEntry } from './frontmatter';
import { ImageLightbox } from './ImageLightbox';
import {
  isMarkdownDocumentTarget,
  isSafeLocalMarkdownImageTarget,
  isSafeLocalMarkdownTarget,
  openMarkdownTarget,
  resolveMarkdownTarget,
  resolveSeedMarkdownTarget,
  sanitizeLinkUrl,
} from './markdownLinks';
import rehypeAlerts, { type AlertKind } from './rehypeAlerts';
import rehypeHeadingSlugs from './rehypeHeadingSlugs';
import rehypeProseTransforms from './proseTransforms';
import rehypeSeedMentions from './rehypeSeedMentions';
import rehypeSourceAnchors from './rehypeSourceAnchors';
import { readerSanitizeSchema } from './sanitizeSchema';
import { scrollToAnchor } from './scrollToAnchor';
import { AnnotationLayer, type AnnotationLayerHandle } from './annotations/AnnotationLayer';
import { useAnnotations } from './annotations/useAnnotations';
import { markdownDocumentPath, type MarkdownDocumentSource } from './documentSource';
import { tilePathBasename } from '../../utils/tilePresentation';
import { isSeedId } from '../../utils/seedId';
import type { SeedMentionIndex } from '../../utils/seedMentions';
import { SeedMentionButton } from '../SeedMentionText';
import type { ReviewBundle, SeedArtifact, SeedArtifactTargetResult } from '../../types/generated';
import type { MarkdownAnnotationsDraft } from './annotations/transport';
import { useOptionalDaemonApi } from '../../contexts/DaemonApiContext';
import { ReviewReader, type ReviewReaderHandle } from '../ReviewReader/ReviewReader';
import type { ReviewMarks } from '../ReviewReader/ReviewMarks';
import { NotShownSlot, StopSlot, SummarySlot } from '../ReviewReader/ReviewLists';
import rehypeReviewChrome, { reviewChromeKey, reviewChromeOf, type ReviewChrome } from '../ReviewReader/reviewChrome';
import './MarkdownReader.css';

// Parsing WITH remark-frontmatter is what makes the anchor lineOffset 0. Both
// arrays MUST stay module-level: react-markdown re-parses on identity change.
const remarkPlugins = [remarkGfm, remarkFrontmatter];
// Order is load-bearing: sanitize before anchors so author HTML cannot forge
// data-*; anchors before the review chrome and alerts; heading slugs before
// prose transforms.
function readerPlugins(index: SeedMentionIndex | null | undefined, reviewChrome: ReviewChrome | null): PluggableList {
  return [
    rehypeRaw,
    [rehypeSanitize, readerSanitizeSchema],
    [rehypeSourceAnchors, { lineOffset: 0 }],
    ...(reviewChrome ? [[rehypeReviewChrome, { chrome: reviewChrome }] as PluggableList[number]] : []),
    rehypeAlerts,
    rehypeHeadingSlugs,
    rehypeProseTransforms,
    ...(index ? [[rehypeSeedMentions, { index }] as PluggableList[number]] : []),
  ];
}
const EMPTY_SEED_ARTIFACTS: readonly SeedArtifact[] = [];

const ALERT_TITLES: Record<AlertKind, string> = {
  note: 'Note',
  tip: 'Tip',
  warning: 'Warning',
  caution: 'Caution',
  important: 'Important',
};

const ALERT_ICON_PATHS: Record<AlertKind, string> = {
  note: 'M0 8a8 8 0 1 1 16 0A8 8 0 0 1 0 8Zm8-6.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13ZM6.5 7.75A.75.75 0 0 1 7.25 7h1a.75.75 0 0 1 .75.75v2.75h.25a.75.75 0 0 1 0 1.5h-2a.75.75 0 0 1 0-1.5h.25v-2h-.25a.75.75 0 0 1-.75-.75ZM8 6a1 1 0 1 1 0-2 1 1 0 0 1 0 2Z',
  tip: 'M8 1.5c-2.363 0-4 1.69-4 3.75 0 .984.424 1.625.984 2.304l.214.253c.223.264.47.556.673.848.284.411.537.896.621 1.49a.75.75 0 0 1-1.484.211c-.04-.282-.163-.547-.37-.847a8.456 8.456 0 0 0-.542-.68c-.084-.1-.173-.205-.268-.32C3.201 7.75 2.5 6.766 2.5 5.25 2.5 2.31 4.863 0 8 0s5.5 2.31 5.5 5.25c0 1.516-.701 2.5-1.328 3.259-.095.115-.184.22-.268.319-.207.245-.383.453-.541.681-.208.3-.33.565-.37.847a.751.751 0 0 1-1.485-.212c.084-.593.337-1.078.621-1.489.203-.292.45-.584.673-.848.075-.088.147-.173.213-.253.561-.679.985-1.32.985-2.304 0-2.06-1.637-3.75-4-3.75ZM5.75 12h4.5a.75.75 0 0 1 0 1.5h-4.5a.75.75 0 0 1 0-1.5ZM6 15.25a.75.75 0 0 1 .75-.75h2.5a.75.75 0 0 1 0 1.5h-2.5a.75.75 0 0 1-.75-.75Z',
  warning: 'M6.457 1.047c.659-1.234 2.427-1.234 3.086 0l6.082 11.378A1.75 1.75 0 0 1 14.082 15H1.918a1.75 1.75 0 0 1-1.543-2.575Zm1.763.707a.25.25 0 0 0-.44 0L1.698 13.132a.25.25 0 0 0 .22.368h12.164a.25.25 0 0 0 .22-.368Zm.53 3.996v2.5a.75.75 0 0 1-1.5 0v-2.5a.75.75 0 0 1 1.5 0ZM9 11a1 1 0 1 1-2 0 1 1 0 0 1 2 0Z',
  caution: 'M4.47.22A.749.749 0 0 1 5 0h6c.199 0 .389.079.53.22l4.25 4.25c.141.14.22.331.22.53v6a.749.749 0 0 1-.22.53l-4.25 4.25A.749.749 0 0 1 11 16H5a.749.749 0 0 1-.53-.22L.22 11.53A.749.749 0 0 1 0 11V5c0-.199.079-.389.22-.53Zm.84 1.28L1.5 5.31v5.38l3.81 3.81h5.38l3.81-3.81V5.31L10.69 1.5ZM8 4a.75.75 0 0 1 .75.75v3.5a.75.75 0 0 1-1.5 0v-3.5A.75.75 0 0 1 8 4Zm0 8a1 1 0 1 1 0-2 1 1 0 0 1 0 2Z',
  important: 'M0 1.75C0 .784.784 0 1.75 0h12.5C15.216 0 16 .784 16 1.75v9.5A1.75 1.75 0 0 1 14.25 13H8.06l-2.573 2.573A1.458 1.458 0 0 1 3 14.543V13H1.75A1.75 1.75 0 0 1 0 11.25Zm1.75-.25a.25.25 0 0 0-.25.25v9.5c0 .138.112.25.25.25h2a.75.75 0 0 1 .75.75v2.19l2.72-2.72a.749.749 0 0 1 .53-.22h6.5a.25.25 0 0 0 .25-.25v-9.5a.25.25 0 0 0-.25-.25Zm7 2.25v2.5a.75.75 0 0 1-1.5 0v-2.5a.75.75 0 0 1 1.5 0ZM9 9a1 1 0 1 1-2 0 1 1 0 0 1 2 0Z',
};

function isAlertKind(value: unknown): value is AlertKind {
  return typeof value === 'string' && value in ALERT_TITLES;
}

const ANCHOR_ATTRS = ['data-block-id', 'data-source-line', 'data-source-line-end'] as const;

function splitAnchorProps<T extends object>(props: T): {
  anchorProps: Record<string, unknown>;
  rest: T;
} {
  const anchorProps: Record<string, unknown> = {};
  const rest = { ...props } as Record<string, unknown>;
  for (const attr of ANCHOR_ATTRS) {
    if (attr in rest) {
      anchorProps[attr] = rest[attr];
      delete rest[attr];
    }
  }
  return { anchorProps, rest: rest as T };
}

function textOf(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') {
    return String(node);
  }
  if (Array.isArray(node)) {
    return node.map(textOf).join('');
  }
  if (isValidElement<{ children?: ReactNode }>(node)) {
    return textOf(node.props.children);
  }
  return '';
}

function codeMeta(children: ReactNode): { text: string; language?: string; isMermaid: boolean } {
  let className = '';
  if (isValidElement<{ className?: string; children?: ReactNode }>(children)) {
    className = children.props.className ?? '';
  }
  const language = className.match(/language-([\w+-]+)/)?.[1];
  return {
    text: textOf(children).replace(/\n$/, ''),
    language,
    isMermaid: language === 'mermaid',
  };
}

interface SeedArtifactReaderContextValue {
  seedId: string;
  version: string;
  resolveTarget: (
    seedId: string,
    relativeTarget: string,
    purpose: 'image' | 'link' | 'artifact',
  ) => Promise<SeedArtifactTargetResult>;
}

const SeedArtifactReaderContext = createContext<SeedArtifactReaderContextValue | null>(null);

function decodedSeedTargetName(target: string): string {
  try {
    return decodeURIComponent(target);
  } catch {
    return target;
  }
}

function SeedArtifactLink({ target, children }: { target: string; children: ReactNode }) {
  const artifactContext = useContext(SeedArtifactReaderContext);
  if (!artifactContext || !isSafeLocalMarkdownTarget(decodedSeedTargetName(target))) {
    return <span title={`Blocked seed artifact target: ${target}`}>{children}</span>;
  }
  const targetName = decodedSeedTargetName(target);
  const openArtifact = () => {
    void artifactContext.resolveTarget(artifactContext.seedId, target, 'link')
      .then((result) => result.path
        ? invoke('open_safe_seed_artifact_target', { path: result.path })
        : Promise.reject(new Error('The daemon returned no artifact path')))
      .catch((error) => console.warn('[MarkdownReader] Failed to open seed artifact:', error));
  };
  const containsArtifactImage = Children.toArray(children)
    .some((child) => isValidElement<{ node?: { tagName?: string } }>(child) && child.props.node?.tagName === 'img');
  if (containsArtifactImage) {
    return (
      <span className="md-reader-linked-artifact-image">
        {children}
        <button
          type="button"
          className="md-reader-artifact-link"
          title={targetName}
          onClick={openArtifact}
        >
          Open {targetName}
        </button>
      </span>
    );
  }
  return (
    <button
      type="button"
      className="md-reader-artifact-link"
      title={targetName}
      onClick={openArtifact}
    >
      {children}
    </button>
  );
}

function SeedNavigationLink({ target, children, onOpenSeed }: {
  target: string;
  children: ReactNode;
  onOpenSeed: (seedId: string) => void;
}) {
  return (
    <button
      type="button"
      className="md-reader-artifact-link"
      data-seed-target={decodedSeedTargetName(target)}
      title={`Open ${decodedSeedTargetName(target)}`}
      onClick={() => onOpenSeed(decodedSeedTargetName(target))}
    >
      {children}
    </button>
  );
}

function SeedArtifactImage({
  target,
  alt,
  imageProps,
  onImageClick,
}: {
  target: string;
  alt: string;
  imageProps: HTMLAttributes<HTMLImageElement>;
  onImageClick: (src: string, alt: string) => void;
}) {
  const artifactContext = useContext(SeedArtifactReaderContext);
  const [src, setSrc] = useState('');
  const safe = isSafeLocalMarkdownImageTarget(decodedSeedTargetName(target));

  useEffect(() => {
    let current = true;
    setSrc('');
    if (!artifactContext || !safe) return () => { current = false; };
    void artifactContext.resolveTarget(artifactContext.seedId, target, 'image')
      .then((result) => {
        if (!current || !result.mime_type || !result.data_base64) return;
        setSrc(`data:${result.mime_type};base64,${result.data_base64}`);
      })
      .catch((error) => {
        if (current) console.warn('[MarkdownReader] Failed to load seed artifact image:', error);
      });
    return () => { current = false; };
  }, [artifactContext, safe, target]);

  if (!artifactContext || !safe) {
    return <span className="md-reader-blocked-image" title={target} data-md-chrome="1">[blocked image: {alt || target}]</span>;
  }
  if (!src) {
    return <span className="md-reader-image-loading" data-md-chrome="1">[loading image: {alt || target}]</span>;
  }
  return (
    <button
      type="button"
      className="md-reader-image-button"
      onClick={(event) => {
        event.stopPropagation();
        onImageClick(src, alt);
      }}
    >
      <img
        {...imageProps}
        className="md-reader-image"
        src={src}
        alt={alt || decodedSeedTargetName(target)}
        title={decodedSeedTargetName(target)}
        loading="lazy"
      />
    </button>
  );
}

export type LocalImageLoader = (path: string) => Promise<string>;

const LocalImageLoaderContext = createContext<LocalImageLoader | null>(null);

export interface RemoteMarkdownDocument {
  hostName: string;
  openMarkdown: (path: string) => void;
}

const RemoteMarkdownDocumentContext = createContext<RemoteMarkdownDocument | null>(null);

function LocalLink({ path, href, children }: { path: string; href: string | undefined; children: ReactNode }) {
  const remote = useContext(RemoteMarkdownDocumentContext);
  if (remote && !isMarkdownDocumentTarget(path)) {
    return <span title={`${path} is on ${remote.hostName}; only markdown files open from there`}>{children}</span>;
  }
  return (
    <a
      href={href}
      title={remote ? `${path} on ${remote.hostName}` : path}
      onClick={(event) => {
        event.preventDefault();
        if (remote) remote.openMarkdown(path);
        else openMarkdownTarget({ kind: 'local', value: path });
      }}
    >
      {children}
    </a>
  );
}

function LocalImage({
  path,
  alt,
  imageProps,
  onImageClick,
}: {
  path: string;
  alt: string | undefined;
  imageProps: HTMLAttributes<HTMLImageElement>;
  onImageClick: (src: string, alt: string) => void;
}) {
  const loadImage = useContext(LocalImageLoaderContext);
  const [loaded, setLoaded] = useState<{ path: string; src: string } | null>(null);
  const [failed, setFailed] = useState<string | null>(null);

  useEffect(() => {
    if (!loadImage) return undefined;
    let current = true;
    setLoaded(null);
    setFailed(null);
    void loadImage(path)
      .then((src) => { if (current) setLoaded({ path, src }); })
      .catch((error: unknown) => {
        if (!current) return;
        console.warn('[MarkdownReader] Failed to load image:', error);
        setFailed(error instanceof Error ? error.message : String(error));
      });
    return () => { current = false; };
  }, [loadImage, path]);

  const altText = alt ?? tilePathBasename(path);
  if (loadImage && failed) {
    return <span className="md-reader-blocked-image" title={failed} data-md-chrome="1">[missing image: {altText}]</span>;
  }
  const src = loadImage ? (loaded?.path === path ? loaded.src : '') : convertFileSrc(path);
  if (!src) {
    return <span className="md-reader-image-loading" data-md-chrome="1">[loading image: {altText}]</span>;
  }
  return (
    <img
      {...imageProps}
      className="md-reader-image"
      src={src}
      alt={altText}
      title={path}
      loading="lazy"
      onClick={(event) => {
        event.stopPropagation();
        onImageClick(src, alt ?? '');
      }}
    />
  );
}

function readerComponents(
  documentPath: string,
  allowLocalTargets: boolean,
  seedDocument: boolean,
  rootRef: { current: HTMLDivElement | null },
  onImageClick: (src: string, alt: string) => void,
  onOpenSeed?: (seedId: string) => void,
): Components {
  return {
    code: CodeRenderer,
    pre({ node: _node, children, ref: _ref, ...preProps }) {
      const { text, language, isMermaid } = codeMeta(children);
      if (isMermaid) {
        return <div {...(preProps as HTMLAttributes<HTMLDivElement>)}>{children}</div>;
      }
      return <CodeBlock code={text} language={language} preProps={preProps} />;
    },
    blockquote({ node: _node, children, ...props }) {
      const { 'data-alert-kind': alertKind, ...rest } = props as Record<string, unknown> &
        HTMLAttributes<HTMLElement>;
      if (!isAlertKind(alertKind)) {
        return <blockquote {...(props as HTMLAttributes<HTMLElement>)}>{children}</blockquote>;
      }
      return (
        <div
          {...(rest as HTMLAttributes<HTMLDivElement>)}
          data-alert-kind={alertKind}
          className={`md-alert md-alert-${alertKind}`}
        >
          {/* data-md-chrome marks React text with no hast counterpart; the
              anchoring DOM walker skips these subtrees. */}
          <div className="md-alert-title" data-md-chrome="1">
            <svg viewBox="0 0 16 16" width="16" height="16" fill="currentColor" aria-hidden="true">
              <path d={ALERT_ICON_PATHS[alertKind]} />
            </svg>
            <span>{ALERT_TITLES[alertKind]}</span>
          </div>
          {children}
        </div>
      );
    },
    table({ node: _node, children, ...props }) {
      // The wrapper takes the anchor attributes, never both: consumers count
      // data-block-id.
      const { anchorProps, rest } = splitAnchorProps(props as HTMLAttributes<HTMLTableElement>);
      return (
        <div className="md-table-wrap" {...anchorProps}>
          <table {...rest}>{children}</table>
        </div>
      );
    },
    div({ node: _node, children, ...props }) {
      const attributes = props as Record<string, unknown>;
      const stopLine = attributes['data-review-stop'];
      if (stopLine !== undefined) return <StopSlot line={Number(stopLine)} />;
      if (attributes['data-review-not-shown'] !== undefined) return <NotShownSlot />;
      return <div {...(props as HTMLAttributes<HTMLDivElement>)}>{children}</div>;
    },
    span({ node: _node, children, ...props }) {
      const mention = (props as Record<string, unknown>)['data-seed-mention'];
      if (typeof mention === 'string' && onOpenSeed) {
        return <SeedMentionButton seedId={mention} onOpenSeed={onOpenSeed}>{children}</SeedMentionButton>;
      }
      return <span {...(props as HTMLAttributes<HTMLSpanElement>)}>{children}</span>;
    },
    a({ node: _node, href, children }) {
      const sanitized = href ? sanitizeLinkUrl(href) : null;
      let target = sanitized ? resolveMarkdownTarget(documentPath, sanitized) : null;
      if (seedDocument && sanitized && (!target || target.kind === 'local')) {
        target = resolveSeedMarkdownTarget(sanitized);
      }
      if (!target) {
        return <span>{children}</span>;
      }
      if (target.kind === 'local' && (!allowLocalTargets || !isSafeLocalMarkdownTarget(target.value))) {
        return <span title={`Blocked local target: ${target.value}`}>{children}</span>;
      }
      if (target.kind === 'fragment') {
        return (
          <a
            href={target.value}
            onClick={(event) => {
              event.preventDefault();
              scrollToAnchor(rootRef.current, target.value);
            }}
          >
            {children}
          </a>
        );
      }
      if (target.kind === 'seed') {
        if (onOpenSeed && isSeedId(decodedSeedTargetName(target.value))) {
          return <SeedNavigationLink target={target.value} onOpenSeed={onOpenSeed}>{children}</SeedNavigationLink>;
        }
        return <SeedArtifactLink target={target.value}>{children}</SeedArtifactLink>;
      }
      if (target.kind === 'local') {
        return <LocalLink path={target.value} href={href}>{children}</LocalLink>;
      }
      return (
        <a
          href={href}
          onClick={(event) => {
            event.preventDefault();
            openMarkdownTarget(target);
          }}
        >
          {children}
        </a>
      );
    },
    img({ node: _node, src, alt, ...props }) {
      // Never spread srcSet/sizes: a remote srcset would override the gated
      // local src (browsers prefer srcset) and break the no-network invariant.
      const { srcSet: _srcSet, sizes: _sizes, ...safeProps } = props as Record<string, unknown> &
        HTMLAttributes<HTMLImageElement>;
      const imgSrc = typeof src === 'string' ? src : undefined;
      let target = imgSrc ? resolveMarkdownTarget(documentPath, imgSrc) : null;
      if (seedDocument && imgSrc && (!target || target.kind === 'local')) {
        target = resolveSeedMarkdownTarget(imgSrc);
      }
      if (target?.kind === 'seed') {
        return (
          <SeedArtifactImage
            target={target.value}
            alt={alt ?? ''}
            imageProps={safeProps}
            onImageClick={onImageClick}
          />
        );
      }
      if (!target || target.kind !== 'local' || !allowLocalTargets || !isSafeLocalMarkdownImageTarget(target.value)) {
        return (
          <span className="md-reader-blocked-image" title={imgSrc} data-md-chrome="1">
            [blocked image: {alt || imgSrc || 'unknown source'}]
          </span>
        );
      }
      return <LocalImage path={target.value} alt={alt} imageProps={safeProps} onImageClick={onImageClick} />;
    },
  };
}

function FrontmatterCard({ entries }: { entries: FrontmatterEntry[] }) {
  if (entries.length === 0) {
    return null;
  }
  return (
    <div className="md-frontmatter">
      <div className="md-frontmatter-grid">
        {entries.map((entry) => (
          <div className="md-frontmatter-row" key={entry.key}>
            <span className="md-frontmatter-key">{entry.key}:</span>
            {Array.isArray(entry.value) ? (
              <span className="md-frontmatter-tags">
                {entry.value.map((item, index) => (
                  <span className="md-frontmatter-tag" key={`${item}-${index}`}>
                    {item}
                  </span>
                ))}
              </span>
            ) : (
              <span className="md-frontmatter-val">{entry.value}</span>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

export interface MarkdownAnnotationsSendHandle {
  flushPendingSave(): Promise<void>;
  /** False until the daemon draft loaded; a send before then formats the
      daemon's STORED draft, not the local list. */
  isHydrated(): boolean;
  adoptSentDraft(uri: string, draft: MarkdownAnnotationsDraft): number;
  getOrphanedIds(): string[];
  openInspector(): void;
  openGlobalComment(anchorEl: HTMLElement): void;
  /** On a review document, shows what a send carries and takes the verdict. False elsewhere. */
  openReviewSend(): boolean;
}

/** What a review reader's marks send through: the tile's destination. */
export type ReviewSend = Pick<ReviewMarks, 'ask' | 'send' | 'destination'>;

export interface MarkdownReaderProps {
  content: string;
  source: MarkdownDocumentSource;
  allowLocalTargets?: boolean;
  annotationsEnabled?: boolean;
  onAnnotationsCountChange?: (count: number) => void;
  annotationsSendRef?: Ref<MarkdownAnnotationsSendHandle | null>;
  seedArtifacts?: readonly SeedArtifact[];
  onOpenSeed?: (seedId: string) => void;
  seedMentions?: SeedMentionIndex | null;
  loadLocalImage?: LocalImageLoader;
  remoteDocument?: RemoteMarkdownDocument;
  review?: ReviewBundle;
  reviewHost?: string;
  reviewSend?: ReviewSend;
  /** The annotation draft generation the daemon announced for this document. */
  annotationsGeneration?: number;
}

interface MarkdownReaderBodyProps {
  content: string;
  path: string;
  allowLocalTargets: boolean;
  seedDocument: boolean;
  rootRef: RefObject<HTMLDivElement | null>;
  onImageClick: (src: string, alt: string) => void;
  onOpenSeed?: (seedId: string) => void;
  plugins: PluggableList;
  reviewing: boolean;
}

// The live-reload poller re-reads the file every 750ms, so an unchanged file must
// produce zero re-renders: a re-render remounts the tree and open <details> shut.
const MarkdownReaderBody = memo(function MarkdownReaderBody({
  content,
  path,
  allowLocalTargets,
  seedDocument,
  rootRef,
  onImageClick,
  onOpenSeed,
  plugins,
  reviewing,
}: MarkdownReaderBodyProps) {
  const frontmatter = extractFrontmatter(content);
  const components = readerComponents(path, allowLocalTargets, seedDocument, rootRef, onImageClick, onOpenSeed);

  return (
    <article className="md-reader-card">
      {reviewing ? <SummarySlot /> : <FrontmatterCard entries={frontmatter.entries} />}
      <ReaderPresentation>
        <ReactMarkdown remarkPlugins={remarkPlugins} rehypePlugins={plugins} components={components}>
          {content}
        </ReactMarkdown>
      </ReaderPresentation>
    </article>
  );
});

export const MarkdownReader = memo(function MarkdownReader({
  content,
  source,
  allowLocalTargets = true,
  annotationsEnabled = false,
  onAnnotationsCountChange,
  annotationsSendRef,
  seedArtifacts = EMPTY_SEED_ARTIFACTS,
  onOpenSeed,
  seedMentions,
  loadLocalImage,
  remoteDocument,
  review,
  reviewHost,
  reviewSend,
  annotationsGeneration,
}: MarkdownReaderProps) {
  const daemon = useOptionalDaemonApi();
  const path = markdownDocumentPath(source);
  const sourceSeedId = source.kind === 'seed' ? source.seedId : '';
  const localTargetsEnabled = allowLocalTargets && source.kind === 'file';
  const rootRef = useRef<HTMLDivElement | null>(null);
  const reviewKey = reviewChromeKey(reviewChromeOf(review));
  const reviewChrome = useMemo<ReviewChrome | null>(() => (reviewKey ? JSON.parse(reviewKey) as ReviewChrome : null), [reviewKey]);
  const plugins = useMemo(() => readerPlugins(seedMentions, reviewChrome), [seedMentions, reviewChrome]);
  const annotationLayerRef = useRef<AnnotationLayerHandle | null>(null);
  const [lightbox, setLightbox] = useState<{ src: string; alt: string; artifactVersion: string } | null>(null);
  const seedArtifactVersion = useMemo(() => seedArtifacts
    .map((artifact) => `${artifact.filename}\0${artifact.size}\0${artifact.modified_at}`)
    .join('\0'), [seedArtifacts]);
  const seedArtifactContext = useMemo<SeedArtifactReaderContextValue | null>(() => (
    sourceSeedId && daemon?.sendSeedArtifactTarget
      ? {
          seedId: sourceSeedId,
          version: seedArtifactVersion,
          resolveTarget: daemon.sendSeedArtifactTarget,
        }
      : null
  ), [daemon?.sendSeedArtifactTarget, seedArtifactVersion, sourceSeedId]);
  // Props of the memoized body: identities must never change (gate contract).
  const handleImageClick = useCallback((src: string, alt: string) => {
    setLightbox({ src, alt, artifactVersion: seedArtifactVersion });
  }, [seedArtifactVersion]);
  const handleLightboxClose = useCallback(() => {
    setLightbox(null);
  }, []);
  const annotationsApi = useAnnotations({
    rootRef,
    content,
    source,
    enabled: annotationsEnabled,
    reviewChrome,
    draftGeneration: annotationsGeneration,
  });
  const reviewHandleRef = useRef<ReviewReaderHandle | null>(null);
  const { annotations, addFileMark, addBlockMark, deleteAnnotation } = annotationsApi;
  const reviewMarks = useMemo<ReviewMarks | undefined>(() => (annotationsEnabled && reviewSend
    ? { annotations, addFileMark, addBlockMark, deleteAnnotation, ...reviewSend }
    : undefined), [addBlockMark, addFileMark, annotations, annotationsEnabled, deleteAnnotation, reviewSend]);

  // The send handle must read call-time state, not a captured api identity.
  const annotationsApiRef = useRef(annotationsApi);
  annotationsApiRef.current = annotationsApi;

  useEffect(() => {
    onAnnotationsCountChange?.(annotationsApi.annotations.length);
  }, [annotationsApi.annotations, onAnnotationsCountChange]);
  useEffect(() => {
    return () => {
      onAnnotationsCountChange?.(0);
    };
  }, [onAnnotationsCountChange]);
  const visibleLightbox = lightbox?.artifactVersion === seedArtifactVersion ? lightbox : null;

  useImperativeHandle(annotationsSendRef, () => ({
    flushPendingSave: () => annotationsApiRef.current.flushPendingSave(),
    isHydrated: () => annotationsApiRef.current.isHydrated(),
    adoptSentDraft: (uri: string, draft: MarkdownAnnotationsDraft) =>
      annotationsApiRef.current.adoptSentDraft(uri, draft),
    getOrphanedIds: () => Array.from(annotationsApiRef.current.orphans.keys()),
    openInspector: () => annotationLayerRef.current?.openInspector(),
    openGlobalComment: (anchorEl: HTMLElement) => annotationLayerRef.current?.openGlobalComment(anchorEl),
    openReviewSend: () => {
      if (!reviewHandleRef.current) return false;
      reviewHandleRef.current.openSend();
      return true;
    },
  }), []);

  const documentView = (
      <div className="md-reader-doc">
        <div className="md-reader-wrap">
          <SeedArtifactReaderContext.Provider value={seedArtifactContext}>
            <LocalImageLoaderContext.Provider value={loadLocalImage ?? null}>
              <RemoteMarkdownDocumentContext.Provider value={remoteDocument ?? null}>
              <MarkdownReaderBody
                content={content}
                path={path}
                allowLocalTargets={localTargetsEnabled}
                seedDocument={source.kind === 'seed'}
                rootRef={rootRef}
                onImageClick={handleImageClick}
                onOpenSeed={onOpenSeed}
                plugins={plugins}
                reviewing={review !== undefined}
              />
              </RemoteMarkdownDocumentContext.Provider>
            </LocalImageLoaderContext.Provider>
          </SeedArtifactReaderContext.Provider>
        </div>
      </div>
  );

  return (
    <div
      className={`md-reader${annotationsEnabled ? ' md-reader--annotating' : ''}${review ? ' md-reader--review' : ''}`}
      ref={rootRef}
      // Focusable so a selection can claim keyboard focus: WebKit leaves focus
      // on the terminal's hidden input otherwise, leaking keys to the shell.
      tabIndex={annotationsEnabled ? -1 : undefined}
    >
      {review ? (
        <ReviewReader bundle={review} host={reviewHost} document={path} content={content} rootRef={rootRef} marks={reviewMarks} handleRef={reviewHandleRef}>
          {documentView}
        </ReviewReader>
      ) : documentView}
      {annotationsEnabled && (
        <AnnotationLayer ref={annotationLayerRef} api={annotationsApi} rootRef={rootRef} source={source} />
      )}
      {visibleLightbox && (
        <ImageLightbox src={visibleLightbox.src} alt={visibleLightbox.alt} onClose={handleLightboxClose} />
      )}
    </div>
  );
});
