import type { Html, HtmlBuilder } from 'foldkit/html'
import type { Nodes, Root, RootContent } from 'mdast'
import { fromMarkdown } from 'mdast-util-from-markdown'
import { gfmFromMarkdown } from 'mdast-util-gfm'
import { gfm } from 'micromark-extension-gfm'

const parsed = new Map<string, Root>()
const parsedLimit = 200

export const parseMarkdown = (markdown: string): Root => {
  const cached = parsed.get(markdown)
  if (cached) return cached
  if (parsed.size >= parsedLimit) parsed.clear()
  const root = fromMarkdown(markdown, { extensions: [gfm()], mdastExtensions: [gfmFromMarkdown()] })
  parsed.set(markdown, root)
  return root
}

const walk = (node: Nodes, visit: (node: Nodes) => void) => {
  visit(node)
  if ('children' in node) for (const child of node.children) walk(child, visit)
}

export type Visuals = { readonly images: ReadonlyArray<string>; readonly diagrams: ReadonlyArray<string> }

// The image sources and mermaid diagrams a piece of markdown shows, each once, in reading order.
export const visualsOf = (markdowns: ReadonlyArray<string>): Visuals => {
  const images = new Set<string>()
  const diagrams = new Set<string>()
  for (const markdown of markdowns) {
    walk(parseMarkdown(markdown), (node) => {
      if (node.type === 'image' && node.url) images.add(node.url)
      if (node.type === 'code' && node.lang === 'mermaid') diagrams.add(node.value)
    })
  }
  return { images: [...images], diagrams: [...diagrams] }
}

export type ImageView =
  | { readonly status: 'loading' }
  | { readonly status: 'loaded'; readonly url: string }
  | { readonly status: 'failed'; readonly message: string }

export type DiagramView =
  | { readonly status: 'loading' }
  | { readonly status: 'rendered'; readonly svg: string }
  | { readonly status: 'failed'; readonly message: string }

export type MarkdownContext<Message> = {
  readonly image: (src: string) => ImageView
  readonly diagram: (code: string) => DiagramView
  readonly zoomImage: (url: string, alt: string) => Message
  readonly zoomDiagram: (svg: string) => Message
}

const textOf = (node: Nodes): string =>
  'value' in node ? node.value : 'children' in node ? node.children.map(textOf).join('') : ''

export const renderMarkdown = <Message>(
  markdown: string,
  context: MarkdownContext<Message>,
  h: HtmlBuilder<Message>,
): Html => {
  const children = (node: Nodes): Array<Html | string> =>
    'children' in node ? node.children.map((child: RootContent) => render(child)) : []

  const image = (src: string, alt: string): Html => {
    const shown = context.image(src)
    switch (shown.status) {
      case 'loaded':
        return h.button(
          [
            h.Class('md-image'),
            h.Type('button'),
            h.AriaLabel(`Open ${alt || 'the image'} full screen`),
            h.OnClick(context.zoomImage(shown.url, alt)),
          ],
          [h.img([h.Src(shown.url), h.Alt(alt)])],
        )
      case 'loading':
        return h.span([h.Class('md-image placeholder')], [alt ? `Loading ${alt}…` : 'Loading image…'])
      case 'failed':
        return h.span([h.Class('md-image failed'), h.Role('img'), h.AriaLabel(alt)], [shown.message])
    }
  }

  const diagram = (code: string): Html => {
    const shown = context.diagram(code)
    switch (shown.status) {
      case 'rendered':
        return h.button(
          [
            h.Class('md-diagram'),
            h.Type('button'),
            h.AriaLabel('Open the diagram full screen'),
            h.OnClick(context.zoomDiagram(shown.svg)),
          ],
          [h.div([h.InnerHTML(shown.svg)], [])],
        )
      case 'loading':
        return h.pre([h.Class('md-diagram placeholder')], [code])
      case 'failed':
        return h.div(
          [h.Class('md-diagram failed')],
          [h.p([], [`Diagram failed to render: ${shown.message}`]), h.pre([], [code])],
        )
    }
  }

  const render = (node: RootContent): Html | string => {
    switch (node.type) {
      case 'text':
        return node.value
      case 'paragraph':
        return h.p([], children(node))
      case 'heading':
        return [h.h1, h.h2, h.h3, h.h4, h.h5, h.h6][node.depth - 1]!([], children(node))
      case 'emphasis':
        return h.em([], children(node))
      case 'strong':
        return h.strong([], children(node))
      case 'delete':
        return h.del([], children(node))
      case 'inlineCode':
        return h.code([], [node.value])
      case 'code':
        return node.lang === 'mermaid' ? diagram(node.value) : h.pre([], [h.code([], [node.value])])
      case 'break':
        return h.br([])
      case 'thematicBreak':
        return h.hr([])
      case 'blockquote':
        return h.blockquote([], children(node))
      case 'list':
        return (node.ordered ? h.ol : h.ul)([h.Class('md-list')], children(node))
      case 'listItem':
        return h.li(
          [],
          node.checked === null || node.checked === undefined
            ? children(node)
            : [
                h.span(
                  [
                    h.Class(`md-check${node.checked ? ' checked' : ''}`),
                    h.AriaLabel(node.checked ? 'done' : 'open'),
                  ],
                  [],
                ),
                ...children(node),
              ],
        )
      case 'link':
        return h.a([h.Href(node.url), h.Target('_blank'), h.Rel('noopener')], children(node))
      case 'image':
        return image(node.url, node.alt ?? '')
      case 'table':
        return h.div([h.Class('md-table')], [h.table([], [h.tbody([], children(node))])])
      case 'tableRow':
        return h.tr([], children(node))
      case 'tableCell':
        return h.td([], children(node))
      case 'html':
        return h.code([h.Class('md-html')], [node.value])
      default:
        return textOf(node)
    }
  }

  return h.div([h.Class('markdown')], children(parseMarkdown(markdown)))
}
