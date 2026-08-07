import { readFile, stat } from 'node:fs/promises'
import { join } from 'node:path'

const dist = new URL('../../internal/api/dist/', import.meta.url)
const manifest = JSON.parse(await readFile(new URL('.vite/manifest.json', dist), 'utf8'))
const entries = Object.values(manifest).filter((chunk) => chunk.isEntry)
const visited = new Set()

async function sizeOf(chunk) {
  if (!chunk || visited.has(chunk.file)) return 0
  visited.add(chunk.file)
  let bytes = (await stat(join(dist.pathname, chunk.file))).size
  for (const imported of chunk.imports || []) bytes += await sizeOf(manifest[imported])
  return bytes
}

let bytes = 0
for (const entry of entries) bytes += await sizeOf(entry)
const budget = 300 * 1024
console.log(`Initial JavaScript: ${(bytes / 1024).toFixed(1)} KiB (budget ${(budget / 1024).toFixed(0)} KiB)`)
if (bytes > budget) process.exit(1)
