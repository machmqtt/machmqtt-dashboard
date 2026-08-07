import { expect, test } from '@playwright/test'

async function expectAuthenticated(page: import('@playwright/test').Page, username: string) {
  await expect(page.getByTitle('Logout')).toBeVisible()
  await expect(page.getByText(username, { exact: true })).toBeVisible()
}

test('renders ordered providers and preserves the explicit local recovery route', async ({ page }) => {
  await page.goto('/login')
  await expect(page.getByRole('button', { name: 'Sign in with dex' })).toBeVisible()
  await expect(page.getByLabel('Username')).toBeVisible()
  await expect(page.getByRole('link', { name: 'Local administrator login' })).toHaveAttribute('href', '/login/local')
})

test('authenticates through real OpenLDAP before local fallback', async ({ page }) => {
  await page.goto('/login')
  await page.getByLabel('Username').fill('fry')
  await page.getByLabel('Password').fill('fry')
  await page.getByRole('button', { name: 'Sign In', exact: true }).click()
  await expectAuthenticated(page, 'fry')
})

test('authenticates through the dedicated local break-glass route', async ({ page }) => {
  await page.goto('/login/local')
  await page.getByLabel('Username').fill('local-admin')
  await page.getByLabel('Password').fill('local-password')
  await page.getByRole('button', { name: 'Sign In', exact: true }).click()
  await expectAuthenticated(page, 'local-admin')
})

test('completes a browser OIDC flow through real Dex and OpenLDAP', async ({ page }) => {
  await page.goto('/login')
  await page.getByRole('button', { name: 'Sign in with dex' }).click()
  await page.locator('input[name="login"]').fill('fry')
  await page.locator('input[name="password"]').fill('fry')
  await page.getByRole('button', { name: 'Login' }).click()
  await expectAuthenticated(page, 'fry')
})
