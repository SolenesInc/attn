import type { Html, HtmlBuilder } from 'foldkit/html'
import type { Nodes, Root, RootContent } from 'mdast'
import { fromMarkdown } from 'mdast-util-from-markdown'
import { gfmFromMarkdown } from 'mdast-util-gfm'
import { gfm } from 'micromark-extension-gfm'

const walk = (node: Nodes, visit: (node: Nodes) => void) => {
  visit(node)
  if ('children' in node) for (const child of node.children) walk(child, visit)
}

const attribute = (tag: string, name: string): string | undefined => {
  const match = new RegExp(`\\s${name}\\s*=\\s*(?:"([^"]*)"|'([^']*)'|([^\\s>]+))`, 'i').exec(tag)
  const value = match?.[1] ?? match?.[2] ?? match?.[3]
  return value === undefined ? undefined : decodeReferences(value)
}

const namedReferences: Record<string, string> = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'" }

const decodeReferences = (value: string) =>
  value.replace(/&(#x[0-9a-f]+|#[0-9]+|[a-z]+);/gi, (reference, name: string) => {
    if (name[0] !== '#') return namedReferences[name.toLowerCase()] ?? reference
    const code =
      name[1] === 'x' || name[1] === 'X' ? parseInt(name.slice(2), 16) : parseInt(name.slice(1), 10)
    return code > 0 && code <= 0x10ffff ? String.fromCodePoint(code) : reference
  })

const htmlImages = (html: string): ReadonlyArray<{ readonly src: string; readonly alt: string }> =>
  [...html.matchAll(/<img\b[^>]*>/gi)].flatMap(([tag]) => {
    const src = attribute(tag, 'src')
    return src ? [{ src, alt: attribute(tag, 'alt') ?? '' }] : []
  })

type Parsed = {
  readonly root: Root
  readonly definitions: ReadonlyMap<string, string>
  readonly images: ReadonlyArray<{ readonly src: string; readonly alt: string }>
  readonly diagrams: ReadonlyArray<string>
}

const parse = (markdown: string): Parsed => {
  const root = fromMarkdown(markdown, { extensions: [gfm()], mdastExtensions: [gfmFromMarkdown()] })
  const definitions = new Map<string, string>()
  walk(root, (node) => {
    if (node.type === 'definition' && !definitions.has(node.identifier))
      definitions.set(node.identifier, node.url)
  })
  const images: Array<{ src: string; alt: string }> = []
  const diagrams: Array<string> = []
  walk(root, (node) => {
    if (node.type === 'image' && node.url) images.push({ src: node.url, alt: node.alt ?? '' })
    if (node.type === 'imageReference') {
      const src = definitions.get(node.identifier)
      if (src) images.push({ src, alt: node.alt ?? '' })
    }
    if (node.type === 'html') images.push(...htmlImages(node.value))
    if (node.type === 'code' && node.lang === 'mermaid') diagrams.push(node.value)
  })
  return { root, definitions, images, diagrams }
}

let parsed = new Map<string, Parsed>()

const parsedMarkdown = (markdown: string): Parsed => {
  const cached = parsed.get(markdown)
  if (cached) return cached
  const fresh = parse(markdown)
  parsed.set(markdown, fresh)
  return fresh
}

export const keepParsed = (markdowns: Iterable<string>) => {
  const kept = new Map<string, Parsed>()
  for (const markdown of markdowns) kept.set(markdown, parsedMarkdown(markdown))
  parsed = kept
}

export type Visuals = { readonly images: ReadonlyArray<string>; readonly diagrams: ReadonlyArray<string> }

export const visualsOf = (markdowns: ReadonlyArray<string>): Visuals => {
  const images = new Set<string>()
  const diagrams = new Set<string>()
  for (const markdown of markdowns) {
    const shown = parsedMarkdown(markdown)
    for (const image of shown.images) images.add(image.src)
    for (const code of shown.diagrams) diagrams.add(code)
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
  const { root, definitions } = parsedMarkdown(markdown)
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
      case 'imageReference': {
        const src = definitions.get(node.identifier)
        return src ? image(src, node.alt ?? '') : `![${node.alt ?? ''}]`
      }
      case 'linkReference': {
        const href = definitions.get(node.identifier)
        return href
          ? h.a([h.Href(href), h.Target('_blank'), h.Rel('noopener')], children(node))
          : textOf(node)
      }
      case 'definition':
        return ''
      case 'table':
        return h.div([h.Class('md-table')], [h.table([], [h.tbody([], children(node))])])
      case 'tableRow':
        return h.tr([], children(node))
      case 'tableCell':
        return h.td([], children(node))
      case 'html': {
        const images = htmlImages(node.value)
        return images.length > 0
          ? h.span(
              [],
              images.map((shown) => image(shown.src, shown.alt)),
            )
          : h.code([h.Class('md-html')], [node.value])
      }
      default:
        return textOf(node)
    }
  }

  return h.div([h.Class('markdown')], children(root))
}
