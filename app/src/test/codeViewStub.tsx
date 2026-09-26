import { forwardRef, useImperativeHandle, useRef, type CSSProperties, type ReactNode, type Ref } from 'react';
import type { CodeViewItem } from '@pierre/diffs/react';

type Side = 'additions' | 'deletions';
type Range = { side: Side; start: number; end: number };
type Item = CodeViewItem<unknown>;
type Annotation = { side: Side; lineNumber: number; metadata?: unknown };

interface CodeViewStubProps {
  items: Item[];
  options?: { onGutterUtilityClick?: (range: Range, context: { item: Item }) => void };
  className?: string;
  style?: CSSProperties;
  containerRef?: Ref<HTMLDivElement>;
  renderAnnotation?: (annotation: Annotation, item: Item) => ReactNode;
  renderHeaderMetadata?: (item: Item) => ReactNode;
  renderHeaderPrefix?: (item: Item) => ReactNode;
}

export const diffRendererScrolls: Array<{ id?: string }> = [];

function Lines({ item, side, onComment }: { item: Item; side: Side; onComment: (range: Range) => void }) {
  if (item.type !== 'diff') return null;
  const lines = side === 'additions' ? item.fileDiff.additionLines : item.fileDiff.deletionLines;
  const label = side === 'additions' ? 'line' : 'old line';
  return (
    <>
      {lines.map((text, index) => (
        <div key={`${side}-${index}`}>
          <button type="button" aria-label={`Comment on ${item.id} ${label} ${index + 1}`} onClick={() => onComment({ side, start: index + 1, end: index + 1 })} />
          <span>{text}</span>
        </div>
      ))}
    </>
  );
}

export const CodeViewStub = forwardRef(function CodeViewStub(props: CodeViewStubProps, ref) {
  const rendered = useRef(new Map<string, HTMLElement>());
  useImperativeHandle(ref, () => ({
    getInstance: () => ({
      getRenderedItems: () => Array.from(rendered.current, ([id, element]) => ({ id, element })),
    }),
    scrollTo: (target: { id?: string }) => { diffRendererScrolls.push(target); },
  }));
  return (
    <div ref={props.containerRef} className={props.className} style={props.style}>
      {props.items.map((item) => (
        <section
          key={item.id}
          aria-label={item.id}
          ref={(element) => {
            if (element) rendered.current.set(item.id, element);
            else rendered.current.delete(item.id);
          }}
        >
          {props.renderHeaderPrefix?.(item)}
          {props.renderHeaderMetadata?.(item)}
          {item.type === 'file' && <pre>{item.file.contents}</pre>}
          <Lines item={item} side="deletions" onComment={(range) => props.options?.onGutterUtilityClick?.(range, { item })} />
          <Lines item={item} side="additions" onComment={(range) => props.options?.onGutterUtilityClick?.(range, { item })} />
          {((item.type === 'diff' ? item.annotations : undefined) as Annotation[] | undefined ?? []).map((annotation, index) => (
            <div key={index}>{props.renderAnnotation?.(annotation, item)}</div>
          ))}
        </section>
      ))}
    </div>
  );
});
