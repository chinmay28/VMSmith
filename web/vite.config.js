import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { fileURLToPath } from 'node:url'
import vmsmithServiceWorker from './build/swPlugin.js'

export default defineConfig({
  plugins: [
    react(),
    // Emits /sw.js (offline shell + update flow) — see docs/PWA.md.
    vmsmithServiceWorker({ entry: fileURLToPath(new URL('./src/pwa/sw.js', import.meta.url)) }),
  ],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
  },
  server: {
    port: 3000,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
})
