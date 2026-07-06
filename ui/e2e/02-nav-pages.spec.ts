import { test, expect } from '@playwright/test'

// Walk every primary sidebar page and assert it routes and renders its heading —
// a cheap guard that no page throws / white-screens after a change. Links are
// targeted by href because some labels (Overview, Connections) appear in both the
// primary nav and the MachMQTT section.
const pages = [
  { href: '/', url: /\/$/, heading: 'Servers' },
  { href: '/topology', url: /\/topology$/, heading: 'Cluster Topology' },
  { href: '/connections', url: /\/connections$/, heading: 'Connections' },
  { href: '/subscriptions', url: /\/subscriptions$/, heading: 'Subscriptions' },
  { href: '/jetstream', url: /\/jetstream$/, heading: 'JetStream' },
  { href: '/accounts', url: /\/accounts$/, heading: 'Accounts' },
]

test('sidebar navigates to each primary page and renders its heading', async ({ page }) => {
  await page.goto('/')
  for (const p of pages) {
    await page.locator(`nav a[href="${p.href}"]`).click()
    await expect(page).toHaveURL(p.url)
    await expect(page.getByRole('heading', { name: p.heading, exact: true }).first()).toBeVisible()
  }
})

test('MachMQTT section and admin pages are reachable for an admin user', async ({ page }) => {
  await page.goto('/')
  const links: Array<[string, string]> = [
    ['/mqtt', 'MachMQTT Fleet'],
    ['/mqtt/connections', 'All MQTT Connections'],
    ['/admin/clusters', 'Cluster Management'],
    ['/admin/users', 'User Management'],
  ]
  for (const [href, heading] of links) {
    await page.locator(`nav a[href="${href}"]`).click()
    await expect(page.getByRole('heading', { name: heading }).first()).toBeVisible()
  }
})
