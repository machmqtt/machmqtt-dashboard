import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  reporter: 'line',
  use: {
    baseURL: 'https://127.0.0.1:18443',
    ignoreHTTPSErrors: true,
    trace: 'retain-on-failure',
  },
})
