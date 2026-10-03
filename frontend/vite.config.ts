import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// Built to dist/ with relative asset paths and embedded into the Go binary.
// During development /api goes to a running Notif.
export default defineConfig({
  plugins: [vue()],
  base: './',
  build: { outDir: 'dist', emptyOutDir: false },
  server: { proxy: { '/api': 'http://127.0.0.1:8097' } },
})
