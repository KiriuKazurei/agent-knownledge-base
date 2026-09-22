// @vitest-environment node
import { describe, expect, test, vi } from 'vitest'
import { showWindowWhenReady } from './windowVisibility'

type Listener = () => void

function windowFixture(): {
  window: Parameters<typeof showWindowWhenReady>[0]
  emitReadyToShow: () => void
  emitDidFinishLoad: () => void
  destroy: () => void
  show: ReturnType<typeof vi.fn>
} {
  let destroyed = false
  let visible = false
  let readyToShow: Listener | undefined
  let didFinishLoad: Listener | undefined
  const show = vi.fn(() => { visible = true })

  return {
    window: {
      once: ((_event: string, listener: Listener) => { readyToShow = listener }) as never,
      webContents: {
        on: ((_event: string, listener: Listener) => { didFinishLoad = listener })
      } as never,
      isDestroyed: () => destroyed,
      isVisible: () => visible,
      show
    } as never,
    emitReadyToShow: () => readyToShow?.(),
    emitDidFinishLoad: () => didFinishLoad?.(),
    destroy: () => { destroyed = true },
    show
  }
}

describe('showWindowWhenReady', () => {
  test('shows after the renderer finishes loading when ready-to-show does not fire', () => {
    const fixture = windowFixture()
    showWindowWhenReady(fixture.window)

    fixture.emitDidFinishLoad()
    fixture.emitDidFinishLoad()

    expect(fixture.show).toHaveBeenCalledTimes(1)
  })

  test('keeps ready-to-show and does not show again after did-finish-load', () => {
    const fixture = windowFixture()
    showWindowWhenReady(fixture.window)

    fixture.emitReadyToShow()
    fixture.emitDidFinishLoad()

    expect(fixture.show).toHaveBeenCalledTimes(1)
  })

  test('does not show a window destroyed before either event', () => {
    const fixture = windowFixture()
    showWindowWhenReady(fixture.window)
    fixture.destroy()

    fixture.emitReadyToShow()
    fixture.emitDidFinishLoad()

    expect(fixture.show).not.toHaveBeenCalled()
  })
})
