import { test, expect } from '@playwright/test'
import { discoverEnv, discoverBridge } from './helpers'

test.describe('MachMQTT Fleet & bridge detail', () => {
  test('fleet shows exactly the bridges the API reports (merge guard)', async ({ page, request }) => {
    const env = await discoverEnv(request)
    const bridge = await discoverBridge(request, env.id)

    await page.goto('/mqtt')
    await expect(page.getByRole('heading', { name: 'MachMQTT Fleet' })).toBeVisible()

    // The configured admin URL merges into the push-discovered instance — there
    // must be exactly ONE card per bridge name, not a duplicate.
    await expect(page.getByRole('heading', { name: bridge.name, level: 2 })).toHaveCount(1)
    await expect(page.getByRole('link', { name: 'Details' })).toHaveCount(bridge.count)
  })

  test('bridge detail loads every tab without the error banner', async ({ page, request }) => {
    const env = await discoverEnv(request)
    const bridge = await discoverBridge(request, env.id)

    await page.goto(`/mqtt/${encodeURIComponent(bridge.name)}/detail`)
    await expect(page.getByRole('heading', { name: bridge.name, level: 1 })).toBeVisible()

    // The "could not load any details" red banner must never appear — push
    // metrics (and now the configured admin API) keep the page populated.
    const banner = page.getByText('Could not load any details for this bridge')
    await expect(banner).toHaveCount(0)

    // NATS Connection (default tab): connection block + truncated server name.
    await expect(page.getByRole('heading', { name: 'Connection' })).toBeVisible()
    await expect(page.getByText('Server Name', { exact: true })).toBeVisible()

    // Metrics tab.
    await page.getByRole('button', { name: 'Metrics', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Connections (established MQTT)' })).toBeVisible()

    // Connection Pool tab.
    await page.getByRole('button', { name: 'Connection Pool', exact: true }).click()
    await expect(page.getByText('Pool Size', { exact: true })).toBeVisible()

    // Cluster tab — single-node broker, so this is the "not enabled" state OR a
    // members table; either is a correct render (not a crash / blank).
    await page.getByRole('button', { name: 'Cluster', exact: true }).click()
    await expect(page.getByText(/clustering is not enabled|Members/i).first()).toBeVisible()

    // License tab — live now that the admin URL is configured.
    await page.getByRole('button', { name: 'License', exact: true }).click()
    await expect(page.getByText('Tier', { exact: true })).toBeVisible()

    // Config tab — live running configuration.
    await page.getByRole('button', { name: 'Config', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Running Configuration' })).toBeVisible()

    // Admin tab — destructive controls render for an admin user (not fired here).
    await page.getByRole('button', { name: 'Admin', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Drain', exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Reload Config' })).toBeVisible()

    // Re-confirm no error banner surfaced during the walk.
    await expect(banner).toHaveCount(0)
  })
})
