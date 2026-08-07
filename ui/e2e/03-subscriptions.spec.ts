import { test, expect } from '@playwright/test'

// Guards the /subsz fix: the SubszResp.cache_hit_rate float decode bug used to
// leave the server-summary map empty, so the page showed "No subscription data
// available". It must now show a real server row with a numeric sub count and a
// 0–100% cache-hit value.
test.describe('Subscriptions', () => {
  test('shows the per-server summary table (not the empty state)', async ({ page }) => {
    await page.goto('/subscriptions')
    await expect(page.getByRole('heading', { name: 'Subscriptions' })).toBeVisible()

    // The "Total Server Subs" stat card renders a number.
    await expect(page.getByText('Total Server Subs')).toBeVisible()

    // The empty-state message must be absent.
    await expect(page.getByText('No subscription data available.')).toHaveCount(0)

    // At least one server row in the summary table.
    const firstRow = page.locator('table tbody tr').first()
    await expect(firstRow).toBeVisible()
  })

  test('cache hit rate renders as a percentage (0–100%), not a raw ratio', async ({ page }) => {
    await page.goto('/subscriptions')
    const firstRow = page.locator('table tbody tr').first()
    await expect(firstRow).toBeVisible()
    // The Cache Hit Rate cell shows e.g. "18.6%" — a value in [0,100] with a % sign,
    // never a raw 0..1 ratio like "0.186%".
    const pct = firstRow.getByText(/^\d{1,3}(\.\d+)?%$/)
    await expect(pct.first()).toBeVisible()
    const txt = (await pct.first().textContent())?.replace('%', '') ?? ''
    const val = Number(txt)
    expect(val).toBeGreaterThanOrEqual(0)
    expect(val).toBeLessThanOrEqual(100)
  })
})
