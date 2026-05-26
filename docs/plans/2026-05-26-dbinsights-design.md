# Database Insights — Design

**Status:** Design approved, not yet implemented
**Date:** 2026-05-26

## Motivation

`launch-util` already does two things well: backs up customer databases (`database/`, `model/`) and reports host system stats (`psutil/`, `pulse`) back to the SaaS via webhook. This design adds a third capability: **database-level observability** — the kind of insights that AWS RDS Performance Insights surfaces (load, top SQL, wait events, schema health), but for any customer database the agent can reach, not just RDS.

The goal is RDS-Insights-class dashboards in the SaaS UI, with near-real-time freshness, that grow from the first customer to thousands without re-architecture.

## Scope

### Insight types (all four in scope)

1. **Health & load metrics** — connections, QPS, replication lag, cache hit ratio, table/index sizes.
2. **Top queries** — slowest and most frequent queries by total time, calls, rows examined.
3. **Wait events** — what the DB is waiting on (IO, locks, latches).
4. **Schema / data quality** — bloat, unused indexes, dead tuples, table growth.

### Engines

| Engine | Phase 1 | Phase 2 | Dropped |
|---|---|---|---|
| MySQL / MariaDB | ✅ | | |
| PostgreSQL | ✅ | | |
| MongoDB | | ✅ | |
| MSSQL | | | ❌ — not supported by the SaaS |
| Redis | | | ❌ — caches, low signal value |
| SQLite | | | ❌ — nothing meaningful to collect |

Phase 1 = the two engines where the collector pays for itself in customer value and where the implementation pattern (cumulative stats tables + delta-based shipping) is identical.

### Freshness & retention

- **Cadence:** near real-time (10-second sample, 10-second ship)
- **Retention:** 7 days at raw 10s resolution
- ClickHouse `TTL ts + INTERVAL 7 DAY` handles deletion automatically.

## Architecture

The agent grows a third subsystem alongside `database/` (backups) and `psutil/` (system pulse). Call it `dbinsights/`. It runs as a long-lived goroutine inside the existing `launch-agent run` daemon, driven by `scheduler/`.

Per configured database, the agent maintains **one persistent connection** (separate from the backup connection so dumps don't interfere). Each engine implements a small `Collector` interface. Samples land in an in-memory ring buffer per DB. A shipper goroutine flushes batches to the SaaS via the existing `notifier/webhook.go` (new `event: "dbinsights"`), gzipped, every 10 seconds.

If the webhook is unreachable, samples stay in the ring buffer up to 5 minutes (configurable). Beyond that, oldest samples drop and a `dropped_samples` counter is included in the next payload so the SaaS knows there was a gap.

```
┌──────────────────────────── agent (per-host) ────────────────────────────┐
│                                                                          │
│  scheduler ──► dbinsights/                                               │
│                  ├── collector(mysql)   ──┐                              │
│                  ├── collector(postgres) ─┼─► ring buffer ─► shipper ─┐  │
│                  └── collector(...)      ─┘                           │  │
│                                                                       │  │
└───────────────────────────────────────────────────────────────────────┼──┘
                                                                        │
                                  webhook (gzip + HMAC, every 10s)      │
                                                                        ▼
┌─────────────────────────────── SaaS ─────────────────────────────────────┐
│  POST /ingest/dbinsights ─► verify HMAC ─► dedup ─► ClickHouse async_insert │
│                                                                          │
│  Dashboard ────► ClickHouse (3 query shapes cover ~90% of panels)        │
└──────────────────────────────────────────────────────────────────────────┘
```

## Per-engine collectors

### Interface

```go
type Collector interface {
    Engine() string
    Capabilities() Caps                                       // probed on startup
    CollectHealth(ctx) (HealthSample, error)                  // every 10s
    CollectTopQueries(ctx, prev *Snapshot) (QSample, error)   // every 30s, delta-based
    CollectWaits(ctx) (WaitsSample, error)                    // every 10s
    CollectSchema(ctx) (SchemaSample, error)                  // every 1h
}
```

On startup, the collector **probes** the DB once and reports a `Capabilities` blob in the first webhook (e.g. `{health:true, top_queries:false, reason:"performance_schema disabled"}`). The dashboard greys out panels the agent can't fill — never silent failure.

### What each engine exposes

| Engine | Health | Top queries | Waits | Schema |
|---|---|---|---|---|
| **MySQL / MariaDB** | `SHOW GLOBAL STATUS`, `INNODB STATUS`, `SHOW REPLICA STATUS` | `performance_schema.events_statements_summary_by_digest` | `events_waits_summary_global_by_event_name` | `sys.schema_index_statistics`, `information_schema.tables` |
| **PostgreSQL** | `pg_stat_database`, `pg_stat_replication` | `pg_stat_statements` *(extension)* | `pg_stat_activity.wait_event` | `pg_stat_user_tables` (dead tuples), `pg_stat_user_indexes` (idx_scan=0) |
| **MongoDB** *(p2)* | `db.serverStatus()`, `rs.status()` | `system.profile` *(needs profiler ≥ L1)* | wiredTiger cache + currentOp | `$indexStats`, `collection.stats()` |

### Two patterns that matter

**1. Delta-based collection for top queries.** All the digest/statement tables (`performance_schema.events_statements_summary_by_digest`, `pg_stat_statements`) are **cumulative since server start**. The collector keeps the previous snapshot in memory and ships only the diff per minute. **Never** call `TRUNCATE`/`pg_stat_statements_reset` — other tools on the customer's DB may rely on those counters.

**2. Query fingerprinting agent-side.** Hash the normalized SQL (`SELECT * FROM users WHERE id = ?`) to a `uint64` on the agent. Ship `(fingerprint, sql_text)` once the first time you see it, then ship only `fingerprint` on every subsequent sample. This is the optimization that makes ClickHouse storage of top-queries actually cheap.

### Connection & permissions

One **dedicated read-only user per database**, distinct from the backup user. Ship a per-engine `setup.sql` template:

```sql
-- MySQL
CREATE USER 'launch_insights'@'%' IDENTIFIED BY '...';
GRANT SELECT, PROCESS, REPLICATION CLIENT ON *.* TO 'launch_insights'@'%';

-- PostgreSQL
CREATE USER launch_insights WITH PASSWORD '...';
GRANT pg_monitor TO launch_insights;   -- pg10+, covers everything cleanly
```

Persistent connection per DB per collector (don't pool — keep one warm, reconnect on failure with backoff). Hard 2-second timeout per collection cycle; skip a cycle rather than queue up if the DB is slow.

### Customer-facing performance overhead

- **MySQL** with `performance_schema` defaults: ~1–3% CPU. **Already on by default in 5.7+.**
- **Postgres** with `pg_stat_statements`: ~1% overhead, but requires `shared_preload_libraries` + **restart** to enable. Document this loudly.
- **Mongo profiler at level 1** (slow ops only, threshold 100ms): ~2–5%. **Off by default.** Don't auto-enable — surface as "to unlock top-query insights, run X."
- **Agent itself:** one TCP conn + one query every 10 s = negligible (<0.1%).

The agent never enables anything on the customer's DB — only *reads* what's already exposed.

## Wire format & ingest path

### Webhook payload

One event type, batched. Agent posts every 10 s:

```json
{
  "event": "dbinsights",
  "agent_id": "agt_8f3...",
  "sent_at": "2026-05-26T14:03:20Z",
  "capabilities": {
    "db_42": {"health": true, "top_queries": true, "waits": true}
  },
  "fingerprints": [
    {"db_id": 42, "fp": 17923847, "sql": "SELECT * FROM users WHERE id = ?"}
  ],
  "samples": {
    "health":  [{"db_id":42,"ts":"...","metric":"qps","value":124.0}],
    "queries": [{"db_id":42,"ts":"...","fp":17923847,"calls":12,"total_ms":340.2,"rows":12}],
    "waits":   [{"db_id":42,"ts":"...","event":"io/file/innodb/data","total_ms":42.1}],
    "schema":  [{"db_id":42,"ts":"...","table":"users","rows":1.2e6,"bytes":3.4e8,"dead_pct":2.1}]
  },
  "dropped_samples": 0
}
```

Transport rules:
- **Gzip** the body (`Content-Encoding: gzip`).
- **HMAC-sign** with the agent's existing shared secret (same pattern as backup webhooks).
- **Idempotency key** header `X-Idempotency-Key: <agent_id>:<batch_seq>` — required because of retries.
- `capabilities` sent only on change; `fingerprints` only for unseen ones.

### Ingest endpoint flow

```
POST /ingest/dbinsights
  1. Verify HMAC                          → reject 401
  2. Check idempotency key (Redis SETNX)  → if seen, return 200 noop
  3. Decompress + JSON-decode
  4. Upsert fingerprints (ReplacingMergeTree)
  5. INSERT samples with async_insert=1, wait_for_async_insert=0
  6. Mark idempotency key (TTL 1h)
  7. Return 200 {accepted: N, server_ts: ...}
```

The handler **never blocks on ClickHouse acknowledging the insert**. `async_insert=1, wait_for_async_insert=0` means ClickHouse buffers and returns immediately. Trades per-request durability for throughput — acceptable because the agent retries on any non-2xx and the idempotency key dedupes.

### Backpressure & retries

1. **SaaS down/slow.** Agent ring buffer holds up to 5 min (configurable). On recovery the agent sends backfill batches (multiple minutes in one payload). Ingest accepts batches up to ~10 MB compressed.
2. **ClickHouse down.** Ingest returns 503 → agent retries with exponential backoff. If CH is down for >5 min the oldest samples are lost from agent ring buffers. That's the v1 trade-off for not having an intermediate queue. Add a Redis/SQS queue in v2 only if this bites.

### Rate limits

Per-agent: ~1 request per 5 s with bursts of 10. Stops a runaway/malicious agent from flooding ClickHouse.

## Storage — ClickHouse from day one

### Why ClickHouse-first (vs MySQL or Mongo)

- ClickHouse is the cheapest storage at scale for this exact workload (Plausible, PostHog, Cloudflare, Sentry all use it for the same shape of data).
- 20–50× compression on metrics with `LowCardinality`, `Gorilla`, `DoubleDelta` codecs.
- Native TTL → no rollup cron.
- Materialized views auto-maintain rollups (raw 10s → 1m → 1h) from a single insert.
- Serves "live" reads within seconds of insert → **no separate Redis hot tier needed**.
- Migrating time-series data later is painful; introduce CH before customers depend on dashboards.

MySQL stays as the source of truth for users, orgs, billing, agent configs. CH only holds insights.

### Schema (shape, not final)

```sql
-- raw samples, 7-day TTL
CREATE TABLE db_health_samples (
    org_id      UInt32,
    db_id       UInt32,
    ts          DateTime CODEC(DoubleDelta),
    metric      LowCardinality(String),
    value       Float64 CODEC(Gorilla)
) ENGINE = MergeTree
PARTITION BY toDate(ts)
ORDER BY (org_id, db_id, metric, ts)
TTL ts + INTERVAL 7 DAY;

-- query fingerprints (small, no TTL)
CREATE TABLE query_fingerprints (
    fingerprint UInt64,
    org_id      UInt32,
    db_id       UInt32,
    sql_text    String,
    first_seen  DateTime
) ENGINE = ReplacingMergeTree
ORDER BY (org_id, db_id, fingerprint);

-- top-query samples reference fingerprint only
CREATE TABLE db_query_samples (
    org_id        UInt32,
    db_id         UInt32,
    ts            DateTime,
    fingerprint   UInt64,
    calls         UInt64,
    total_time_ms Float64,
    rows_examined UInt64
) ENGINE = MergeTree
PARTITION BY toDate(ts)
ORDER BY (org_id, db_id, ts, fingerprint)
TTL ts + INTERVAL 7 DAY;
```

### The one operational gotcha — insert batching

**ClickHouse hates many small inserts.** Inserting one row per webhook would destroy it.

- **v1:** use `async_insert=1, wait_for_async_insert=0`. ClickHouse-side buffering, normal INSERTs from the webhook handler. Easiest.
- **v2:** if you outgrow async_insert, move to an app-side batcher (queue + flusher every 1 s or 1000 rows).

### Hosting — Contabo self-hosted

- **VPS-M NVMe** (~$10–15/mo, 6 vCPU / 16 GB RAM / 400 GB NVMe) handles **hundreds of customer DBs** at 7-day retention.
- Step up to VPS-L (~$20/mo, 8 vCPU / 30 GB RAM) around ~500 DBs.
- **Insist on NVMe** plans, not older SSD — ClickHouse merges are I/O heavy.
- **Don't collocate** with MySQL (the billing/user store). Separate boxes.
- **Backups:** nightly `clickhouse-backup` to Backblaze B2 (~$6/TB/mo). Cheap insurance; can also be skipped if you accept that 7 days of insights is rebuildable from agent re-emission.
- **Region:** pick what's closest to where most customer agents live.
- **No HA on a single VM** is acceptable for this data — losing recent insights is not a customer-facing incident.

### Sizing assumptions

Per customer DB (~20 health metrics + top-20 queries tracked):
- ~50–150 MB / DB / week on ClickHouse after compression.
- **100 customer DBs ≈ 5–15 GB** total for 7 days of insights.
- Outgrows a single VPS-M around 500–1000 customer DBs — at that point scale CH vertically or shard.

## Config schema (`launch.yml`)

A new top-level block, mirroring how `pulse:` lives next to `models:`. Insights are configured **independently of backups**.

```yaml
dbinsights:
  enabled: true
  webhook:
    url: https://app.your-saas.com/ingest/dbinsights
    headers:
      Authorization: 'Bearer this-is-token'
  ship_interval: 10s
  ring_buffer_max: 5m
  databases:
    primary_mysql:
      type: mysql
      host: localhost
      port: 3306
      database: app_production
      username: launch_insights
      password: '...'
      collect:
        health: true
        top_queries: true
        waits: true
        schema: true
      sample_intervals:
        health: 10s
        waits: 10s
        top_queries: 30s
        schema: 1h
    analytics_pg:
      type: postgresql
      host: db-internal
      port: 5432
      database: analytics
      username: launch_insights
      password: '...'
      collect:
        health: true
        top_queries: true
        waits: true
        schema: false
```

Design choices:
- **`databases:` is a separate map from `models.*.databases`.** Don't reuse the backup DB config — different user, different permissions, different lifecycle.
- **Per-insight opt-in** so a customer worried about `performance_schema` overhead can disable `top_queries` and still get health/waits.
- **No `enabled` flag per database** — leave it out of the YAML or it's enabled. Avoids the "did I forget to set enabled: true" footgun.
- **SIGHUP reload** works for free — `reloadHandler` in `main.go` already re-inits config; the dbinsights subsystem just diffs old vs new and starts/stops collectors.

## Read path

ClickHouse + the schema above means dashboard queries stay short and fast. Three query shapes cover ~90% of panels.

### Live load gauge (last 5 min, raw 10 s)

```sql
SELECT ts, metric, value
FROM db_health_samples
WHERE org_id = ? AND db_id = ?
  AND ts >= now() - INTERVAL 5 MINUTE
  AND metric IN ('qps', 'conns', 'cache_hit')
ORDER BY ts;
```

Sub-100 ms even when the table holds 7 days × hundreds of DBs. This is the panel that justified ClickHouse.

### Historical chart (last 24 h, 1-min buckets server-side)

```sql
SELECT toStartOfMinute(ts) AS bucket,
       metric,
       avg(value) AS avg_v,
       quantile(0.95)(value) AS p95
FROM db_health_samples
WHERE org_id = ? AND db_id = ?
  AND ts >= now() - INTERVAL 24 HOUR
GROUP BY bucket, metric
ORDER BY bucket;
```

If this gets slow at scale, promote to a materialized view (`CREATE MATERIALIZED VIEW db_health_1m …`). Don't do this in v1 — wait until raw stops fitting your latency budget. ClickHouse will surprise you.

### Top queries (last hour, joined to fingerprints)

```sql
SELECT f.sql_text,
       sum(s.calls) AS calls,
       sum(s.total_time_ms) AS total_ms,
       sum(s.total_time_ms) / sum(s.calls) AS avg_ms
FROM db_query_samples s
INNER JOIN query_fingerprints f
  ON f.fingerprint = s.fingerprint
 AND f.org_id = s.org_id
 AND f.db_id = s.db_id
WHERE s.org_id = ? AND s.db_id = ?
  AND s.ts >= now() - INTERVAL 1 HOUR
GROUP BY f.sql_text
ORDER BY total_ms DESC
LIMIT 20;
```

### Two read-path rules

1. **Always filter on `(org_id, db_id, ts)`.** Matches the table's `ORDER BY`, so ClickHouse reads only the relevant granules. A missing `org_id` filter scans every customer's data — slow *and* a security incident waiting to happen. Enforce in a thin query helper, not in raw SQL strings sprinkled around the app.
2. **Cap every query with a `LIMIT` and a max time window.** Even "show me everything for this DB" should have a hard 30-day ceiling. Misbehaving frontends shouldn't be able to scan TBs.

### Liveness without websockets

Dashboard polls the live-load query every 5–10 s. ClickHouse is fast enough that polling feels live. Add SSE/websocket streaming in v2 if you want to drop polling overhead, but it's pure ergonomics, not a real need.

## Phasing summary

| Phase | Scope |
|---|---|
| **v1** | MySQL + PostgreSQL collectors. Health + top queries + waits + schema. ClickHouse on a single Contabo VPS-M. `async_insert` ingest path. Polling dashboards. |
| **v2** | MongoDB collector. Materialized views for 1-min rollups if raw queries get slow. Optional Redis/SQS queue in front of ClickHouse if backpressure becomes painful. SSE for live panels. |
| **Deferred / dropped** | MSSQL, Redis insights, SQLite insights. |

## Open items to decide before implementation

- **Ingest endpoint location.** Same SaaS app, or a separate small Go service in front of ClickHouse? Recommend: same SaaS app for v1 — fewer moving parts.
- **Fingerprint normalization library.** For MySQL we can lean on `performance_schema` digests directly (already normalized). For Postgres, `pg_stat_statements` provides `queryid`. So the agent's own fingerprinting only matters as a fallback — confirm we don't need a separate normalizer.
- **Multi-tenancy isolation in ClickHouse.** `org_id` in `ORDER BY` is enough for v1. Re-evaluate if/when we need per-tenant resource limits or hard separation.
- **Mongo profiler enablement UX.** How the SaaS UI guides customers to enable `db.setProfilingLevel(1, { slowms: 100 })` without auto-running it.
