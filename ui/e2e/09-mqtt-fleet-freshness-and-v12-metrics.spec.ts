import { test, expect, type Page } from '@playwright/test'
import { discoverEnv } from './helpers'

// Fleet freshness states (probing, stale) and the MachMQTT v1.2 metric groups
// cannot be produced on demand from a live broker, so these specs intercept the
// bridge reads in the browser. The Go server, embedded bundle, routing and the
// authenticated session stay real; no broker is needed.

const BRIDGE = 'e2e-v12-broker'

function fleetBridge(overrides: Record<string, unknown> = {}, statusOverrides: Record<string, unknown> = {}) {
  const b = {
    ip: '',
    server_id: 'NV12',
    server_name: 'nats-1',
    configured_name: BRIDGE,
    admin_url: '',
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
    last_seen: new Date().toISOString(),
    status: {
      name: BRIDGE,
      url: '',
      ready: true,
      draining: false,
      jetstream_degraded: false,
      connections: 7,
      nats_connected: true,
      connz_available: false,
      total_connections: 7,
      ...statusOverrides,
    },
  }
  return Object.assign(b, overrides)
}

function fulfillJSON(body: unknown) {
  return { status: 200, contentType: 'application/json', body: JSON.stringify(body) }
}

async function mockFleet(page: Page, envId: string, bridges: unknown[]) {
  await page.route(`**/api/environments/${envId}/mqtt/bridges`, (route) => route.fulfill(fulfillJSON({ bridges })))
}

// A metrics payload exercising the v1.2 additions: distinct values so an
// assertion can only pass when its exact field rendered.
const V12_METRICS = {
  connections_active: 7,
  connections_max: 41,
  rejected_mem_budget: 17,
  hook_vetoes: 3,
  hook_panics: 1,
  sys_tree_published: 5000,
  sys_publish_blocked: 9,
  qos2_sync_persist_failed: 5,
  will_verify_failures: 2,
  subscribe_flush_failures: 6,
  session_persist_panics: 4,
  cluster_lease_revision_regressions: 1,
  consumer_deleted_under_consume: 13,
  subscribe_consumer_failures: 16,
  subscribe_consumer_retries: 17,
  jetstream_api_errors: 18,
  jetstream_api_total: 19,
  jetstream_health_probe_failures: 20,
  stream_ensure_retries: 21,
  stream_ensure_stalls: 22,
  nats_connected: 1,
  jetstream_degraded: 1,
  consumers_awaiting_reattach: 23,
  reattach_sweep_duration_ms: 24,
  shared_consumer_recreated: 14,
  legacy_named_consumers: 15,
  cluster_heartbeat_publish_failures: 8,
  suback_rejected_by_reason: { '0x87': 4 },
  consumer_pending_messages: -1,
  reactor: {
    task_queue_depth: 11,
    task_queue_depth_max: 32,
    loop_panics: 0,
    read_continuations: 123,
    write_backpressure: 45,
    feed_write_overflows: 0,
    loop_deaths: 0,
  },
  pool: { size: 2, buffered_bytes: 2048, buffered_bytes_max: 8192, slots: [] },
}

async function mockDetailReads(page: Page, envId: string, metrics: Record<string, unknown>) {
  const base = `**/api/environments/${envId}/mqtt`
  await mockFleet(page, envId, [fleetBridge({ reachable: true, admin_url: 'http://127.0.0.1:18082' })])
  await page.route(`${base}/*/diag`, (route) => route.fulfill(fulfillJSON({ connection: { connected: true, server_name: 'nats-1', url: 'nats://127.0.0.1:4222', rtt: '1ms', reconnects: 0, in_msgs: 1, out_msgs: 1, in_bytes: 1, out_bytes: 1 } })))
  await page.route(`${base}/*/diag/config`, (route) => route.fulfill(fulfillJSON({ version: '1.2.0', config: {} })))
  await page.route(`${base}/*/metrics`, (route) => route.fulfill(fulfillJSON(metrics)))
  await page.route(`${base}/*/pool`, (route) => route.fulfill(fulfillJSON({ size: 2, slots: [] })))
  await page.route(`${base}/*/cluster`, (route) => route.fulfill(fulfillJSON({ available: false, reason: 'clustering is not enabled' })))
  await page.route(`${base}/*/license`, (route) => route.fulfill(fulfillJSON({ status: 'valid', tier: 'enterprise', max_connections: 0 })))
  await page.route(`${base}/*/readyz`, (route) => route.fulfill(fulfillJSON({ available: true, status: 'ready', ready: true, draining: false, jetstream_degraded: false, nats_connected: true })))
}

test.describe('fleet freshness states', () => {
  test('a probing configured bridge shows a neutral pill, not an error', async ({ page, request }) => {
    const env = await discoverEnv(request)
    // Mirrors the backend's probePendingReason for a configured bridge whose
    // first background probe has not answered yet.
    await mockFleet(page, env.id, [
      fleetBridge(
        { reachable: false, admin_url: 'http://127.0.0.1:18099', last_seen: undefined },
        { ready: false, nats_connected: false, error: 'probing the bridge admin API' },
      ),
    ])

    await page.goto('/mqtt')
    await expect(page.getByRole('heading', { name: BRIDGE, level: 2 })).toBeVisible()
    await expect(page.getByText('Probing…')).toBeVisible()
    // Neither the red error banner nor the unreachable banner while probing.
    await expect(page.getByText('probing the bridge admin API', { exact: true })).toHaveCount(0)
    await expect(page.getByText('Bridge admin API not reachable. Showing NATS-side data only.')).toHaveCount(0)
  })

  test('a stale bridge is flagged instead of presented as live', async ({ page, request }) => {
    const env = await discoverEnv(request)
    await mockFleet(page, env.id, [
      fleetBridge({ last_seen: new Date(Date.now() - 5 * 60 * 1000).toISOString() }),
    ])

    await page.goto('/mqtt')
    await expect(page.getByRole('heading', { name: BRIDGE, level: 2 })).toBeVisible()
    await expect(page.getByText(/last seen \d+s ago/)).toBeVisible()
  })

  test('a fresh bridge carries no staleness hint', async ({ page, request }) => {
    const env = await discoverEnv(request)
    await mockFleet(page, env.id, [fleetBridge()])

    await page.goto('/mqtt')
    await expect(page.getByRole('heading', { name: BRIDGE, level: 2 })).toBeVisible()
    await expect(page.getByText(/last seen \d+s ago/)).toHaveCount(0)
    await expect(page.getByText('Probing…')).toHaveCount(0)
  })
})

test.describe('v1.2 metric groups on the detail page', () => {
  test('new counters render their values', async ({ page, request }) => {
    const env = await discoverEnv(request)
    await mockDetailReads(page, env.id, V12_METRICS)

    await page.goto(`/mqtt/${encodeURIComponent(BRIDGE)}/detail`)
    await page.getByRole('button', { name: 'Metrics', exact: true }).click()

    // One representative row per new group; DI renders title=<value>.
    const row = (label: string, value: string) =>
      expect(
        page.locator('div', { has: page.getByText(label, { exact: true }) }).locator(`div[title="${value}"]`).first(),
      ).toBeVisible()

    await row('Peak Active', '41')
    await row('Memory Budget', '17')
    await row('Hook Vetoes', '3')
    await row('$SYS Publishes Blocked', '9')
    await row('QoS 2 Sync-Persist Failed', '5')
    await row('Verify Failures', '2')
    await row('Subscribe Flush Failures', '6')
    await row('Persist Failed: Panic', '4')
    await row('Lease Revision Regressions', '1')

    // Consumer-lifecycle counters the broker added after the initial v1.2 cut.
    await row('Deleted Under Consume', '13')
    await row('Shared Consumers Rebuilt', '14')
    await row('Legacy-Named Consumers', '15')

    // Consumer-create and JetStream-account health.
    await row('Consumer-Create Failures', '16')
    await row('Account API Errors', '18')
    await row('Stream Ensure Stalls', '22')

    // The 0/1 state gauges must read as words, never as "1".
    await row('NATS Socket', 'Connected')
    await row('JetStream', 'Degraded')
    await row('Consumers Awaiting Re-Attach', '23')
    await row('Last Re-Attach Sweep', '24 ms')

    // SUBACK reason-code map renders as its own section.
    await expect(page.getByText('SUBACK Rejections by Reason Code')).toBeVisible()
    await row('0x87', '4')

    // Reactor group present with its values.
    await expect(page.getByText('I/O Reactor')).toBeVisible()
    await row('Read Continuations', '123')
    await row('Write Backpressure', '45')
  })

  test('the reactor group is hidden when the broker does not report it', async ({ page, request }) => {
    const env = await discoverEnv(request)
    const { reactor: _reactor, ...withoutReactor } = V12_METRICS
    await mockDetailReads(page, env.id, withoutReactor)

    await page.goto(`/mqtt/${encodeURIComponent(BRIDGE)}/detail`)
    await page.getByRole('button', { name: 'Metrics', exact: true }).click()

    await expect(page.getByText('Peak Active')).toBeVisible()
    await expect(page.getByText('I/O Reactor')).toHaveCount(0)
    // No uncurated payload → no section.
    await expect(page.getByText('Uncurated Metrics')).toHaveCount(0)
  })

  test('uncurated metrics from a newer broker render raw with help text', async ({ page, request }) => {
    const env = await discoverEnv(request)
    await mockDetailReads(page, env.id, {
      ...V12_METRICS,
      uncurated: { machmqtt_future_widget_total: 12345, 'machmqtt_future_by_kind_total{kind="a"}': 7 },
      uncurated_help: { machmqtt_future_widget_total: 'Widgets processed by a feature this dashboard predates.' },
    })

    await page.goto(`/mqtt/${encodeURIComponent(BRIDGE)}/detail`)
    await page.getByRole('button', { name: 'Metrics', exact: true }).click()

    await expect(page.getByText('Uncurated Metrics')).toBeVisible()
    await expect(page.getByText('machmqtt_future_widget_total', { exact: true })).toBeVisible()
    await expect(page.locator('div[title="12.3K"]')).toBeVisible()
    await expect(page.getByText('machmqtt_future_by_kind_total{kind="a"}')).toBeVisible()
    // The broker's HELP text rides as the label's tooltip.
    await expect(
      page.locator('div[title="Widgets processed by a feature this dashboard predates."]'),
    ).toBeVisible()
  })
})
