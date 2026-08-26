import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { client } from './api'

const runtimeConfig = vi.fn(async () => ({ apiBaseUrl: 'http://127.0.0.1:48761/api/v1', desktopToken: 'desktop-test', dataRoot: 'data', version: 'test' }))

function response(body: unknown, status = 200): Response {
  return { ok: status >= 200 && status < 300, status, statusText: 'error', json: async () => body } as Response
}

beforeEach(() => {
  Object.defineProperty(window, 'kah', { configurable: true, value: { runtimeConfig } })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.clearAllMocks()
})

test('waits for a Skill import job before resolving', async () => {
  const fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
  fetchMock
    .mockResolvedValueOnce(response({ id: 'job-1', kind: 'skill_import', status: 'queued', progress: 0 }))
    .mockResolvedValueOnce(response([{ id: 'job-1', kind: 'skill_import', status: 'completed', progress: 1, message: 'Imported Skill demo' }]))

  await expect(client.importSkill('C:\\skills\\demo\\SKILL.md')).resolves.toMatchObject({ id: 'job-1', status: 'completed' })
  expect(fetchMock).toHaveBeenCalledTimes(2)
  expect(fetchMock.mock.calls[0][0]).toBe('http://127.0.0.1:48761/api/v1/skills/import')
  expect(fetchMock.mock.calls[1][0]).toBe('http://127.0.0.1:48761/api/v1/jobs')
})

test('surfaces a failed Skill import job', async () => {
  const fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
  fetchMock
    .mockResolvedValueOnce(response({ id: 'job-2', kind: 'skill_import', status: 'queued', progress: 0 }))
    .mockResolvedValueOnce(response([{ id: 'job-2', kind: 'skill_import', status: 'failed', progress: 0, message: 'invalid skill package' }]))

  await expect(client.importSkill('C:\\skills\\broken.zip')).rejects.toThrow('invalid skill package')
})