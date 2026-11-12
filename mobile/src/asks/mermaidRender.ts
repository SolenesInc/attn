export type MermaidTheme = 'dark' | 'neutral'

// Loaded lazily so mermaid (large) is code-split out of the main bundle and
// only fetched when a document actually contains a mermaid fence.
let mermaidModulePromise: Promise<typeof import('mermaid')> | null = null
function loadMermaid() {
  if (!mermaidModulePromise) {
    mermaidModulePromise = import('mermaid')
  }
  return mermaidModulePromise
}

// Resolves to sanitized SVG markup (securityLevel: 'strict').
export async function renderMermaid(id: string, code: string, theme: MermaidTheme): Promise<string> {
  const { default: mermaid } = await loadMermaid()
  mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', theme })
  try {
    const { svg } = await mermaid.render(id, code)
    return svg
  } catch (err) {
    // mermaid can leave an orphan error node in the document on a failed render.
    document.getElementById(`d${id}`)?.remove()
    throw err
  }
}
