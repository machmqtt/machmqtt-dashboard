import { test, expect, type APIRequestContext } from '@playwright/test'
import { discoverEnv, discoverBridge } from './helpers'

// DESTRUCTIVE — runs last (99-*). Fires every MachMQTT admin action against the
// live broker: drain → undrain → reload → kick-all. kick-all disconnects the
// traffic-gen clients (they auto-reconnect). The broker is force-undrained in
// finally + afterAll so a mid-test failure can't leave it drained for later runs.
//
// Each action is a button → inline confirm panel ("Confirm: <label>?") → a
// "Confirm" button → a success toast "<label>: <result>". The toast text (label
// + colon) is unique to the toast, so it never matches the confirm panel.

async function forceUndrain(request: APIRequestContext, envId: string, bridge: string): Promise<void> {
  await request
    .post(`/api/environments/${envId}/mqtt/${encodeURIComponent(bridge)}/admin/undrain`)
    .catch(() => {})
}

test.describe.serial('MachMQTT admin actions (destructive)', () => {
  test('drain, undrain, reload, and kick-all from the Admin tab', async ({ page, request }) => {
    const env = await discoverEnv(request)
    const bridge = await discoverBridge(request, env.id)

    const confirmAction = async (buttonName: string | RegExp, toast: RegExp) => {
      await page.getByRole('button', { name: buttonName, exact: typeof buttonName === 'string' }).click()
      await page.getByRole('button', { name: 'Confirm', exact: true }).click()
      await expect(page.getByText(toast).first()).toBeVisible()
    }

    try {
      await page.goto(`/mqtt/${encodeURIComponent(bridge.name)}/detail`)
      await page.getByRole('button', { name: 'Admin', exact: true }).click()
      await expect(page.getByRole('button', { name: 'Drain', exact: true })).toBeVisible()

      // Drain → "Draining" badge appears in the header.
      await confirmAction('Drain', /Drain this instance:/)
      await expect(page.getByText('Draining', { exact: true }).first()).toBeVisible()

      // Undrain → badge clears.
      await confirmAction('Undrain', /Undrain this instance:/)

      // Reload config.
      await confirmAction('Reload Config', /Reload config from disk:/)

      // Kick all local clients (disconnects traffic-gen; they reconnect).
      await confirmAction('Kick All (local)', /Kick all clients on this instance:/)
    } finally {
      await forceUndrain(request, env.id, bridge.name)
    }
  })

  test.afterAll(async ({ request }) => {
    const env = await discoverEnv(request)
    const bridge = await discoverBridge(request, env.id)
    await forceUndrain(request, env.id, bridge.name)
  })
})
