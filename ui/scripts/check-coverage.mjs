import { readFileSync } from 'node:fs'

const minimums = { statements: 95, lines: 95, functions: 95, branches: 90 }
const summary = JSON.parse(readFileSync(new URL('../coverage/coverage-summary.json', import.meta.url), 'utf8')).total
const failures = Object.entries(minimums)
  .filter(([metric, minimum]) => summary[metric].pct < minimum)
  .map(([metric, minimum]) => `${metric}: ${summary[metric].pct}% (minimum ${minimum}%)`)

if (failures.length > 0) {
  console.error(`Coverage gate failed:\n${failures.join('\n')}`)
  process.exit(1)
}

console.log(`Coverage gate passed: ${Object.keys(minimums).map((metric) => `${metric} ${summary[metric].pct}%`).join(', ')}`)
