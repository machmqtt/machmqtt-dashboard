import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  // This root config is the enterprise-auth fixture config. The general live
  // dashboard suite has its own e2e/playwright.config.ts and requires NATS/MQTT
  // topology data that is intentionally absent from the identity-only fixture.
  testMatch: 'enterprise-auth.spec.ts',
  fullyParallel: false,
  workers: 1,
  reporter: 'line',
  use: {
    baseURL: 'https://127.0.0.1:18443',
    ignoreHTTPSErrors: true,
    trace: 'retain-on-failure',
  },
})
