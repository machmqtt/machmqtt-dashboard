import { chromium, request, type FullConfig } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

const BASE_URL = process.env.E2E_BASE_URL || 'http://127.0.0.1:8090'
const USER = process.env.E2E_ADMIN_USER || 'e2e-admin'
const PASS = process.env.E2E_ADMIN_PASS || 'E2ePlaywright!2026'

// ensureUser guarantees a deterministic admin login exists. It first tries to log
// in as the test admin; if that fails AND bootstrap admin creds are supplied
// (E2E_BOOTSTRAP_USER/PASS — an existing admin whose password you know), it
// creates the test admin. Creation is idempotent: a 409 "already exists" on a
// second run is treated as success.
async function ensureUser(): Promise<void> {
  const ctx = await request.newContext({ baseURL: BASE_URL })
  try {
    const login = await ctx.post('/api/login', { data: { username: USER, password: PASS } })
    if (login.ok()) return

    const bu = process.env.E2E_BOOTSTRAP_USER
    const bp = process.env.E2E_BOOTSTRAP_PASS
    if (!bu || !bp) {
      throw new Error(
        `Cannot log in as "${USER}" and no E2E_BOOTSTRAP_USER/E2E_BOOTSTRAP_PASS set to create it.\n` +
          `Create the test admin once (as an existing admin):\n` +
          `  curl -b <admin-cookie> -X POST ${BASE_URL}/api/admin/users \\\n` +
          `    -H 'Content-Type: application/json' \\\n` +
          `    -d '{"username":"${USER}","password":"${PASS}","role":"admin"}'\n` +
          `See e2e/README.md.`,
      )
    }
    const blogin = await ctx.post('/api/login', { data: { username: bu, password: bp } })
    if (!blogin.ok()) throw new Error(`bootstrap admin "${bu}" login failed (HTTP ${blogin.status()})`)
    const create = await ctx.post('/api/admin/users', {
      data: { username: USER, password: PASS, role: 'admin' },
    })
    if (!create.ok() && create.status() !== 409) {
      throw new Error(`failed to create "${USER}": HTTP ${create.status()} ${await create.text()}`)
    }
    const verify = await ctx.post('/api/login', { data: { username: USER, password: PASS } })
    if (!verify.ok()) throw new Error(`created "${USER}" but login still fails (HTTP ${verify.status()})`)
  } finally {
    await ctx.dispose()
  }
}

export default async function globalSetup(_config: FullConfig): Promise<void> {
  await ensureUser()

  const authDir = path.join(__dirname, '.auth')
  fs.mkdirSync(authDir, { recursive: true })

  // Log in through the real UI form (covers the auth screen) and persist the
  // session cookie as storageState for every spec.
  const browser = await chromium.launch()
  try {
    const page = await browser.newPage()
    await page.goto(BASE_URL + '/')
    // The login form has one text input (username) and one password input.
    await page.locator('input[type="text"]').fill(USER)
    await page.locator('input[type="password"]').fill(PASS)
    await page.getByRole('button', { name: 'Sign In' }).click()
    // The Overview page (Servers table) only renders once authenticated.
    await page.getByRole('heading', { name: 'Servers' }).waitFor({ state: 'visible', timeout: 15_000 })
    await page.context().storageState({ path: path.join(authDir, 'state.json') })
  } finally {
    await browser.close()
  }
}
