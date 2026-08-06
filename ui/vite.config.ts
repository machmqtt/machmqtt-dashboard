import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: '../internal/api/dist',
    emptyOutDir: true,
    manifest: true,
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
    setupFiles: ['./src/test/setup.ts'],
    coverage: {
      provider: 'v8',
      include: ['src/**/*.{ts,tsx}'],
      exclude: ['src/test/**', 'src/main.tsx'],
      reporter: ['text', 'json-summary'],
      thresholds: { statements: 95, lines: 95, functions: 95, branches: 90 },
    },
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
})
