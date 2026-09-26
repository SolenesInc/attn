import { createContext, memo, useContext, useMemo, type ReactNode } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkBreaks from 'remark-breaks';
import remarkGfm from 'remark-gfm';
import { CodeFrame } from './CodeFrame';
import { MermaidDiagram } from './MermaidDiagram';
import { useShikiHighlight } from './shiki';

// A context rather than a per-render closure: it keeps CodeRenderer's identity
// stable, so a fresh callback never remounts an in-flight MermaidDiagram.
const DiagramLayoutChangeContext = createContext<(() => void) | undefined>(undefined);
const MarkdownPresentationContext = createContext<'static' | 'reader'>('static');

export function ReaderPresentation({ children }: { children: ReactNode }) {
  return (
    <MarkdownPresentationContext.Provider value="reader">
      {children}
    </MarkdownPresentationContext.Provider>
  );
}

const PreRenderer: Components['pre'] = ({ children, className, ...props }) => {
  const presentation = useContext(MarkdownPresentationContext);
  if (presentation !== 'reader') {
    return <pre className={className} {...props}>{children}</pre>;
  }
  return <CodeFrame className={className}>{children}</CodeFrame>;
};

function highlightableLanguage(className: string | undefined): string | undefined {
  const found = /language-([\w-]+)/.exec(className ?? '');
  const language = found?.[1];
  if (!language || language === 'mermaid') return undefined;
  return language;
}

// react-markdown v10's `code` component gets no `inline` flag; a fenced block
// carries a `language-*` className, inline code carries none.
export const CodeRenderer: Components['code'] = ({ className, children, ...props }) => {
  const onDiagramLayoutChange = useContext(DiagramLayoutChangeContext);
  const presentation = useContext(MarkdownPresentationContext);
  const language = highlightableLanguage(className);
  const code = language ? String(children) : '';
  const highlighted = useShikiHighlight(code, language);
  if (className?.includes('language-mermaid')) {
    return (
      <MermaidDiagram
        code={String(children).trimEnd()}
        onLayoutChange={onDiagramLayoutChange}
        presentation={presentation}
      />
    );
  }
  if (highlighted) {
    return (
      <code
        className={`${className ?? ''} markdown-shiki`.trim()}
        {...props}
        // eslint-disable-next-line react/no-danger -- shiki output is
        // library-generated spans over the code text, not document HTML.
        dangerouslySetInnerHTML={{ __html: highlighted.html }}
      />
    );
  }
  return (
    <code className={className} {...props}>
      {children}
    </code>
  );
};

const defaultComponents: Components = { code: CodeRenderer, pre: PreRenderer };

const MarkdownDocument = memo(function MarkdownDocument({
  source,
  remarkPlugins,
  components,
}: {
  source: string;
  remarkPlugins: NonNullable<Parameters<typeof ReactMarkdown>[0]['remarkPlugins']>;
  components: Components;
}) {
  return (
    <ReactMarkdown remarkPlugins={remarkPlugins} components={components}>
      {source}
    </ReactMarkdown>
  );
});

interface MarkdownProps {
  children: string;
  className?: string;
  components?: Components;
  breaks?: boolean;
  onDiagramLayoutChange?: () => void;
}

export function Markdown({ children, className, components, breaks, onDiagramLayoutChange }: MarkdownProps) {
  const remarkPlugins = useMemo(() => (breaks ? [remarkGfm, remarkBreaks] : [remarkGfm]), [breaks]);
  const merged = useMemo(() => ({ ...defaultComponents, ...components }), [components]);
  return (
    <div className={className}>
      <DiagramLayoutChangeContext.Provider value={onDiagramLayoutChange}>
        <MarkdownDocument source={children} remarkPlugins={remarkPlugins} components={merged} />
      </DiagramLayoutChangeContext.Provider>
    </div>
  );
}
