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
    // Point local dev at another install: AKMATORI_API_URL=http://host:8080 npm run dev
    proxy: Object.fromEntries(
      ['/api', '/auth', '/mcp'].map((path) => [
        path,
        { target: process.env.AKMATORI_API_URL ?? 'https://api.vibevm.akmatori.com', changeOrigin: true },
      ]),
    ),
  },
})
