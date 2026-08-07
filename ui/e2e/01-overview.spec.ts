import { test, expect } from '@playwright/test'

test.describe('Overview', () => {
  test('loads with the server table and at least one server row', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Servers' })).toBeVisible()
    // At least one discovered server.
    await expect(page.locator('table tbody tr').first()).toBeVisible()
  })

  test('long server name is truncated with a full-name tooltip (overflow fix)', async ({ page }) => {
    await page.goto('/')
    // The Name cell is a link with the `truncate` class so a long NKEY-style
    // name can't overflow into adjacent columns; its title carries the full name.
    const nameLink = page.locator('table tbody tr').first().locator('td').first().locator('a[title]')
    await expect(nameLink).toBeVisible()
    await expect(nameLink).toHaveClass(/truncate/)
    const title = await nameLink.getAttribute('title')
    expect(title?.length, 'tooltip should carry the full server name').toBeGreaterThan(0)
  })

  test('clicking a server name opens its detail page', async ({ page }) => {
    await page.goto('/')
    const nameLink = page.locator('table tbody tr').first().locator('td').first().locator('a[href^="/servers/"]')
    await expect(nameLink).toBeVisible()
    await nameLink.click()
    await expect(page).toHaveURL(/\/servers\/.+/)
    // The detail page rendered (not "Server not found"): the Server ID field shows.
    await expect(page.getByText('Server ID', { exact: true })).toBeVisible()
  })
})
