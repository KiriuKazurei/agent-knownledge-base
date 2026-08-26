export function isExternalMarkdownLink(href: string): boolean {
  return /^(?:https?:|mailto:)/i.test(href.trim())
}

function decodeLink(value: string): string {
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}

function normalizeLocalPath(value: string): string {
  const replaced = value.replace(/\//g, '\\')
  const drive = replaced.match(/^[A-Za-z]:\\?/)?.[0] ?? ''
  const unc = !drive && replaced.startsWith('\\\\') ? '\\\\' : ''
  const prefix = drive || unc
  const rest = replaced.slice(prefix.length)
  const parts: string[] = []
  for (const part of rest.split('\\')) {
    if (!part || part === '.') continue
    if (part === '..' && parts.length > 0 && parts[parts.length - 1] !== '..') {
      parts.pop()
      continue
    }
    if (part !== '..' || !prefix) parts.push(part)
  }
  return (prefix + parts.join('\\')).replace(/\\$/, '')
}

export function canonicalDocumentPath(value: string): string {
  return normalizeLocalPath(value).toLowerCase()
}

function fileUrlToPath(value: string): string {
  const parsed = new URL(value)
  const pathname = decodeLink(parsed.pathname).replace(/^\/(?:([A-Za-z]):)/, '$1:')
  return normalizeLocalPath(pathname)
}

export function resolveMarkdownDocumentPath(sourcePath: string | undefined, href: string): string {
  const raw = href.trim()
  if (/^file:\/\//i.test(raw)) return fileUrlToPath(raw)
  const decoded = decodeLink(raw)
  const pathPart = decoded.split('#', 1)[0].split('?', 1)[0]
  if (!sourcePath || !pathPart) return canonicalDocumentPath(sourcePath ?? pathPart)
  if (/^(?:[A-Za-z]:[\\/]|\\\\|\/)/.test(pathPart)) return normalizeLocalPath(pathPart)
  const source = normalizeLocalPath(sourcePath)
  const separator = Math.max(source.lastIndexOf('\\'), source.lastIndexOf('/'))
  const base = separator >= 0 ? source.slice(0, separator) : ''
  return normalizeLocalPath(base ? base + '\\' + pathPart : pathPart)
}
