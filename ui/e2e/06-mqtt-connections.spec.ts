import { test, expect } from '@playwright/test'
import { discoverEnv, discoverBridge } from './helpers'

// The per-bridge MQTT Connections page. When the bridge exposes connz (admin URL
// + clients_snapshot_interval) it lists live clients; otherwise it shows the
// "Connection details not available" reason banner. Both are correct renders —
// assert the page resolves to one of them, never a crash or perpetual skeleton.
test('bridge connections page lists clients or explains why it cannot', async ({ page, request }) => {
  const env = await discoverEnv(request)
  const bridge = await discoverBridge(request, env.id)

  await page.goto(`/mqtt/${encodeURIComponent(bridge.name)}/connections`)
  await expect(page.getByRole('heading', { name: `${bridge.name} — MQTT Connections` })).toBeVisible()

  const clientRow = page.locator('table tbody tr', { hasText: /pub-|sub-|client/i })
  const reasonBanner = page.getByText('Connection details not available')

  await expect(clientRow.first().or(reasonBanner)).toBeVisible()
})
