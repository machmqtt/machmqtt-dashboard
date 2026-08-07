import { type APIRequestContext, expect } from '@playwright/test'

// Discovered identifiers are read from the live API at runtime — never hardcode
// the env id or bridge name, which change on every fresh stack.

export interface Env {
  id: string
  name: string
}

export async function discoverEnv(request: APIRequestContext): Promise<Env> {
  const res = await request.get('/api/environments')
  expect(res.ok(), 'GET /api/environments should succeed').toBeTruthy()
  const envs = (await res.json()).environments as Array<{ id: string; name: string }>
  expect(envs.length, 'at least one environment must be configured').toBeGreaterThan(0)
  const wanted = process.env.E2E_ENV_NAME || 'local-dev'
  const env = envs.find((e) => e.name === wanted) ?? envs[0]
  return { id: env.id, name: env.name }
}

export interface Bridge {
  name: string
  adminUrl: string
  count: number
}

export async function discoverBridge(request: APIRequestContext, envId: string): Promise<Bridge> {
  const res = await request.get(`/api/environments/${envId}/mqtt/bridges`)
  expect(res.ok(), 'GET mqtt/bridges should succeed').toBeTruthy()
  const bridges = ((await res.json()).bridges ?? []) as Array<{
    configured_name?: string
    server_name?: string
    ip?: string
    admin_url?: string
  }>
  expect(bridges.length, 'at least one MQTT bridge must be discovered').toBeGreaterThan(0)
  const b = bridges[0]
  return {
    name: b.configured_name || b.server_name || b.ip || '',
    adminUrl: b.admin_url || '',
    count: bridges.length,
  }
}
