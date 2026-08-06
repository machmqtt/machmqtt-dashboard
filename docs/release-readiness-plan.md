# Release Readiness Remediation Plan

Baseline: `origin/dev`
Remediation branch: `fix/release-readiness-remediation`
Original remediation completed: 2026-07-31
Rebase revalidation started: 2026-08-06

This is the durable remediation ledger. An item is checked only when its code, tests, documentation, and applicable release gate pass.

## Release exit criteria

- [ ] No known reachable vulnerabilities: `govulncheck` (without JSON exit-code masking) reports zero reachable vulnerabilities and `npm audit --audit-level=moderate` reports zero vulnerabilities.
- [x] All P0/P1 findings below are resolved.
- [x] A bootstrap account cannot use privileged APIs until its one-time password is rotated.
- [ ] Go vet, pinned golangci-lint, race detection, frontend lint/type-check/build, and the Docker provider suite pass after the `dev` rebase.
- [ ] Aggregate first-party Go statement coverage is at least 95%, and every critical `internal` package is at least 95%, after the `dev` rebase.
- [x] Frontend statements, lines, and functions are at least 95%; branches are at least 90%.
- [ ] Local-only, LDAP + local, OIDC + local, and LDAP + OIDC + local pass against digest-pinned OpenLDAP and Dex after the `dev` rebase.
- [x] Indexed query-plan tests, migration fixtures, representative datasets, and release benchmarks pass.
- [x] HTTP, auth, provider, collector, database, queue, cache, WebSocket, and runtime signals are observable.
- [x] Configuration, bootstrap, recovery, retention, performance, and deployment documentation matches implemented behavior.

Current `dev`-rebase evidence:

| Gate | Result |
|---|---|
| Frontend coverage | 96.32% statements / 97.99% lines / 95.39% functions / 90.27% branches; 105 tests |
| Frontend lint / type-check / build / bundle | Pass; initial JavaScript 248.8 KiB / 300 KiB |
| Offline Go race tests | Pass for dashboard, API, auth, config, log buffer, store, and WebSocket packages |
| Rebased available-package aggregate coverage | 95.2% (full gate still awaits the uncached NATS test dependency) |
| Rebased critical package coverage | API 95.5% / auth 95.3% / config 97.8% / log buffer 100% / store 95.2% / WebSocket 98.3% |
| Release compilation | Pass for Linux amd64, macOS amd64/arm64, and Windows amd64 |
| Pending network-backed gates | Full Go suite/coverage, non-JSON `govulncheck`, npm audit, and Docker OpenLDAP/Dex matrix |

Latest fully measured gates before the `dev` rebase (must be revalidated where unchecked above):

| Gate | Result |
|---|---|
| Go aggregate coverage | 95.3% |
| Changed Go mutation efficacy | 100% (472 killed, 0 survived) |
| Changed Go mutant coverage | 98.13% |
| Critical frontend mutation score | 81.40% (394 killed) |
| `internal/api` | 96.1% |
| `internal/auth` | 95.9% |
| `internal/collector` | 95.0% |
| `internal/config` | 96.5% |
| `internal/store` | 95.2% |
| `internal/ws` | 98.6% |
| Frontend statements / lines / functions / branches | 96.97% / 98.07% / 95.78% / 90.14% |
| Frontend tests | 88 passing |
| Real-provider Chromium journeys | 4 passing |
| Initial JavaScript | 246.2 KiB / 300 KiB budget |

## Phase 0 — Security and release blockers

### SEC-001 — Go toolchain vulnerabilities (P1)

- [x] Go, CI, and container builds use Go 1.26.5.
- [x] Module metadata is reconciled.
- [x] Pinned `govulncheck` reports zero reachable vulnerabilities.
- [x] Race and real-provider suites pass on the upgraded toolchain.

### SEC-002 — Frontend dependency advisories (P1)

- [x] React Router, Vite, PostCSS, and affected transitives are on fixed versions.
- [x] The lockfile is generated and clean-installed with pinned npm 10.9.8 in CI and Docker.
- [x] `npm audit`, lint, tests, build, bundle budget, and authentication regressions pass.

### SEC-003 — Privileged bootstrap credential (P0)

- [x] Removed the known `admin/admin` credential.
- [x] Startup requires an operator-supplied one-time secret when no local admin exists.
- [x] Bootstrap secrets never appear in normal logs.
- [x] Middleware limits forced-rotation sessions to inspection, logout, and password change.
- [x] Rotation increments the session version and invalidates old sessions.
- [x] The final local administrator cannot be deleted.

### CON-001 — MQTT snapshot ownership/races (P1)

- [x] Collector snapshots and bridge slices use defensive ownership.
- [x] Sorting/naming uses request-local data.
- [x] Concurrent collection, API, metrics, and shutdown paths pass the race detector.

### API-001 — NATS connection pagination (P1)

- [x] Upstream total/offset/limit are honored and all bounded pages are fetched.
- [x] Stable server/CID/IP/port ordering is defined.
- [x] Fan-out, page size, cluster rows, memory, and request duration are bounded.
- [x] Truncation and failed-server partial metadata are returned.
- [x] Connection detail, account detail, and subscription aggregation were audited and tested.

### QA-001 — Project quality gates (P1)

- [x] Aggregate and per-critical-package Go coverage gates replace the auth-only gate.
- [x] Mutation gates prove changed Go behavior and critical frontend behavior fail under injected faults, with reports retained by CI.
- [x] Generated/vendor/dependency paths are excluded without excluding first-party production code.
- [x] API, collector, store, WebSocket, startup/shutdown, frontend unit/component, and browser tests are present.
- [x] Pinned golangci-lint configuration passes with zero findings.
- [x] Race, coverage, lint, audit, build, bundle, and provider integration failures block CI.
- [x] Real-provider tests remain a distinct CI job with digest-pinned images.

## Phase 1 — Database correctness and durability

### DB-001 — Metrics indexes/query plans (P1)

- [x] Environment/time, entity/time, cleanup, and topology indexes match supported queries.
- [x] `EXPLAIN QUERY PLAN` tests prohibit unbounded retained-row scans.
- [x] Insert cost, query latency, allocations, and representative database size are benchmarked and documented.

### DB-002 — Explicit migrations (P1)

- [x] Ordered schema versions replace ignored schema operations.
- [x] Migrations are transactional and idempotent with precise startup errors.
- [x] Fresh, every historical version, repeat startup, partial/interrupted replay, and rollback cases are tested.

### DB-003 — SQLite policy (P2)

- [x] WAL, busy timeout, bounded pool, connection lifetime, integrity, checkpoint, backup, restore, and recovery policy are defined.
- [x] Busy/locked events, pool state, transaction/query/cleanup latency, WAL bytes, and cleanup are observable.
- [x] A non-blocking data-directory lock enforces the supported single-process architecture.
- [x] Backup/restore and integrity checks are automated.

### DB-004 — Loss-aware metrics persistence (P1)

- [x] Queue overflow and stopped-writer rejection are counted and logged.
- [x] Queue depth/capacity/age, batch rows, write/query/cleanup duration, busy events, failures, and dropped/written samples are exported.
- [x] Accepted samples drain deterministically during shutdown.
- [x] Inserts are prepared per atomic transaction; benchmark evidence and the batching decision are documented.
- [x] Retention is validated/configurable and cleanup is bounded.

## Phase 2 — Logging, metrics, and health

### OBS-001 — Structured HTTP observability (P1)

- [x] Validated/generated request IDs propagate in responses and logs.
- [x] Normalized route, method, status, duration, response bytes, and bounded client class are logged.
- [x] Panic recovery is structured and returns a safe JSON error.
- [x] Sensitive-data regression tests cover passwords, cookies, OIDC values, bind secrets, and bearer tokens.
- [x] JSON error shapes are consistent and encoder failures are handled/logged without values.

### OBS-002 — Operational metrics (P1)

- [x] HTTP/in-flight/panic, auth/provider/rate-limit/OIDC-flow, collector, database/queue/WAL, cache, WebSocket, and Go runtime metrics are exported.
- [x] Labels are bounded to routes, status classes, configured providers/environments, and finite reason/endpoint classes.
- [x] Cardinality and secret-leak behavior is tested.

### OBS-003 — Health/dependency status (P1)

- [x] `/livez` is dependency-free.
- [x] `/readyz` covers initialized SQLite, workers, and shutdown state.
- [x] Detailed dependency status is administrator-only.
- [x] External identity outages and stale NATS observations degrade status without making process liveness depend on them.

## Phase 3 — Runtime performance and scalability

### PERF-001 — Collector polling (P1)

- [x] Independent endpoints and servers use bounded worker groups with poll-wide deadlines.
- [x] Endpoint failures and partial/stale snapshots are explicit and observable.
- [x] Worker shutdown is waitable and leak/race tested.
- [x] 100-server overview/topology benchmarks track latency and allocations.

### PERF-002 — API fan-out/cache (P1)

- [x] Multi-server fan-out is bounded and cancellable with partial-result semantics.
- [x] Subscription cache is server-owned, keyed by scope, size/TTL bounded, and single-flight coalesced.
- [x] Cache hit/miss/entry/eviction signals are exported.
- [x] 100,000-connection and 50,000-subscription benchmarks establish bounded worst cases.

### PERF-003 — HTTP transport/body bounds (P1)

- [x] NATS and MQTT transports are reused with explicit connect/TLS/header/overall/idle timeouts.
- [x] JSON and Prometheus bodies are size limited.
- [x] Idle transports close during shutdown.

### PERF-004 — Frontend bundle/request churn (P2)

- [x] Expensive routes are lazy-loaded.
- [x] CI enforces a 300 KiB initial JavaScript budget; current output is 246.1 KiB.
- [x] Hook dependencies are stable and polling/WebSocket/request lifecycles cancel correctly.
- [x] Timeouts, visible failure states, reconnect behavior, and retry jitter are tested.

## Phase 4 — Authentication and resilience

### AUTH-001 — Session lifecycle (P1)

- [x] Password, role, forced rotation, deletion, and disable-sensitive paths revoke existing sessions.
- [x] Tokens include issuer, audience, subject, JTI, issued/not-before/expiry, provider, and session version.
- [x] A 24-hour absolute/no-sliding-idle policy and signing-key rotation behavior are documented.
- [x] Logout’s cookie-only threat model and emergency revocation steps are documented.

### AUTH-002 — Abuse controls (P2)

- [x] Ordered external and local recovery routes intentionally use separate bounded budgets.
- [x] Limiter key count/rejections and cleanup are observable and tested.
- [x] No account lockout is used; IP-aware backoff avoids account-lockout denial of service.
- [x] Only configured trusted proxies can supply forwarding headers; spoofing is tested.

### AUTH-003 — OIDC flow deployment semantics (P2)

- [x] Single-instance operation is enforced and process-local OIDC state is documented.
- [x] State is browser-bound, one-time, ten-minute limited, and capacity bounded.
- [x] Restart loss, replay, expiry, capacity, size, and eviction behavior are tested/observable.

### CFG-001 — Strict configuration (P1)

- [x] Unknown/duplicate/unsafe values and incompatible provider/TLS/secret fields fail startup with field-specific errors.
- [x] CA content, URLs, ports, durations, roles, provider identifiers, and secret sources are validated.

### RES-001 — Explicit lifecycle ownership (P2)

- [x] HTTP, collectors, metrics writer, rate-limit cleanup, discovery, transports, and WebSockets have explicit owners.
- [x] Shutdown stops intake, drains accepted durable work, joins workers, and reports errors within a bound.
- [x] The race gate found and drove a fix for an unjoined HTTP serve goroutine.

## Phase 5 — Functional and test completeness

### TEST-001 — Real-provider scenarios (P1)

- [x] Digest-pinned OpenLDAP and Dex cover all four local/external combinations.
- [x] Protocol-faithful fixtures cover AD nested groups, LDAPS/StartTLS trust success/failure, outages, timeouts, collisions, ordering, first match, wrong passwords, and explicit local bypass.
- [x] OIDC discovery, authorization code, PKCE, nonce, browser binding, callback replay, group mapping, and unavailable-provider behavior are tested.

### TEST-002 — Database/performance regressions (P1)

- [x] Migration matrix, query plans, representative data, concurrent read/write/cleanup, and backup/restore tests run in CI/release jobs.
- [x] API, collector, connection, subscription, topology, metrics, and MQTT benchmarks track allocations/latency.
- [x] Supported limits, methodology, reference results, and retention sizing are documented in `docs/performance.md`.

### TEST-003 — Frontend and end-to-end coverage (P1)

- [x] Unit/component tests cover authentication hydration, forced change, recovery, provider errors, logout, expiry, admin authorization, polling, cancellation, WebSocket reconnect/malformed messages, and environment changes.
- [x] Actual Chromium tests run through the rendered SPA and Go backend against real OpenLDAP and Dex.
- [x] Browser journeys verify provider rendering/order, LDAP login, local recovery, and full Dex/OpenLDAP OIDC redirect/callback.

## Phase 6 — Documentation and release operations

### DOC-001 — Documentation reconciliation (P1)

- [x] Bootstrap, local recovery, provider order/outcomes, collision handling, session/logout policy, retention, sizing, health, metrics, logs, backup/restore, and incident recovery are documented.
- [x] Single-instance/OIDC callback semantics and enforcement are documented.
- [x] Compose requires an explicit bootstrap secret and no documentation advertises `admin/admin`.

### REL-001 — Repeatable release verification (P2)

- [x] Go/npm/scanners/container inputs are version or digest pinned; GitHub actions use approved major ranges.
- [x] CI retains coverage, the text-mode vulnerability report, benchmark output, release binary, and SPDX JSON SBOM.
- [x] The release candidate binary is built once and smoke-tested before artifact retention.
- [x] Fresh install, migration upgrade/replay, backup/restore, provider outage, real-provider browser flows, and graceful shutdown are exercised.

## Verification commands

```bash
make test-go-coverage
go test -race -count=1 -timeout 240s ./cmd/... ./internal/...
go vet ./cmd/... ./internal/... ./test/enterprise-auth/app
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4 run
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./cmd/... ./internal/... ./test/enterprise-auth/app
cd ui && npm audit --audit-level=moderate && npm run lint && npm run test:coverage && npm run build && npm run check:bundle
make test-enterprise-auth
make benchmark-release
```
