import { appendFileSync, existsSync, mkdirSync, renameSync, statSync, unlinkSync } from 'node:fs'
import path from 'node:path'
import { formatWithOptions } from 'node:util'

export function redact(value: string): string {
  return value.replace(/\b(?:desktop_|kah_)[A-Za-z0-9_-]{20,}\b/g, '[REDACTED]')
    .replace(/(Bearer\s+)[^\s"',}]+/gi, '$1[REDACTED]')
    .replace(/\bsk-[A-Za-z0-9_-]+/g, '[REDACTED]')
}

export function installDiagnostics(directory: string): void {
  mkdirSync(directory, { recursive: true })
  const file = path.join(directory, 'desktop.log')
  for (const level of ['info', 'warn', 'error'] as const) {
    const original = console[level].bind(console)
    console[level] = (...values: unknown[]) => {
      const message = redact(formatWithOptions({ depth: 4 }, ...values))
      try {
        if (existsSync(file) && statSync(file).size >= 5 * 1024 * 1024) {
          if (existsSync(`${file}.3`)) unlinkSync(`${file}.3`)
          for (let i = 2; i >= 1; i--) if (existsSync(`${file}.${i}`)) renameSync(`${file}.${i}`, `${file}.${i + 1}`)
          renameSync(file, `${file}.1`)
        }
        appendFileSync(file, JSON.stringify({ time: new Date().toISOString(), level, pid: process.pid, message }) + '\n', 'utf8')
      } catch (error) { original('Cannot write diagnostic log', error) }
      original(message)
    }
  }
}
