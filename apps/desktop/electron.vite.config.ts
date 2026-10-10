import { defineConfig } from 'electron-vite'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  main: {
    // Build-time only. Official release verification rejects nonempty test
    // feeds; runtime environment variables cannot redirect production updates.
    define: { __KAH_UPDATE_TEST_URL__: JSON.stringify(process.env.KAH_UPDATE_TEST_URL ?? '') },
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
