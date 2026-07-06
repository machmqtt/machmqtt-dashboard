// Capture screenshots of the demo dashboard for the website. Reuses the chromium
// that the e2e suite installs (run with cwd = <repo>/ui so @playwright/test
// resolves). Driven by env: BASE_URL, OUT_DIR, DASH_USER, DASH_PASS.
import { createRequire } from 'node:module'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// @playwright/test lives in <repo>/ui/node_modules; resolve it from there
// (it's CJS, so require via a ui-anchored createRequire).
const here = path.dirname(fileURLToPath(import.meta.url))
const uiDir = process.env.UI_DIR || path.resolve(here, '../../ui')
const require = createRequire(path.join(uiDir, 'package.json'))
const { chromium } = require('@playwright/test')

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8095'
const OUT = process.env.OUT_DIR || '.run/shots'
const USER = process.env.DASH_USER || 'admin'
const PASS = process.env.DASH_PASS || 'demopassword'

fs.mkdirSync(OUT, { recursive: true })

const shots = [
  { name: 'overview', url: '/', wait: 'heading:Servers' },
  { name: 'topology', url: '/topology', settle: 4000 },
  { name: 'fleet', url: '/mqtt', wait: 'heading:MachMQTT Fleet', settle: 1500 },
  { name: 'mqtt-connections', url: '/mqtt/connections', wait: 'heading:All MQTT Connections', settle: 1500 },
  { name: 'bridge-detail', url: '/mqtt/edge-broker-1/detail', tab: 'Metrics', settle: 1000 },
  { name: 'jetstream', url: '/jetstream', wait: 'heading:JetStream' },
  { name: 'subscriptions', url: '/subscriptions', wait: 'heading:Subscriptions' },
]

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1600, height: 1000 }, deviceScaleFactor: 2 })

// Log in.
await page.goto(BASE + '/')
await page.locator('input[type="text"]').fill(USER)
await page.locator('input[type="password"]').fill(PASS)
await page.getByRole('button', { name: 'Sign In' }).click()
await page.getByRole('heading', { name: 'Servers' }).waitFor({ state: 'visible', timeout: 15000 })

for (const s of shots) {
  await page.goto(BASE + s.url)
  if (s.wait?.startsWith('heading:')) {
    await page.getByRole('heading', { name: s.wait.slice(8) }).first().waitFor({ state: 'visible', timeout: 15000 })
  }
  if (s.tab) {
    await page.getByRole('button', { name: s.tab, exact: true }).click().catch(() => {})
  }
  // Let charts/graphs animate and data settle before capturing.
  await page.waitForTimeout(s.settle ?? 1200)
  const file = path.join(OUT, `${s.name}.png`)
  await page.screenshot({ path: file, fullPage: true })
  console.log('captured', file)
}

await browser.close()
