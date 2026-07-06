import { test, expect, type APIRequestContext } from '@playwright/test'

// Full CRUD against the Cluster Management page using a throwaway cluster. The
// cluster's monitoring URL is intentionally unreachable — creation only persists
// config, so the collector failing to poll it is harmless and it never pollutes
// the real env's data. Cleanup is guaranteed in finally + afterAll (the API is
// the source of truth) so a UI failure can't leave the throwaway behind.
const NAME = 'e2e-throwaway-cluster'
const FAKE_URL = 'http://127.0.0.1:59999'

async function deleteByName(request: APIRequestContext, name: string): Promise<void> {
  const res = await request.get('/api/admin/clusters')
  if (!res.ok()) return
  const clusters = ((await res.json()).clusters ?? []) as Array<{ id: string; name: string }>
  for (const c of clusters.filter((c) => c.name === name)) {
    await request.delete(`/api/admin/clusters/${c.id}`)
  }
}

test.describe.serial('Cluster Management CRUD', () => {
  test.beforeAll(async ({ request }) => {
    await deleteByName(request, NAME) // clear any leftover from a prior aborted run
  })
  test.afterAll(async ({ request }) => {
    await deleteByName(request, NAME)
  })

  test('create, edit, and delete a throwaway cluster', async ({ page, request }) => {
    try {
      await page.goto('/admin/clusters')
      await expect(page.getByRole('heading', { name: 'Cluster Management' })).toBeVisible()

      // ── Create ──
      await page.getByRole('button', { name: 'Add Cluster' }).click()
      await expect(page.getByRole('heading', { name: 'New Cluster' })).toBeVisible()
      await page.getByPlaceholder('production').fill(NAME)
      await page.getByPlaceholder('http://nats-1:8222').first().fill(FAKE_URL)
      await page.getByRole('button', { name: 'Create Cluster' }).click()

      const row = page.locator('tr', { hasText: NAME })
      await expect(row).toBeVisible()
      await expect(row).toContainText(FAKE_URL)

      // ── Edit ── (open modal, rename, save)
      const renamed = NAME + '-edited'
      await row.getByTitle('Edit cluster').click()
      const modalHeading = page.getByRole('heading', { name: /Edit Cluster:/ })
      await expect(modalHeading).toBeVisible()
      await page.getByPlaceholder('production').fill(renamed)
      await page.getByRole('button', { name: 'Save Changes' }).click()
      await expect(page.locator('tr', { hasText: renamed })).toBeVisible()

      // ── Delete ── (native confirm dialog → accept)
      page.once('dialog', (d) => d.accept())
      await page.locator('tr', { hasText: renamed }).getByTitle('Delete cluster').click()
      await expect(page.locator('tr', { hasText: renamed })).toHaveCount(0)
    } finally {
      await deleteByName(request, NAME)
      await deleteByName(request, NAME + '-edited')
    }
  })
})
