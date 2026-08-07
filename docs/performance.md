# Performance and capacity

The dashboard bounds expensive work rather than accepting unlimited upstream results. Connection aggregation fetches at most 50,000 rows per NATS server, keeps at most 100,000 cluster rows, uses four concurrent upstream requests, and has a 20-second request deadline. Subscription detail is capped at 50,000 rows and cached per environment for 15 seconds in at most 50 entries with single-flight miss coalescing. Collector endpoint fan-out is bounded to eight workers; MQTT discovery uses four.

These are safety ceilings, not latency promises. The supported production target is 100 NATS servers per dashboard environment, a 100,000-connection aggregate view, and a 50,000-row subscription view. Deployments near a ceiling must run the release benchmarks and a representative staging load test with their real monitoring latency, accounts, subscriptions, and TLS configuration.

## Repeatable baselines

Run:

```bash
make benchmark-release
```

The suite reports allocations and latency for 1- and 100-server metric writes, a 10,000-point indexed metrics query, 100-server overview/topology construction, 100,000-row connection merge/sort, subscription-cache copies, MQTT Prometheus parsing, and an authenticated API request. CI retains the output as release evidence. Compare results on the same machine and Go version; investigate a latency or allocation increase above 20% before release.

Query-plan tests use representative retained datasets and require `(env, ts)` or entity-specific time-window indexes rather than table scans. Migration tests exercise every historical schema version and replay an applied-but-unrecorded migration. The race suite combines concurrent readers, writers, and cleanup.

Reference run on 2026-07-31 using an Apple M3 Pro, Darwin/arm64, Go 1.26.5, and a 100 ms benchmark window:

| Workload | Observed time/op | Observed allocation/op |
|---|---:|---:|
| 100-server metric sample | 8.1 ms | 92 KB |
| 10,000-point metrics query, 60-second buckets | 6.3 ms | 153 KB |
| 100-server overview and topology | 0.032 ms | 80 KB |
| 100,000-connection merge and stable sort | 44 ms | 110 MB |
| 50,000-subscription cache copy | 0.51 ms | 7.2 MB |
| MQTT metric parsing | 0.018 ms | 11 KB |
| Authenticated environments API | 0.022 ms | 15 KB |

These figures are comparison baselines, not service-level objectives. The connection cap intentionally bounds a worst-case in-memory operation; use pagination below the cap for routine access.

## Retention sizing

Each poll writes one environment row, one row per NATS server, and one row per discovered MQTT bridge. Estimate raw row count as:

```text
(retention / poll_interval) * (1 + servers + bridges)
```

SQLite indexes, WAL activity, variable-length identifiers, and topology/identity data add overhead, so measure an actual staging database and provision at least twice its observed steady-state size. Monitor `nats_dashboard_database_wal_bytes`, queue depth/age, write duration, commit failures, cleanup duration, and database busy events. Retention cleanup is bounded; normal checkpoints may cause WAL size to rise and fall.

Insert statements are prepared once per transaction. One poll is committed atomically so a sample is either complete or accounted as failed. The write benchmark records the cost of that decision; cross-transaction prepared-statement reuse is not currently justified because SQLite transaction-bound statements and batching already keep work bounded.
