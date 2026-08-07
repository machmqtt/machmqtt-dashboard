import { test, expect } from '@playwright/test'

// Regression guard for the "data refresh reloads the whole page / loses scroll"
// bug: the MachMQTT Fleet page polls every ~3s and used to swap its entire body
// for a skeleton on each poll, collapsing the page and resetting scroll to the
// top. It must now refresh the bridge cards in place.
test('Fleet refreshes in place without resetting scroll', async ({ page }) => {
  // A short viewport guarantees the page overflows and is scrollable.
  await page.setViewportSize({ width: 1000, height: 500 })
  await page.goto('/mqtt')
  await expect(page.getByRole('heading', { name: 'MachMQTT Fleet' })).toBeVisible()
  const details = page.getByRole('link', { name: 'Details' }).first()
  await expect(details).toBeVisible()

  // Scroll to the bottom and confirm the page is actually scrollable.
  await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight))
  const before = await page.evaluate(() => window.scrollY)
  expect(before, 'fleet page should be tall enough to scroll').toBeGreaterThan(0)

  // Wait across more than one 3s refresh cycle. A skeleton swap would collapse
  // the page and clamp scrollY toward 0; an in-place refresh preserves it.
  await page.waitForTimeout(4000)

  const after = await page.evaluate(() => window.scrollY)
  expect(after, 'scroll position should survive a background refresh').toBeGreaterThan(before / 2)
  // The fleet card (not a skeleton) is still mounted.
  await expect(details).toBeVisible()
})
