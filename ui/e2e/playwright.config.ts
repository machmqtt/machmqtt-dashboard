import { defineConfig, devices } from '@playwright/test'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

// The suite runs against an ALREADY-RUNNING dashboard (the shipped :8090 binary
// with the embedded UI bundle), not the vite dev server — so it exercises the
// real Go server + embedded assets. See README.md for the stack prerequisites.
const BASE_URL = process.env.E2E_BASE_URL || 'http://127.0.0.1:8090'

// Absolute so it resolves the same regardless of the cwd `playwright test` runs
// from (the global-setup writes to this exact path).
const STORAGE_STATE = path.join(__dirname, '.auth', 'state.json')

export default defineConfig({
  testDir: '.',
  testMatch: '*.spec.ts',
  // The live stack has shared, mutating state (one broker, 6 traffic-gen clients,
  // destructive admin actions). Serialize everything: no parallelism, one worker,
  // file order honored. The destructive spec (99-*) runs last by filename.
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  forbidOnly: !!process.env.CI,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  reporter: [['list'], ['html', { outputFolder: '.report', open: 'never' }]],
  outputDir: '.artifacts',
  globalSetup: './global-setup.ts',
  use: {
    baseURL: BASE_URL,
    storageState: STORAGE_STATE,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    actionTimeout: 10_000,
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
