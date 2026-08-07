import { test, expect, type Page } from '@playwright/test'
import { discoverEnv } from './helpers'

// A JetStream outage and a tampered/expired license cannot be produced on demand
// from a live broker, so these specs intercept the bridge reads in the browser.
// Everything else stays real: the Go server, the embedded UI bundle, routing and
// the authenticated session. Unlike the other specs this one therefore needs no
// broker — only a running dashboard.

const BRIDGE = 'e2e-degraded-broker'

// A bridge whose readyz answered 503 "jetstream-degraded": the admin API is up
// (connz available, NATS connected), JetStream is not. statusOverrides swaps in
// another readyz-derived state on the same reachable bridge.
function degradedBridge(statusOverrides: Record<string, unknown> = {}) {
  const b = {
    bridges: [
      {
        ip: '127.0.0.1',
        server_id: 'NDEGRADED',
        server_name: 'nats-1',
        configured_name: BRIDGE,
        admin_url: 'http://127.0.0.1:18081',
        reachable: true,
        pool_connections: 4,
        total_subs: 12,
        total_in_msgs: 100,
        total_out_msgs: 90,
        total_in_bytes: 1000,
        total_out_bytes: 900,
        in_msgs_rate: 1.5,
        out_msgs_rate: 1.25,
        in_bytes_rate: 128,
        out_bytes_rate: 96,
        status: {
          name: BRIDGE,
          url: 'http://127.0.0.1:18081',
          ready: false,
          draining: false,
          jetstream_degraded: true,
          connections: 7,
          nats_connected: true,
          connz_available: true,
          total_connections: 7,
        },
      },
    ],
  }
  Object.assign(b.bridges[0].status, statusOverrides)
  return b
}

function license(status: string) {
  return {
    status,
    license_id: 'LIC-E2E-1',
    company: 'E2E Co',
    kind: 'commercial',
    tier: 'enterprise',
    max_connections: 1000,
    max_qos: 2,
    connections_local: 7,
    connections_global: 7,
    instances: 1,
    expires_at: '2026-01-01T00:00:00Z',
    grace_days: 7,
  }
}

// mockBridgeReads serves every read the fleet and detail pages issue for BRIDGE.
// Registered from generic to specific so the specific ones win (Playwright uses
// the most recently registered matching route).
async function mockBridgeReads(page: Page, envId: string, licenseStatus: string) {
  const base = `**/api/environments/${envId}/mqtt`
  const json = (body: unknown) => ({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })

  await page.route(`${base}/bridges`, (route) => route.fulfill(json(degradedBridge())))
  await page.route(`${base}/*/diag`, (route) => route.fulfill(json({ connection: { connected: true, server_name: 'nats-1', url: 'nats://127.0.0.1:4222', rtt: '1ms', reconnects: 0, in_msgs: 1, out_msgs: 1, in_bytes: 1, out_bytes: 1 } })))
  await page.route(`${base}/*/diag/config`, (route) => route.fulfill(json({ version: '1.2.0', config: {} })))
  await page.route(`${base}/*/metrics`, (route) => route.fulfill(json({ connections_active: 7, consumer_pending_messages: -1 })))
  await page.route(`${base}/*/pool`, (route) => route.fulfill(json({ size: 2, slots: [] })))
  await page.route(`${base}/*/cluster`, (route) => route.fulfill(json({ available: false, reason: 'clustering is not enabled' })))
  await page.route(`${base}/*/license`, (route) => route.fulfill(json(license(licenseStatus))))
  await page.route(`${base}/*/readyz`, (route) =>
    route.fulfill(json({ available: true, status: 'jetstream-degraded', ready: false, draining: false, jetstream_degraded: true, nats_connected: true })),
  )
}

test.describe('JetStream-degraded bridge & license severity', () => {
  test('fleet card shows the degraded state, not unreachable', async ({ page, request }) => {
    const env = await discoverEnv(request)
    await mockBridgeReads(page, env.id, 'valid')

    await page.goto('/mqtt')
    await expect(page.getByRole('heading', { name: 'MachMQTT Fleet' })).toBeVisible()
    await expect(page.getByRole('heading', { name: BRIDGE, level: 2 })).toBeVisible()

    // The distinct degraded label, and the explanation that MQTT is still serving.
    await expect(page.getByText('JS Degraded')).toBeVisible()
    await expect(page.getByText(/JetStream unavailable/)).toBeVisible()

    // The bug being fixed: a 503 readyz used to render the bridge as unreachable.
    await expect(page.getByText('Bridge admin API not reachable. Showing NATS-side data only.')).toHaveCount(0)
    // Reachable, so the detail link and the live connections link are both offered.
    await expect(page.getByRole('link', { name: 'Details' })).toBeVisible()
    await expect(page.getByRole('link', { name: /Connections \(7\)/ })).toBeVisible()
  })

  test('fleet card labels a reachable but not-ready bridge', async ({ page, request }) => {
    const env = await discoverEnv(request)
    await mockBridgeReads(page, env.id, 'valid')
    // Same reachable bridge, readyz state "not ready" (still starting / NATS down).
    await page.route(`**/api/environments/${env.id}/mqtt/bridges`, (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(degradedBridge({ jetstream_degraded: false, nats_connected: false })),
      }),
    )

    await page.goto('/mqtt')
    await expect(page.getByRole('heading', { name: BRIDGE, level: 2 })).toBeVisible()
    await expect(page.getByText('Not Ready')).toBeVisible()
    await expect(page.getByText('JS Degraded')).toHaveCount(0)
    await expect(page.getByText('Bridge admin API not reachable. Showing NATS-side data only.')).toHaveCount(0)
  })

  test('detail header labels the degraded bridge', async ({ page, request }) => {
    const env = await discoverEnv(request)
    await mockBridgeReads(page, env.id, 'valid')

    await page.goto(`/mqtt/${encodeURIComponent(BRIDGE)}/detail`)
    await expect(page.getByRole('heading', { name: BRIDGE, level: 1 })).toBeVisible()
    await expect(page.getByText('JS Degraded')).toBeVisible()
    // Degraded is not a load failure: the tabs still populate.
    await expect(page.getByText('Could not load any details for this bridge')).toHaveCount(0)
  })

  // The license status string carries the severity: tampered/expired need action
  // (danger), grace is a warning, valid is healthy, anything else stays neutral.
  const licenseCases: { status: string; tone: RegExp | null; banner: RegExp | null }[] = [
    { status: 'tampered', tone: /text-red-600/, banner: /investigate a possibly tampered binary/ },
    { status: 'expired', tone: /text-red-600/, banner: /License expired/ },
    { status: 'grace', tone: /text-amber-600/, banner: /grace period/ },
    { status: 'valid', tone: /text-green-600/, banner: null },
    { status: 'something-new', tone: null, banner: null },
  ]

  for (const c of licenseCases) {
    test(`license status "${c.status}" renders its severity`, async ({ page, request }) => {
      const env = await discoverEnv(request)
      await mockBridgeReads(page, env.id, c.status)

      await page.goto(`/mqtt/${encodeURIComponent(BRIDGE)}/detail`)
      await page.getByRole('button', { name: 'License', exact: true }).click()

      // DI renders the value with title=<value>, so this is the Status field.
      const statusValue = page.locator(`div[title="${c.status}"]`)
      await expect(statusValue).toBeVisible()
      await expect(statusValue).toHaveText(c.status)
      if (c.tone) {
        await expect(statusValue).toHaveClass(c.tone)
      } else {
        await expect(statusValue).not.toHaveClass(/text-(red|amber|green)-600/)
      }

      if (c.banner) {
        await expect(page.getByText(c.banner)).toBeVisible()
      } else {
        // No severity banner for healthy/unknown statuses.
        await expect(page.getByText(/investigate a possibly tampered binary|License expired|grace period/)).toHaveCount(0)
      }
    })
  }
})
