import { defineConfig } from 'electron-vite'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  main: {
    build: {
      rollupOptions: {
        input: { index: path.join(path.dirname(fileURLToPath(import.meta.url)), 'src/main/index.ts') }
      }
    }
  },
  preload: {
    build: {
      rollupOptions: {
        output: {
          format: 'cjs',
          entryFileNames: '[name].cjs',
          chunkFileNames: '[name]-[hash].cjs'
        }
      }
    }
  },
  renderer: {
    plugins: [react(), tailwindcss()],
    resolve: { alias: { '@': new URL('./src/renderer/src', import.meta.url).pathname } }
  }
})
