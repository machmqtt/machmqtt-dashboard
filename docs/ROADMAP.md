# Roadmap: Enterprise Features

Candidate features for making the dashboard an enterprise-grade tool for
monitoring and maintaining fleets of NATS servers and MachMQTT bridges.
This is a planning document, not a commitment; items are grouped by theme
and ordered roughly by expected value.

## Guiding principle: complement Prometheus, don't replace it

The dashboard is a purpose-built operational console for NATS + MachMQTT — not
a general metrics platform. Alerting, long-term metric storage, and ad-hoc
querying belong in Prometheus/Grafana (MachMQTT already exposes `/metrics`;
NATS has `prometheus-nats-exporter`). The dashboard's SQLite time series stays
what it is today: a short-horizon (default 24h) trend store that works with
zero external dependencies. Roadmap items below therefore focus on what a
dedicated console does best — fleet-level operational awareness, guided
diagnostics, and safe administrative actions — plus the integration points
that let Prometheus do its job.

## 1. Prometheus & observability-stack integration

**Why:** Enterprises already run Prometheus/Grafana/Alertmanager. The
dashboard should slot into that stack, both as a monitored component and as a
consumer of its signals.

- **Dashboard self-metrics endpoint** — expose `/metrics` (Prometheus format)
  on the dashboard itself: poll latency/failures per cluster, `$SYS` fallback
  state, push-subscriber staleness, WebSocket client counts and drops, SQLite
  write failures. Everything `GET /api/admin/health` reports today, as
  scrapeable gauges/counters. This is the single highest-leverage item: it
  makes the monitor monitorable.
- **Fleet-derived metrics for alerting** — export the dashboard's *derived*
  state (per-bridge reachability, staleness, license degraded/clamped flags,
  cluster peer count) on the same `/metrics` endpoint so Prometheus can alert
  on conditions only the dashboard computes (e.g. "bridge stopped publishing
  metrics but its NATS connection is still up").
- **Alertmanager webhook receiver (optional)** — accept Alertmanager webhook
  posts and surface active alerts as banners on the affected cluster/bridge
  pages, so operators see alert context where they act on it. The rules and
  routing stay in Prometheus.
- **Grafana deep links** — per-bridge/per-server configurable link templates
  (e.g. `grafana_url` + instance label) so every detail page can jump to the
  matching Grafana dashboard. Ship reference Grafana dashboards + example
  Prometheus alert rules for MachMQTT's metric families in `contrib/`.

## 2. RBAC, SSO, and audit

**Why:** Enterprise access control is table stakes; the dashboard already has
admin-gated destructive actions (kick/drain/reload) that deserve per-user
accountability.

- **OIDC SSO** — login via an OIDC provider (Okta, Entra, Keycloak) alongside
  local accounts; group→role mapping.
- **Per-environment roles** — viewer / operator / admin scoped per cluster,
  not just globally, so a team can operate its own environment without seeing
  others.
- **Audit log** — persist who did what (admin actions, cluster CRUD, user
  management, logins) with timestamps and source IP; viewable in the admin UI
  and exportable. MachMQTT audits its side; the dashboard should audit its
  own.
- **Scoped API tokens** — non-interactive tokens (read-only or per-environment)
  for CI checks and automation against the REST API.

## 3. Fleet & capacity management

**Why:** The dashboard already collects license and per-bridge data the
Prometheus stack doesn't interpret; turning it into fleet-level awareness is
unique value.

- **Cross-environment fleet view** — one screen aggregating every cluster and
  bridge: health rollup, version spread, license posture. Today each
  environment is viewed in isolation.
- **License utilization & headroom** — trend `connections_global` vs
  `max_connections` per license (data already fetched from `/license`), warn
  at configurable thresholds, and flag `degraded`/`capacity_clamped`/
  `peer_discrepancy` states prominently fleet-wide.
- **Version drift detection** — surface bridges/servers running different
  versions (both already collected) with a "fleet is consistent" / "3 bridges
  behind" rollup, useful during rolling upgrades.
- **Config drift detection** — diff the sanitized `/diag` config across
  bridges in the same environment and highlight divergence (auth mode, limits,
  TLS settings), which is how fleet misconfigurations are actually found.

## 4. Availability & incident timeline

**Why:** "What happened and when" is the first question in every incident
review. The collector already observes every health transition; it just
doesn't remember them.

- **State-transition history** — persist server/bridge up↔down/degraded/drained
  transitions (compact events, not metrics) and render an availability
  timeline per component and per environment.
- **Event annotations** — record notable observed events (bridge restart
  detected via `instance_id` change, license state change, NATS reconnect
  storms) on the same timeline, and overlay them on trend charts.
- **Uptime summaries** — 7/30-day availability percentages per component,
  computed from the transition history rather than a metrics database.

## 5. JetStream deep monitoring

**Why:** Aligns with the existing read-only-first JetStream goal; current
coverage is streams/consumers/usage, with KV visible only inside MQTT bridge
diagnostics.

- **KV & Object-store visibility** — buckets, sizes, TTLs, and value counts
  for general NATS accounts (the MQTT bridge diag already shows its own KV
  buckets; generalize it).
- **Consumer lag surfacing** — highlight consumers with growing pending/ack
  floors and stalled consumers at the environment level, not just inside
  MachMQTT's own gauges.
- **Quota posture** — stream/account bytes vs configured limits with
  approaching-limit warnings in the UI (alert thresholds live in Prometheus
  per §1).

## 6. Diagnostics & ops tooling

**Why:** Shrinks time-to-resolution — the dashboard already knows every
admin endpoint; packaging diagnosis workflows is cheap for it and expensive
for humans.

- **One-click diag bundle** — collect `/diag`, `/diag/nats`, `/license`,
  `/pool`, `/metrics`, and recent dashboard-side observations for a bridge (or
  a whole environment) into a downloadable archive, mirroring what
  `machmqtt-diag` gathers locally.
- **Guided health checks** — a "why is this bridge unhealthy?" panel that
  walks the known failure modes (NATS link down, JetStream degraded, license
  clamped, pool slot disconnected, draining) and states which one is active,
  instead of making the operator scan six tabs.
- **Maintenance mode** — mark a component as under maintenance so its
  known-bad state is visually distinguished from a real incident (and excluded
  from availability stats in §4).

## 7. Dashboard hardening & HA

**Why:** A monitoring console must be more available than what it monitors.

- **Pluggable store** — optional PostgreSQL backend behind the existing store
  interface so multiple dashboard replicas can share state; SQLite remains the
  zero-dependency default.
- **Stateless-ish replicas** — with a shared store, run N replicas behind a
  load balancer; collectors coordinate via a simple leader lease so clusters
  aren't double-polled.
- **Config & data backup** — export/import of dashboard configuration
  (clusters, users, settings) for disaster recovery and environment
  promotion; document a backup story for the data directory.
- **Declarative config (GitOps)** — extend the existing idempotent
  `environments:` seeding into full declarative management (users, tokens,
  link templates), so the dashboard can be rebuilt from a config file in a
  repo.

## Suggested sequencing

| Phase | Items | Rationale |
|---|---|---|
| 1.1 | §1 self-metrics + fleet-derived metrics, §2 audit log | Highest leverage, no schema risk, unlocks Prometheus alerting on day one |
| 1.2 | §3 fleet view + license/version drift, §4 transition history | Builds on data already collected |
| 1.3 | §2 OIDC + per-env roles + API tokens, §6 diag bundle + guided checks | Enterprise access + ops workflows |
| 2.0 | §7 Postgres/HA, §5 JetStream deep monitoring, §6 maintenance mode | Larger architectural changes |
