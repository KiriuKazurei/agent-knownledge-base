import { expect, test } from 'vitest'
import { canonicalDocumentPath, isExternalMarkdownLink, resolveMarkdownDocumentPath } from './markdownLinks'

test('resolves percent-encoded Chinese Markdown links relative to the source file', () => {
  expect(resolveMarkdownDocumentPath('E:\\资料\\目录\\首页.md', './章节/第二篇%20中文.md#概要'))
    .toBe('E:\\资料\\目录\\章节\\第二篇 中文.md')
})

test('normalizes parent links and compares document paths case-insensitively', () => {
  const resolved = resolveMarkdownDocumentPath('C:\\Docs\\A\\index.md', '../B/README.md')
  expect(resolved).toBe('C:\\Docs\\B\\README.md')
  expect(canonicalDocumentPath('C:/Docs/B/README.md')).toBe(canonicalDocumentPath(resolved))
})

test('recognizes external links without treating local Chinese paths as URLs', () => {
  expect(isExternalMarkdownLink('https://example.com/中文')).toBe(true)
  expect(isExternalMarkdownLink('mailto:test@example.com')).toBe(true)
  expect(isExternalMarkdownLink('./中文文档.md')).toBe(false)
})
