import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: [],
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        // Point local dev at another install: AKMATORI_API_URL=http://host:8080 npm run dev
        target: process.env.AKMATORI_API_URL ?? 'https://api.vibevm.akmatori.com',
        changeOrigin: true,
      },
    },
  },
})
