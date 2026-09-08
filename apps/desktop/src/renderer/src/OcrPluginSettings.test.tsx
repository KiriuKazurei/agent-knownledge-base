import '@testing-library/jest-dom/vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, expect, test, vi } from 'vitest'
import { OcrPluginSettings } from './OcrPluginSettings'
import { api } from './api'
vi.mock('./api', () => ({api:vi.fn()}))
afterEach(cleanup)
test('uninstall sends DELETE and shows install action after resource removal', async () => {
 let installed=true
 vi.mocked(api).mockImplementation(async (_path,init) => {
   if (init?.method === 'DELETE') installed=false
   return {installed,enabled:installed,version:'test',sizeBytes:1024} as never
 })
 render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><OcrPluginSettings/></QueryClientProvider>)
 fireEvent.click(await screen.findByRole('button',{name:'卸载 OCR 插件'}))
 await waitFor(() => expect(api).toHaveBeenCalledWith('/plugins/ocr',{method:'DELETE',body:undefined}))
 expect(await screen.findByRole('button',{name:'安装 OCR 离线包'})).toBeInTheDocument()
 expect(screen.queryByRole('button',{name:'卸载 OCR 插件'})).not.toBeInTheDocument()
})
