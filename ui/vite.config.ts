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
    // The page suites that drive a whole screen (JetStream, Clusters, MQTT bridge
    // detail) spend most of their time in Testing Library's *ByRole queries, which
    // call jsdom's getComputedStyle for every candidate element. That makes their
    // wall time track machine load rather than their own work: ~1.5s for the
    // JetStream suite on an idle box, 5.4s when vitest saturates every core, and
    // 21.7s inside scripts/verify.sh on a host also running the broker's docker
    // suites. The 5s default failed intermittently, and a 20s guard sized against
    // an idle machine still failed under verify.sh.
    //
    // So size it for the loaded case. This is a hang guard, not a performance
    // budget -- a test that never resolves still fails, just later -- and the
    // ceiling it needs to clear is set by whatever else shares the host.
    testTimeout: 60_000,
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
